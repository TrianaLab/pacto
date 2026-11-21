package dashboard

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetgen"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var mapNow = time.Date(2026, 11, 20, 9, 40, 0, 0, time.UTC)

// genQuery is the query over a generated fleet of n services.
func genQuery(t testing.TB, n int) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return mapNow }},
		fleet.NewMemorySource("fleetgen", fleet.SourceKindKubernetes, fleetgen.Collection(n, 1, mapNow)))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func mapServer(t testing.TB, o Options) string {
	t.Helper()
	srv := httptest.NewServer(NewServer(o).APIHandler())
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFleetMapRoute(t *testing.T) {
	q := genQuery(t, 400)
	base := mapServer(t, Options{Fleet: fleetOf(q)})
	var m fleet.FleetMap
	getJSON(t, base+"/api/fleet/map", http.StatusOK, &m)
	want, _ := q.Map(fleet.MapQuery{})
	if m.GroupBy != fleet.MapGroupByDomain || len(m.Groups) != len(want.Groups) || m.Totals != want.Totals || m.Meta.SnapshotID == "" {
		t.Errorf("map = %s, %d groups, totals %+v", m.GroupBy, len(m.Groups), m.Totals)
	}
	getJSON(t, base+"/api/fleet/map?groupBy=team&environment=dev", http.StatusOK, &m)
	if m.GroupBy != fleet.MapGroupByTeam {
		t.Errorf("groupBy = %s", m.GroupBy)
	}
	expectStatus(t, base+"/api/fleet/map?groupBy=planet", http.StatusUnprocessableEntity)
	errBase := mapServer(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return nil, context.Canceled }})
	for _, p := range []string{"/api/fleet/map", "/api/fleet/map/nodes?group=x", "/api/fleet/map/focus?service=x"} {
		expectStatus(t, errBase+p, http.StatusServiceUnavailable)
	}
}

func TestFleetMapNodesRoute(t *testing.T) {
	q := genQuery(t, 400)
	base := mapServer(t, Options{Fleet: fleetOf(q)})
	m, _ := q.Map(fleet.MapQuery{})
	g := m.Groups[0]
	var page fleet.MapNodes
	getJSON(t, base+"/api/fleet/map/nodes?group="+g.Key+"&limit=2&offset=1&environment=prod-eu", http.StatusOK, &page)
	if page.Group != g.Key || len(page.Nodes) != 2 || page.Total != g.Services || !page.Truncated || page.Nodes[0].InEnvironment == nil {
		t.Errorf("page = %+v", page)
	}
	expectStatus(t, base+"/api/fleet/map/nodes?group=ghost", http.StatusNotFound)
	expectStatus(t, base+"/api/fleet/map/nodes?group="+g.Key+"&limit=6000", http.StatusUnprocessableEntity)
	expectStatus(t, base+"/api/fleet/map/nodes", http.StatusUnprocessableEntity)
}

func TestFleetMapFocusRoute(t *testing.T) {
	q := genQuery(t, 400)
	base := mapServer(t, Options{Fleet: fleetOf(q)})
	var f ProductMapFocus
	getJSON(t, base+"/api/fleet/map/focus?service=platform-00000&hops=2", http.StatusOK, &f)
	if f.Focus.Name != "platform-00000" || f.Hops != 2 || len(f.Columns) != 4 || f.Focus.Href == "" {
		t.Fatalf("focus = %+v", f.MapFocusAnswer)
	}
	for _, c := range f.Columns {
		for _, n := range c.Nodes {
			if !strings.HasPrefix(n.Href, "/fleet/services/") {
				t.Errorf("node %s href %q", n.Name, n.Href)
			}
		}
	}
	for _, e := range f.Edges {
		if e.From.Href == "" || e.To.Href == "" || e.Href == "" {
			t.Errorf("edge %s -> %s without links", e.From.Key, e.To.Key)
		}
	}
	expectStatus(t, base+"/api/fleet/map/focus?service=ghost", http.StatusNotFound)
	expectStatus(t, base+"/api/fleet/map/focus?service=platform-00000&hops=3", http.StatusUnprocessableEntity)
}

func TestFleetMapEnvironmentOrderFromStages(t *testing.T) {
	q := genQuery(t, 400)
	api := stagesDelivery{stages: []delivery.StageObject{
		stageTo("dev", "dev", ""), stageTo("staging", "staging", "dev"), stageTo("eu", "prod-eu", "staging"), stageTo("us", "prod-us", "eu"),
	}}
	base := mapServer(t, Options{Fleet: fleetOf(q), Delivery: func(*http.Request) (DeliveryAPI, error) { return api, nil }})
	var m fleet.FleetMap
	getJSON(t, base+"/api/fleet/map?groupBy=environment", http.StatusOK, &m)
	if got := envColumns(m); got != "dev,staging,prod-eu,prod-us" || m.EnvironmentOrder != "stages" || hasLimitation(m.Meta.Limitations, LimitationEnvironmentOrderUnknown) {
		t.Errorf("stage order = %s (%s), limitations %+v", got, m.EnvironmentOrder, m.Meta.Limitations)
	}
	// No delivery: by name, and the answer says why.
	getJSON(t, mapServer(t, Options{Fleet: fleetOf(q)})+"/api/fleet/map?groupBy=environment", http.StatusOK, &m)
	if got := envColumns(m); got != "dev,prod-eu,prod-us,staging" || m.EnvironmentOrder != "name" || !hasLimitation(m.Meta.Limitations, LimitationEnvironmentOrderUnknown) {
		t.Errorf("name order = %s (%s), limitations %+v", got, m.EnvironmentOrder, m.Meta.Limitations)
	}
}

// envColumns is the environment groups in column order.
func envColumns(m fleet.FleetMap) string {
	cols := make([]string, len(m.Groups))
	n := 0
	for _, g := range m.Groups {
		if g.Kind == fleet.MapKindEnvironment && g.Depth != nil {
			cols[*g.Depth] = g.Key
			n = max(n, *g.Depth+1)
		}
	}
	return strings.Join(cols[:n], ",")
}

// TestFleetMapRestrictedViewer: a viewer who may read only the platform
// namespace sees no service of another, in any count, page or focus.
func TestFleetMapRestrictedViewer(t *testing.T) {
	q := genQuery(t, 400)
	base := serveOptions(t, Options{
		Fleet: fleetOf(q),
		View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(_, ns string) bool { return ns == "platform" }, nil), nil
		},
		Auth: &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h }, State: func(*http.Request) AuthState { return AuthState{SignedIn: true} }},
	})
	full, _ := q.Map(fleet.MapQuery{})
	var m fleet.FleetMap
	getJSON(t, base+"/api/fleet/map", http.StatusOK, &m)
	if m.Totals.Services >= full.Totals.Services {
		t.Errorf("restricted totals %+v, full %+v", m.Totals, full.Totals)
	}
	for _, g := range m.Groups {
		if g.Key == "payments" || g.Key == "checkout" {
			t.Errorf("hidden domain %s is a group", g.Key)
		}
	}
	resp, err := http.Get(base + "/api/fleet/map")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if body := string(raw); strings.Contains(body, "payments-") || strings.Contains(body, "checkout-") {
		t.Error("the restricted map names a hidden service")
	}
	expectStatus(t, base+"/api/fleet/map/nodes?group=payments", http.StatusNotFound)
	expectStatus(t, base+"/api/fleet/map/focus?service=payments-00010", http.StatusNotFound)
}
