// Package dashboard serves the Pacto dashboard: a REST API and web UI that
// aggregates contract and runtime data from multiple sources — Kubernetes (via
// the operator), OCI registries, local directories, and on-disk cache — into a
// single view of a service fleet. It computes compliance and readiness, builds
// dependency graphs, and diffs contract versions.
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// Server serves the dashboard web UI and REST API.
type Server struct {
	source      DataSource
	resolved    *ResolvedSource // may be nil for non-resolved usage
	resolver    *oci.Resolver   // optional: enables lazy resolution of remote OCI dependencies
	cacheSource *CacheSource    // optional: for rescanning after cache writes
	cacheDir    string          // optional: OCI cache dir for on-demand CacheSource creation
	memCache    Cache           // optional: for invalidating after cache writes
	ociSource   *OCISource      // optional: for tracking discovery state
	ui          fs.FS
	sourceInfo  []SourceInfo
	diagnostics *SourceDiagnostics
	listenAddr  string // optional: server URL for OpenAPI spec
	version     string // optional: Pacto version to expose via /health
	corsOrigin  string // optional: explicit cross-origin allowed to call the API (startup-only)

	// logger is injected into every request context (see corsMiddleware), so the
	// handler and source code that log via logging.LoggerFromContext reach the
	// command-configured logger rather than the process default. Set from the
	// dashboard command via SetLogger; defaults to slog.Default().
	logger *slog.Logger

	// cfgMu guards the optional wiring fields that can be set AFTER startup from
	// request goroutines via lazy OCI enrichment (wireOCIEnrichment) or
	// RefreshCacheSources: ociSource, cacheSource, memCache, sourceInfo.
	cfgMu sync.RWMutex

	// Cached service index for scan-heavy endpoints (dependents, cross-refs, graph).
	indexMu    sync.Mutex
	indexCache *serviceIndexCache

	// Lazy OCI enrichment: retries discovery when OCI was not available at startup.
	lazyEnrich    func(ctx context.Context) bool
	enrichMu      sync.Mutex
	enrichDone    atomic.Bool // read on the hot path without enrichMu
	enrichLastTry time.Time

	// K8s re-detection: allows swapping the k8s client when kubeconfig changes.
	k8sRedetect   func(ctx context.Context) (DataSource, error) // returns new cached k8s source
	k8sRedetectMu sync.Mutex
	k8sLastCheck  time.Time

	// Background version enrichment: tracks in-flight goroutine for testing.
	versionEnriching atomic.Bool
	versionWg        sync.WaitGroup
}

// serviceIndexCache holds a pre-built index of all service details with a short TTL.
type serviceIndexCache struct {
	services    []Service
	index       map[string]*ServiceDetails
	aliases     map[string]string // OCI repo name -> contract name
	globalGraph *GlobalGraph      // precomputed global graph
	builtAt     time.Time
}

const indexCacheTTL = 15 * time.Second

// APIConfig returns the Huma configuration for the dashboard API.
func APIConfig() huma.Config {
	return huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:   "Pacto Dashboard API",
				Version: "1.0.0",
				Description: "REST API for the Pacto service contract dashboard. " +
					"Resolves contract data from local filesystem or OCI registries, " +
					"enriched with runtime state from Kubernetes.",
			},
		},
		OpenAPIPath:   "/openapi",
		DocsPath:      "/docs",
		SchemasPath:   "/schemas",
		Formats:       huma.DefaultFormats,
		DefaultFormat: "application/json",
	}
}

// NewServer creates a dashboard server backed by the given data source.
// ui is the embedded filesystem containing the web UI assets.
func NewServer(source DataSource, ui fs.FS) *Server {
	return &Server{source: source, ui: ui, logger: slog.Default()}
}

// NewResolvedServer creates a dashboard server with the contract+runtime resolution model.
func NewResolvedServer(resolved *ResolvedSource, ui fs.FS, sourceInfo []SourceInfo, diagnostics *SourceDiagnostics) *Server {
	return &Server{
		source:      resolved,
		resolved:    resolved,
		ui:          ui,
		sourceInfo:  sourceInfo,
		diagnostics: diagnostics,
		logger:      slog.Default(),
	}
}

// SetResolver enables lazy on-demand resolution of remote OCI dependencies.
func (s *Server) SetResolver(r *oci.Resolver) {
	s.resolver = r
}

// SetCacheSource registers the CacheSource so the server can trigger a rescan
// after new bundles are cached (via resolve or fetch-all-versions).
func (s *Server) SetCacheSource(cs *CacheSource, memCache Cache) {
	s.cfgMu.Lock()
	s.cacheSource = cs
	s.memCache = memCache
	s.cfgMu.Unlock()
}

// getCacheSource / getMemCache / getOCISource / getSourceInfo read the
// post-startup-mutable wiring fields under cfgMu so they never race with a
// concurrent lazy-enrichment write.
func (s *Server) getCacheSource() *CacheSource {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cacheSource
}

func (s *Server) getMemCache() Cache {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.memCache
}

func (s *Server) getOCISource() *OCISource {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.ociSource
}

func (s *Server) getSourceInfo() []SourceInfo {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.sourceInfo
}

// SetCacheDir stores the OCI cache directory path so the server can create
// a CacheSource on-the-fly after fetch-all-versions, even when --no-cache
// was used and no CacheSource existed at startup.
func (s *Server) SetCacheDir(dir string) {
	s.cacheDir = dir
}

// SetOCISource registers the OCISource so the server can report discovery state.
func (s *Server) SetOCISource(src *OCISource) {
	s.cfgMu.Lock()
	s.ociSource = src
	s.cfgMu.Unlock()
}

// unresolvedReasonFn returns a reason-lookup function for the graph builder.
// Returns nil when no OCI source is configured (graph uses no reason info).
func (s *Server) unresolvedReasonFn() unresolvedReasonFunc {
	oci := s.getOCISource()
	if oci == nil {
		return nil
	}
	return oci.UnresolvedReason
}

// SetLazyEnrich registers a callback that attempts OCI enrichment from K8s.
// The callback is invoked on-demand (from API handlers) if OCI was not
// available at startup. It returns true if enrichment succeeded.
func (s *Server) SetLazyEnrich(fn func(ctx context.Context) bool) {
	s.lazyEnrich = fn
}

// SetK8sRedetect registers a callback that recreates the k8s client from
// fresh kubeconfig and returns a new cached DataSource. Called periodically
// to detect kubectl context switches without requiring a dashboard restart.
func (s *Server) SetK8sRedetect(fn func(ctx context.Context) (DataSource, error)) {
	s.k8sRedetect = fn
}

