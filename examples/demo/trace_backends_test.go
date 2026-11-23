package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoTraceBackendRowsMatchTheDashboard: each demo trace row is the row
// the dashboard's own source reports for the same live state: a Prometheus
// holding the demo's observed calls as service-graph series, a Jaeger with no
// dependency and an OTLP receiver turned off by the service graph.
func TestDemoTraceBackendRowsMatchTheDashboard(t *testing.T) {
	res, err := demoDetection(demoClusterID)
	if err != nil {
		t.Fatal(err)
	}
	_, demo := demoTraceSources(res, demoNow())
	want := map[string]fleet.Provider{}
	for _, s := range demo {
		col, err := s.Collect(context.Background())
		if err != nil || len(col.Providers) != 1 {
			t.Fatalf("%s: %+v, %v", s.ID(), col, err)
		}
		want[s.ID()] = col.Providers[0]
	}
	for id, got := range dashboardTraceRows(t, res) {
		if !reflect.DeepEqual(got, want[id]) {
			t.Errorf("%s:\n demo      %+v\n dashboard %+v", id, want[id], got)
		}
	}
	if len(want) != 3 {
		t.Errorf("demo trace rows = %+v, want three", want)
	}
}

// dashboardTraceRows runs the dashboard's three edge sources over res, each
// backend served by a test server, in the order a refresh settles them: the
// service graph wins before the receiver reads.
func dashboardTraceRows(t *testing.T, res detect.Result) map[string]fleet.Provider {
	t.Helper()
	now := func() time.Time { return demoNow() }
	entry := func(kind, endpoint string) detect.Entry {
		e := res.Entries[slices.IndexFunc(res.Entries, func(e detect.Entry) bool { return e.Kind == kind })]
		e.Endpoint = endpoint
		return e
	}
	var series []map[string]any
	for _, e := range demoObservedEdges(demoNow()) {
		series = append(series, map[string]any{"metric": map[string]string{"client": e.From, "server": e.To}, "value": []any{float64(demoNow().Unix()), strconv.Itoa(e.Count)}})
	}
	prom := serve(t, map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": series}})
	jaeger := serve(t, map[string]any{"data": []any{}})
	live := fleetsrc.NewLiveEdges(now)
	r, err := fleetsrc.NewReceiver("http://pacto-dashboard.pacto-system:4318", now)
	if err != nil {
		t.Fatal(err)
	}
	live.Receiver = r
	collector := entry("otel-collector", "")
	sources := []fleet.Source{
		fleetsrc.NewServiceGraphSource(demoServiceGraphID, res, entry("tempo", prom), live, now),
		fleetsrc.NewJaegerSource(demoJaegerID, res, entry("jaeger", jaeger), live, now),
		fleetsrc.NewReceiverSource(demoReceiverID, &res, &collector, live),
	}
	rows := map[string]fleet.Provider{}
	for _, s := range sources {
		col, err := s.Collect(context.Background())
		if err != nil || len(col.Providers) != 1 {
			t.Fatalf("%s: %+v, %v", s.ID(), col, err)
		}
		rows[s.ID()] = col.Providers[0]
	}
	return rows
}

// serve is a test server answering every request with body as JSON.
func serve(t *testing.T, body any) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}
