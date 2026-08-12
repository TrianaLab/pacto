package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/dashboard"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

func newDashboardCommand(svc *app.Service, v *viper.Viper, version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard [sources...]",
		Short: "Start a local web dashboard for exploring service contracts",
		Long: `Launches an operational dashboard that aggregates data from all
available sources (local filesystem, Kubernetes, OCI registries).

The dashboard is the exploration and observability layer of the Pacto system.
It visualizes the same contracts the CLI manages and the operator verifies,
organised around four workflows: an operational Overview, the Services
inventory, the Operational Graph, and Change analysis.

Each positional argument is a pacto source reference:
  - oci://registry/repo  → OCI registry source (can be repeated)
  - ./path/to/dir        → local filesystem source (at most one)

When no arguments are given, sources are auto-detected:
  - local: enabled if pacto.yaml is found in the working directory
  - k8s:   enabled if a valid kubeconfig is found and the cluster is reachable
  - oci:   auto-discovered from K8s status.contract.resolvedRef, or via PACTO_DASHBOARD_REPO env var

Materialized bundles on disk (~/.cache/pacto/oci) are used internally by the
OCI source to enrich version data (hash, classification, timestamps) without
appearing as a separate source. The --no-cache flag skips pre-existing cache
at startup but still allows same-session materialization (e.g. fetch-all-versions).

When running alongside the Kubernetes operator, OCI repositories are automatically
discovered from the status.contract.resolvedRef fields of Pacto CRD resources. This provides full
contract bundles, version history, interfaces, and diffs — without needing
explicit OCI arguments. The result is a hybrid view: runtime truth from the
operator combined with contract truth from OCI.

Services are grouped by name across sources and merged using priority rules:
  - Kubernetes for runtime state (contract status, checks, endpoints)
  - OCI for contract content and version history
  - Local for in-progress contract changes`,
		Example: `  # Start dashboard with auto-detected sources
  pacto dashboard

  # Start from a specific directory
  pacto dashboard ./services

  # Include OCI repositories
  pacto dashboard oci://ghcr.io/org/order-service oci://ghcr.io/org/payment-service

  # Mix local and OCI sources
  pacto dashboard ./services oci://ghcr.io/org/payment-service

  # Custom port
  pacto dashboard --port 9090

  # Specify Kubernetes namespace (default: all namespaces)
  pacto dashboard --namespace production`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			host := v.GetString("dashboard.host")
			port := v.GetInt("dashboard.port")
			namespace := v.GetString("dashboard.namespace")
			noCache := v.GetBool("no-cache")
			diagnostics := v.GetBool("dashboard.diagnostics")
			corsOrigin := v.GetString("dashboard.cors-origin")
			traces := v.GetStringSlice("dashboard.traces")
			traceSources := v.GetStringSlice("dashboard.trace-sources")

			dir, repos, err := parseDashboardArgs(args)
			if err != nil {
				return err
			}

			// Resolve the observation sources before anything is started: a
			// configuration that cannot name its Data Sources unambiguously is a
			// startup error, not something to discover halfway through a snapshot.
			observation, err := observationSources(traces, traceSources)
			if err != nil {
				return err
			}

			cacheDir := v.GetString("cache-dir")
			// Resolve cacheDir from the BundleStore when not explicitly set,
			// so the server can create a CacheSource on-the-fly (e.g. after
			// fetch-all-versions with --no-cache).
			if cacheDir == "" {
				if cs, ok := svc.BundleStore.(interface{ CacheDir() string }); ok {
					cacheDir = cs.CacheDir()
				}
			}

			// Auto-detect available sources.
			detectResult := dashboard.DetectSources(cmd.Context(), dashboard.DetectOptions{
				Dir:       dir,
				Namespace: namespace,
				Repos:     repos,
				Store:     svc.BundleStore,
				CacheDir:  cacheDir,
				NoCache:   noCache,
			})

			// Try a single OCI enrichment attempt from K8s (non-blocking).
			// If it fails, lazy enrichment retries on first API request.
			needsLazyEnrich := tryOCIEnrichment(
				cmd.Context(),
				detectResult, svc.BundleStore, cacheDir, repos,
			)

			activeSources := detectResult.ActiveSources()
			if len(activeSources) == 0 {
				printSourceErrors(cmd, detectResult.Sources)
				return fmt.Errorf("at least one data source must be available")
			}

			printDetectedSources(cmd, deduplicateSourceInfo(detectResult.Sources))

			// Wrap each source with cache (different TTLs per source type).
			memCache := dashboard.NewMemoryCache()
			allSources := detectResult.AllSources()
			cachedSources := make(map[string]dashboard.DataSource, len(allSources))
			for st, ds := range allSources {
				ttl := cacheTTL(st)
				cachedSources[st] = dashboard.NewCachedDataSource(ds, memCache, ttl, st+":")
			}

			// Wire OCI background discovery to refresh cache sources when
			// new services are discovered. refreshCacheSources handles
			// on-the-fly CacheSource creation (critical for --no-cache),
			// cache rescan, OCI wiring, and memory cache invalidation.
			wireOCICache(detectResult)

			// Build resolved source with contract + runtime separation.
			resolved := dashboard.BuildResolvedSource(cachedSources)

			// Build server with embedded UI.
			uiFS := dashboard.EmbeddedUI()
			var diag *dashboard.SourceDiagnostics
			if diagnostics {
				diag = detectResult.Diagnostics
			}
			server := dashboard.NewResolvedServer(resolved, uiFS, detectResult.Sources, diag)
			// Thread this command's logger into the server so request handlers and
			// background discovery log through it (via request-context injection)
			// rather than the process-global slog default.
			server.SetLogger(logging.LoggerFromContext(cmd.Context()))
			server.UpdateSourceInfo(detectResult.Sources)
			server.SetVersion(version)
			server.SetListenAddr(host, port)
			server.SetCORSOrigin(corsOrigin)

			// Enable lazy resolution of remote OCI dependencies when a BundleStore is available.
			if svc.BundleStore != nil {
				server.SetResolver(oci.NewResolver(svc.BundleStore))
			}

			// Enable the read-only operational-graph (fleet) endpoints from every
			// source the dashboard detected — local bundles, OCI repos, the disk
			// cache and the live cluster — so the operational graph reflects the
			// whole fleet, not just the local root. The dashboard becomes a
			// CONSUMER of the reusable fleet layer rather than re-deriving graph,
			// freshness and completeness semantics itself. A single snapshot
			// Manager serves many requests from one coherent, atomically-refreshed
			// snapshot instead of rebuilding per request.
			if fopts, ok := dashboardFleetOptions(dir, repos, namespace, observation, detectResult); ok {
				mgr := fleet.NewManager(func(ctx context.Context) (*fleet.FleetSnapshot, error) {
					return svc.Fleet(ctx, fopts)
				}, fleet.ManagerOptions{})
				go mgr.Start(cmd.Context(), fleetRefreshInterval)
				server.SetFleetProvider(managerFleetProvider(mgr))
				server.SetImpactProvider(impactProviderForFleet(svc, mgr))
			}

			// Track OCI discovery state for progressive loading in the UI.
			if detectResult.OCI != nil {
				server.SetOCISource(detectResult.OCI)
			}

			// Enable k8s re-detection for kubectl context switches.
			server.SetK8sRedetect(wireK8sRedetect(namespace, memCache, dashboard.CurrentKubeContext, dashboard.RedetectK8s))

			// Register cache source (if available) and memory cache for runtime
			// refresh after resolve or fetch-all-versions operations.
			// Always pass memCache so refreshCacheSources can invalidate stale
			// data even when CacheSource is created on-the-fly (--no-cache).
			server.SetCacheSource(detectResult.Cache, memCache)
			// Always store the cache directory so fetch-all-versions can
			// create a CacheSource on-the-fly (even with --no-cache).
			server.SetCacheDir(cacheDir)

			// Wire OCI background discovery to refreshCacheSources. This
			// handles on-the-fly CacheSource creation (critical for --no-cache),
			// cache rescan, OCI wiring, and memory cache invalidation — all
			// in one callback that fires after each discovery cycle.
			if detectResult.OCI != nil {
				detectResult.OCI.SetOnDiscover(server.RefreshCacheSources)
			}

			// Lazy OCI enrichment: if startup retries didn't find OCI repos,
			// register a callback so the server can retry on first API request.
			if needsLazyEnrich {
				server.SetLazyEnrich(wireOCIEnrichment(
					detectResult, resolved, server, memCache,
					svc.BundleStore, cacheDir,
				))
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			var sourceNames []string
			for st := range activeSources {
				sourceNames = append(sourceNames, st)
			}
			addr := fmt.Sprintf("http://%s:%d", displayHost(host), port)
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nPacto Dashboard running at %s\nSources: %s\nPress Ctrl+C to stop\n", addr, strings.Join(sourceNames, ", "))

			return server.Serve(ctx, port, host)
		},
	}

	cmd.Flags().String("host", "127.0.0.1", "bind address for the dashboard server")
	cmd.Flags().Int("port", 3000, "port for the dashboard server")
	cmd.Flags().String("namespace", "", "Kubernetes namespace (empty = all namespaces)")
	cmd.Flags().Bool("diagnostics", false, "enable source diagnostics panel in the dashboard UI")
	cmd.Flags().String("cors-origin", "", "explicit cross-origin allowed to call the API (default: same-origin only)")
	cmd.Flags().StringArray("traces", nil, "OTLP/JSON trace file to fold observed dependencies from (repeatable; also PACTO_DASHBOARD_TRACES)")
	cmd.Flags().StringArray("trace-source", nil, "named offline OTLP/JSON trace source as NAME=PATH, where NAME is its stable data-source identity (repeatable; also PACTO_DASHBOARD_TRACE_SOURCES)")

	// Bind to viper so flags can be overridden via PACTO_DASHBOARD_* env vars.
	_ = v.BindPFlag("dashboard.host", cmd.Flags().Lookup("host"))
	_ = v.BindPFlag("dashboard.port", cmd.Flags().Lookup("port"))
	_ = v.BindPFlag("dashboard.namespace", cmd.Flags().Lookup("namespace"))
	_ = v.BindPFlag("dashboard.diagnostics", cmd.Flags().Lookup("diagnostics"))
	_ = v.BindPFlag("dashboard.cors-origin", cmd.Flags().Lookup("cors-origin"))
	_ = v.BindPFlag("dashboard.traces", cmd.Flags().Lookup("traces"))
	_ = v.BindPFlag("dashboard.trace-sources", cmd.Flags().Lookup("trace-source"))

	return cmd
}

