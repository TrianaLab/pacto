package fleet

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// This file is the projection counterexample suite. It proves the knowledge-view
// invariant, the honest observation-scope model and the target identity rules for
// the revision and target projections. It complements the happy-path
// coverage in projection_test.go with adversarial cases that must NOT hold.

func revKeyForVersion(t *testing.T, q *Query, service, version string) RevisionKey {
	t.Helper()
	for k, r := range q.snap.Revisions {
		if r.Service == service && r.Version == version {
			return k
		}
	}
	t.Fatalf("no revision for %s@%s", service, version)
	return ""
}

// obsScopeFleet exercises the observation-scope counterexample: service "a" has two
// revisions (a@1.0.0 declares b, a@2.0.0 declares nothing), service-level telemetry
// observes a->b, and a target runs a@1.0.0 exactly.
func obsScopeFleet(t *testing.T) *Query {
	t.Helper()
	dep := func(name string) contract.Dependency {
		return contract.Dependency{Name: name, Ref: "oci://x/" + name, Required: true, Compatibility: "^1.0.0"}
	}
	mk := func(name, version string, deps ...contract.Dependency) *contract.Contract {
		return &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "t"}}, Workload: contract.WorkloadService, Dependencies: deps, Readiness: readyContract()}
	}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: mk("b", "1.0.0"), FS: fstest.MapFS{}}, Digest: "sha256:b"},
			{Bundle: &contract.Bundle{Contract: mk("a", "1.0.0", dep("b")), FS: fstest.MapFS{}}, Digest: "sha256:a1"},
			{Bundle: &contract.Bundle{Contract: mk("a", "2.0.0"), FS: fstest.MapFS{}}, Digest: "sha256:a2"},
		},
		Observed: []ObservedEdge{{From: "a", To: "b", Count: 5, FirstSeen: fixedNow().Add(-time.Hour), LastSeen: fixedNow().Add(-time.Minute)}},
		Targets:  []RawTarget{{Scope: "prod", Kind: "k8s", Name: "a-prod", Service: "a", Digest: "sha256:a1", Compliance: StatusCompliant}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// TestRevisionProjection_ViewInvariance is A1/A2 (M1/M2): the requested views drive
// the revision projection exactly as they drive the service projection. The revision
// graph is declared-only, so an expected view shows the declared edge and an
// observed-only view traverses NO declared edge and reaches no node through one.
func TestRevisionProjection_ViewInvariance(t *testing.T) {
	q := projectionFleet(t)
	web := string(revKeyFor(t, q, "web"))
	api := string(revKeyFor(t, q, "api"))

	exp, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	if findEdge(exp, web, api) == nil {
		t.Errorf("expected view must show the declared web->api revision edge")
	}

	obs, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionDependencies, Views: []KnowledgeView{ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Edges) != 0 {
		t.Errorf("observed-only revision projection must traverse no declared edge, got %d edges", len(obs.Edges))
	}
	if nodeByKey(obs, api) != nil {
		t.Errorf("observed-only revision projection must not reach api via an excluded declared edge")
	}
	if len(obs.Nodes) != 1 {
		t.Errorf("observed-only revision projection should return just the focus node, got %d", len(obs.Nodes))
	}
}

// TestRevisionProjection_ExpansionViewAware is A4/A5 (M3): a node's expansion
// affordances are computed from the SAME knowledge set as the traversal, so an
// observed-only revision query advertises no expansion that exists only through the
// excluded declared knowledge.
func TestRevisionProjection_ExpansionViewAware(t *testing.T) {
	q := projectionFleet(t)
	web := string(revKeyFor(t, q, "web"))

	exp, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionBoth, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	f := nodeByKey(exp, web)
	if f == nil || len(f.Expansions) == 0 {
		t.Errorf("expected view: web focus must advertise its declared dependency expansion, got %+v", f)
	}

	obs, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionBoth, Views: []KnowledgeView{ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	fo := nodeByKey(obs, web)
	if fo == nil {
		t.Fatal("focus node missing from observed-only revision projection")
	}
	if len(fo.Expansions) != 0 {
		t.Errorf("observed-only view: focus must advertise NO expansion (declared knowledge excluded), got %+v", fo.Expansions)
	}
}

// TestRevisionProjection_ServiceObservationNotPromoted is B (M5): service-scoped
// telemetry (a->b observed) is surfaced as CONTEXT on a revision edge (service
// corroboration), never promoted to a revision-scoped observed claim; and a sibling
// revision that does not declare the dependency never acquires the edge.
func TestRevisionProjection_ServiceObservationNotPromoted(t *testing.T) {
	q := obsScopeFleet(t)
	a1 := string(revKeyForVersion(t, q, "a", "1.0.0"))
	a2 := string(revKeyForVersion(t, q, "a", "2.0.0"))

	nb1, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: a1, Perspective: PerspectiveRevision, Direction: DirectionDependencies, Views: []KnowledgeView{ViewDifferences}})
	if err != nil {
		t.Fatal(err)
	}
	e := findEdge(nb1, a1, "b")
	if e == nil {
		t.Fatalf("expected a1->b(service) edge; edges: %+v", nb1.Edges)
	}
	if e.Observed {
		t.Errorf("a revision edge must NOT be marked observed from service-scoped telemetry: %+v", e)
	}
	if e.ObservationScope != ObservationScopeService {
		t.Errorf("edge observationScope = %q, want service", e.ObservationScope)
	}
	if e.ServiceCorroboration != CorroborationMatched {
		t.Errorf("edge serviceCorroboration = %q, want matched (a->b was observed at service scope)", e.ServiceCorroboration)
	}
	if e.Difference != "" {
		t.Errorf("a fine-grained edge must carry no edge-scope difference verdict, got %q", e.Difference)
	}

	nb2, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: a2, Perspective: PerspectiveRevision, Direction: DirectionBoth, Views: []KnowledgeView{ViewDifferences}})
	if err != nil {
		t.Fatal(err)
	}
	if findEdge(nb2, a2, "b") != nil {
		t.Errorf("a@2.0.0 does not declare b; service-scoped observation must not create an a2->b revision edge")
	}
}