// UpdateSourceInfo replaces the source metadata shown by /api/sources.
// Deduplicates by type (keeps last occurrence).
func (s *Server) UpdateSourceInfo(info []SourceInfo) {
	seen := make(map[string]int)
	var deduped []SourceInfo
	for _, si := range info {
		if idx, ok := seen[si.Type]; ok {
			deduped[idx] = si
		} else {
			seen[si.Type] = len(deduped)
			deduped = append(deduped, si)
		}
	}
	s.cfgMu.Lock()
	s.sourceInfo = deduped
	s.cfgMu.Unlock()
}

// enrichCooldown is the minimum interval between lazy enrichment attempts.
const enrichCooldown = 10 * time.Second

// k8sRedetectInterval is the minimum interval between k8s re-detection checks.
const k8sRedetectInterval = 30 * time.Second

// redetectK8sIfNeeded checks whether the k8s source needs re-creation
// (e.g. due to a kubectl context switch). Called from getCachedIndex.
func (s *Server) redetectK8sIfNeeded(ctx context.Context) {
	if s.k8sRedetect == nil {
		return
	}
	s.k8sRedetectMu.Lock()
	if time.Since(s.k8sLastCheck) < k8sRedetectInterval {
		s.k8sRedetectMu.Unlock()
		return
	}
	s.k8sLastCheck = time.Now()
	s.k8sRedetectMu.Unlock()

	newSource, err := s.k8sRedetect(ctx)
	if err != nil {
		logging.LoggerFromContext(ctx).Debug("k8s re-detection: failed", "error", err)
		return
	}
	if newSource == nil {
		return
	}
	logging.LoggerFromContext(ctx).Info("k8s re-detection: context change detected, swapping source")
	if s.resolved != nil {
		s.resolved.SetRuntimeSource(newSource)
	}
	if mc := s.getMemCache(); mc != nil {
		mc.InvalidateAll()
	}
	s.indexMu.Lock()
	s.indexCache = nil
	s.indexMu.Unlock()
}

// ensureOCIEnriched attempts lazy OCI enrichment if it was not available at startup.
// Safe for concurrent calls: guarded by enrichMu with a cooldown between attempts.
func (s *Server) ensureOCIEnriched(ctx context.Context) {
	if s.lazyEnrich == nil || s.enrichDone.Load() {
		return
	}
	s.enrichMu.Lock()
	defer s.enrichMu.Unlock()
	if s.enrichDone.Load() {
		return
	}
	if time.Since(s.enrichLastTry) < enrichCooldown {
		return
	}
	s.enrichLastTry = time.Now()
	logging.LoggerFromContext(ctx).Info("lazy OCI enrichment: attempting discovery from K8s")
	if s.lazyEnrich(ctx) {
		s.enrichDone.Store(true)
		logging.LoggerFromContext(ctx).Info("lazy OCI enrichment: succeeded, OCI source now active")
	} else {
		logging.LoggerFromContext(ctx).Debug("lazy OCI enrichment: not yet available, will retry on next request")
	}
}

