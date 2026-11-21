package fleetsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var jaegerNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// jaegerServer answers /api/dependencies with links, recording the query.
func jaegerServer(t *testing.T, links any) (*httptest.Server, *string) {
	t.Helper()
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/dependencies" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"data": links})
	}))
	t.Cleanup(srv.Close)
	return srv, &query
}

// jaegerSource reads endpoint for an entry naming candidates query Services,
// partial with several as the detector writes it.
func jaegerSource(endpoint string, candidates int) (*JaegerSource, *LiveEdges) {
	e := detect.Entry{Kind: "jaeger", Value: "Jaeger", Evidence: "query API on port 16686", Completeness: detect.Complete, Endpoint: endpoint}
	for i := range candidates {
		e.Objects = append(e.Objects, detect.Object{Kind: "Service", Namespace: "observability", Name: fmt.Sprintf("jaeger-query-%d", i)})
	}
	if candidates > 1 {
		e.Completeness, e.Reason = detect.Partial, fmt.Sprintf("%d Jaeger query Services found; Pacto read none of them as complete", candidates)
	}
	res := detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: jaegerNow.Add(-time.Minute), Entries: []detect.Entry{e}}
	live := NewLiveEdges(clockAt(jaegerNow))
	return NewJaegerSource("detected-jaeger", res, e, live, clockAt(jaegerNow)), live
}

func collectJaeger(t *testing.T, s *JaegerSource) (*fleet.Collection, fleet.Provider) {
	t.Helper()
	col, err := s.Collect(context.Background())
	if err != nil || col == nil || !col.Advisory || !col.ObservedUndeclaredOnly || col.State == nil || len(col.Providers) != 1 {
		t.Fatalf("Collect = %+v, %v", col, err)
	}
	return col, col.Providers[0]
}

func link(parent, child string, calls int) map[string]any {
	return map[string]any{"parent": parent, "child": child, "callCount": calls}
}

func TestJaegerSourceMapsDependencies(t *testing.T) {
	srv, query := jaegerServer(t, []any{link("checkout", "payments", 3), link("checkout", "db.orders", 2), link("checkout", "payments", 4), link("cart", "cart", 9)})
	s, live := jaegerSource(srv.URL, 1)
	col, row := collectJaeger(t, s)
	if len(col.Observed) != 2 || col.Observed[0].To != "db.orders" || col.Observed[1].To != "payments" || col.Observed[1].Count != 7 {
		t.Fatalf("observed = %+v", col.Observed)
	}
	if row.Status != fleet.SourceAvailable || col.State.Status != fleet.SourceAvailable || !strings.Contains(row.Evidence, "2 edges in the last 15 minutes") ||
		row.UsedFor[0] != fleet.UseObservedEdges || row.Provenance != fleet.ProvenanceInferred {
		t.Fatalf("row = %+v, state %+v", row, col.State)
	}
	if want := fmt.Sprintf("endTs=%d&lookback=900000", jaegerNow.UnixMilli()); *query != want {
		t.Fatalf("query = %q, want %q", *query, want)
	}
	if c, ok := live.complete["Jaeger"]; !ok || !c.at.Equal(jaegerNow) || len(c.edges) != 2 {
		t.Fatalf("complete coverage = %+v, %v", c, ok)
	}
}

func TestJaegerEdgesCarryTheReadWindow(t *testing.T) {
	srv, _ := jaegerServer(t, []any{link("a", "b", 1)})
	s, _ := jaegerSource(srv.URL, 1)
	col, row := collectJaeger(t, s)
	e := col.Observed[0]
	if !e.FirstSeen.Equal(jaegerNow.Add(-15*time.Minute)) || !e.LastSeen.Equal(jaegerNow) || row.AsOf == nil || !row.AsOf.Equal(jaegerNow) {
		t.Fatalf("edge = %+v, row asOf %v", e, row.AsOf)
	}
}

func TestJaegerSourceNoLinksIsPartial(t *testing.T) {
	srv, _ := jaegerServer(t, []any{})
	s, live := jaegerSource(srv.URL, 1)
	col, row := collectJaeger(t, s)
	if row.Status != fleet.SourcePartial || col.State.Status != fleet.SourcePartial || len(row.Limitations) != 1 ||
		row.Limitations[0].Code != fleet.LimitationEdgeSourcePartial || !strings.Contains(row.Limitations[0].Message, "spark-dependencies") {
		t.Fatalf("row = %+v", row)
	}
	if len(live.complete) != 0 {
		t.Fatal("an empty read counted as complete coverage")
	}
}

func TestJaegerSourceSeveralCandidatesIsPartialAndNeverWins(t *testing.T) {
	srv, _ := jaegerServer(t, []any{link("a", "b", 1)})
	s, live := jaegerSource(srv.URL, 2)
	col, row := collectJaeger(t, s)
	// Anyone may have made one of them: the edges add callers and never
	// corroborate.
	if row.Status != fleet.SourcePartial || len(col.Observed) != 1 || !col.ObservedCautionOnly ||
		!strings.Contains(row.Limitations[0].Message, "2 Jaeger query Services found; Pacto read none of them as complete") {
		t.Fatalf("row = %+v", row)
	}
	if single, _ := jaegerSource(srv.URL, 1); collectCol(t, single).ObservedCautionOnly {
		t.Fatal("a single complete candidate is caution-only")
	}
	if len(live.complete) != 0 {
		t.Fatal("several candidates won")
	}
}

func TestJaegerSourceDownIsPartialNotUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	s, _ := jaegerSource(srv.URL, 1)
	col, row := collectJaeger(t, s)
	if col.State.Status != fleet.SourcePartial || row.Status != fleet.SourceUnavailable || len(col.Observed) != 0 ||
		row.Limitations[0].Message != "the Jaeger query API did not answer" {
		t.Fatalf("state %+v, row %+v", col.State, row)
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return jaegerNow }}, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 1 || snap.Providers[0].Source != "detected-jaeger" || snap.Providers[0].Status != fleet.SourceUnavailable {
		t.Fatalf("providers = %+v", snap.Providers)
	}
}

func TestJaegerSourceRefusesRedirect(t *testing.T) {
	target, _ := jaegerServer(t, []any{link("a", "b", 1)})
	srv := httptest.NewServer(http.RedirectHandler(target.URL+"/api/dependencies", http.StatusFound))
	t.Cleanup(srv.Close)
	s, _ := jaegerSource(srv.URL, 1)
	col, row := collectJaeger(t, s)
	if len(col.Observed) != 0 || row.Limitations[0].Message != "the Jaeger query API answered 302" {
		t.Fatalf("row = %+v", row)
	}
}

func TestJaegerSourceCapsLinks(t *testing.T) {
	links := make([]any, maxJaegerLinks+1)
	for i := range links {
		links[i] = link(fmt.Sprintf("svc-%d", i), "sink", 1)
	}
	srv, _ := jaegerServer(t, links)
	s, live := jaegerSource(srv.URL, 1)
	col, row := collectJaeger(t, s)
	if len(col.Observed) != maxJaegerLinks || row.Status != fleet.SourcePartial || !strings.Contains(row.Limitations[0].Message, "the read stopped at 5000 dependencies") {
		t.Fatalf("edges = %d, row %+v", len(col.Observed), row.Limitations)
	}
	if len(live.complete) != 0 {
		t.Fatal("a capped read counted as complete coverage")
	}
}

func TestJaegerSourceDropsK8sShapedNames(t *testing.T) {
	srv, _ := jaegerServer(t, []any{link("k8s:shop/Deployment/cart", "payments", 1), link("unknown", "b", 1), link("a", "b", 0), link("a", "b", 1)})
	s, live := jaegerSource(srv.URL, 1)
	col, row := collectJaeger(t, s)
	if len(col.Observed) != 1 || col.Observed[0].From != "a" || row.Status != fleet.SourcePartial ||
		!strings.Contains(row.Limitations[0].Message, "dependencies naming no attributable service: 2; dependencies with no calls: 1") {
		t.Fatalf("observed %+v, row %+v", col.Observed, row)
	}
	if len(live.complete) != 0 {
		t.Fatal("a read that dropped calls counted as complete coverage")
	}
}

func TestJaegerSourceErrorNamesNoURL(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret-body {")) }))
	t.Cleanup(bad.Close)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "secret-body", http.StatusBadGateway) }))
	t.Cleanup(failing.Close)
	for endpoint, want := range map[string]string{
		bad.URL:          "the Jaeger query API answered something Pacto cannot read",
		failing.URL:      "the Jaeger query API answered 502",
		"":               "the detection names no in-cluster Jaeger query URL",
		"http://x\x7f.y": "the detection names an unusable Jaeger query URL",
	} {
		s, _ := jaegerSource(endpoint, 1)
		_, row := collectJaeger(t, s)
		msg := row.Limitations[0].Message
		if msg != want || strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "secret-body") {
			t.Errorf("%q: reason = %q, want %q", endpoint, msg, want)
		}
	}
	if s, _ := jaegerSource("", 1); s.ID() != "detected-jaeger" || s.Kind() != fleet.SourceKindTraces {
		t.Fatalf("id/kind = %s/%s", s.ID(), s.Kind())
	}
}

// collectCol is s's collection.
func collectCol(t *testing.T, s fleet.Source) *fleet.Collection {
	t.Helper()
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return col
}

// Every refresh builds new sources: each closes its connection once read, so
// none is left idle behind a discarded client.
func TestEdgeSourcesCloseTheirConnections(t *testing.T) {
	for name, serve := range map[string]func(w http.ResponseWriter){
		"jaeger": func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"data":[{"parent":"a","child":"b","callCount":1}]}`))
		},
		"graph": func(w http.ResponseWriter) { _, _ = w.Write([]byte(vector(edge("a", "b", "1")))) },
	} {
		closed := make(chan struct{}, 1)
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { serve(w) }))
		srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
			if st == http.StateClosed {
				closed <- struct{}{}
			}
		}
		srv.Start()
		var s fleet.Source
		if name == "jaeger" {
			s, _ = jaegerSource(srv.URL, 1)
		} else {
			s, _ = graphSource("tempo", srv.URL)
		}
		collectCol(t, s)
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Errorf("%s left its connection open", name)
		}
		srv.Close()
	}
}
