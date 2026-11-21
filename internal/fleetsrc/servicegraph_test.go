package fleetsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// graphServer is a Prometheus answering every query with body, recording the
// last query.
func graphServer(t *testing.T, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		query = r.URL.Query().Get("query")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &query
}

// edge is one service-graph series of v calls from client to server.
func edge(client, server, v string) string {
	b, _ := json.Marshal(map[string]any{"metric": map[string]string{"client": client, "server": server}, "value": []any{float64(jaegerNow.Unix()), v}})
	return string(b)
}

// graphSource reads endpoint for a complete entry of kind.
func graphSource(kind, endpoint string) (*ServiceGraphSource, *LiveEdges) {
	e := detect.Entry{Kind: kind, Value: kind, Evidence: "running in 1 workload", Completeness: detect.Complete, Endpoint: endpoint,
		Objects: []detect.Object{{Kind: "Service", Namespace: "monitoring", Name: "prometheus"}}}
	res := detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: jaegerNow.Add(-time.Minute), Entries: []detect.Entry{e}}
	live := NewLiveEdges(clockAt(jaegerNow))
	return NewServiceGraphSource("detected-service-graph", res, e, live, clockAt(jaegerNow)), live
}

func collectGraph(t *testing.T, s *ServiceGraphSource) (*fleet.Collection, fleet.Provider) {
	t.Helper()
	col, err := s.Collect(context.Background())
	if err != nil || col == nil || !col.Advisory || !col.ObservedUndeclaredOnly || col.State == nil || len(col.Providers) != 1 {
		t.Fatalf("Collect = %+v, %v", col, err)
	}
	if s.ID() != "detected-service-graph" || s.Kind() != fleet.SourceKindTraces {
		t.Fatalf("id %q kind %q", s.ID(), s.Kind())
	}
	return col, col.Providers[0]
}

// reason is the row's EDGE_SOURCE_PARTIAL message, "" when it has none.
func reason(p fleet.Provider) string {
	for _, l := range p.Limitations {
		if l.Code == fleet.LimitationEdgeSourcePartial {
			return l.Message
		}
	}
	return ""
}

func TestServiceGraphSourceMapsSeries(t *testing.T) {
	srv, _ := graphServer(t, 200, vector(edge("checkout", "payments", "6.6"), edge("cart", "checkout", "2"), edge("cart", "cart", "9"), edge("a", "b", "0")))
	s, live := graphSource("tempo", srv.URL)
	col, row := collectGraph(t, s)
	if len(col.Observed) != 2 || col.Observed[0].From != "checkout" || col.Observed[0].Count != 7 || col.Observed[1].To != "checkout" {
		t.Fatalf("observed = %+v", col.Observed)
	}
	if row.ID != "tempo" || row.Name != serviceGraphName || row.Status != fleet.SourceAvailable || col.State.Status != fleet.SourceAvailable ||
		!strings.Contains(row.Evidence, "2 edges in the last 15 minutes") || row.UsedFor[0] != fleet.UseObservedEdges ||
		row.Provenance != fleet.ProvenanceInferred || !row.AsOf.Equal(jaegerNow) {
		t.Fatalf("row = %+v, state %+v", row, col.State)
	}
	if c, ok := live.complete[serviceGraphName]; !ok || len(c.edges) != 2 {
		t.Fatalf("complete coverage = %+v, %v", c, ok)
	}
}

func TestServiceGraphEdgesCarryTheReadWindow(t *testing.T) {
	srv, _ := graphServer(t, 200, vector(edge("a", "b", "1")))
	s, _ := graphSource("tempo", srv.URL)
	col, _ := collectGraph(t, s)
	if e := col.Observed[0]; !e.FirstSeen.Equal(jaegerNow.Add(-15*time.Minute)) || !e.LastSeen.Equal(jaegerNow) {
		t.Fatalf("window = %v..%v", e.FirstSeen, e.LastSeen)
	}
}

func TestServiceGraphSourceSendsTheBoundedQuery(t *testing.T) {
	srv, query := graphServer(t, 200, vector())
	s, _ := graphSource("tempo", srv.URL)
	collectGraph(t, s)
	if !strings.Contains(*query, "topk(5000,") || !strings.Contains(*query, "[15m]") || !strings.Contains(*query, "traces_service_graph_request_total") {
		t.Fatalf("query = %q", *query)
	}
}