// Serve starts the HTTP server on the given host and port and blocks until ctx is cancelled.
// An empty host defaults to 127.0.0.1.
func (s *Server) Serve(ctx context.Context, port int, host ...string) error {
	h := "127.0.0.1"
	if len(host) > 0 && host[0] != "" {
		h = host[0]
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", h, port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return s.ServeOnListener(ctx, ln)
}

// SetListenAddr sets the server URL exposed in the OpenAPI spec.
func (s *Server) SetListenAddr(host string, port int) {
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	s.listenAddr = fmt.Sprintf("http://%s:%d", host, port)
}

// ServeOnListener starts the HTTP server on an existing listener.
func (s *Server) ServeOnListener(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()

	s.registerAPI(mux)

	// Static UI — served on the raw mux, not through Huma.
	mux.Handle("/", http.FileServer(http.FS(s.ui)))

	// Every request derives from baseCtx, so Ctrl+C can actively cancel slow
	// in-flight handlers (e.g. lazy OCI enrichment holding a rate-limited pull)
	// instead of Shutdown blocking on them until the deadline.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()

	srv := &http.Server{
		Handler:           s.corsMiddleware(mux),
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is intentionally unset: the static handler streams the
		// multi-MB embedded UI bundle, which can be slow on throttled links.
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		// Stop the detached OCI background discovery loop so it does not
		// outlive the server.
		if oci := s.getOCISource(); oci != nil {
			oci.Close()
		}
		// Cancel in-flight request contexts so slow handlers abort at once, then
		// drain with a short bounded timeout. Ctrl+C is a clean, user-initiated
		// stop, so a drain timeout is not an error to surface.
		cancelBase()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// registerAPI registers all Huma operations on the given mux.
func (s *Server) registerAPI(mux *http.ServeMux) {
	cfg := APIConfig()
	if s.listenAddr != "" {
		cfg.Servers = []*huma.Server{{URL: s.listenAddr}}
	}
	api := humago.New(mux, cfg)
	s.RegisterOperations(api)
}

// RegisterOperations registers all dashboard API operations on the given Huma API.
// Exported so that OpenAPI specs can be generated without starting a server.
func (s *Server) RegisterOperations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
		Description: "Returns service health status.",
		Tags:        []string{"Health"},
	}, s.health)

	huma.Register(api, huma.Operation{
		OperationID: "metrics",
		Method:      http.MethodGet,
		Path:        "/metrics",
		Summary:     "Basic metrics",
		Description: "Returns basic service metrics.",
		Tags:        []string{"Health"},
	}, s.metrics)

	huma.Register(api, huma.Operation{
		OperationID: "list-services",
		Method:      http.MethodGet,
		Path:        "/api/services",
		Summary:     "List services",
		Description: "Returns an enriched list of all services across all sources.",
		Tags:        []string{"Services"},
	}, s.listServices)

	huma.Register(api, huma.Operation{
		OperationID: "get-service",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}",
		Summary:     "Get service details",
		Description: "Returns full details for a single service by name.",
		Tags:        []string{"Services"},
	}, s.getService)

	huma.Register(api, huma.Operation{
		OperationID: "get-service-versions",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/versions",
		Summary:     "Get service versions",
		Description: "Returns the version history for a service.",
		Tags:        []string{"Services"},
	}, s.getVersions)

	huma.Register(api, huma.Operation{
		OperationID: "get-service-version",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/versions/{version}",
		Summary:     "Get service details at a version",
		Description: "Returns full details for a specific version of a service.",
		Tags:        []string{"Services"},
	}, s.getServiceVersion)

	huma.Register(api, huma.Operation{
		OperationID: "get-service-sources",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/sources",
		Summary:     "Get service sources",
		Description: "Returns per-source breakdown and merged view for a service.",
		Tags:        []string{"Services"},
	}, s.getServiceSources)

	huma.Register(api, huma.Operation{
		OperationID: "get-global-graph",
		Method:      http.MethodGet,
		Path:        "/api/graph",
		Summary:     "Get global dependency graph",
		Description: "Returns the full dependency graph across all services.",
		Tags:        []string{"Graph"},
	}, s.getGlobalGraph)

	huma.Register(api, huma.Operation{
		OperationID: "get-service-graph",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/graph",
		Summary:     "Get service dependency graph",
		Description: "Returns the dependency graph centered on a specific service.",
		Tags:        []string{"Graph"},
	}, s.getServiceGraph)

	huma.Register(api, huma.Operation{
		OperationID: "get-service-dependents",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/dependents",
		Summary:     "Get service dependents",
		Description: "Returns services that depend on the given service.",
		Tags:        []string{"Services"},
	}, s.getDependents)

	huma.Register(api, huma.Operation{
		OperationID: "get-service-refs",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/refs",
		Summary:     "Get service cross-references",
		Description: "Returns config/policy cross-references for a service.",
		Tags:        []string{"Services"},
	}, s.getCrossRefs)

	huma.Register(api, huma.Operation{
		OperationID: "get-diff",
		Method:      http.MethodGet,
		Path:        "/api/diff",
		Summary:     "Diff two service versions",
		Description: "Compares two service versions and returns classified changes.",
		Tags:        []string{"Diff"},
	}, s.getDiff)

	huma.Register(api, huma.Operation{
		OperationID: "get-sources",
		Method:      http.MethodGet,
		Path:        "/api/sources",
		Summary:     "Get detected sources",
		Description: "Returns the list of detected data sources and their status.",
		Tags:        []string{"Sources"},
	}, s.getSources)

	huma.Register(api, huma.Operation{
		OperationID: "refresh",
		Method:      http.MethodPost,
		Path:        "/api/refresh",
		Summary:     "Force refresh all sources",
		Description: "Invalidates all caches, re-detects k8s context changes, and forces a fresh data fetch.",
		Tags:        []string{"Sources"},
	}, s.refresh)

	if s.resolver != nil {
		huma.Register(api, huma.Operation{
			OperationID: "resolve-ref",
			Method:      http.MethodPost,
			Path:        "/api/resolve",
			Summary:     "Resolve a remote OCI dependency",
			Description: "Lazily resolves a remote Pacto bundle from an OCI reference. " +
				"Checks the local cache first, then pulls from the registry if needed. " +
				"Successfully pulled artifacts are cached for future use.",
			Tags: []string{"Services"},
		}, s.resolveRef)

		huma.Register(api, huma.Operation{
			OperationID: "list-remote-versions",
			Method:      http.MethodPost,
			Path:        "/api/versions",
			Summary:     "List available versions from OCI registry",
			Description: "Queries the OCI registry for all semver tags of a given repo reference. " +
				"Returns versions sorted descending (latest first).",
			Tags: []string{"Services"},
		}, s.listRemoteVersions)
	}

	if s.diagnostics != nil {
		huma.Register(api, huma.Operation{
			OperationID: "debug-sources",
			Method:      http.MethodGet,
			Path:        "/api/debug/sources",
			Summary:     "Debug source diagnostics",
			Description: "Returns detailed diagnostic information about source detection.",
			Tags:        []string{"Debug"},
		}, s.debugSources)

		huma.Register(api, huma.Operation{
			OperationID: "debug-services",
			Method:      http.MethodGet,
			Path:        "/api/debug/services",
			Summary:     "Debug per-source services",
			Description: "Returns per-source service breakdown for debugging.",
			Tags:        []string{"Debug"},
		}, s.debugServices)
	}
}

// ExportOpenAPI builds the Huma API with all operations registered and returns the
// serialized OpenAPI 3.1 specification. This can be called without starting a server.
func ExportOpenAPI() ([]byte, error) {
	mux := http.NewServeMux()
	api := humago.New(mux, APIConfig())

	// Register with a nil-source server — we only need the schema, not runtime behavior.
	s := &Server{}
	s.RegisterOperations(api)

	return api.OpenAPI().MarshalJSON()
}

// ── Health / Metrics types ───────────────────────────────────────────

type healthOutput struct {
	Body struct {
		Status  string `json:"status" example:"ok" doc:"Health status"`
		Version string `json:"version,omitempty" example:"1.2.3" doc:"Pacto version"`
	}
}

type metricsOutput struct {
	Body struct {
		ServiceCount int `json:"serviceCount" doc:"Number of known services"`
		SourceCount  int `json:"sourceCount" doc:"Number of active data sources"`
	}
}

// ── Huma operation input/output types ────────────────────────────────

// ServiceNameInput is the path parameter for service-scoped endpoints.
type ServiceNameInput struct {
	Name string `path:"name" maxLength:"255" example:"order-service" doc:"Service name"`
}

type listServicesOutput struct {
	Body []ServiceListEntry `doc:"List of enriched services"`
}

type getServiceOutput struct {
	Body *ServiceDetails `doc:"Service details"`
}

type getVersionsOutput struct {
	Body []Version `doc:"Version history"`
}

type serviceVersionInput struct {
	Name    string `path:"name" maxLength:"255" example:"order-service" doc:"Service name"`
	Version string `path:"version" maxLength:"255" example:"1.2.0" doc:"Service version tag"`
}

type getServiceVersionOutput struct {
	Body *ServiceDetails `doc:"Service details at a specific version"`
}

type getServiceSourcesOutput struct {
	Body *AggregatedService `doc:"Per-source breakdown and merged view"`
}

type getGlobalGraphOutput struct {
	Body *GlobalGraph `doc:"Global dependency graph"`
}

type getServiceGraphOutput struct {
	Body *DependencyGraph `doc:"Service dependency graph"`
}

type getDependentsOutput struct {
	Body []DependentInfo `doc:"Services that depend on this service"`
}

type getCrossRefsOutput struct {
	Body *CrossReferences `doc:"Config/policy cross-references"`
}

