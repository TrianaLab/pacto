package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/internal/oci"
)

// Server serves the dashboard web UI and REST API.
type Server struct {
	source      DataSource
	resolved    *ResolvedSource // may be nil for non-resolved usage
	resolver    *oci.Resolver   // optional: enables lazy resolution of remote OCI dependencies
	cacheSource *CacheSource    // optional: for rescanning after cache writes
	memCache    Cache           // optional: for invalidating after cache writes
	ociSource   *OCISource      // optional: for tracking discovery state
	ui          fs.FS
	sourceInfo  []SourceInfo
	diagnostics *SourceDiagnostics
	listenAddr  string // optional: server URL for OpenAPI spec
	version     string // optional: Pacto version to expose via /health

	// Cached service index for scan-heavy endpoints (dependents, cross-refs, graph).
	indexMu    sync.Mutex
	indexCache *serviceIndexCache

	// Lazy OCI enrichment: retries discovery when OCI was not available at startup.
	lazyEnrich    func(ctx context.Context) bool
	enrichMu      sync.Mutex
	enrichDone    bool
	enrichLastTry time.Time
}

// serviceIndexCache holds a pre-built index of all service details with a short TTL.
type serviceIndexCache struct {
	services []Service
	index    map[string]*ServiceDetails
	aliases  map[string]string // OCI repo name -> contract name
	builtAt  time.Time
}

const indexCacheTTL = 3 * time.Second

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
	return &Server{source: source, ui: ui}
}

// NewResolvedServer creates a dashboard server with the contract+runtime resolution model.
func NewResolvedServer(resolved *ResolvedSource, ui fs.FS, sourceInfo []SourceInfo, diagnostics *SourceDiagnostics) *Server {
	return &Server{
		source:      resolved,
		resolved:    resolved,
		ui:          ui,
		sourceInfo:  sourceInfo,
		diagnostics: diagnostics,
	}
}

// SetResolver enables lazy on-demand resolution of remote OCI dependencies.
func (s *Server) SetResolver(r *oci.Resolver) {
	s.resolver = r
}

// SetCacheSource registers the CacheSource so the server can trigger a rescan
// after new bundles are cached (via resolve or fetch-all-versions).
func (s *Server) SetCacheSource(cs *CacheSource, memCache Cache) {
	s.cacheSource = cs
	s.memCache = memCache
}

// SetOCISource registers the OCISource so the server can report discovery state.
func (s *Server) SetOCISource(src *OCISource) {
	s.ociSource = src
}

// SetLazyEnrich registers a callback that attempts OCI enrichment from K8s.
// The callback is invoked on-demand (from API handlers) if OCI was not
// available at startup. It returns true if enrichment succeeded.
func (s *Server) SetLazyEnrich(fn func(ctx context.Context) bool) {
	s.lazyEnrich = fn
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
	s.sourceInfo = deduped
}

// enrichCooldown is the minimum interval between lazy enrichment attempts.
const enrichCooldown = 10 * time.Second