// TestRevisionProjection_DifferencesComparisonOnly is A3: a differences view over a
// revision projection includes the declared edge as a comparison fact carrying its
// service-scoped corroboration, and never upgrades it to an observed revision edge.
func TestRevisionProjection_DifferencesComparisonOnly(t *testing.T) {
	q := obsScopeFleet(t)
	a1 := string(revKeyForVersion(t, q, "a", "1.0.0"))
	diff, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: a1, Perspective: PerspectiveRevision, Direction: DirectionDependencies, Views: []KnowledgeView{ViewDifferences}})
	if err != nil {
		t.Fatal(err)
	}
	e := findEdge(diff, a1, "b")
	if e == nil {
		t.Fatal("differences view must include the declared a1->b edge as a comparison fact")
	}
	if e.Observed {
		t.Errorf("differences view must not upgrade a revision edge to observed")
	}
	if e.ServiceCorroboration != CorroborationMatched {
		t.Errorf("differences view must carry the service-scoped corroboration, got %q", e.ServiceCorroboration)
	}
}

// TestTargetProjection_ViewInvariance is M4/A6: the target projection is view-aware.
// The declared target->service dependency edges appear only when declared knowledge is
// in view; the structural runs edge is always present (it is the target's identity
// link, not a declared-vs-observed dependency).
func TestTargetProjection_ViewInvariance(t *testing.T) {
	q := projectionFleet(t)
	apiProd := string(targetKeyFor(t, q, "api-prod"))
	apiRev := string(revKeyFor(t, q, "api"))

	exp, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: apiProd, Perspective: PerspectiveTarget, Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	if findEdge(exp, apiProd, "db") == nil {
		t.Errorf("expected view: target->db declared dependency edge must be present")
	}

	obs, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: apiProd, Perspective: PerspectiveTarget, Direction: DirectionDependencies, Views: []KnowledgeView{ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range obs.Edges {
		if e.Relation == RelationDependency {
			t.Errorf("observed-only target view must exclude declared dependency edges: %+v", e)
		}
	}
	if findEdge(obs, apiProd, apiRev) == nil {
		t.Errorf("observed-only target view must still show the structural runs edge")
	}
}

// TestTargetProjection_ServiceObservationNotPromoted is B (M6): a target's dependency
// edge is never promoted to observed from service telemetry, while the runs edge is a
// genuine target-scoped observed fact.
func TestTargetProjection_ServiceObservationNotPromoted(t *testing.T) {
	q := obsScopeFleet(t)
	tk := string(targetKeyFor(t, q, "a-prod"))
	a1 := string(revKeyForVersion(t, q, "a", "1.0.0"))
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: tk, Perspective: PerspectiveTarget, Direction: DirectionBoth, Views: []KnowledgeView{ViewDifferences}})
	if err != nil {
		t.Fatal(err)
	}
	runs := findEdge(nb, tk, a1)
	if runs == nil || !runs.Observed || runs.ObservationScope != ObservationScopeTarget {
		t.Fatalf("target->revision runs edge must be observed at TARGET scope: %+v", runs)
	}
	dep := findEdge(nb, tk, "b")
	if dep == nil {
		t.Fatalf("expected target->b dependency edge; edges: %+v", nb.Edges)
	}
	if dep.Observed {
		t.Errorf("a target dependency edge must NOT be observed from service telemetry: %+v", dep)
	}
	if dep.ObservationScope != ObservationScopeService {
		t.Errorf("target dependency edge scope = %q, want service", dep.ObservationScope)
	}
	if dep.ServiceCorroboration != CorroborationMatched {
		t.Errorf("target dependency edge corroboration = %q, want matched", dep.ServiceCorroboration)
	}
}