type diffInput struct {
	FromName    string `query:"from_name" required:"true" example:"order-service" doc:"Source service name"`
	FromVersion string `query:"from_version" example:"1.0.0" doc:"Source version"`
	ToName      string `query:"to_name" required:"true" example:"order-service" doc:"Target service name"`
	ToVersion   string `query:"to_version" example:"2.0.0" doc:"Target version"`
}

type getDiffOutput struct {
	Body *DiffResult `doc:"Classified diff between two versions"`
}

type getSourcesOutput struct {
	Body struct {
		Sources     []SourceInfo `json:"sources" doc:"Detected data sources"`
		Discovering bool         `json:"discovering" doc:"True while OCI dependency discovery is still running"`
	}
}

type debugSourcesOutput struct {
	Body struct {
		Sources     []SourceInfo       `json:"sources"`
		Diagnostics *SourceDiagnostics `json:"diagnostics,omitempty"`
		Live        *liveDebugInfo     `json:"live,omitempty"`
	}
}

type resolveRefInput struct {
	Body struct {
		Ref           string `json:"ref" required:"true" example:"ghcr.io/org/service-pacto:1.0.0" doc:"OCI reference to resolve"`
		Compatibility string `json:"compatibility,omitempty" example:"^4.0.0" doc:"Semver constraint for untagged refs"`
	}
}

type resolveRefOutput struct {
	Body *ServiceDetails `doc:"Resolved service details"`
}

type listRemoteVersionsInput struct {
	Body struct {
		Ref   string `json:"ref" required:"true" example:"ghcr.io/org/service-pacto" doc:"OCI repository reference (without tag)"`
		Fetch bool   `json:"fetch,omitempty" doc:"When true, pull and cache all discovered versions"`
	}
}

type listRemoteVersionsOutput struct {
	Body struct {
		Versions []string `json:"versions" doc:"Semver tags sorted descending"`
	}
}

type debugServicesOutput struct {
	Body struct {
		PerSource      []perSourceResult   `json:"perSource"`
		AggregatedList []debugServiceEntry `json:"aggregatedList"`
	}
}

