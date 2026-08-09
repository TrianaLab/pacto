package fleet

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// chainFleet builds web -> api -> {db, cache} with a db -> web cycle, all declared
// and resolved, to exercise multi-depth traversal and the visited guard.
func chainFleet(t *testing.T) *Query {
	t.Helper()
	mk := func(name string, deps ...string) *contract.Contract {
		var d []contract.Dependency
		for _, dep := range deps {
			d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true, Compatibility: "^1.0.0"})
		}
		return &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Dependencies: d}
	}
	src := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: mk("web", "api"), FS: fstest.MapFS{}}, Digest: "sha256:web"},
		{Bundle: &contract.Bundle{Contract: mk("api", "db", "cache"), FS: fstest.MapFS{}}, Digest: "sha256:api"},
		{Bundle: &contract.Bundle{Contract: mk("db", "web"), FS: fstest.MapFS{}}, Digest: "sha256:db"},
		{Bundle: &contract.Bundle{Contract: mk("cache"), FS: fstest.MapFS{}}, Digest: "sha256:cache"},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func nodeKeys(nb *Neighborhood) map[string]bool {
	m := map[string]bool{}
	for _, n := range nb.Nodes {
		m[n.Ref.Key] = true
	}
	return m
}

func TestNeighborhood_DefaultExpectedView(t *testing.T) {
	q := productFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if nb.Meta.SchemaVersion != ProductSchemaVersion || nb.Direction != DirectionBoth || nb.Depth != DefaultNeighborhoodDepth {
		t.Errorf("defaults wrong: schema=%q dir=%q depth=%d", nb.Meta.SchemaVersion, nb.Direction, nb.Depth)
	}
	// The default view is expected, so traversal follows declared adjacency only:
	// alpha (focus) + leaf-svc (declared dependency). beta is only an observed
	// dependent, so it is NOT reached in the expected view.
	keys := nodeKeys(nb)
	if !keys["alpha"] || !keys["leaf-svc"] || keys["beta"] || len(nb.Nodes) != 2 {
		t.Fatalf("unexpected nodes: %v", keys)
	}
	// Only the declared edge appears; the observed-only beta->alpha shadow edge is
	// withheld and beta is out of scope entirely.
	if len(nb.Edges) != 1 {
		t.Fatalf("expected 1 declared edge, got %d", len(nb.Edges))
	}
	// Expected-only: the payload carries the declared claim and NOTHING observed. Even
	// though alpha->leaf-svc is also observed in the fixture, the expected view must not
	// leak the observed fact or the comparison verdict into the edge payload.
	if got := shapeOf(&nb.Edges[0]); got != (edgeShape{expected: true, hasDeclared: true, provenance: ProvenanceDeclared}) {
		t.Errorf("alpha->leaf edge = %+v, want expected-only (declared claim, no observed/difference)", got)
	}
}

func TestNeighborhood_FocusNode(t *testing.T) {
	q := productFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	focus := focusNode(nb)
	if focus == nil {
		t.Fatal("no focus node marked")
	}
	// Default (expected) view: alpha declares a dependency on leaf-svc but has NO
	// declared dependent (beta->alpha is observed-only). A view-aware expansion
	// affordance must therefore offer ONLY dependencies here; advertising a
	// dependents expansion would leak observed knowledge the expected view excludes.
	if focus.Ref.Key != "alpha" || focus.Owner != "team-a" {
		t.Errorf("focus node wrong: %+v", focus)
	}
	if len(focus.Expansions) != 1 || focus.Expansions[0] != DirectionDependencies {
		t.Errorf("expected-view expansions = %v, want [dependencies] only (no observed-only leak)", focus.Expansions)
	}
}

func focusNode(nb *Neighborhood) *NeighborhoodNode {
	for i := range nb.Nodes {
		if nb.Nodes[i].Focus {
			return &nb.Nodes[i]
		}
	}
	return nil
}

func TestNeighborhood_Views(t *testing.T) {
	q := productFleet(t)
	obs, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{ViewObserved}})
	if len(obs.Edges) != 2 {
		t.Errorf("observed view edges = %d, want 2", len(obs.Edges))
	}
	diff, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{ViewDifferences}})
	if len(diff.Edges) != 2 {
		t.Fatalf("differences view edges = %d, want 2", len(diff.Edges))
	}
	sawShadow := false
	for _, e := range diff.Edges {
		if e.From.Key == "beta" && e.To.Key == "alpha" {
			if e.Expected || !e.Observed || e.Provenance != ProvenanceObserved {
				t.Errorf("observed-only edge misclassified: %+v", e)
			}
			sawShadow = true
		}
	}
	if !sawShadow {
		t.Error("differences view must surface the observed-only shadow edge")
	}
}

