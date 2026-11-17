package dashboard

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/trianalab/pacto/v4/pkg/backstage"
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
// full catalog and orphans every entity missing from it. Whether records are
// missing is judged on the published snapshot before the reader's view
// narrows it (see [BackstageServable]), so a reader whose view holds nothing
// of a whole fleet gets an empty 200.
func (s *Server) backstageEntities(w http.ResponseWriter, r *http.Request) {
	refuse := func(why string) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, why, http.StatusServiceUnavailable)
	}
	base, err := s.opts.Fleet(r.Context())
	if err == nil && base == nil {
		err = errors.New("no snapshot was published")
	}
	if err != nil {
		refuse("the fleet snapshot is not available yet: " + err.Error())
		return
	}
	if why, ok := BackstageServable(base.SnapshotForSerialization()); !ok {
		refuse(why + ", so Backstage keeps the entities it has")
		return
	}
	q, err := s.view(r.Context(), base)
	if err != nil {
		refuse("could not narrow the fleet to this reader: " + err.Error())
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