func TestServiceGraphSourceFromCollectorOnly(t *testing.T) {
	srv, _ := graphServer(t, 200, vector(edge("a", "b", "1")))
	s, _ := graphSource("otel-collector", srv.URL)
	_, row := collectGraph(t, s)
	if row.ID != "service-graph" || row.Name != serviceGraphName || row.Status != fleet.SourceAvailable {
		t.Fatalf("row = %+v", row)
	}
}

func TestServiceGraphSourceTempoWithoutPrometheusIsPartial(t *testing.T) {
	s, live := graphSource("tempo", "")
	col, row := collectGraph(t, s)
	if row.Status != fleet.SourcePartial || col.State.Status != fleet.SourcePartial || reason(row) != serviceGraphNoProm || len(col.Observed) != 0 || len(live.complete) != 0 {
		t.Fatalf("row = %+v, state %+v", row, col.State)
	}
}

func TestServiceGraphSourceNoSeriesIsPartial(t *testing.T) {
	srv, _ := graphServer(t, 200, vector())
	s, live := graphSource("tempo", srv.URL)
	_, row := collectGraph(t, s)
	if row.Status != fleet.SourcePartial || reason(row) != serviceGraphNoSeries || len(live.complete) != 0 {
		t.Fatalf("row = %+v", row)
	}
}

func TestServiceGraphSourceCapReachedIsPartial(t *testing.T) {
	items := make([]string, maxServiceGraphSeries)
	for i := range items {
		items[i] = edge(fmt.Sprintf("c%d", i), "server", "1")
	}
	srv, _ := graphServer(t, 200, vector(items...))
	s, live := graphSource("tempo", srv.URL)
	col, row := collectGraph(t, s)
	if row.Status != fleet.SourcePartial || !strings.Contains(reason(row), "5000 busiest") || len(col.Observed) != maxServiceGraphSeries || len(live.complete) != 0 {
		t.Fatalf("row = %+v, %d edges", row, len(col.Observed))
	}
}

func TestServiceGraphSourceDownIsPartialNotUnavailable(t *testing.T) {
	srv, _ := graphServer(t, 503, "down")
	s, _ := graphSource("tempo", srv.URL)
	col, row := collectGraph(t, s)
	if col.State.Status != fleet.SourcePartial || row.Status != fleet.SourceUnavailable || reason(row) != "the Prometheus Pacto found refused the query (HTTP 503)" ||
		strings.Contains(row.Evidence, "edges in the last") {
		t.Fatalf("row = %+v, state %+v", row, col.State)
	}
}

func TestServiceGraphSourceRefusesRedirects(t *testing.T) {
	target, query := graphServer(t, 200, `{"status":"success","data":{"resultType":"vector","result":[`+edge("a", "b", "3")+`]}}`)
	srv := httptest.NewServer(http.RedirectHandler(target.URL+"/api/v1/query", http.StatusFound))
	t.Cleanup(srv.Close)
	s, _ := graphSource("tempo", srv.URL)
	col, row := collectGraph(t, s)
	if *query != "" || len(col.Observed) != 0 || row.Status != fleet.SourceUnavailable || reason(row) != "the Prometheus Pacto found refused the query (HTTP 302)" {
		t.Fatalf("redirect followed: query %q, row %+v", *query, row)
	}
}

func TestServiceGraphSourceSkipsUserVirtualNode(t *testing.T) {
	srv, query := graphServer(t, 200, vector(edge("user", "frontend", "5"), edge("frontend", "user", "1"), edge("frontend", "api", "1")))
	s, _ := graphSource("tempo", srv.URL)
	col, row := collectGraph(t, s)
	if !strings.Contains(*query, `client!="user",server!="user"`) || len(col.Observed) != 1 || col.Observed[0].To != "api" || row.Status != fleet.SourceAvailable {
		t.Fatalf("query %q, observed %+v, row %+v", *query, col.Observed, row)
	}
}

func TestServiceGraphSourceDropsUnattributableNames(t *testing.T) {
	srv, _ := graphServer(t, 200, vector(edge("unknown", "api", "3"), edge("k8s:ns/x", "api", "1"), edge("web", "api", "1")))
	s, live := graphSource("tempo", srv.URL)
	col, row := collectGraph(t, s)
	if len(col.Observed) != 1 || row.Status != fleet.SourcePartial || reason(row) != "2 call pairs are not attributable to a named service" || len(live.complete) != 0 {
		t.Fatalf("observed %+v, row %+v", col.Observed, row)
	}
}