// ensureOCIEnriched attempts lazy OCI enrichment if it was not available at startup.
// Safe for concurrent calls: guarded by enrichMu with a cooldown between attempts.
func (s *Server) ensureOCIEnriched(ctx context.Context) {
	if s.lazyEnrich == nil || s.enrichDone {
		return
	}
	s.enrichMu.Lock()
	defer s.enrichMu.Unlock()
	if s.enrichDone {
		return
	}
	if time.Since(s.enrichLastTry) < enrichCooldown {
		return
	}
	s.enrichLastTry = time.Now()
	slog.Info("lazy OCI enrichment: attempting discovery from K8s")
	if s.lazyEnrich(ctx) {
		s.enrichDone = true
		slog.Info("lazy OCI enrichment: succeeded, OCI source now active")
	} else {
		slog.Debug("lazy OCI enrichment: not yet available, will retry on next request")
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

	srv := &http.Server{Handler: corsMiddleware(mux)}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		return srv.Close()
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
	Body []ServiceListEntry `json:"body" doc:"List of enriched services"`
}

type getServiceOutput struct {
	Body *ServiceDetails `json:"body" doc:"Service details"`
}

type getVersionsOutput struct {
	Body []Version `json:"body" doc:"Version history"`
}

type getServiceSourcesOutput struct {
	Body *AggregatedService `json:"body" doc:"Per-source breakdown and merged view"`
}

type getGlobalGraphOutput struct {
	Body *GlobalGraph `json:"body" doc:"Global dependency graph"`
}

type getServiceGraphOutput struct {
	Body *DependencyGraph `json:"body" doc:"Service dependency graph"`
}

type getDependentsOutput struct {
	Body []DependentInfo `json:"body" doc:"Services that depend on this service"`
}

type getCrossRefsOutput struct {
	Body *CrossReferences `json:"body" doc:"Config/policy cross-references"`
}

type diffInput struct {
	FromName    string `query:"from_name" required:"true" example:"order-service" doc:"Source service name"`
	FromVersion string `query:"from_version" example:"1.0.0" doc:"Source version"`
	ToName      string `query:"to_name" required:"true" example:"order-service" doc:"Target service name"`
	ToVersion   string `query:"to_version" example:"2.0.0" doc:"Target version"`
}

type getDiffOutput struct {
	Body *DiffResult `json:"body" doc:"Classified diff between two versions"`
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
	Body *ServiceDetails `json:"body" doc:"Resolved service details"`
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
	Name             string   `json:"name"`
	MergedSource     string   `json:"mergedSource"`
	MergedSources    []string `json:"mergedSources"`
	MergedPhase      Phase    `json:"mergedPhase"`
	MergedVersion    string   `json:"mergedVersion"`
	PresentInSources []string `json:"presentInSources"`
}

// ── Huma operation handlers ─────────────────────────────────────────

// SetVersion sets the Pacto version exposed by the health endpoint.
func (s *Server) SetVersion(v string) {
	s.version = v
}

func (s *Server) health(_ context.Context, _ *struct{}) (*healthOutput, error) {
	out := &healthOutput{}
	out.Body.Status = "ok"
	out.Body.Version = s.version
	return out, nil
}

func (s *Server) metrics(ctx context.Context, _ *struct{}) (*metricsOutput, error) {
	out := &metricsOutput{}
	out.Body.SourceCount = len(s.sourceInfo)
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
	enriched := make([]ServiceListEntry, len(services))
	for i, svc := range services {
		entry := ServiceListEntry{Service: svc}
		if d, ok := index[svc.Name]; ok {
			entry.Namespace = d.Namespace
			entry.BlastRadius = computeBlastRadius(svc.Name, index, aliases)
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
				c := ComputeCompliance(svc.Phase, d.Conditions)
				entry.ComplianceStatus = c.Status
				entry.ComplianceScore = c.Score
				if c.Summary != nil {
					entry.ComplianceErrors = c.Summary.Errors
					entry.ComplianceWarns = c.Summary.Warnings
				}
			}
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
	return &getServiceOutput{Body: details}, nil
}

func (s *Server) getVersions(ctx context.Context, input *ServiceNameInput) (*getVersionsOutput, error) {
	s.ensureOCIEnriched(ctx)
	versions, err := s.source.GetVersions(ctx, input.Name)
	if err != nil {
		// No version history is a valid state (e.g. k8s-only service without
		// OCI cache). Return an empty list instead of 500.
		return &getVersionsOutput{Body: []Version{}}, nil
	}
	return &getVersionsOutput{Body: versions}, nil
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
	graph := buildGlobalGraph(cached.services, cached.index)
	return &getGlobalGraphOutput{Body: graph}, nil
}

func (s *Server) getServiceGraph(ctx context.Context, input *ServiceNameInput) (*getServiceGraphOutput, error) {
	cached := s.getCachedIndex(ctx)
	root, ok := cached.index[input.Name]
	if !ok {
		return nil, huma.Error404NotFound("service not found: " + input.Name)
	}
	graph := buildGraph(root, cached.index)
	return &getServiceGraphOutput{Body: graph}, nil
}

func (s *Server) getDependents(ctx context.Context, input *ServiceNameInput) (*getDependentsOutput, error) {
	cached := s.getCachedIndex(ctx)
	aliases := cached.aliases

	var dependents []DependentInfo
	for _, d := range cached.index {
		for _, dep := range d.Dependencies {
			if depRefMatchesName(dep.Ref, input.Name, aliases) {
				dependents = append(dependents, DependentInfo{
					Name:          d.Name,
					Version:       d.Version,
					Phase:         string(d.Phase),
					Required:      dep.Required,
					Compatibility: dep.Compatibility,
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
		return &getCrossRefsOutput{Body: &CrossReferences{}}, nil
	}

	result := CrossReferences{}
	result.References = appendOutgoingRef(result.References, configRef(target), "config", cached.index, aliases)
	result.References = appendOutgoingRef(result.References, policyRef(target), "policy", cached.index, aliases)

	for svcName, d := range cached.index {
		if svcName == input.Name {
			continue
		}
		result.ReferencedBy = appendIncomingRef(result.ReferencedBy, d, input.Name, "config", configRef(d), cached.index, aliases)
		result.ReferencedBy = appendIncomingRef(result.ReferencedBy, d, input.Name, "policy", policyRef(d), cached.index, aliases)
	}

	return &getCrossRefsOutput{Body: &result}, nil
}

func (s *Server) getDiff(ctx context.Context, input *diffInput) (*getDiffOutput, error) {
	s.ensureOCIEnriched(ctx)
	a := Ref{Name: input.FromName, Version: input.FromVersion}
	b := Ref{Name: input.ToName, Version: input.ToVersion}

	result, err := s.source.GetDiff(ctx, a, b)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	return &getDiffOutput{Body: result}, nil
}

func (s *Server) getSources(_ context.Context, _ *struct{}) (*getSourcesOutput, error) {
	out := &getSourcesOutput{}
	out.Body.Sources = s.sourceInfo
	if s.ociSource != nil {
		out.Body.Discovering = s.ociSource.Discovering()
	}
	return out, nil
}

func (s *Server) debugSources(ctx context.Context, _ *struct{}) (*debugSourcesOutput, error) {
	out := &debugSourcesOutput{}
	out.Body.Sources = s.sourceInfo
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
			Name:             svc.Name,
			MergedSource:     svc.Source,
			MergedSources:    svc.Sources,
			MergedPhase:      svc.Phase,
			MergedVersion:    svc.Version,
			PresentInSources: svc.Sources,
		})
	}

	return out, nil
}

func (s *Server) resolveRef(ctx context.Context, input *resolveRefInput) (*resolveRefOutput, error) {
	bundle, err := s.resolver.ResolveConstrained(ctx, input.Body.Ref, input.Body.Compatibility, oci.RemoteAllowed)
	if err != nil {
		var authErr *oci.AuthenticationError
		var notFoundErr *oci.ArtifactNotFoundError
		var invalidRefErr *oci.InvalidRefError
		var invalidBundleErr *oci.InvalidBundleError
		var noMatchErr *oci.NoMatchingVersionError

		switch {
		case errors.As(err, &invalidRefErr):
			return nil, huma.Error422UnprocessableEntity(err.Error())
		case errors.As(err, &noMatchErr):
			return nil, huma.Error422UnprocessableEntity(err.Error())
		case errors.As(err, &authErr):
			return nil, huma.Error403Forbidden(err.Error())
		case errors.As(err, &notFoundErr):
			return nil, huma.Error404NotFound(err.Error())
		case errors.As(err, &invalidBundleErr):
			return nil, huma.Error422UnprocessableEntity(err.Error())
		default:
			return nil, huma.Error502BadGateway(err.Error())
		}
	}

	details := ServiceDetailsFromBundle(bundle, "oci")
	// Rescan disk cache and invalidate in-memory caches so the resolved
	// service becomes a first-class cached artifact visible everywhere.
	s.refreshCacheSources()

	return &resolveRefOutput{Body: details}, nil
}

func (s *Server) listRemoteVersions(ctx context.Context, input *listRemoteVersionsInput) (*listRemoteVersionsOutput, error) {
	var versions []string
	var err error

	if input.Body.Fetch {
		// Fetch mode: pull every version so they persist in cache.
		versions, err = s.resolver.FetchAllVersions(ctx, input.Body.Ref)
		if err == nil {
			s.refreshCacheSources()
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

	rebuilt := &serviceIndexCache{
		services: services,
		index:    index,
		aliases:  aliases,
		builtAt:  time.Now(),
	}

	s.indexMu.Lock()
	s.indexCache = rebuilt
	s.indexMu.Unlock()

	return rebuilt
}

type liveDebugInfo struct {
	ServiceCount int      `json:"serviceCount"`
	ServiceNames []string `json:"serviceNames,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func configRef(d *ServiceDetails) string {
	if d.Configuration != nil {
		return d.Configuration.Ref
	}
	return ""
}

func policyRef(d *ServiceDetails) string {
	if d.Policy != nil {
		return d.Policy.Ref
	}
	return ""
}

func appendOutgoingRef(refs []CrossReference, ref, refType string, index map[string]*ServiceDetails, aliases map[string]string) []CrossReference {
	if ref == "" {
		return refs
	}
	refName := resolveServiceName(extractServiceNameFromRef(ref), index, aliases)
	phase := ""
	if d := index[refName]; d != nil {
		phase = string(d.Phase)
	}
	return append(refs, CrossReference{Name: refName, RefType: refType, Ref: ref, Phase: phase})
}

func appendIncomingRef(refs []CrossReference, d *ServiceDetails, targetName, refType, ref string, index map[string]*ServiceDetails, aliases map[string]string) []CrossReference {
	if ref == "" {
		return refs
	}
	resolved := resolveServiceName(extractServiceNameFromRef(ref), index, aliases)
	if resolved == targetName {
		refs = append(refs, CrossReference{Name: d.Name, RefType: refType, Ref: ref, Phase: string(d.Phase)})
	}
	return refs
}

// refreshCacheSources rescans the disk cache and invalidates the in-memory
// data source cache so newly cached bundles become visible immediately.
func (s *Server) refreshCacheSources() {
	if s.cacheSource != nil {
		s.cacheSource.Rescan()
	}
	if s.memCache != nil {
		s.memCache.InvalidateAll()
	}
	s.indexMu.Lock()
	s.indexCache = nil
	s.indexMu.Unlock()
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
