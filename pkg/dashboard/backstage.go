package dashboard

import (
	"bytes"
	"net/http"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// RegisterRawRoutes registers the routes huma cannot describe: today only GET
// /api/backstage/entities, multi-document YAML for a Backstage catalog
// location. They are absent from the OpenAPI document on purpose.
func (s *Server) RegisterRawRoutes(mux *http.ServeMux) {
	if s.opts.Fleet == nil {
		return
	}
	mux.HandleFunc("GET /api/backstage/entities", s.backstageEntities)
}

// backstageEntities serves the caller's view as Backstage entities. Anything
// short of a whole view is a 503: Backstage reads a successful answer as the
// full catalog and orphans every entity missing from it.
func (s *Server) backstageEntities(w http.ResponseWriter, r *http.Request) {
	q, err := s.fleetQuery(r.Context())
	if err != nil {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "the fleet snapshot is not available yet: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if id, status, ok := missingRecords(q); ok {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "source "+id+" is "+status+", so Backstage keeps the entities it has", http.StatusServiceUnavailable)
		return
	}
	// Buffered so a 200 is never a truncated catalog.
	var buf bytes.Buffer
	_ = backstage.Encode(&buf, backstage.Map(q)) // ponytail: bytes.Buffer never fails and yaml.v3 errors only come from the writer; 516-S1 tests Encode's error with a failing writer
	h := w.Header()
	h.Set("Content-Type", "application/yaml; charset=utf-8")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Vary", "Authorization, Cookie")
	_, _ = w.Write(buf.Bytes())
}

// missingRecords names the first source of q whose records may be missing:
// one that is unavailable, or partial for a reason other than reading a
// declared namespace list. A stale source keeps its records.
func missingRecords(q *fleet.Query) (id, status string, ok bool) {
	snap := q.SnapshotForSerialization()
	for _, st := range snap.Sources {
		if st.Status == fleet.SourceUnavailable || st.Status == fleet.SourcePartial && !onlyScoped(snap.Limitations, st.ID) {
			return st.ID, string(st.Status), true
		}
	}
	return "", "", false
}

// onlyScoped reports whether source's only limitations are its namespace
// scope and the SOURCE_PARTIAL that Build adds to every partial source.
// ponytail: a SOURCE_PARTIAL a scoped source raised itself would pass too;
// no Kubernetes source raises one today, it reports K8S_NAMESPACE_UNREADABLE.
func onlyScoped(ls []fleet.Limitation, source string) bool {
	scoped := false
	for _, l := range ls {
		if l.Source != source {
			continue
		}
		switch l.Code {
		case fleet.LimitationK8sNamespaceScoped:
			scoped = true
		case fleet.LimitationSourcePartial:
		default:
			return false
		}
	}
	return scoped
}
