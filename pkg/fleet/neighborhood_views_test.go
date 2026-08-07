package fleet

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// These tests are the counterexamples for the neighborhood semantics rework: the
// requested views must drive BOTH traversal and returned edges (an expected-only
// query never reaches a node via an observed edge, and vice versa); declared
// claims are preserved per source revision; the backend states the difference
// verdict explicitly; unresolved declared dependencies are surfaced; and focus is
// honestly a service-neighborhood root.

func edgeBetween(nb *Neighborhood, from, to string) *NeighborhoodEdge {
	for i := range nb.Edges {
		if nb.Edges[i].From.Key == from && nb.Edges[i].To.Key == to {
			return &nb.Edges[i]
		}
	}
	return nil
}

// Counterexample 1: expected-only must not include a node reachable ONLY through
// an observed edge (beta -> alpha is observed-only).
func TestNeighborhood_ExpectedExcludesObservedOnlyNeighbor(t *testing.T) {
	q := productFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	if nodeKeys(nb)["beta"] {
		t.Error("expected-only neighborhood must not reach beta, which is only an observed dependent")
	}
}

// Counterexample 2: observed-only must not include a node reachable ONLY through a
// declared edge (chainFleet is entirely declared, no observed edges).
func TestNeighborhood_ObservedExcludesDeclaredOnlyNeighbor(t *testing.T) {
	q := chainFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "web", Direction: DirectionDependencies, Views: []KnowledgeView{ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	if nodeKeys(nb)["api"] {
		t.Error("observed-only neighborhood must not reach api, which is only a declared dependency")
	}
	if len(nb.Edges) != 0 {
		t.Errorf("observed view over a declared-only fleet must have no edges, got %d", len(nb.Edges))
	}
}

// Counterexample 3: multiple revisions declaring different required/compatibility
// values must be preserved as separate declared claims, never collapsed.
func TestNeighborhood_PreservesPerRevisionDeclaredClaims(t *testing.T) {
	lib := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "lib", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	appRev := func(req bool, compat, digest string) RawRevision {
		return RawRevision{Bundle: &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: "app", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
			Dependencies: []contract.Dependency{{Name: "lib", Ref: "oci://x/lib", Required: req, Compatibility: compat}},
		}, FS: fstest.MapFS{}}, Digest: digest}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: lib, FS: fstest.MapFS{}}, Digest: "sha256:lib"},
		appRev(true, "^1.0.0", "sha256:app1"),
		appRev(false, "^2.0.0", "sha256:app2"),
	}}))
	if err != nil {
		t.Fatal(err)
	}
	nb, err := NewQuery(snap).Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "app", Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	e := edgeBetween(nb, string(NewServiceKey("app")), string(NewServiceKey("lib")))
	if e == nil {
		t.Fatal("no app->lib edge")
	}
	if e.DeclaredClaims.Count != 2 || e.DeclaredClaims.Total != 2 {
		t.Fatalf("expected 2 declared claims (one per revision), got %+v", e.DeclaredClaims)
	}
	reqs := map[bool]string{}
	for _, c := range e.DeclaredClaims.Items {
		reqs[c.Required] = c.Compatibility
	}
	if reqs[true] != "^1.0.0" || reqs[false] != "^2.0.0" {
		t.Errorf("declared claims collapsed or wrong: %+v", e.DeclaredClaims)
	}
}

// Counterexample 4: a purely observed edge is stated as observed-not-expected by
// the backend, not left for the frontend to infer from booleans.
func TestNeighborhood_ObservedNotExpectedDifference(t *testing.T) {
	q := productFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Views: []KnowledgeView{ViewDifferences}})
	if err != nil {
		t.Fatal(err)
	}
	e := edgeBetween(nb, "beta", "alpha")
	if e == nil {
		t.Fatal("no observed beta->alpha shadow edge")
	}
	if e.Difference != DifferenceObservedNotExpected {
		t.Errorf("shadow edge difference = %q, want observed-not-expected", e.Difference)
	}
	// The matched edge states matched.
	m := edgeBetween(nb, string(NewServiceKey("alpha")), string(NewServiceKey("leaf-svc")))
	if m == nil || m.Difference != DifferenceMatched {
		t.Errorf("alpha->leaf difference = %v, want matched", m)
	}
}