// parseDashboardArgs splits positional arguments into a local directory and
// OCI repository references. Arguments prefixed with "oci://" are treated as
// OCI refs (prefix stripped); all others are local paths. At most one local
// path is allowed. When no OCI args are given, falls back to the
// PACTO_DASHBOARD_REPO env var. When no local path is given, defaults to ".".
func parseDashboardArgs(args []string) (dir string, repos []string, err error) {
	for _, arg := range args {
		if ref, ok := strings.CutPrefix(arg, "oci://"); ok {
			if ref == "" {
				return "", nil, fmt.Errorf("empty OCI reference: %q", arg)
			}
			repos = append(repos, ref)
		} else {
			if dir != "" {
				return "", nil, fmt.Errorf("only one local path is allowed, got both %q and %q", dir, arg)
			}
			dir = arg
		}
	}
	if len(repos) == 0 {
		if envRepos := os.Getenv("PACTO_DASHBOARD_REPO"); envRepos != "" {
			repos = strings.Split(envRepos, ",")
		}
	}
	if dir == "" {
		dir = "."
	}
	return dir, repos, nil
}

// displayHost returns a user-friendly address for display (maps 0.0.0.0 to 127.0.0.1).
func displayHost(host string) string {
	if host == "" || host == "0.0.0.0" {
		return "127.0.0.1"
	}
	return host
}

