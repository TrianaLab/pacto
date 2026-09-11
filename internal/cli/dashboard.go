package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/fleetsrc"
	"github.com/trianalab/pacto/v3/internal/k8sclient"
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
		Long: `Launches an operational dashboard over the fleet snapshot: the same
operational graph the CLI's ` + "`pacto fleet`" + ` commands query, served as a web UI.

The dashboard is the exploration and observability layer of the Pacto system.
It visualizes the same contracts the CLI manages and the operator verifies,
organised around four workflows: an operational Overview, the Services
inventory, the Operational Graph, and Change analysis.

Each positional argument is a pacto source reference:
  - oci://registry/repo  → OCI registry source (can be repeated)
  - ./path/to/dir        → local filesystem source (at most one)

When no arguments are given, sources are auto-detected:
  - local: enabled if pacto.yaml is found in the working directory
  - cache: enabled if the OCI bundle cache (~/.cache/pacto/oci) holds bundles
  - k8s:   enabled if a kubeconfig or in-cluster config resolves
  - oci:   from the positional arguments, the PACTO_DASHBOARD_REPO env var, or
           the status.contract.resolvedRef of the cluster's Pacto resources

When running alongside the Kubernetes operator, OCI repositories are
automatically discovered from the status.contract.resolvedRef fields of Pacto
CRD resources, on every refresh rather than once at startup. That gives a hybrid
view: runtime truth from the operator combined with contract truth from OCI.

Every source contributes to one snapshot, rebuilt in the background, and each
answer carries the as-of time and the completeness of the sources behind it.`,
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
			// Resolve cacheDir from the BundleStore when not explicitly set, so the
			// disk cache the store actually writes is the one detection looks at.
			if cacheDir == "" {
				if cs, ok := svc.BundleStore.(oci.CacheLocator); ok {
					cacheDir = cs.CacheDir()
				}
			}

			// A cluster the ambient configuration cannot even build a client for is
			// not a source. Reachability is NOT probed here: an unreachable cluster
			// is reported per refresh, as an unavailable source with a reason, which
			// is the only place that answer stays true.
			cluster, _ := k8sclient.NewGoClient()
			det := dashboardSources{
				local:   localRootHasBundle(dir),
				cache:   !noCache && cacheHasEntries(cacheDir),
				cluster: cluster,
			}

			fopts := dashboardFleetOptions(dir, repos, namespace, observation, det)
			configured := configuredSources(fopts)
			if len(configured) == 0 {
				return fmt.Errorf("no data sources detected: no pacto.yaml in %s, no oci:// argument, no cached bundles and no Kubernetes configuration", dir)
			}

			// One snapshot Manager serves many requests from one coherent,
			// atomically-refreshed view of the whole fleet, rather than rebuilding
			// per request. The dashboard is a CONSUMER of the reusable fleet layer:
			// graph, freshness and completeness semantics live there, once.
			discover := clusterContractRefs(cluster, namespace)
			cacheUse := cacheLifecycle{
				permitted:    !noCache,
				baseline:     fopts.IncludeCache,
				materialized: cacheMaterialization(svc.BundleStore),
			}
			mgr := fleet.NewManager(func(ctx context.Context) (*fleet.FleetSnapshot, error) {
				return svc.Fleet(ctx, withClusterContractRefs(ctx, fopts, discover, cacheUse))
			}, fleet.ManagerOptions{})
			go mgr.Start(cmd.Context(), fleetRefreshInterval)

			server := dashboard.NewServer(dashboard.EmbeddedUI())
			// Thread this command's logger into the server so request handlers log
			// through it (via request-context injection) rather than the
			// process-global slog default.
			server.SetLogger(logging.LoggerFromContext(cmd.Context()))
			server.SetVersion(version)
			server.SetListenAddr(host, port)
			server.SetCORSOrigin(corsOrigin)
			server.SetFleetProvider(managerFleetProvider(mgr))
			server.SetImpactProvider(impactProviderForFleet(svc, mgr))
			server.SetFleetRefresher(mgr.Refresh)

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			addr := fmt.Sprintf("http://%s:%d", displayHost(host), port)
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nPacto Dashboard running at %s\nSources: %s\nPress Ctrl+C to stop\n", addr, strings.Join(configured, ", "))

			return server.Serve(ctx, port, host)
		},
	}

	cmd.Flags().String("host", "127.0.0.1", "bind address for the dashboard server")
	cmd.Flags().Int("port", 3000, "port for the dashboard server")
	cmd.Flags().String("namespace", "", "Kubernetes namespace (empty = all namespaces)")
	cmd.Flags().String("cors-origin", "", "explicit cross-origin allowed to call the API (default: same-origin only)")
	cmd.Flags().StringArray("traces", nil, "OTLP/JSON trace file to fold observed dependencies from (repeatable; also PACTO_DASHBOARD_TRACES)")
	cmd.Flags().StringArray("trace-source", nil, "named offline OTLP/JSON trace source as NAME=PATH, where NAME is its stable data-source identity (repeatable; also PACTO_DASHBOARD_TRACE_SOURCES)")

	// Bind to viper so flags can be overridden via PACTO_DASHBOARD_* env vars.
	_ = v.BindPFlag("dashboard.host", cmd.Flags().Lookup("host"))
	_ = v.BindPFlag("dashboard.port", cmd.Flags().Lookup("port"))
	_ = v.BindPFlag("dashboard.namespace", cmd.Flags().Lookup("namespace"))
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