// TestTargetProjection_UnresolvedNoDependencyInheritance is C1 (M7): an unresolved
// target must not acquire the union of every revision's declared dependencies.
func TestTargetProjection_UnresolvedNoDependencyInheritance(t *testing.T) {
	q := paymentsFleet(t, "sha256:nomatch", "")
	tk := string(targetKeyFor(t, q, "payments"))
	if tv, _ := q.GetTarget(tk); tv.Target.RevisionMatch == revisionMatchExact || tv.Target.RevisionMatch == revisionMatchInferred {
		t.Fatalf("precondition: expected an unresolved target link, got %q", tv.Target.RevisionMatch)
	}
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: tk, Perspective: PerspectiveTarget, Direction: DirectionBoth})
	if err != nil {
		t.Fatal(err)
	}
	if findEdge(nb, tk, "redis") != nil || findEdge(nb, tk, "postgres") != nil {
		t.Errorf("unresolved target inherited a revision's declared dependency; edges: %+v", nb.Edges)
	}
	if !hasLimitation(nb.Limitations.Items, "TARGET_REVISION_UNRESOLVED") {
		t.Errorf("unresolved target must surface TARGET_REVISION_UNRESOLVED: %+v", nb.Limitations)
	}
	for _, e := range nb.Edges {
		if e.Relation == RelationRuns {
			t.Errorf("unresolved target must have no runs edge: %+v", e)
		}
	}
}

// TestTargetProjection_AmbiguousNoDependencyInheritance is C1 (M8): an ambiguous
// target (its mutable reference matches several revisions) must not inherit any one
// revision's declared dependencies.
func TestTargetProjection_AmbiguousNoDependencyInheritance(t *testing.T) {
	q := paymentsFleet(t, "", "oci://x/payments:1.0.0")
	tk := string(targetKeyFor(t, q, "payments"))
	tv, err := q.GetTarget(tk)
	if err != nil {
		t.Fatal(err)
	}
	// An ambiguous link self-describes via the REVISION_LINK_AMBIGUOUS limitation and
	// leaves RevisionMatch empty (only exact/inferred set it), so the target is not
	// authoritatively linked.
	if !hasLimitation(tv.Target.Limitations, LimitationRevisionAmbiguous) {
		t.Fatalf("precondition: expected an ambiguous target link, got match=%q limitations=%+v", tv.Target.RevisionMatch, tv.Target.Limitations)
	}
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: tk, Perspective: PerspectiveTarget, Direction: DirectionBoth})
	if err != nil {
		t.Fatal(err)
	}
	if findEdge(nb, tk, "redis") != nil || findEdge(nb, tk, "postgres") != nil {
		t.Errorf("ambiguous target inherited a specific revision's declared dependency; edges: %+v", nb.Edges)
	}
	for _, e := range nb.Edges {
		if e.Relation == RelationRuns {
			t.Errorf("ambiguous target must have no authoritative runs edge: %+v", e)
		}
	}
}