type perSourceResult struct {
	SourceType string    `json:"sourceType"`
	Count      int       `json:"count"`
	Services   []Service `json:"services,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type debugServiceEntry struct {
	Name                 string         `json:"name"`
	MergedSource         string         `json:"mergedSource"`
	MergedSources        []string       `json:"mergedSources"`
	MergedContractStatus ContractStatus `json:"mergedContractStatus"`
	MergedVersion        string         `json:"mergedVersion"`
	PresentInSources     []string       `json:"presentInSources"`
}

// ── Huma operation handlers ─────────────────────────────────────────

// SetVersion sets the Pacto version exposed by the health endpoint.
func (s *Server) SetVersion(v string) {
	s.version = v
}

// SetCORSOrigin allows an explicit cross-origin client to call the API. When
// empty (the default) the API is same-origin only and cross-origin mutating
// requests are rejected. Must be called before Serve.
func (s *Server) SetCORSOrigin(origin string) {
	s.corsOrigin = origin
}

// SetLogger sets the logger injected into every request context, so handlers
// and sources that log via logging.LoggerFromContext use the command-configured
// logger. A nil logger falls back to slog.Default(). Must be called before Serve.
func (s *Server) SetLogger(lg *slog.Logger) {
	if lg == nil {
		lg = slog.Default()
	}
	s.logger = lg
}

func (s *Server) health(_ context.Context, _ *struct{}) (*healthOutput, error) {
	out := &healthOutput{}
	out.Body.Status = "ok"
	out.Body.Version = s.version
	return out, nil
}

func (s *Server) metrics(ctx context.Context, _ *struct{}) (*metricsOutput, error) {
	out := &metricsOutput{}
	out.Body.SourceCount = len(s.getSourceInfo())
	if s.source != nil {
		services, err := s.source.ListServices(ctx)
		if err == nil {
			out.Body.ServiceCount = len(services)
		}
	}
	return out, nil
}

func (s *Server) listServices(ctx context.Context, _ *struct{}) (*listServicesOutput, error) {
	cached := s.getCachedIndex(ctx)
	services := cached.services
	index := cached.index
	aliases := cached.aliases
	// Build the reverse-dependency map once (not per service) so blast radius is
	// O(V·E) total instead of O(V²·E) — critical with hundreds of services.
	reverseDeps := buildReverseDeps(index, aliases)
	enriched := make([]ServiceListEntry, len(services))
	for i, svc := range services {
		entry := ServiceListEntry{Service: svc}
		if d, ok := index[svc.Name]; ok {
			entry.Namespace = d.Namespace
			entry.BlastRadius = blastRadiusFrom(svc.Name, reverseDeps)
			entry.DependencyCount = len(d.Dependencies)
			if d.ChecksSummary != nil {
				entry.ChecksPassed = d.ChecksSummary.Passed
				entry.ChecksTotal = d.ChecksSummary.Total
				entry.ChecksFailed = d.ChecksSummary.Failed
			}
			if len(d.Insights) > 0 {
				entry.TopInsight = d.Insights[0].Title
			}
			// Compliance from pre-computed details or computed here.
			if d.Compliance != nil {
				entry.ComplianceStatus = d.Compliance.Status
				entry.ComplianceScore = d.Compliance.Score
				if d.Compliance.Summary != nil {
					entry.ComplianceErrors = d.Compliance.Summary.Errors
					entry.ComplianceWarns = d.Compliance.Summary.Warnings
				}
			} else {
				c := ComputeCompliance(svc.ContractStatus, d.Conditions)
				entry.ComplianceStatus = c.Status
				entry.ComplianceScore = c.Score
				if c.Summary != nil {
					entry.ComplianceErrors = c.Summary.Errors
					entry.ComplianceWarns = c.Summary.Warnings
				}
			}
			entry.UpdateAvailable = d.UpdateAvailable
			entry.Readiness = d.Readiness
			entry.EvaluationCoverage = d.EvaluationCoverage
		}
		enriched[i] = entry
	}
	return &listServicesOutput{Body: enriched}, nil
}

func (s *Server) getService(ctx context.Context, input *ServiceNameInput) (*getServiceOutput, error) {
	s.ensureOCIEnriched(ctx)
	details, err := s.source.GetService(ctx, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	details.GenerateInsights()

	// The resolved source populates SectionMeta during its merge; on the
	// non-resolved path (a single plain source) it is absent, which would leave
	// the UI unable to distinguish "not applicable" from "empty". Compute it from
	// what the source provided so every path returns explained sections.
	if details.SectionMeta == nil {
		computeSectionMeta(details, details.Source, details.RuntimeEvaluated)
	}

	// Enrich with version tracking from the cached index, gated by the index TTL
	// (like the other index-backed endpoints) so version policy and latest-available
	// reflect a recent rebuild rather than an arbitrarily stale snapshot.
	cached := s.getCachedIndex(ctx)
	if indexed, ok := cached.index[input.Name]; ok {
		if details.VersionPolicy == "" {
			details.VersionPolicy = indexed.VersionPolicy
		}
		details.LatestAvailable = indexed.LatestAvailable
		// Recompute against fresh version — the cached value may be
		// stale if the operator changed the version within the TTL.
		details.UpdateAvailable = isUpdateAvailable(details.Version, indexed.LatestAvailable)
	}
	// Conservative fallback: only when neither operator nor index provided a policy.
	if details.VersionPolicy == "" {
		details.VersionPolicy = classifyVersionPolicy(details.ResolvedRef)
	}

	// Carry dependency drift (computed once for the index) onto this fresh detail
	// so the detail table agrees with the graph and dependents views.
	enrichDetailDriftFromIndex(details, cached.index)

	// Enrich remote refs with values from the referenced service.
	enrichConfigRefs(details, cached.index, cached.aliases)
	enrichPolicyRefs(details, cached.index, cached.aliases)

	return &getServiceOutput{Body: details}, nil
}

// enrichConfigRefs resolves remote configuration references against the CURRENT
// index (used by the current-version view).
func enrichConfigRefs(details *ServiceDetails, index map[string]*ServiceDetails, aliases map[string]string) {
	enrichConfigRefsWith(details, func(ref string) (*ServiceDetails, bool) {
		return resolveServiceByName(extractServiceNameFromRef(ref), index, aliases), false
	})
}

// enrichConfigRefsVersioned is the historical-view variant: it resolves each ref
// to the exact version it pins (when pinned and available), falling back to the
// current index and flagging the values so the UI can label them "(current)".
func (s *Server) enrichConfigRefsVersioned(ctx context.Context, details *ServiceDetails, index map[string]*ServiceDetails, aliases map[string]string) {
	enrichConfigRefsWith(details, func(ref string) (*ServiceDetails, bool) {
		return s.resolveRefTarget(ctx, ref, index, aliases)
	})
}

// enrichConfigRefsWith fills in remote config ref values using the supplied
// resolver, which returns the referenced service's details and whether they came
// from the current (rather than the ref-pinned) version.
func enrichConfigRefsWith(details *ServiceDetails, resolve func(ref string) (*ServiceDetails, bool)) {
	for i, cfg := range details.Configurations {
		if cfg.Ref == "" || len(cfg.Values) > 0 {
			continue
		}
		target, fromCurrent := resolve(cfg.Ref)
		if target == nil || len(target.Configurations) == 0 {
			continue
		}
		for _, tc := range target.Configurations {
			if len(tc.Values) > 0 {
				details.Configurations[i].Values = tc.Values
				details.Configurations[i].HasSchema = true
				details.Configurations[i].ValuesAreCurrent = fromCurrent
				break
			}
		}
	}
}

// enrichPolicyRefs resolves remote policy references against the CURRENT index
// (used by the current-version view).
func enrichPolicyRefs(details *ServiceDetails, index map[string]*ServiceDetails, aliases map[string]string) {
	enrichPolicyRefsWith(details, func(ref string) (*ServiceDetails, bool) {
		return resolveServiceByName(extractServiceNameFromRef(ref), index, aliases), false
	})
}

// enrichPolicyRefsVersioned is the historical-view variant (see
// enrichConfigRefsVersioned).
func (s *Server) enrichPolicyRefsVersioned(ctx context.Context, details *ServiceDetails, index map[string]*ServiceDetails, aliases map[string]string) {
	enrichPolicyRefsWith(details, func(ref string) (*ServiceDetails, bool) {
		return s.resolveRefTarget(ctx, ref, index, aliases)
	})
}

// enrichPolicyRefsWith fills in remote policy ref values/metadata using the
// supplied resolver.
func enrichPolicyRefsWith(details *ServiceDetails, resolve func(ref string) (*ServiceDetails, bool)) {
	for i, pol := range details.Policies {
		if pol.Ref == "" || len(pol.Values) > 0 {
			continue
		}
		target, fromCurrent := resolve(pol.Ref)
		if target == nil || len(target.Policies) == 0 {
			continue
		}
		// Copy values and metadata from the referenced service's first policy with values.
		for _, tp := range target.Policies {
			if len(tp.Values) > 0 {
				details.Policies[i].Values = tp.Values
				details.Policies[i].HasSchema = true
				details.Policies[i].ValuesAreCurrent = fromCurrent
				if tp.Title != "" {
					details.Policies[i].Title = tp.Title
				}
				if tp.Description != "" {
					details.Policies[i].Description = tp.Description
				}
				break
			}
		}
	}
}

// resolveRefTarget resolves a config/policy ref to the referenced service's
// details. When the ref pins a version the source can provide, it returns that
// exact version (fromCurrent=false). Otherwise it falls back to the current index
// (fromCurrent=true) so the UI can flag the values as current.
func (s *Server) resolveRefTarget(ctx context.Context, ref string, index map[string]*ServiceDetails, aliases map[string]string) (target *ServiceDetails, fromCurrent bool) {
	refName := extractServiceNameFromRef(ref)
	if refVersion := extractVersionFromRef(ref); refVersion != "" {
		if d, err := s.source.GetServiceVersion(ctx, Ref{Name: refName, Version: refVersion}); err == nil && d != nil {
			return d, false
		}
	}
	return resolveServiceByName(refName, index, aliases), true
}

// resolveServiceByName looks up a service by extracted ref name, trying
// direct match, alias resolution, and pacto-suffix stripping.
func resolveServiceByName(name string, index map[string]*ServiceDetails, aliases map[string]string) *ServiceDetails {
	if d, ok := index[name]; ok {
		return d
	}
	if resolved, ok := aliases[name]; ok {
		if d, ok := index[resolved]; ok {
			return d
		}
	}
	if stripped, ok := stripPactoSuffix(name); ok {
		if d, ok := index[stripped]; ok {
			return d
		}
	}
	return nil
}

func (s *Server) getVersions(ctx context.Context, input *ServiceNameInput) (*getVersionsOutput, error) {
	s.ensureOCIEnriched(ctx)
	versions, err := s.source.GetVersions(ctx, input.Name)
	if err != nil {
		// No version history is a valid state (e.g. k8s-only service without
		// OCI cache). Return an empty list instead of 500.
		return &getVersionsOutput{Body: []Version{}}, nil
	}

	// Mark the currently active version.
	if detail, detailErr := s.source.GetService(ctx, input.Name); detailErr == nil && detail != nil {
		markCurrentVersion(versions, detail.Version)
	}

	return &getVersionsOutput{Body: versions}, nil
}

func (s *Server) getServiceVersion(ctx context.Context, input *serviceVersionInput) (*getServiceVersionOutput, error) {
	s.ensureOCIEnriched(ctx)
	details, err := s.source.GetServiceVersion(ctx, Ref{Name: input.Name, Version: input.Version})
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	// The route's version is authoritative for display, regardless of what the
	// bundle declares.
	details.Version = input.Version
	details.GenerateInsights()
	// Single plain sources don't populate SectionMeta; compute it so every
	// section self-explains (matching getService).
	if details.SectionMeta == nil {
		computeSectionMeta(details, details.Source, details.RuntimeEvaluated)
	}

	// Resolve remote config/policy refs to the version each ref pins (falling
	// back to the current index, flagged for the UI). Mirrors getService's
	// enrichment but version-aware, so a historical view shows ref values too.
	cached := s.getCachedIndex(ctx)
	// Carry dependency drift (index-computed) onto the historical detail too, so the
	// drift badge is consistent with the current graph regardless of selected version.
	enrichDetailDriftFromIndex(details, cached.index)
	s.enrichConfigRefsVersioned(ctx, details, cached.index, cached.aliases)
	s.enrichPolicyRefsVersioned(ctx, details, cached.index, cached.aliases)

	return &getServiceVersionOutput{Body: details}, nil
}

func (s *Server) getServiceSources(ctx context.Context, input *ServiceNameInput) (*getServiceSourcesOutput, error) {
	s.ensureOCIEnriched(ctx)
	if s.resolved == nil {
		details, err := s.source.GetService(ctx, input.Name)
		if err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		return &getServiceSourcesOutput{Body: &AggregatedService{
			Name:    input.Name,
			Sources: []ServiceSourceData{{SourceType: details.Source, Service: details}},
			Merged:  details,
		}}, nil
	}

	agg, err := s.resolved.GetAggregated(ctx, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	return &getServiceSourcesOutput{Body: agg}, nil
}

func (s *Server) getGlobalGraph(ctx context.Context, _ *struct{}) (*getGlobalGraphOutput, error) {
	cached := s.getCachedIndex(ctx)
	return &getGlobalGraphOutput{Body: cached.globalGraph}, nil
}

func (s *Server) getServiceGraph(ctx context.Context, input *ServiceNameInput) (*getServiceGraphOutput, error) {
	cached := s.getCachedIndex(ctx)
	root, ok := cached.index[input.Name]
	if !ok {
		return nil, huma.Error404NotFound("service not found: " + input.Name)
	}
	graph := buildGraph(root, cached.index, s.unresolvedReasonFn())
	return &getServiceGraphOutput{Body: graph}, nil
}

func (s *Server) getDependents(ctx context.Context, input *ServiceNameInput) (*getDependentsOutput, error) {
	cached := s.getCachedIndex(ctx)
	if _, ok := cached.index[input.Name]; !ok {
		return nil, huma.Error404NotFound("service not found: " + input.Name)
	}
	aliases := cached.aliases

	dependents := []DependentInfo{}
	for _, d := range cached.index {
		for _, dep := range d.Dependencies {
			if depRefMatchesName(dep.Ref, input.Name, aliases) {
				dependents = append(dependents, DependentInfo{
					Name:           d.Name,
					Version:        d.Version,
					ContractStatus: string(d.ContractStatus),
					Required:       dep.Required,
					Compatibility:  dep.Compatibility,
				})
				break
			}
		}
	}

	return &getDependentsOutput{Body: dependents}, nil
}

func (s *Server) getCrossRefs(ctx context.Context, input *ServiceNameInput) (*getCrossRefsOutput, error) {
	cached := s.getCachedIndex(ctx)
	aliases := cached.aliases

	target := cached.index[input.Name]
	if target == nil {
		return nil, huma.Error404NotFound("service not found: " + input.Name)
	}

	result := CrossReferences{}
	for _, ref := range configRefs(target) {
		result.References = appendOutgoingRef(result.References, ref, "config", cached.index, aliases)
	}
	for _, ref := range policyRefs(target) {
		result.References = appendOutgoingRef(result.References, ref, "policy", cached.index, aliases)
	}

	for svcName, d := range cached.index {
		if svcName == input.Name {
			continue
		}
		for _, ref := range configRefs(d) {
			result.ReferencedBy = appendIncomingRef(result.ReferencedBy, d, input.Name, "config", ref, cached.index, aliases)
		}
		for _, ref := range policyRefs(d) {
			result.ReferencedBy = appendIncomingRef(result.ReferencedBy, d, input.Name, "policy", ref, cached.index, aliases)
		}
	}

	return &getCrossRefsOutput{Body: &result}, nil
}

func (s *Server) getDiff(ctx context.Context, input *diffInput) (*getDiffOutput, error) {
	s.ensureOCIEnriched(ctx)
	a := Ref{Name: input.FromName, Version: input.FromVersion}
	b := Ref{Name: input.ToName, Version: input.ToVersion}

	result, err := s.source.GetDiff(ctx, a, b)
	if err != nil {
		// Classify so user-level conditions (no bundle data, bad ref, auth, not
		// found) are not reported as 500 — matching resolveRef and the way
		// version listing degrades gracefully for the same services.
		if strings.Contains(err.Error(), "diff requires contract bundle data") {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return nil, ociHTTPError(err)
	}
	return &getDiffOutput{Body: result}, nil
}

type refreshOutput struct {
	Body struct {
		Status string `json:"status" example:"ok" doc:"Refresh result"`
	}
}

func (s *Server) refresh(ctx context.Context, _ *struct{}) (*refreshOutput, error) {
	// Force k8s re-detection (bypass cooldown).
	if s.k8sRedetect != nil {
		s.k8sRedetectMu.Lock()
		s.k8sLastCheck = time.Time{} // reset cooldown
		s.k8sRedetectMu.Unlock()
		s.redetectK8sIfNeeded(ctx)
	}

	// Invalidate all caches.
	if mc := s.getMemCache(); mc != nil {
		mc.InvalidateAll()
	}
	s.indexMu.Lock()
	s.indexCache = nil
	s.indexMu.Unlock()

	out := &refreshOutput{}
	out.Body.Status = "ok"
	return out, nil
}

func (s *Server) getSources(_ context.Context, _ *struct{}) (*getSourcesOutput, error) {
	out := &getSourcesOutput{}
	out.Body.Sources = s.getSourceInfo()
	if oci := s.getOCISource(); oci != nil {
		out.Body.Discovering = oci.Discovering()
	}
	return out, nil
}

func (s *Server) debugSources(ctx context.Context, _ *struct{}) (*debugSourcesOutput, error) {
	out := &debugSourcesOutput{}
	out.Body.Sources = s.getSourceInfo()
	out.Body.Diagnostics = s.diagnostics

	if s.source != nil {
		live := &liveDebugInfo{}
		services, err := s.source.ListServices(ctx)
		if err != nil {
			live.Error = err.Error()
		} else {
			live.ServiceCount = len(services)
			for _, svc := range services {
				live.ServiceNames = append(live.ServiceNames, svc.Name)
			}
		}
		out.Body.Live = live
	}

	return out, nil
}

func (s *Server) debugServices(ctx context.Context, _ *struct{}) (*debugServicesOutput, error) {
	out := &debugServicesOutput{}

	if s.resolved != nil {
		for _, st := range s.resolved.SourceTypes() {
			ds := s.resolved.GetSource(st)
			result := perSourceResult{SourceType: st}
			svcs, err := ds.ListServices(ctx)
			if err != nil {
				result.Error = err.Error()
			} else {
				result.Count = len(svcs)
				result.Services = svcs
			}
			out.Body.PerSource = append(out.Body.PerSource, result)
		}
	}

	services, err := s.source.ListServices(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	for _, svc := range services {
		out.Body.AggregatedList = append(out.Body.AggregatedList, debugServiceEntry{
			Name:                 svc.Name,
			MergedSource:         svc.Source,
			MergedSources:        svc.Sources,
			MergedContractStatus: svc.ContractStatus,
			MergedVersion:        svc.Version,
			PresentInSources:     svc.Sources,
		})
	}

	return out, nil
}

// ociHTTPError maps an OCI resolution error to the appropriate Huma HTTP error
// so user-level conditions (bad ref, no matching version, invalid bundle, auth,
// not found) are not reported as 500s. Shared by getDiff and resolveRef.
func ociHTTPError(err error) error {
	var authErr *oci.AuthenticationError
	var notFoundErr *oci.ArtifactNotFoundError
	var invalidRefErr *oci.InvalidRefError
	var invalidBundleErr *oci.InvalidBundleError
	var noMatchErr *oci.NoMatchingVersionError
	switch {
	case errors.As(err, &invalidRefErr), errors.As(err, &noMatchErr), errors.As(err, &invalidBundleErr):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.As(err, &authErr):
		return huma.Error403Forbidden(err.Error())
	case errors.As(err, &notFoundErr):
		return huma.Error404NotFound(err.Error())
	default:
		return huma.Error502BadGateway(err.Error())
	}
}

func (s *Server) resolveRef(ctx context.Context, input *resolveRefInput) (*resolveRefOutput, error) {
	bundle, err := s.resolver.ResolveConstrained(ctx, input.Body.Ref, input.Body.Compatibility, oci.RemoteAllowed)
	if err != nil {
		return nil, ociHTTPError(err)
	}

	details := ServiceDetailsFromBundle(bundle, "oci")
	// Rescan disk cache and invalidate in-memory caches so the resolved
	// service becomes a first-class cached artifact visible everywhere.
	s.RefreshCacheSources()

	return &resolveRefOutput{Body: details}, nil
}

func (s *Server) listRemoteVersions(ctx context.Context, input *listRemoteVersionsInput) (*listRemoteVersionsOutput, error) {
	var versions []string
	var err error

	if input.Body.Fetch {
		// Fetch mode: pull every version so they persist in cache.
		versions, err = s.resolver.FetchAllVersions(ctx, input.Body.Ref)
		if err == nil {
			s.RefreshCacheSources()
		}
	} else {
		versions, err = s.resolver.ListVersions(ctx, input.Body.Ref)
	}

	if err != nil {
		var authErr *oci.AuthenticationError
		if errors.As(err, &authErr) {
			return nil, huma.Error403Forbidden(err.Error())
		}
		return nil, huma.Error502BadGateway(err.Error())
	}
	out := &listRemoteVersionsOutput{}
	out.Body.Versions = versions
	return out, nil
}

// ── Shared helpers ──────────────────────────────────────────────────

// getCachedIndex returns the cached service index, rebuilding it if stale.
func (s *Server) getCachedIndex(ctx context.Context) *serviceIndexCache {
	s.ensureOCIEnriched(ctx)
	s.redetectK8sIfNeeded(ctx)
	s.indexMu.Lock()
	if s.indexCache != nil && time.Since(s.indexCache.builtAt) < indexCacheTTL {
		cached := s.indexCache
		s.indexMu.Unlock()
		return cached
	}
	stale := s.indexCache
	s.indexMu.Unlock()

	// Rebuild outside the lock to avoid blocking concurrent requests.
	services, err := s.source.ListServices(ctx)
	if err != nil {
		if stale != nil {
			return stale // return stale on error
		}
		return &serviceIndexCache{index: map[string]*ServiceDetails{}}
	}

	index := make(map[string]*ServiceDetails, len(services))
	for _, svc := range services {
		d, err := s.source.GetService(ctx, svc.Name)
		if err == nil && d != nil {
			d.GenerateInsights()
			index[d.Name] = d
		}
	}

	aliases := buildRefAliases(index)
	for _, d := range index {
		for i, dep := range d.Dependencies {
			d.Dependencies[i].Name = resolveServiceName(dep.Name, index, aliases)
		}
	}

	// Drift: compare each locked dependency digest against the dependency target's
	// runtime digest (carried on the target ServiceDetails from k8s/runtime). The
	// dep name is already resolved to the target service name above. Done before
	// the graph is built so dependency edges carry the drift status too.
	enrichDrift(index)

	// Fast version policy classification (no I/O, just heuristics).
	for _, d := range index {
		if d.VersionPolicy == "" {
			d.VersionPolicy = classifyVersionPolicy(d.ResolvedRef)
		}
	}

	rebuilt := &serviceIndexCache{
		services:    services,
		index:       index,
		aliases:     aliases,
		globalGraph: buildGlobalGraph(services, index, s.unresolvedReasonFn()),
		builtAt:     time.Now(),
	}

	s.indexMu.Lock()
	s.indexCache = rebuilt
	s.indexMu.Unlock()

	// Enrich version tracking in background so the first paint is not blocked
	// by N×M GetVersions API calls. The enriched cache replaces the base once done.
	if s.versionEnriching.CompareAndSwap(false, true) {
		s.versionWg.Add(1)
		go s.deferredVersionEnrich(context.WithoutCancel(ctx), rebuilt)
	}

	return rebuilt
}

type liveDebugInfo struct {
	ServiceCount int      `json:"serviceCount"`
	ServiceNames []string `json:"serviceNames,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func configRefs(d *ServiceDetails) []string {
	var refs []string
	for _, cfg := range d.Configurations {
		if cfg.Ref != "" {
			refs = append(refs, cfg.Ref)
		}
	}
	return refs
}