// Counterexample 5: an unresolved declared dependency is surfaced, not silently
// dropped because its ToService is empty (alpha declares dep on ghost).
func TestNeighborhood_SurfacesUnresolvedDependency(t *testing.T) {
	q := productFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range nb.UnresolvedDependencies.Items {
		if u.Ref == "ghost" || u.RequestedRef == "oci://x/ghost" {
			found = true
		}
	}
	if !found {
		t.Errorf("unresolved declared dependency 'ghost' not surfaced: %+v", nb.UnresolvedDependencies)
	}
}

// Counterexample 6: an observed edge older than the recency window is marked stale.
func TestNeighborhood_StaleObservation(t *testing.T) {
	a := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "caller", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	b := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "callee", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: a, FS: fstest.MapFS{}}, Digest: "sha256:a"},
			{Bundle: &contract.Bundle{Contract: b, FS: fstest.MapFS{}}, Digest: "sha256:b"},
		},
		Observed: []ObservedEdge{{From: "caller", To: "callee", Count: 1, FirstSeen: fixedNow().Add(-72 * time.Hour), LastSeen: fixedNow().Add(-48 * time.Hour)}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	nb, err := NewQuery(snap).Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "caller", Direction: DirectionDependencies, Views: []KnowledgeView{ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	e := edgeBetween(nb, string(NewServiceKey("caller")), string(NewServiceKey("callee")))
	if e == nil || !e.Stale {
		t.Errorf("48h-old observed edge must be stale: %+v", e)
	}
}

// Counterexamples 7 & 8: a target or revision focus maps to its service
// neighborhood: RequestedFocus keeps the selected identity, FocusService is the
// logical service root, and every node is a service node.
func TestNeighborhood_HonestFocusMapping(t *testing.T) {
	q := productFleet(t)
	tgtKey := string(NewTargetKey("prod", "k8s", "alpha-app"))
	tgt, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: tgtKey})
	if err != nil {
		t.Fatal(err)
	}
	if tgt.RequestedFocus.Kind != KindTarget {
		t.Errorf("target requestedFocus kind = %q, want target", tgt.RequestedFocus.Kind)
	}
	if tgt.FocusService.Kind != KindService || tgt.FocusService.Key != string(NewServiceKey("alpha")) {
		t.Errorf("target focusService = %+v, want the alpha service", tgt.FocusService)
	}
	for _, n := range tgt.Nodes {
		if n.Ref.Kind != KindService {
			t.Errorf("neighborhood node %q is not a service node (kind %q)", n.Ref.Key, n.Ref.Kind)
		}
	}

	view, _ := q.GetService("alpha")
	revKey := string(view.Revisions[0].Key)
	rev, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: revKey})
	if err != nil {
		t.Fatal(err)
	}
	if rev.RequestedFocus.Kind != KindRevision || rev.FocusService.Kind != KindService {
		t.Errorf("revision focus mapping wrong: requested=%q focusService=%q", rev.RequestedFocus.Kind, rev.FocusService.Kind)
	}
}