// paymentsFleet builds the C1 fleet: two payments revisions with DIFFERENT declared
// dependencies (v1->redis, v2->postgres). The target references payments either by a
// non-matching digest (unresolved) or by a mutable version ref that matches both
// revisions (ambiguous), per the arguments.
func paymentsFleet(t *testing.T, digest, resolvedRef string) *Query {
	t.Helper()
	dep := func(name string) contract.Dependency {
		return contract.Dependency{Name: name, Ref: "oci://x/" + name, Required: true, Compatibility: "^1.0.0"}
	}
	mk := func(name, version string, deps ...contract.Dependency) *contract.Contract {
		return &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "t"}}, Workload: contract.WorkloadService, Dependencies: deps, Readiness: readyContract()}
	}
	// For the ambiguous case both revisions share version 1.0.0 so a version-suffixed
	// mutable ref matches both; for the unresolved case the versions differ.
	v2 := "2.0.0"
	if resolvedRef != "" {
		v2 = "1.0.0"
	}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: mk("redis", "1.0.0"), FS: fstest.MapFS{}}, Digest: "sha256:redis"},
			{Bundle: &contract.Bundle{Contract: mk("postgres", "1.0.0"), FS: fstest.MapFS{}}, Digest: "sha256:pg"},
			{Bundle: &contract.Bundle{Contract: mk("payments", "1.0.0", dep("redis")), FS: fstest.MapFS{}}, Digest: "sha256:pay1"},
			{Bundle: &contract.Bundle{Contract: mk("payments", v2, dep("postgres")), FS: fstest.MapFS{}}, Digest: "sha256:pay2"},
		},
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "payments", Service: "payments", Digest: digest, ResolvedRef: resolvedRef, Compliance: StatusUnknown}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// TestServiceCorroboration_Verdicts covers the non-matched service-corroboration
// verdicts on fine-grained edges: insufficient (no observation anywhere) and
// expected-not-observed (observation exists but did not witness this edge).
func TestServiceCorroboration_Verdicts(t *testing.T) {
	pq := paymentsFleet(t, "sha256:nomatch", "")
	pv1 := string(revKeyForVersion(t, pq, "payments", "1.0.0"))
	pnb, err := pq.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: pv1, Perspective: PerspectiveRevision, Direction: DirectionDependencies, Views: []KnowledgeView{ViewDifferences}})
	if err != nil {
		t.Fatal(err)
	}
	if e := findEdge(pnb, pv1, "redis"); e == nil || e.ServiceCorroboration != CorroborationInsufficient {
		t.Errorf("no-observation edge must have insufficient corroboration, got %+v", e)
	}

	q := projectionFleet(t)
	api := string(revKeyFor(t, q, "api"))
	anb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: api, Perspective: PerspectiveRevision, Direction: DirectionDependencies, Views: []KnowledgeView{ViewDifferences}})
	if err != nil {
		t.Fatal(err)
	}
	if e := findEdge(anb, api, "db"); e == nil || e.ServiceCorroboration != CorroborationExpectedNotObserved {
		t.Errorf("declared-but-unwitnessed edge must have expected-not-observed corroboration, got %+v", e)
	}
}

