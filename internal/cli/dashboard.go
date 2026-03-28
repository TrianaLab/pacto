package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/dashboard"
)

func newDashboardCommand(svc *app.Service, v *viper.Viper, version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard [dir]",
		Short: "Start a local web dashboard for exploring service contracts",
		Long: `Launches a contract exploration dashboard that aggregates data from all
available sources (local filesystem, Kubernetes, OCI registries).

The dashboard is the exploration and observability layer of the Pacto system.
It visualizes the same contracts the CLI manages and the operator verifies —
dependency graphs, version history, interfaces, configuration schemas, diffs,
and runtime compliance — in a single unified view.

Public sources are auto-detected at startup:
  - local: enabled if pacto.yaml is found in the working directory
  - k8s:   enabled if a valid kubeconfig is found and the cluster is reachable
  - oci:   enabled if --repo is specified, or auto-discovered from K8s imageRefs

Materialized bundles on disk (~/.cache/pacto/oci) are used internally by the
OCI source to enrich version data (hash, classification, timestamps) without
appearing as a separate source. The --no-cache flag skips pre-existing cache
at startup but still allows same-session materialization (e.g. fetch-all-versions).

When running alongside the Kubernetes operator, OCI repositories are automatically
discovered from the imageRef fields of Pacto CRD resources. This provides full
contract bundles, version history, interfaces, and diffs — without needing
explicit --repo flags. The result is a hybrid view: runtime truth from the
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
  pacto dashboard --repo ghcr.io/org/order-service --repo ghcr.io/org/payment-service

  # Custom port
  pacto dashboard --port 9090

  # Specify Kubernetes namespace (default: all namespaces)
  pacto dashboard --namespace production`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := v.GetString("dashboard.host")
			port := v.GetInt("dashboard.port")
			namespace := v.GetString("dashboard.namespace")
			repos := dashboardRepos(cmd)
			noCache := v.GetBool("no-cache")
			diagnostics := v.GetBool("dashboard.diagnostics")
			dir := optionalArg(args)

			if dir == "" {
				dir = "."
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
			if detectResult.OCI != nil {
				// Wire internal cache into OCI for version enrichment
				// (hash, createdAt, classification) without exposing cache
				// as a separate public source.
				if detectResult.Cache != nil {
					detectResult.OCI.SetCache(detectResult.Cache)
				}
			}

			// Build resolved source with contract + runtime separation.
			resolved := dashboard.BuildResolvedSource(cachedSources)

			// Build server with embedded UI.
			uiFS := dashboard.EmbeddedUI()
			var diag *dashboard.SourceDiagnostics
			if diagnostics {
				diag = detectResult.Diagnostics
			}
			server := dashboard.NewResolvedServer(resolved, uiFS, detectResult.Sources, diag)
			server.UpdateSourceInfo(detectResult.Sources)
			server.SetVersion(version)
			server.SetListenAddr(host, port)

			// Enable lazy resolution of remote OCI dependencies when a BundleStore is available.
			if svc.BundleStore != nil {
				server.SetResolver(oci.NewResolver(svc.BundleStore))
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
			// Always store the cache directory so fetch-all-versions can
			// create a CacheSource on-the-fly (even with --no-cache).
			server.SetCacheDir(cacheDir)

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
	cmd.Flags().StringArray("repo", nil, "OCI repository to scan (can be repeated)")
	cmd.Flags().Bool("diagnostics", false, "enable source diagnostics panel in the dashboard UI")

	// Bind to viper so flags can be overridden via PACTO_DASHBOARD_* env vars.
	_ = v.BindPFlag("dashboard.host", cmd.Flags().Lookup("host"))
	_ = v.BindPFlag("dashboard.port", cmd.Flags().Lookup("port"))
	_ = v.BindPFlag("dashboard.namespace", cmd.Flags().Lookup("namespace"))
	_ = v.BindPFlag("dashboard.diagnostics", cmd.Flags().Lookup("diagnostics"))

	return cmd
}

// dashboardRepos returns OCI repos from --repo flags, falling back to PACTO_DASHBOARD_REPO env var.
func dashboardRepos(cmd *cobra.Command) []string {
	repos, _ := cmd.Flags().GetStringArray("repo")
	if len(repos) == 0 {
		if envRepos := os.Getenv("PACTO_DASHBOARD_REPO"); envRepos != "" {
			return strings.Split(envRepos, ",")
		}
	}
	return repos
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

		slog.Info("lazy OCI enrichment: wiring OCI source into pipeline")

		// Wrap the new OCI source with in-memory caching.
		ociCached := dashboard.NewCachedDataSource(
			detectResult.OCI, memCache, cacheTTL("oci"), "oci:",
		)
		resolved.AddContractSource("oci", ociCached)

		// Wire OCI discovery callbacks to RefreshCacheSources so that
		// on-the-fly CacheSource creation works (critical for --no-cache).
		detectResult.OCI.SetOnDiscover(server.RefreshCacheSources)
		server.SetOCISource(detectResult.OCI)

		// Wire cache internally into OCI source for enrichment.
		if detectResult.Cache != nil {
			detectResult.OCI.SetCache(detectResult.Cache)
		}
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
