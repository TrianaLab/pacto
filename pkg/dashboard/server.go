package dashboard

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Server serves the dashboard web UI and REST API.
type Server struct {
	source      DataSource
	aggregated  *AggregatedSource // may be nil for non-aggregated usage
	ui          fs.FS
	sourceInfo  []SourceInfo
	diagnostics *SourceDiagnostics

	// Cached service index for scan-heavy endpoints (dependents, cross-refs, graph).
	indexMu    sync.Mutex
	indexCache *serviceIndexCache
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
					"Aggregates data from local filesystem, Kubernetes, OCI registries, and disk cache.",
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

// NewAggregatedServer creates a dashboard server with multi-source aggregation.
func NewAggregatedServer(agg *AggregatedSource, ui fs.FS, sourceInfo []SourceInfo, diagnostics *SourceDiagnostics) *Server {
	return &Server{
		source:      agg,
		aggregated:  agg,
		ui:          ui,
		sourceInfo:  sourceInfo,
		diagnostics: diagnostics,
	}
}

// Serve starts the HTTP server on the given port and blocks until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return s.ServeOnListener(ctx, ln)
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
	api := humago.New(mux, APIConfig())
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
		Status string `json:"status" example:"ok" doc:"Health status"`
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
	Body []SourceInfo `json:"body" doc:"Detected data sources"`
}

type debugSourcesOutput struct {
	Body struct {
		Sources     []SourceInfo       `json:"sources"`
		Diagnostics *SourceDiagnostics `json:"diagnostics,omitempty"`
		Live        *liveDebugInfo     `json:"live,omitempty"`
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

func (s *Server) health(_ context.Context, _ *struct{}) (*healthOutput, error) {
	out := &healthOutput{}
	out.Body.Status = "ok"
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
	details, err := s.source.GetService(ctx, input.Name)
	if err != nil {
		return nil, huma.Error404NotFound(err.Error())
	}
	return &getServiceOutput{Body: details}, nil
}

func (s *Server) getVersions(ctx context.Context, input *ServiceNameInput) (*getVersionsOutput, error) {
	versions, err := s.source.GetVersions(ctx, input.Name)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	return &getVersionsOutput{Body: versions}, nil
}

func (s *Server) getServiceSources(ctx context.Context, input *ServiceNameInput) (*getServiceSourcesOutput, error) {
	if s.aggregated == nil {
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

	agg, err := s.aggregated.GetAggregated(ctx, input.Name)
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
	a := Ref{Name: input.FromName, Version: input.FromVersion}
	b := Ref{Name: input.ToName, Version: input.ToVersion}

	result, err := s.source.GetDiff(ctx, a, b)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	return &getDiffOutput{Body: result}, nil
}

func (s *Server) getSources(_ context.Context, _ *struct{}) (*getSourcesOutput, error) {
	return &getSourcesOutput{Body: s.sourceInfo}, nil
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

	if s.aggregated != nil {
		for _, st := range s.aggregated.SourceTypes() {
			ds := s.aggregated.sources[st]
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

// ── Shared helpers ──────────────────────────────────────────────────

// getCachedIndex returns the cached service index, rebuilding it if stale.
func (s *Server) getCachedIndex(ctx context.Context) *serviceIndexCache {
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

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
