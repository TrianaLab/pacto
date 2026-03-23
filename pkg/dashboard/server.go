package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"
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

	// REST API
	mux.HandleFunc("GET /api/services", s.handleListServices)
	mux.HandleFunc("GET /api/services/{name}", s.handleGetService)
	mux.HandleFunc("GET /api/services/{name}/versions", s.handleGetVersions)
	mux.HandleFunc("GET /api/services/{name}/sources", s.handleGetServiceSources)
	mux.HandleFunc("GET /api/graph", s.handleGetGlobalGraph)
	mux.HandleFunc("GET /api/services/{name}/graph", s.handleGetGraph)
	mux.HandleFunc("GET /api/services/{name}/dependents", s.handleGetDependents)
	mux.HandleFunc("GET /api/services/{name}/refs", s.handleGetCrossRefs)
	mux.HandleFunc("GET /api/diff", s.handleGetDiff)
	mux.HandleFunc("GET /api/sources", s.handleGetSources)
	if s.diagnostics != nil {
		mux.HandleFunc("GET /api/debug/sources", s.handleDebugSources)
		mux.HandleFunc("GET /api/debug/services", s.handleDebugServices)
	}

	// Static UI
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

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	cached := s.getCachedIndex(r.Context())
	services := cached.services
	index := cached.index
	aliases := cached.aliases
	enriched := make([]ServiceListEntry, len(services))
	for i, svc := range services {
		entry := ServiceListEntry{Service: svc}
		if d, ok := index[svc.Name]; ok {
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
		}
		enriched[i] = entry
	}
	writeJSON(w, enriched)
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	details, err := s.source.GetService(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, details)
}

func (s *Server) handleGetVersions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	versions, err := s.source.GetVersions(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, versions)
}

func (s *Server) handleGetDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	a := Ref{Name: q.Get("from_name"), Version: q.Get("from_version")}
	b := Ref{Name: q.Get("to_name"), Version: q.Get("to_version")}

	if a.Name == "" || b.Name == "" {
		writeError(w, http.StatusBadRequest, "from_name, from_version, to_name, and to_version are required")
		return
	}

	result, err := s.source.GetDiff(r.Context(), a, b)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleGetServiceSources(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.aggregated == nil {
		// Non-aggregated: return single-source view.
		details, err := s.source.GetService(r.Context(), name)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, AggregatedService{
			Name:    name,
			Sources: []ServiceSourceData{{SourceType: details.Source, Service: details}},
			Merged:  details,
		})
		return
	}

	agg, err := s.aggregated.GetAggregated(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, agg)
}

func (s *Server) handleGetGlobalGraph(w http.ResponseWriter, r *http.Request) {
	cached := s.getCachedIndex(r.Context())
	graph := buildGlobalGraph(cached.services, cached.index)
	writeJSON(w, graph)
}

func (s *Server) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cached := s.getCachedIndex(r.Context())
	depIndex := cached.index

	root, ok := depIndex[name]
	if !ok {
		writeError(w, http.StatusNotFound, "service not found: "+name)
		return
	}

	graph := buildGraph(root, depIndex)
	writeJSON(w, graph)
}

// getCachedIndex returns the cached service index, rebuilding it if stale.
func (s *Server) getCachedIndex(ctx context.Context) *serviceIndexCache {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()

	if s.indexCache != nil && time.Since(s.indexCache.builtAt) < indexCacheTTL {
		return s.indexCache
	}

	services, err := s.source.ListServices(ctx)
	if err != nil {
		if s.indexCache != nil {
			return s.indexCache // return stale on error
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

	// Resolve dependency names using ref aliases (e.g., "my-svc-pacto" -> "my-svc").
	aliases := buildRefAliases(index)
	for _, d := range index {
		for i, dep := range d.Dependencies {
			d.Dependencies[i].Name = resolveServiceName(dep.Name, index, aliases)
		}
	}

	s.indexCache = &serviceIndexCache{
		services: services,
		index:    index,
		aliases:  aliases,
		builtAt:  time.Now(),
	}
	return s.indexCache
}

func (s *Server) handleGetDependents(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cached := s.getCachedIndex(r.Context())
	aliases := cached.aliases

	var dependents []DependentInfo
	for _, d := range cached.index {
		for _, dep := range d.Dependencies {
			if depRefMatchesName(dep.Ref, name, aliases) {
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

	writeJSON(w, dependents)
}

func (s *Server) handleGetCrossRefs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cached := s.getCachedIndex(r.Context())
	aliases := cached.aliases

	target := cached.index[name]
	if target == nil {
		writeJSON(w, CrossReferences{})
		return
	}

	result := CrossReferences{}

	// Outgoing: config/policy refs from this service.
	result.References = appendOutgoingRef(result.References, configRef(target), "config", cached.index, aliases)
	result.References = appendOutgoingRef(result.References, policyRef(target), "policy", cached.index, aliases)

	// Incoming: scan all services in cached index.
	for svcName, d := range cached.index {
		if svcName == name {
			continue
		}
		result.ReferencedBy = appendIncomingRef(result.ReferencedBy, d, name, "config", configRef(d), cached.index, aliases)
		result.ReferencedBy = appendIncomingRef(result.ReferencedBy, d, name, "policy", policyRef(d), cached.index, aliases)
	}

	writeJSON(w, result)
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

func (s *Server) handleGetSources(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.sourceInfo)
}

func (s *Server) handleDebugSources(w http.ResponseWriter, r *http.Request) {
	debug := struct {
		Sources     []SourceInfo       `json:"sources"`
		Diagnostics *SourceDiagnostics `json:"diagnostics,omitempty"`
		Live        *liveDebugInfo     `json:"live,omitempty"`
	}{
		Sources:     s.sourceInfo,
		Diagnostics: s.diagnostics,
	}

	// Add live service counts.
	if s.source != nil {
		live := &liveDebugInfo{}
		services, err := s.source.ListServices(r.Context())
		if err != nil {
			live.Error = err.Error()
		} else {
			live.ServiceCount = len(services)
			for _, svc := range services {
				live.ServiceNames = append(live.ServiceNames, svc.Name)
			}
		}
		debug.Live = live
	}

	writeJSON(w, debug)
}

func (s *Server) handleDebugServices(w http.ResponseWriter, r *http.Request) {
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

	type debugServicesOutput struct {
		PerSource      []perSourceResult   `json:"perSource"`
		AggregatedList []debugServiceEntry `json:"aggregatedList"`
	}

	out := debugServicesOutput{}

	// Query each source independently.
	if s.aggregated != nil {
		for _, st := range s.aggregated.SourceTypes() {
			ds := s.aggregated.sources[st]
			result := perSourceResult{SourceType: st}
			svcs, err := ds.ListServices(r.Context())
			if err != nil {
				result.Error = err.Error()
			} else {
				result.Count = len(svcs)
				result.Services = svcs
			}
			out.PerSource = append(out.PerSource, result)
		}
	}

	// Query the aggregated list.
	services, err := s.source.ListServices(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, svc := range services {
		out.AggregatedList = append(out.AggregatedList, debugServiceEntry{
			Name:             svc.Name,
			MergedSource:     svc.Source,
			MergedSources:    svc.Sources,
			MergedPhase:      svc.Phase,
			MergedVersion:    svc.Version,
			PresentInSources: svc.Sources,
		})
	}

	writeJSON(w, out)
}

type liveDebugInfo struct {
	ServiceCount int      `json:"serviceCount"`
	ServiceNames []string `json:"serviceNames,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
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