// TestTargetProjection_DependentsCapHit covers the node-cap branch in the target
// projection: with the focus consuming the only node slot, the runs-revision node
// cannot be added and the projection reports truncated.
func TestTargetProjection_DependentsCapHit(t *testing.T) {
	q := projectionFleet(t)
	apiProd := string(targetKeyFor(t, q, "api-prod"))
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: apiProd, Perspective: PerspectiveTarget, Direction: DirectionDependents, MaxNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(nb.Nodes) != 1 || !nb.Truncated {
		t.Errorf("maxNodes=1 target dependents must yield just the focus + truncated, got %d nodes truncated=%v", len(nb.Nodes), nb.Truncated)
	}
}

// TestProjection_EffectiveDepthHonest is D (M12): the target projection reports
// EffectiveDepth 1 no matter how large a depth is requested, while the revision and
// service projections evaluate the requested depth.
func TestProjection_EffectiveDepthHonest(t *testing.T) {
	q := projectionFleet(t)
	web := string(revKeyFor(t, q, "web"))
	apiProd := string(targetKeyFor(t, q, "api-prod"))

	tnb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: apiProd, Perspective: PerspectiveTarget, Depth: 6})
	if err != nil {
		t.Fatal(err)
	}
	if tnb.EffectiveDepth != 1 {
		t.Errorf("target effectiveDepth = %d, want 1 (one-hop projection)", tnb.EffectiveDepth)
	}

	rnb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if rnb.EffectiveDepth != 3 {
		t.Errorf("revision effectiveDepth = %d, want 3", rnb.EffectiveDepth)
	}
	snb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "web", Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if snb.EffectiveDepth != 2 {
		t.Errorf("service effectiveDepth = %d, want 2", snb.EffectiveDepth)
	}
}

// TestProjection_ImmutableAcrossQueries is M15: mutating a returned projection answer
// cannot leak into the snapshot or a later identical query.
func TestProjection_ImmutableAcrossQueries(t *testing.T) {
	q := projectionFleet(t)
	web := string(revKeyFor(t, q, "web"))
	a, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionBoth, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Nodes {
		a.Nodes[i].Status = "MUTATED"
		a.Nodes[i].Expansions = nil
	}
	for i := range a.Edges {
		a.Edges[i].ServiceCorroboration = "MUTATED"
		a.Edges[i].Observed = true
	}
	a.Limitations.Items = nil

	b, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionBoth, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range b.Nodes {
		if n.Status == "MUTATED" {
			t.Errorf("mutating a returned node leaked into a later query: %+v", n)
		}
	}
	for _, e := range b.Edges {
		if e.ServiceCorroboration == "MUTATED" {
			t.Errorf("mutating a returned edge leaked into a later query: %+v", e)
		}
	}
}

// TestRevisionProjection_ExpectedOnlyNoCorroboration is Part-2 counterexample 5 for the
// revision projection: an expected-only fine-grained edge carries its declared claim but
// NO service corroboration, observation scope, difference or observed flag. The
// service-scoped corroboration is a comparison fact, so it belongs to the differences
// view only (proven present there by TestRevisionProjection_DifferencesComparisonOnly).
func TestRevisionProjection_ExpectedOnlyNoCorroboration(t *testing.T) {
	q := obsScopeFleet(t)
	a1 := string(revKeyForVersion(t, q, "a", "1.0.0"))
	exp, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: a1, Perspective: PerspectiveRevision, Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	e := findEdge(exp, a1, "b")
	if e == nil {
		t.Fatalf("expected the declared a1->b edge; edges: %+v", exp.Edges)
	}
	if !e.Expected || e.DeclaredClaims.Count == 0 {
		t.Errorf("expected-only revision edge must keep its declared claim: %+v", e)
	}
	if e.ServiceCorroboration != "" || e.ObservationScope != "" || e.Difference != "" || e.Observed {
		t.Errorf("expected-only fine-grained edge leaked observed/comparison context: %+v", e)
	}
}

