package cli

import (
	"fmt"
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
		Long: `Launches a read-only web dashboard on localhost that aggregates data from
all available sources (local filesystem, Kubernetes, OCI registries, disk cache).

Sources are auto-detected at startup:
  - local: enabled if pacto.yaml is found in the working directory
  - k8s:   enabled if a valid kubeconfig is found and the cluster is reachable
  - oci:   enabled if --repo is specified and the OCI client is configured
  - cache: enabled if ~/.cache/pacto/oci contains cached bundles

Services are grouped by name across sources and merged using priority rules:
  - Kubernetes for runtime state (phase, resources, ports)
  - OCI/cache for version history
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

			// Auto-detect available sources.
			detectResult := dashboard.DetectSources(cmd.Context(), dashboard.DetectOptions{
				Dir:       dir,
				Namespace: namespace,
				Repos:     repos,
				Store:     svc.BundleStore,
				NoCache:   noCache,
			})

			activeSources := detectResult.ActiveSources()
			if len(activeSources) == 0 {
				printSourceErrors(cmd, detectResult.Sources)
				return fmt.Errorf("at least one data source must be available")
			}

			printDetectedSources(cmd, detectResult.Sources)

			// Wrap each source with cache (different TTLs per source type).
			memCache := dashboard.NewMemoryCache()
			allSources := detectResult.AllSources()
			cachedSources := make(map[string]dashboard.DataSource, len(allSources))
			for st, ds := range allSources {
				ttl := cacheTTL(st)
				cachedSources[st] = dashboard.NewCachedDataSource(ds, memCache, ttl, st+":")
			}

			// Wire OCI background discovery to invalidate caches when
			// new services are discovered, so they surface immediately.
			if detectResult.OCI != nil {
				detectResult.OCI.SetOnDiscover(memCache.InvalidateAll)
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

			// Register the cache source and memory cache for runtime refresh
			// after resolve or fetch-all-versions operations.
			if detectResult.Cache != nil {
				server.SetCacheSource(detectResult.Cache, memCache)
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
	case "cache":
		return 10 * time.Minute // disk cache is static, long TTL
	case "local":
		return 2 * time.Second // very short for local files
	default:
		return 30 * time.Second
	}
}