func TestNeighborhood_Direction(t *testing.T) {
	q := productFleet(t)
	// Use the differences view so both the declared dependency (leaf-svc) and the
	// observed dependent (beta) are eligible; the direction must still separate them.
	diff := []KnowledgeView{ViewDifferences}
	deps, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Direction: DirectionDependencies, Views: diff})
	if k := nodeKeys(deps); k["beta"] || !k["leaf-svc"] {
		t.Errorf("dependencies direction leaked a dependent: %v", k)
	}
	dependents, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Direction: DirectionDependents, Views: diff})
	if k := nodeKeys(dependents); k["leaf-svc"] || !k["beta"] {
		t.Errorf("dependents direction leaked a dependency: %v", k)
	}
}

func TestNeighborhood_DepthAndCycle(t *testing.T) {
	q := chainFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "web", Direction: DirectionDependencies, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	keys := nodeKeys(nb)
	for _, want := range []string{"web", "api", "db", "cache"} {
		if !keys[want] {
			t.Errorf("missing node %q; got %v", want, keys)
		}
	}
	if len(nb.Nodes) != 4 {
		t.Errorf("nodes = %d, want 4 (cycle must not duplicate)", len(nb.Nodes))
	}
	if len(nb.Edges) != 4 { // web->api, api->db, api->cache, db->web
		t.Errorf("edges = %d, want 4", len(nb.Edges))
	}
}

func TestNeighborhood_Truncation(t *testing.T) {
	q := productFleet(t)
	nb, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", MaxNodes: 1})
	if len(nb.Nodes) != 1 || !nb.Truncated {
		t.Errorf("node cap not enforced: nodes=%d truncated=%v", len(nb.Nodes), nb.Truncated)
	}
	edgeCap, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{ViewDifferences}, MaxEdges: 1})
	if len(edgeCap.Edges) != 1 || !edgeCap.Truncated {
		t.Errorf("edge cap not enforced: edges=%d truncated=%v", len(edgeCap.Edges), edgeCap.Truncated)
	}
}

func TestNeighborhood_FocusResolution(t *testing.T) {
	q := productFleet(t)
	view, _ := q.GetService("alpha")
	revKey := string(view.Revisions[0].Key)

	rev, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: revKey})
	if err != nil {
		t.Fatal(err)
	}
	if rev.Nodes[0].RevisionState != revisionMatchExact {
		t.Errorf("revision focus state = %q, want exact", rev.Nodes[0].RevisionState)
	}

	tgtKey := string(NewTargetKey("prod", "k8s", "alpha-app"))
	tgt, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: tgtKey})
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Nodes[0].RevisionState != revisionMatchExact {
		t.Errorf("target focus state = %q, want exact", tgt.Nodes[0].RevisionState)
	}
}

func TestNeighborhood_Errors(t *testing.T) {
	q := productFleet(t)
	amb := NewQuery(twoDomainSnap(t))
	cases := []struct {
		name string
		q    NeighborhoodQuery
		useQ *Query
	}{
		{"revision not found", NeighborhoodQuery{Kind: KindRevision, Key: "nope@x"}, q},
		{"target not found", NeighborhoodQuery{Kind: KindTarget, Key: "prod/k8s/nope"}, q},
		{"owner not a graph node", NeighborhoodQuery{Kind: KindOwner, Key: "team-a"}, q},
		{"bad direction", NeighborhoodQuery{Kind: KindService, Key: "alpha", Direction: "sideways"}, q},
		{"bad view", NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{"weird"}}, q},
		{"service not found", NeighborhoodQuery{Kind: KindService, Key: "ghostly"}, q},
		{"ambiguous service", NeighborhoodQuery{Kind: KindService, Key: "shared"}, amb},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.useQ.Neighborhood(c.q); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestObservedStaleAndWindows(t *testing.T) {
	q := productFleet(t)
	old := fixedNow().Add(-48 * time.Hour)
	recent := fixedNow().Add(-1 * time.Minute)
	if !q.observedStale(&old) {
		t.Error("48h-old observed edge must be stale")
	}
	if q.observedStale(&recent) {
		t.Error("recent observed edge must not be stale")
	}
	if q.observedStale(nil) {
		t.Error("a nil last-seen is not stale")
	}

	a := fixedNow()
	b := fixedNow().Add(time.Hour)
	// Cover both argument orders so every branch of the min/max helpers runs.
	if !earlier(&a, &b).Equal(a) || !earlier(&b, &a).Equal(a) || !earlier(nil, &b).Equal(b) || !earlier(&a, nil).Equal(a) {
		t.Error("earlier() wrong")
	}
	if !later(&a, &b).Equal(b) || !later(&b, &a).Equal(b) || !later(nil, &a).Equal(a) || !later(&a, nil).Equal(a) {
		t.Error("later() wrong")
	}
}