func policyRefs(d *ServiceDetails) []string {
	var refs []string
	for _, pol := range d.Policies {
		if pol.Ref != "" {
			refs = append(refs, pol.Ref)
		}
	}
	return refs
}

func appendOutgoingRef(refs []CrossReference, ref, refType string, index map[string]*ServiceDetails, aliases map[string]string) []CrossReference {
	if ref == "" {
		return refs
	}
	refName := resolveServiceName(extractServiceNameFromRef(ref), index, aliases)
	cs := ""
	if d := index[refName]; d != nil {
		cs = string(d.ContractStatus)
	}
	return append(refs, CrossReference{Name: refName, RefType: refType, Ref: ref, ContractStatus: cs})
}

func appendIncomingRef(refs []CrossReference, d *ServiceDetails, targetName, refType, ref string, index map[string]*ServiceDetails, aliases map[string]string) []CrossReference {
	if ref == "" {
		return refs
	}
	resolved := resolveServiceName(extractServiceNameFromRef(ref), index, aliases)
	if resolved == targetName {
		refs = append(refs, CrossReference{Name: d.Name, RefType: refType, Ref: ref, ContractStatus: string(d.ContractStatus)})
	}
	return refs
}

// RefreshCacheSources rescans the disk cache and invalidates the in-memory
// data source cache so newly cached bundles become visible immediately.
// If no CacheSource exists yet (e.g. --no-cache was used) but a cacheDir is
// known, it creates one on-the-fly and wires it into the OCI source's
// internal cache (never as a separate public source).
func (s *Server) RefreshCacheSources() {
	cacheSource := s.getCacheSource()
	ociSource := s.getOCISource()
	if cacheSource == nil && s.cacheDir != "" {
		cs := NewCacheSource(s.cacheDir)
		if cs.ServiceCount() > 0 {
			cacheSource = cs
			s.cfgMu.Lock()
			s.cacheSource = cs
			s.cfgMu.Unlock()
			// Wire into OCI's internal cache for enrichment, not as a public source.
			if ociSource != nil {
				ociSource.SetCache(cs)
			}
		}
	}
	if cacheSource != nil {
		cacheSource.Rescan()
	}
	// Also tell the OCI source to rescan its internal cache view.
	if ociSource != nil {
		ociSource.RescanCache()
	}
	if mc := s.getMemCache(); mc != nil {
		mc.InvalidateAll()
	}
	s.indexMu.Lock()
	s.indexCache = nil
	s.indexMu.Unlock()
}