func printSourceErrors(cmd *cobra.Command, sources []dashboard.SourceInfo) {
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "No data sources detected:")
	for _, s := range sources {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s: %s\n", s.Type, s.Reason)
	}
}

func printDetectedSources(cmd *cobra.Command, sources []dashboard.SourceInfo) {
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Detected sources:")
	for _, s := range sources {
		status := "disabled"
		if s.Enabled {
			status = "enabled"
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  %s: %s (%s)\n", s.Type, status, s.Reason)
	}
}

// cacheTTL returns the cache TTL for each source type.
func cacheTTL(sourceType string) time.Duration {
	switch sourceType {
	case "k8s":
		return 10 * time.Second // short TTL for runtime data
	case "oci":
		return 5 * time.Minute // longer TTL for registry data
	case "local":
		return 2 * time.Second // very short for local files
	default:
		return 30 * time.Second
	}
}

// tryOCIEnrichment makes a single non-blocking attempt to discover OCI repos
// from K8s. Returns true if lazy enrichment is needed (OCI not found yet).
func tryOCIEnrichment(
	ctx context.Context,
	detectResult *dashboard.DetectResult,
	store oci.BundleStore,
	cacheDir string,
	repos []string,
) bool {
	if len(repos) != 0 || store == nil {
		return false
	}
	detectResult.EnrichFromK8s(ctx, store, cacheDir)
	return detectResult.OCI == nil
}

// deduplicateSourceInfo keeps only the last occurrence of each source type.
func deduplicateSourceInfo(info []dashboard.SourceInfo) []dashboard.SourceInfo {
	seen := make(map[string]int)
	var out []dashboard.SourceInfo
	for _, si := range info {
		if idx, ok := seen[si.Type]; ok {
			out[idx] = si
		} else {
			seen[si.Type] = len(out)
			out = append(out, si)
		}
	}
	return out
}

// wireK8sRedetect returns a callback that recreates the k8s client from fresh
// kubeconfig. Returns a new cached DataSource on success, or an error if k8s
// is not available or unchanged. Uses the current kubeconfig context name to
// detect context switches.
func wireK8sRedetect(
	namespace string,
	memCache dashboard.Cache,
	getContext func() string,
	redetect func(ctx context.Context, result *dashboard.DetectResult, namespace string),
) func(ctx context.Context) (dashboard.DataSource, error) {
	var currentContext string
	return func(ctx context.Context) (dashboard.DataSource, error) {
		ctxName := getContext()
		if ctxName == currentContext {
			return nil, fmt.Errorf("no change")
		}

		result := &dashboard.DetectResult{
			Diagnostics: &dashboard.SourceDiagnostics{},
		}
		redetect(ctx, result, namespace)
		if result.K8s == nil {
			if currentContext != "" {
				// Context changed but k8s is now unreachable.
				currentContext = ctxName
				return nil, nil
			}
			return nil, fmt.Errorf("k8s not available")
		}

		currentContext = ctxName
		cached := dashboard.NewCachedDataSource(result.K8s, memCache, cacheTTL("k8s"), "k8s:")
		return cached, nil
	}
}

// wireOCICache wires cache and K8s repo provider into OCI source when available.
func wireOCICache(detectResult *dashboard.DetectResult) {
	if detectResult.OCI != nil {
		if detectResult.Cache != nil {
			detectResult.OCI.SetCache(detectResult.Cache)
		}
		if detectResult.K8s != nil {
			detectResult.OCI.SetRepoProvider(dashboard.RepoProviderFromSource(detectResult.K8s))
		}
	}
}

// wireOCIEnrichment returns a callback that attempts OCI discovery from K8s
// and wires the new sources into the existing pipeline. Called lazily by the
// server when OCI was not available at startup.
func wireOCIEnrichment(
	detectResult *dashboard.DetectResult,
	resolved *dashboard.ResolvedSource,
	server *dashboard.Server,
	memCache dashboard.Cache,
	store oci.BundleStore,
	cacheDir string,
) func(ctx context.Context) bool {
	return func(ctx context.Context) bool {
		detectResult.EnrichFromK8s(ctx, store, cacheDir)
		if detectResult.OCI == nil {
			return false
		}

		logging.LoggerFromContext(ctx).Info("lazy OCI enrichment: wiring OCI source into pipeline")

		// Wrap the new OCI source with in-memory caching.
		ociCached := dashboard.NewCachedDataSource(
			detectResult.OCI, memCache, cacheTTL("oci"), "oci:",
		)
		resolved.AddContractSource("oci", ociCached)

		// Wire OCI discovery callbacks to RefreshCacheSources so that
		// on-the-fly CacheSource creation works (critical for --no-cache).
		detectResult.OCI.SetOnDiscover(server.RefreshCacheSources)
		server.SetOCISource(detectResult.OCI)

		wireOCICache(detectResult)

		// Always pass memCache so RefreshCacheSources can invalidate stale
		// data even when CacheSource is created on-the-fly (--no-cache).
		server.SetCacheSource(detectResult.Cache, memCache)

		// Update source metadata for /api/sources.
		server.UpdateSourceInfo(detectResult.Sources)

		// Invalidate all caches so new data surfaces immediately.
		memCache.InvalidateAll()

		return true
	}
}

// fleetRefreshInterval is how often the dashboard's snapshot Manager rebuilds
// the operational graph in the background.
const fleetRefreshInterval = 30 * time.Second

// currentSnapshot returns the Manager's published snapshot (the original, with
// its query indexes intact — NOT a serialization clone), triggering a coalesced
// first build if none exists yet.
func currentSnapshot(ctx context.Context, mgr *fleet.Manager) (*fleet.FleetSnapshot, error) {
	snap, err := mgr.Current()
	if errors.Is(err, fleet.ErrNoSnapshot) {
		if rerr := mgr.Refresh(ctx); rerr != nil {
			return nil, rerr
		}
		return mgr.Current()
	}
	return snap, err
}

// currentQuery returns a query over the Manager's published snapshot, triggering
// a coalesced first build if none exists yet.
func currentQuery(ctx context.Context, mgr *fleet.Manager) (*fleet.Query, error) {
	snap, err := currentSnapshot(ctx, mgr)
	if err != nil {
		return nil, err
	}
	return fleet.NewQuery(snap), nil
}

// managerFleetProvider serves the fleet query from a shared snapshot Manager.
func managerFleetProvider(mgr *fleet.Manager) func(context.Context) (*fleet.Query, error) {
	return func(ctx context.Context) (*fleet.Query, error) { return currentQuery(ctx, mgr) }
}

// impactProviderForFleet returns an impact provider backing /api/fleet/impact.
// It resolves the old/new refs and analyzes the change against the SAME snapshot
// the dashboard is currently serving (the Manager's published one), so the impact
// answer's snapshotId matches the Operational Graph the user is looking at — never
// a freshly rebuilt, divergent snapshot. Extracted so the wiring is testable.
func impactProviderForFleet(svc *app.Service, mgr *fleet.Manager) func(ctx context.Context, oldRef, newRef string, includeObserved bool) (*impact.Result, error) {
	return func(ctx context.Context, oldRef, newRef string, includeObserved bool) (*impact.Result, error) {
		// Use the ORIGINAL published snapshot (with its query indexes), not a
		// serialization clone — impact traverses the dependency graph, which a
		// clone cannot answer.
		snap, err := currentSnapshot(ctx, mgr)
		if err != nil {
			return nil, err
		}
		return svc.ImpactWithSnapshot(ctx, app.ImpactOptions{
			OldPath: oldRef, NewPath: newRef,
			IncludeObserved: includeObserved,
		}, snap)
	}
}

// observationSources resolves the dashboard's two ways of naming offline trace
// input into one identified list: `--traces PATH` keeps the ad-hoc positional
// id, while `--trace-source NAME=PATH` carries an explicit id that survives
// reordering — the form a declarative configuration (the operator-managed
// dashboard) uses. Ids are rejected as duplicates here rather than collapsed
// downstream: an identity two configured sources share is not an identity.
//
// A named source also declares a read root: the file's own directory, which it
// may not read outside of. That is what makes the declarative form safe over
// storage Pacto does not own — the operator mounts each source at its own
// directory with the export directly inside it, so the file's parent IS the
// mount, and a symlink placed in the volume cannot walk out of it.
func observationSources(traces, named []string) ([]app.ObservationSourceSpec, error) {
	specs := app.TraceFileSources(traces)
	for _, raw := range named {
		// Cut on the FIRST "=", so a path may contain one and a name may not.
		name, p, found := strings.Cut(raw, "=")
		if !found || name == "" || p == "" {
			return nil, fmt.Errorf("invalid --trace-source %q: want NAME=PATH", raw)
		}
		specs = append(specs, app.ObservationSourceSpec{
			ID: name, Root: filepath.Dir(p), Path: filepath.Base(p),
		})
	}
	seen := make(map[string]struct{}, len(specs))
	for _, s := range specs {
		if _, dup := seen[s.ID]; dup {
			return nil, fmt.Errorf("duplicate observation source name %q: each trace source needs its own stable identity", s.ID)
		}
		seen[s.ID] = struct{}{}
	}
	return specs, nil
}

// dashboardFleetOptions builds fleet source options from everything the
// dashboard detected, so the operational-graph endpoints span the whole fleet.
// The second return is false when no source is active (fleet stays disabled).
func dashboardFleetOptions(dir string, repos []string, namespace string, observation []app.ObservationSourceSpec, dr *dashboard.DetectResult) (app.FleetOptions, bool) {
	var fopts app.FleetOptions
	ok := false
	if dr.Local != nil && dir != "" {
		fopts.LocalRoots = []string{dir}
		ok = true
	}
	// Offline OTLP/JSON trace files become observation sources, so the normal
	// dashboard's Operational Graph, reconciliation and Impact see observed
	// dependencies. They come from --traces / PACTO_DASHBOARD_TRACES (ad-hoc,
	// positional ids) or --trace-source / PACTO_DASHBOARD_TRACE_SOURCES (explicit,
	// stable ids — what the operator-managed dashboard is configured with after
	// mounting each file read-only).
	if len(observation) > 0 {
		fopts.ObservationSources = observation
		ok = true
	}
	if dr.OCI != nil && len(repos) > 0 {
		fopts.OCIRefs = repos
		ok = true
	}
	if dr.Cache != nil {
		fopts.IncludeCache = true
		ok = true
	}
	if dr.K8s != nil {
		fopts.IncludeK8s = true
		fopts.K8sNamespace = namespace
		ok = true
	}
	// An operator-wired dashboard learns its managed Evidence Server via env; when
	// set, consume its read-only contribution. Unset means no evidence source
	// (unconfigured), not an unavailable one — so add nothing.
	if url := os.Getenv("PACTO_EVIDENCE_SOURCE_URL"); url != "" {
		fopts.EvidenceURLs = append(fopts.EvidenceURLs, url)
		ok = true
	}
	return fopts, ok
}
