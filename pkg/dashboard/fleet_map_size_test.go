package dashboard

import (
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// mapBudget is one fleet map request and what it may cost (Review note N5).
// The race build checks bytes only; the !race budget test checks time too.
type mapBudget struct {
	name     string
	path     string
	maxTime  time.Duration
	maxBytes int
}

// mapBudgets are the map requests over a generated fleet of n services: the
// three groupings, a page of the largest domain group and of the largest
// environment group (1000 targets), and a two-hop focus on a hub.
//
// ponytail: an environment page carries target keys, each with its
// cluster's UID, so a page of 1000 is about 400 KB; its bound is 450 KB
// until the transport compresses or the browser pages smaller. Past 40 teams
// the team map nests every team as a sub-cluster of its domain, so at 10000
// services (400 teams) it is held to 250 KB.
func mapBudgets(t testing.TB, q *fleet.Query, n int) []mapBudget {
	t.Helper()
	mapTime, mapBytes, teamBytes := 300*time.Millisecond, 80_000, 80_000
	if n > 3000 {
		mapTime, mapBytes, teamBytes = 900*time.Millisecond, 200_000, 250_000
	}
	dom, err := q.Map(fleet.MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := q.Map(fleet.MapQuery{GroupBy: fleet.MapGroupByEnvironment})
	hub, err := q.MapNodes(fleet.MapNodesQuery{Group: dom.Groups[0].Key, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	return []mapBudget{
		{"map by domain", "/api/fleet/map", mapTime, mapBytes},
		{"map by team", "/api/fleet/map?groupBy=team", mapTime, teamBytes},
		{"map by environment", "/api/fleet/map?groupBy=environment", mapTime, mapBytes},
		{"domain page", "/api/fleet/map/nodes?group=" + url.QueryEscape(dom.Groups[0].Key), 200 * time.Millisecond, 300_000},
		{"environment page", "/api/fleet/map/nodes?groupBy=environment&group=" + url.QueryEscape(env.Groups[0].Key), 200 * time.Millisecond, 450_000},
		{"focus hops 2", "/api/fleet/map/focus?hops=2&service=" + url.QueryEscape(hub.Nodes[0].Key), 100 * time.Millisecond, 60_000},
	}
}

// timedGet GETs base+path and returns how long it took and the body size.
func timedGet(t testing.TB, base, path string) (time.Duration, int) {
	t.Helper()
	start := time.Now()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	took := time.Since(start)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, resp.StatusCode, b)
	}
	return took, len(b)
}

// TestFleetMapSizeBounds: at 3000 services every map answer stays within its
// byte bound, and a node page within its count.
func TestFleetMapSizeBounds(t *testing.T) {
	q := genQuery(t, 3000)
	base := mapServer(t, Options{Fleet: fleetOf(q)})
	for _, b := range mapBudgets(t, q, 3000) {
		if _, size := timedGet(t, base, b.path); size > b.maxBytes {
			t.Errorf("%s: %d bytes, bound %d", b.name, size, b.maxBytes)
		}
	}
	var page fleet.MapNodes
	m, _ := q.Map(fleet.MapQuery{GroupBy: fleet.MapGroupByEnvironment})
	getJSON(t, base+"/api/fleet/map/nodes?groupBy=environment&group="+url.QueryEscape(m.Groups[0].Key), http.StatusOK, &page)
	if len(page.Nodes) != fleet.DefaultMapNodes || !page.Truncated {
		t.Errorf("environment page: %d nodes, truncated %v", len(page.Nodes), page.Truncated)
	}
}