// deferredVersionEnrich runs in a background goroutine to populate
// LatestAvailable and UpdateAvailable without blocking the first paint.
// It clones the index entries to avoid data races, then atomically swaps
// the enriched cache in if the base cache is still current.
func (s *Server) deferredVersionEnrich(ctx context.Context, base *serviceIndexCache) {
	defer s.versionWg.Done()
	defer s.versionEnriching.Store(false)

	// Clone index entries so readers of the base cache are not affected.
	enrichedIndex := make(map[string]*ServiceDetails, len(base.index))
	for k, v := range base.index {
		clone := *v
		enrichedIndex[k] = &clone
	}

	for _, d := range enrichedIndex {
		versions, err := s.source.GetVersions(ctx, d.Name)
		if err != nil || len(versions) == 0 {
			continue
		}
		d.LatestAvailable = computeLatestAvailable(versions)
		d.UpdateAvailable = isUpdateAvailable(d.Version, d.LatestAvailable)
	}

	enriched := &serviceIndexCache{
		services:    base.services,
		index:       enrichedIndex,
		aliases:     base.aliases,
		globalGraph: base.globalGraph,
		builtAt:     time.Now(),
	}

	s.indexMu.Lock()
	// Only replace if no newer cache was built while we were enriching.
	if s.indexCache == base {
		s.indexCache = enriched
	}
	s.indexMu.Unlock()
}

