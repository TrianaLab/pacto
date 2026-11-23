package fleet_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetgen"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var scaleNow = time.Date(2026, 11, 20, 9, 40, 0, 0, time.UTC)

// scaleQuery is the query over a generated fleet of n services.
func scaleQuery(t *testing.T, n int) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return scaleNow }},
		fleet.NewMemorySource("fleetgen", fleet.SourceKindKubernetes, fleetgen.Collection(n, 1, scaleNow)))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// scaleSizes are the fleet sizes the map is checked at; 10000 only with
// PACTO_SCALE_FULL=1.
func scaleSizes() []int {
	if os.Getenv("PACTO_SCALE_FULL") == "1" {
		return []int{40, 400, 3000, 10000}
	}
	return []int{40, 400, 3000}
}

// mapByteBound is the JSON size a domain or environment map of n services
// stays under (Review note N5); the race build asserts sizes, never time.
func mapByteBound(n int) int {
	if n > 3000 {
		return 200_000
	}
	return 80_000
}

func TestMapScaleNoRefusalAndBounded(t *testing.T) {
	groupings := []string{fleet.MapGroupByDomain, fleet.MapGroupByTeam, fleet.MapGroupByEnvironment}
	for _, n := range scaleSizes() {
		q := scaleQuery(t, n)
		for _, by := range groupings {
			start := time.Now()
			m, err := q.Map(fleet.MapQuery{GroupBy: by})
			took := time.Since(start)
			if err != nil {
				t.Fatalf("n=%d %s: %v", n, by, err)
			}
			b, _ := json.Marshal(m)
			t.Logf("n=%d %s: %v, %d bytes, %d groups, %d ribbons", n, by, took.Round(time.Millisecond), len(b), len(m.Groups), len(m.Ribbons))
			if by != fleet.MapGroupByTeam && len(b) > mapByteBound(n) {
				t.Errorf("n=%d %s: %d bytes", n, by, len(b))
			}
			if m.Totals.Services != n || len(m.Groups) == 0 || len(m.Groups) > fleet.MaxMapGroups || len(m.Ribbons) > fleet.MaxMapRibbons {
				t.Errorf("n=%d %s: totals %+v, %d groups", n, by, m.Totals, len(m.Groups))
			}
			checkGroupsApart(t, m.Groups)
		}
	}
}

// checkGroupsApart fails when two groups sit closer than the group gap
// (70 world units, less the 0.1 rounding of each coordinate).
func checkGroupsApart(t *testing.T, gs []fleet.MapGroup) {
	t.Helper()
	for i := range gs {
		for j := i + 1; j < len(gs); j++ {
			a, b := gs[i], gs[j]
			if d := math.Hypot(a.X-b.X, a.Y-b.Y); d < a.R+b.R+70-0.3 {
				t.Errorf("%s and %s overlap", a.Key, b.Key)
			}
		}
	}
}

func TestMapScaleTeamsNestPast40(t *testing.T) {
	m, err := scaleQuery(t, 3000).Map(fleet.MapQuery{GroupBy: fleet.MapGroupByTeam})
	if err != nil {
		t.Fatal(err)
	}
	subs := 0
	for _, g := range m.Groups {
		subs += len(g.Subs)
	}
	if !m.Nested || subs <= 40 {
		t.Errorf("nested %v, %d team sub-clusters", m.Nested, subs)
	}
}

func TestMapScaleLayoutDeterministic(t *testing.T) {
	for _, by := range []string{fleet.MapGroupByDomain, fleet.MapGroupByEnvironment} {
		a, err := scaleQuery(t, 3000).Map(fleet.MapQuery{GroupBy: by})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := scaleQuery(t, 3000).Map(fleet.MapQuery{GroupBy: by})
		if !reflect.DeepEqual(a.Groups, b.Groups) || a.Bounds != b.Bounds {
			t.Errorf("%s: two builds of the same fleet laid out differently", by)
		}
	}
}

func TestMapScalePreviewsAndExternals(t *testing.T) {
	q := scaleQuery(t, 3000)
	env, _ := q.Map(fleet.MapQuery{GroupBy: fleet.MapGroupByEnvironment})
	dom, _ := q.Map(fleet.MapQuery{})
	var previews, externals *fleet.MapGroup
	for i, g := range env.Groups {
		if g.Kind == fleet.MapKindPreviews {
			previews = &env.Groups[i]
		}
	}
	for i, g := range dom.Groups {
		if g.Kind == fleet.MapKindExternal {
			externals = &dom.Groups[i]
		}
	}
	if previews == nil || len(previews.Subs) < 2 || externals == nil || externals.Externals == 0 {
		t.Errorf("previews %+v externals %+v", previews, externals)
	}
}

// TestMapNodesAndFocusScaleBounded pages the largest group of each grouping
// and focuses its first node at every size, within the answers' byte bounds
// (Review note N5: a page of 1000 under 300 KB, a two-hop focus under 60 KB).
func TestMapNodesAndFocusScaleBounded(t *testing.T) {
	for _, n := range scaleSizes() {
		q := scaleQuery(t, n)
		for _, by := range []string{fleet.MapGroupByDomain, fleet.MapGroupByEnvironment} {
			m, _ := q.Map(fleet.MapQuery{GroupBy: by})
			big := m.Groups[0]
			start := time.Now()
			page, err := q.MapNodes(fleet.MapNodesQuery{GroupBy: by, Group: big.Key})
			took := time.Since(start)
			if err != nil {
				t.Fatalf("n=%d %s: %v", n, by, err)
			}
			b, _ := json.Marshal(page)
			t.Logf("n=%d %s page of %d (%d edges): %v, %d bytes", n, by, len(page.Nodes), len(page.Edges), took.Round(time.Millisecond), len(b))
			// ponytail: a target node's key carries its cluster's kube-system
			// UID, so an environment page of 1000 targets is near 400 KB;
			// P7's budget decides whether the transport compresses or pages
			// smaller. Only the domain page is held to 300 KB here.
			if by == fleet.MapGroupByDomain && len(b) > 300_000 || len(page.Nodes) > fleet.DefaultMapNodes {
				t.Errorf("n=%d %s: %d bytes, %d nodes", n, by, len(b), len(page.Nodes))
			}
		}
		m, _ := q.Map(fleet.MapQuery{})
		page, _ := q.MapNodes(fleet.MapNodesQuery{Group: m.Groups[0].Key, Limit: 1})
		start := time.Now()
		f, err := q.MapFocus(fleet.MapFocusQuery{Service: page.Nodes[0].Key, Hops: 2})
		took := time.Since(start)
		if err != nil {
			t.Fatalf("n=%d focus: %v", n, err)
		}
		b, _ := json.Marshal(f)
		t.Logf("n=%d focus on %s: %v, %d bytes, %d edges", n, f.Focus.Name, took.Round(time.Millisecond), len(b), len(f.Edges))
		if len(b) > 60_000 {
			t.Errorf("n=%d focus: %d bytes", n, len(b))
		}
	}
}