// dashboardSources is what startup detection found: which of the dashboard's
// possible inputs exist on this machine at all. It answers whether a source is
// worth CONFIGURING, never whether it is currently healthy — that second
// question belongs to the refresh that asks it, and is reported per snapshot.
type dashboardSources struct {
	local   bool
	cache   bool
	cluster k8sclient.K8sClient // nil when no cluster client could be built
}

// localRootHasBundle reports whether dir looks like a place contracts live: a
// pacto.yaml in the root or in one immediate, non-hidden subdirectory.
//
// The guard matters because dir defaults to the working directory. Configuring
// a local source unconditionally would point the fleet's recursive walk at
// whatever the user happened to be sitting in — $HOME, or /, where the scan
// costs far more than the zero contracts it finds.
func localRootHasBundle(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, name, "pacto.yaml")); err == nil {
			return true
		}
	}
	return false
}

// cacheHasEntries reports whether the OCI bundle cache holds anything, so an
// empty cache is not published as a Data Source that answers nothing.
func cacheHasEntries(cacheDir string) bool {
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		cacheDir = oci.CacheDirFor(home)
	}
	entries, err := os.ReadDir(cacheDir)
	return err == nil && len(entries) > 0
}

// configuredSources names, in a stable order, the Data Sources this invocation
// builds the fleet from. It is what the startup banner reports and what the
// "nothing to show" check reads: a dashboard with no configured source has no
// question it can answer, so it says so instead of serving an empty fleet.
func configuredSources(fopts app.FleetOptions) []string {
	var names []string
	for _, s := range []struct {
		name string
		on   bool
	}{
		{"local", len(fopts.LocalRoots) > 0},
		{"oci", len(fopts.OCIRefs) > 0},
		{"cache", fopts.IncludeCache},
		{"k8s", fopts.IncludeK8s},
		{"observation", len(fopts.ObservationSources) > 0},
		{"evidence", len(fopts.EvidenceURLs) > 0},
	} {
		if s.on {
			names = append(names, s.name)
		}
	}
	return names
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

// clusterContractRefs returns the callback that reads the contract references
// the live cluster attributes to its services, or nil when no cluster client
// could be built. A cluster that is configured but unreadable contributes no
// references rather than failing the refresh: the Kubernetes source itself is
// what reports that unavailability, with its reason, and failing here would
// take the local, OCI and cache baselines down with it.
//
// The order is deliberate. [fleetsrc.ContractRefs] emits both the exact resolved
// ref a target runs and the repository that ref names, sorted ascending — and a
// bare repository sorts BEFORE its own tagged form because it is a prefix of it.
// Both fold into one revision key, and the merge keeps the first arrival's
// requested ref, so ascending order would record every pinned target as having
// requested a bare repository. Reversing puts the exact ref first.
func clusterContractRefs(client k8sclient.K8sClient, namespace string) func(context.Context) []string {
	if client == nil {
		return nil
	}
	return func(ctx context.Context) []string {
		refs, err := fleetsrc.ContractRefs(ctx, client, namespace)
		if err != nil {
			return nil
		}
		slices.Reverse(refs)
		return refs
	}
}

// cacheLifecycle answers one question per refresh — may the disk cache
// contribute a baseline? — from three facts that are NOT the same fact.
//
// Deriving it from "are there OCI refs right now" conflated all three. An
// operator-managed pod starts with an emptyDir cache and no reconciled CRs, so
// the baseline is off; refs then appear, the pulls fill the cache — and the next
// Kubernetes read that fails or comes back empty took the cache away again,
// exactly when the offline baseline was the only thing left that could answer.
// The same expression also turned the cache on for explicit refs under
// --no-cache, publishing a source over pre-existing entries the store then
// refuses to read: a partial baseline made of limitations.
type cacheLifecycle struct {
	// permitted is false when --no-cache excluded whatever the cache already
	// held. Cold start is a promise about pre-existing state, and the walk that
	// backs a cache source cannot tell those entries from this session's.
	permitted bool
	// baseline is what startup detection found: a cache with content to read.
	baseline bool
	// materialized asks the store whether THIS process has filled the cache,
	// which is the fact a pod's empty startup cache cannot report and no
	// re-inspection of a discovery result can substitute for.
	materialized func() bool
}

// contributes reports whether this refresh should read the disk cache.
func (c cacheLifecycle) contributes() bool {
	return c.permitted && (c.baseline || c.materialized())
}

// cacheMaterialization reports whether the store has written cache entries
// during this process. A store that cannot say has not filled anything this
// process can claim.
func cacheMaterialization(store oci.BundleStore) func() bool {
	if m, ok := store.(oci.CacheObserver); ok {
		return m.Materialized
	}
	return func() bool { return false }
}

// withClusterContractRefs returns the snapshot options for ONE refresh: the
// configured sources plus whatever contract references the cluster reports right
// now, and the cache verdict for this moment in the cache's lifecycle.
//
// The refresh, not startup, is the only honest place to ask. An operator-managed
// dashboard is created by the operator BEFORE any Pacto CR has been reconciled,
// so a set of references read once at startup is the empty set — permanently,
// for the life of the pod. The Product would then show runtime targets with no
// contract revision behind them: no declared dependencies, no reconciliation
// against what was observed, and no change analysis, on a cluster where the
// operator had resolved every one of those contracts.
//
// Explicit `oci://` arguments still win their place in the list; discovery adds
// to what the operator configured rather than replacing it.
func withClusterContractRefs(ctx context.Context, opts app.FleetOptions, discover func(context.Context) []string, cache cacheLifecycle) app.FleetOptions {
	if discover != nil {
		if found := discover(ctx); len(found) > 0 {
			seen := make(map[string]struct{}, len(opts.OCIRefs)+len(found))
			merged := make([]string, 0, len(opts.OCIRefs)+len(found))
			for _, ref := range slices.Concat(opts.OCIRefs, found) {
				if _, dup := seen[ref]; dup {
					continue
				}
				seen[ref] = struct{}{}
				merged = append(merged, ref)
			}
			opts.OCIRefs = merged
		}
	}
	opts.IncludeCache = cache.contributes()
	return opts
}

// dashboardFleetOptions builds fleet source options from everything the
// dashboard detected, so the operational-graph endpoints span the whole fleet
// rather than the local root alone.
func dashboardFleetOptions(dir string, repos []string, namespace string, observation []app.ObservationSourceSpec, det dashboardSources) app.FleetOptions {
	// Recency is one horizon per product surface, not one per code path. The
	// overview already calls evidence older than [fleet.RecentEvidenceWindow] not
	// recent; leaving the build's window at zero disabled staleness classification
	// altogether, so the same payload simultaneously withheld a target from its
	// recent-evidence list and painted it green "Fresh evidence". `pacto fleet`
	// defaults the window off because there a human picks --freshness per query;
	// a long-running dashboard has nobody to ask, and "never evaluated" is not a
	// safe thing to render as fresh.
	fopts := app.FleetOptions{FreshnessWindow: fleet.RecentEvidenceWindow}
	if det.local && dir != "" {
		fopts.LocalRoots = []string{dir}
	}
	// Offline OTLP/JSON trace files become observation sources, so the normal
	// dashboard's Operational Graph, reconciliation and Impact see observed
	// dependencies. They come from --traces / PACTO_DASHBOARD_TRACES (ad-hoc,
	// positional ids) or --trace-source / PACTO_DASHBOARD_TRACE_SOURCES (explicit,
	// stable ids — what the operator-managed dashboard is configured with after
	// mounting each file read-only).
	if len(observation) > 0 {
		fopts.ObservationSources = observation
	}
	if len(repos) > 0 {
		fopts.OCIRefs = repos
	}
	if det.cache {
		fopts.IncludeCache = true
	}
	if det.cluster != nil {
		fopts.IncludeK8s = true
		fopts.K8sNamespace = namespace
	}
	// An operator-wired dashboard learns its managed Evidence Server via env; when
	// set, consume its read-only contribution. Unset means no evidence source
	// (unconfigured), not an unavailable one — so add nothing.
	if url := os.Getenv("PACTO_EVIDENCE_SOURCE_URL"); url != "" {
		fopts.EvidenceURLs = append(fopts.EvidenceURLs, url)
	}
	return fopts
}