// WaitForVersionEnrich blocks until any in-flight background version
// enrichment completes. Used by tests to synchronize.
func (s *Server) WaitForVersionEnrich() {
	s.versionWg.Wait()
}

// corsMiddleware handles CORS and protects mutating endpoints from
// browser-driven cross-origin (CSRF/SSRF) requests. The dashboard UI is served
// same-origin, so by default no Access-Control-Allow-Origin is emitted and
// cross-origin mutating requests are rejected. An explicit cross-origin client
// can be allowed via SetCORSOrigin.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if s.corsOrigin != "" && origin == s.corsOrigin {
			w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Reject cross-origin mutating requests (the SSRF/CSRF surface on
		// /api/resolve, /api/versions, /api/refresh). Same-origin requests and
		// non-browser clients (no Origin header) are allowed.
		if isMutatingMethod(r.Method) && !s.originAllowed(r) {
			http.Error(w, "cross-origin request forbidden", http.StatusForbidden)
			return
		}
		// Carry the command-configured logger on the request context so handlers
		// and sources log through it (not the process default) via
		// logging.LoggerFromContext. Detached background contexts derived with
		// context.WithoutCancel (e.g. OCI discovery) preserve this value.
		next.ServeHTTP(w, r.WithContext(logging.WithLogger(r.Context(), s.logger)))
	})
}

// originAllowed reports whether r may perform a mutating request: true for
// same-origin requests, the explicitly allowed cross-origin, or clients that
// send no Origin header (e.g. curl, the CLI).
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if s.corsOrigin != "" && origin == s.corsOrigin {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func isMutatingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