// TestTargetProjection_ExpectedOnlyNoCorroboration is Part-2 counterexample 5 for the
// target projection: an expected-only target dependency edge carries no service
// corroboration/observation-scope, while the structural runs edge (the target's
// identity link) is shown intact regardless of the requested views.
func TestTargetProjection_ExpectedOnlyNoCorroboration(t *testing.T) {
	q := obsScopeFleet(t)
	tk := string(targetKeyFor(t, q, "a-prod"))
	a1 := string(revKeyForVersion(t, q, "a", "1.0.0"))
	exp, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: tk, Perspective: PerspectiveTarget, Direction: DirectionDependencies, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	e := findEdge(exp, tk, "b")
	if e == nil {
		t.Fatalf("expected the declared target->b edge; edges: %+v", exp.Edges)
	}
	if e.ServiceCorroboration != "" || e.ObservationScope != "" || e.Difference != "" || e.Observed {
		t.Errorf("expected-only target dependency edge leaked observed/comparison context: %+v", e)
	}
	if r := findEdge(exp, tk, a1); r == nil || !r.Observed || r.ObservationScope != ObservationScopeTarget {
		t.Errorf("runs edge (identity link) must be shown intact under expected-only: %+v", r)
	}
}

// TestProjection_NodeDepthWithinEffectiveDepth is the Part-3 invariant across every
// projection: no node's depth may exceed the response's EffectiveDepth. It proves the
// target one-hop is honest by construction (no disconnected depth-2 component) and that
// the service/revision projections never emit a node deeper than they evaluated.
func TestProjection_NodeDepthWithinEffectiveDepth(t *testing.T) {
	q := projectionFleet(t)
	web := string(revKeyFor(t, q, "web"))
	apiProd := string(targetKeyFor(t, q, "api-prod"))
	cases := []NeighborhoodQuery{
		{Kind: KindService, Key: "web", Perspective: PerspectiveService, Direction: DirectionBoth, Depth: 3, Views: []KnowledgeView{ViewExpected, ViewObserved, ViewDifferences}},
		{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionBoth, Depth: 3, Views: []KnowledgeView{ViewExpected}},
		{Kind: KindTarget, Key: apiProd, Perspective: PerspectiveTarget, Direction: DirectionBoth, Depth: 6, Views: []KnowledgeView{ViewExpected, ViewObserved}},
	}
	for _, nq := range cases {
		nb, err := q.Neighborhood(nq)
		if err != nil {
			t.Fatalf("%s projection: %v", nq.Perspective, err)
		}
		for _, n := range nb.Nodes {
			if n.Depth > nb.EffectiveDepth {
				t.Errorf("%s projection: node %q depth %d exceeds effectiveDepth %d", nq.Perspective, n.Ref.Key, n.Depth, nb.EffectiveDepth)
			}
		}
	}
}

// TestRevisionProjection_TargetFocus_ProjectionFocus is the Part-4 backend contract: a
// target focus under the revision perspective keeps RequestedFocus truthful (the target
// the request asked for) and surfaces the resolved revision as an explicit
// ProjectionFocus, so a client can canonicalize the URL to the revision identity without
// RequestedFocus ever lying. A direct revision focus sets no ProjectionFocus.
func TestRevisionProjection_TargetFocus_ProjectionFocus(t *testing.T) {
	q := obsScopeFleet(t)
	tk := string(targetKeyFor(t, q, "a-prod"))
	a1 := revKeyForVersion(t, q, "a", "1.0.0")
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: tk, Perspective: PerspectiveRevision})
	if err != nil {
		t.Fatal(err)
	}
	if nb.RequestedFocus.Kind != KindTarget || nb.RequestedFocus.Key != tk {
		t.Errorf("requestedFocus must remain the requested target, got %+v", nb.RequestedFocus)
	}
	if nb.ProjectionFocus == nil || nb.ProjectionFocus.Kind != KindRevision || nb.ProjectionFocus.Key != string(a1) {
		t.Errorf("projectionFocus must be the resolved revision %q, got %+v", a1, nb.ProjectionFocus)
	}
	direct, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: string(a1), Perspective: PerspectiveRevision})
	if err != nil {
		t.Fatal(err)
	}
	if direct.RequestedFocus.Kind != KindRevision || direct.ProjectionFocus != nil {
		t.Errorf("direct revision focus: want requestedFocus=revision and no projectionFocus, got req=%+v proj=%+v", direct.RequestedFocus, direct.ProjectionFocus)
	}
}