// mixedFleet: A declares B (+ two unresolved deps), B declares C (+ one unresolved
// dep), and A is observed calling C directly (a shadow edge). This exercises the
// expected-not-observed verdict (A->B, B->C are declared but not observed while the
// snapshot HAS observation data), the observed-only shadow being withheld from the
// expected view, and multiple unresolved dependencies.
func mixedFleet(t *testing.T) *Query {
	t.Helper()
	svc := func(name string, deps ...contract.Dependency) *contract.Contract {
		return &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Dependencies: deps}
	}
	dep := func(name string) contract.Dependency {
		return contract.Dependency{Name: name, Ref: "oci://x/" + name, Required: true, Compatibility: "^1.0.0"}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: svc("a", dep("b"), dep("ghost1"), dep("ghost2")), FS: fstest.MapFS{}}, Digest: "sha256:a"},
			{Bundle: &contract.Bundle{Contract: svc("b", dep("c"), dep("ghost3")), FS: fstest.MapFS{}}, Digest: "sha256:b"},
			{Bundle: &contract.Bundle{Contract: svc("c"), FS: fstest.MapFS{}}, Digest: "sha256:c"},
		},
		Observed: []ObservedEdge{{From: "a", To: "c", Count: 2, FirstSeen: fixedNow().Add(-30 * time.Minute), LastSeen: fixedNow().Add(-5 * time.Minute)}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestNeighborhood_ExpectedNotObservedAndShadowWithheld(t *testing.T) {
	q := mixedFleet(t)
	// Expected view, depth 2: A, B, C reachable via declared edges. A->B and B->C
	// are declared-not-observed; the observed-only A->C shadow is withheld.
	exp, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "a", Direction: DirectionDependencies, Depth: 2, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	ab := edgeBetween(exp, string(NewServiceKey("a")), string(NewServiceKey("b")))
	if ab == nil || ab.Difference != DifferenceExpectedNotObserved {
		t.Errorf("a->b difference = %v, want expected-not-observed", ab)
	}
	if edgeBetween(exp, string(NewServiceKey("a")), string(NewServiceKey("c"))) != nil {
		t.Error("the observed-only a->c shadow must be withheld from the expected view")
	}
	// Unresolved declared deps are surfaced and deterministically ordered.
	if exp.UnresolvedDependencies.Count != 3 || exp.UnresolvedDependencies.Total != 3 {
		t.Fatalf("unresolved deps = %+v, want 3 (ghost1, ghost2, ghost3)", exp.UnresolvedDependencies)
	}

	// Observed view: the shadow edge appears; unresolved (declared) deps are NOT
	// surfaced because declared knowledge is not in view.
	obs, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "a", Direction: DirectionDependencies, Depth: 2, Views: []KnowledgeView{ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	if edgeBetween(obs, string(NewServiceKey("a")), string(NewServiceKey("c"))) == nil {
		t.Error("observed view must surface the a->c shadow edge")
	}
	if obs.UnresolvedDependencies.Count != 0 {
		t.Errorf("observed view must not surface declared unresolved deps: %+v", obs.UnresolvedDependencies)
	}

	// A neighborhood rooted at C excludes A and B, so their unresolved deps (whose
	// from-service is out of scope) are not surfaced.
	fromC, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "c", Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	if fromC.UnresolvedDependencies.Count != 0 {
		t.Errorf("out-of-scope unresolved deps must not be surfaced: %+v", fromC.UnresolvedDependencies)
	}
}

// Counterexample 10 (bounds): negative depth/maxNodes/maxEdges are rejected;
// excessive values are capped rather than honored.
func TestNeighborhood_BoundsRejectAndCap(t *testing.T) {
	q := productFleet(t)
	for _, nq := range []NeighborhoodQuery{
		{Kind: KindService, Key: "alpha", Depth: -1},
		{Kind: KindService, Key: "alpha", MaxNodes: -1},
		{Kind: KindService, Key: "alpha", MaxEdges: -1},
	} {
		if _, err := q.Neighborhood(nq); err == nil {
			t.Errorf("negative bound %+v must be rejected", nq)
		}
	}
	capped, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "alpha", Depth: 1_000_000, MaxNodes: 1_000_000, MaxEdges: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if capped.Depth != MaxNeighborhoodDepth || capped.MaxNodes != MaxNeighborhoodNodes || capped.MaxEdges != MaxNeighborhoodEdges {
		t.Errorf("excessive bounds not capped: depth=%d maxNodes=%d maxEdges=%d", capped.Depth, capped.MaxNodes, capped.MaxEdges)
	}
}
