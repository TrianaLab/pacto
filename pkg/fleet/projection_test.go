package fleet

import (
	"context"
	"encoding/json"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// projectionFleet builds a fleet exercising every projection counterexample:
//   - web@rev LOCKS api at a digest that matches api's revision, so web's api
//     dependency resolves to a SPECIFIC provider revision (revision->revision).
//   - app@rev has NO lock, so its api dependency resolves only to the logical service
//     (revision->service; a provider revision must NEVER be fabricated).
//   - web declares a dependency on "ghost", which resolves to nothing (unresolved).
//   - api depends on db (a further provider), and target "api-prod" runs api exactly.
func projectionFleet(t *testing.T) *Query {
	t.Helper()
	dep := func(name, ref string) contract.Dependency {
		return contract.Dependency{Name: name, Ref: ref, Required: true, Compatibility: "^1.0.0"}
	}
	mk := func(name, version string, deps ...contract.Dependency) *contract.Contract {
		return &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "t"}},
			Workload:     contract.WorkloadService, Dependencies: deps, Readiness: readyContract(),
		}
	}
	webLock := &lock.Lock{LockVersion: 1, Dependencies: []lock.Entry{{Name: "api", Source: "oci", Digest: "sha256:api", Version: "1.0.0"}}}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: mk("db", "1.0.0"), FS: fstest.MapFS{}}, Digest: "sha256:db"},
			{Bundle: &contract.Bundle{Contract: mk("cache", "1.0.0"), FS: fstest.MapFS{}}, Digest: "sha256:cache"},
			// api has TWO resolved deps (db, cache) and one unresolved (missing1).
			{Bundle: &contract.Bundle{Contract: mk("api", "1.0.0", dep("db", "oci://x/db"), dep("cache", "oci://x/cache"), dep("missing1", "oci://x/missing1")), FS: fstest.MapFS{}}, Digest: "sha256:api"},
			// web LOCKS api and declares two UNRESOLVED deps, with an unresolved FIRST
			// (so hasResolvedDep must skip it before finding the resolved api dep).
			{Bundle: &contract.Bundle{Contract: mk("web", "2.0.0", dep("ghost", "oci://x/ghost"), dep("api", "oci://x/api"), dep("ghost2", "oci://x/ghost2")), FS: fstest.MapFS{}}, Digest: "sha256:web", Lock: webLock},
			{Bundle: &contract.Bundle{Contract: mk("app", "1.0.0", dep("api", "oci://x/api")), FS: fstest.MapFS{}}, Digest: "sha256:app"},
			// a SECOND revision of the same "app" service also depends on api, so a
			// service-level reverse-dep scan must dedupe app to a single consumer.
			{Bundle: &contract.Bundle{Contract: mk("app", "2.0.0", dep("api", "oci://x/api")), FS: fstest.MapFS{}}, Digest: "sha256:app2"},
		},
		// An observed edge (web->api) so the declared-only revision index skips it.
		Observed: []ObservedEdge{{From: "web", To: "api", Count: 3, FirstSeen: fixedNow().Add(-time.Hour), LastSeen: fixedNow().Add(-time.Minute)}},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "api-prod", Service: "api", Digest: "sha256:api", Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow().Add(-time.Minute))},
			// a target whose digest matches no revision -> unresolved link.
			{Scope: "prod", Kind: "k8s", Name: "mystery", Service: "api", Digest: "sha256:nomatch", Compliance: StatusUnknown},
			// an unresolved-link target for a service with TWO revisions both declaring
			// the same dependency, so the service-level dep scan must dedupe them.
			{Scope: "prod", Kind: "k8s", Name: "app-mystery", Service: "app", Digest: "sha256:nomatch", Compliance: StatusUnknown},
		},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func revKeyFor(t *testing.T, q *Query, service string) RevisionKey {
	t.Helper()
	for k, r := range q.snap.Revisions {
		if string(r.ServiceKey) == service {
			return k
		}
	}
	t.Fatalf("no revision for service %q", service)
	return ""
}

func targetKeyFor(t *testing.T, q *Query, name string) TargetKey {
	t.Helper()
	for k, tr := range q.snap.Targets {
		if tr.Name == name {
			return k
		}
	}
	t.Fatalf("no target named %q", name)
	return ""
}

func findEdge(nb *Neighborhood, fromKey, toKey string) *NeighborhoodEdge {
	for i := range nb.Edges {
		if nb.Edges[i].From.Key == fromKey && nb.Edges[i].To.Key == toKey {
			return &nb.Edges[i]
		}
	}
	return nil
}

func nodeByKey(nb *Neighborhood, key string) *NeighborhoodNode {
	for i := range nb.Nodes {
		if nb.Nodes[i].Ref.Key == key {
			return &nb.Nodes[i]
		}
	}
	return nil
}

// TestRevisionProjection_LockedProviderIsRevision proves a revision-scoped dependency
// whose provider revision IS established (a lock matching a known revision) draws a
// real revision->revision edge.
func TestRevisionProjection_LockedProviderIsRevision(t *testing.T) {
	q := projectionFleet(t)
	web := revKeyFor(t, q, "web")
	api := revKeyFor(t, q, "api")
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: string(web), Perspective: PerspectiveRevision, Direction: DirectionDependencies})
	if err != nil {
		t.Fatal(err)
	}
	if nb.Perspective != PerspectiveRevision {
		t.Fatalf("perspective = %q, want revision", nb.Perspective)
	}
	e := findEdge(nb, string(web), string(api))
	if e == nil {
		t.Fatalf("no web->api edge; edges: %+v", nb.Edges)
	}
	if e.To.Kind != KindRevision {
		t.Errorf("locked provider edge To.Kind = %q, want revision (a specific provider revision)", e.To.Kind)
	}
	if e.Relation != RelationDependency {
		t.Errorf("relation = %q, want dependency", e.Relation)
	}
	// the api node in the graph is the REVISION, not the logical service.
	if n := nodeByKey(nb, string(api)); n == nil || n.Ref.Kind != KindRevision {
		t.Errorf("api node = %+v, want a revision node", n)
	}
}

// TestRevisionProjection_UnlockedProviderIsServiceNotFabricated is the core J2
// counterexample: an unlocked dependency resolves only to the logical provider
// service, so the edge is revision->service and NO provider revision is fabricated.
func TestRevisionProjection_UnlockedProviderIsServiceNotFabricated(t *testing.T) {
	q := projectionFleet(t)
	app := revKeyFor(t, q, "app")
	apiRev := revKeyFor(t, q, "api")
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: string(app), Perspective: PerspectiveRevision, Direction: DirectionDependencies})
	if err != nil {
		t.Fatal(err)
	}
	// The provider node for api is the logical SERVICE (key "api"), not a revision.
	e := findEdge(nb, string(app), "api")
	if e == nil {
		t.Fatalf("no app->api(service) edge; edges: %+v", nb.Edges)
	}
	if e.To.Kind != KindService {
		t.Errorf("unlocked provider edge To.Kind = %q, want service (no fabricated provider revision)", e.To.Kind)
	}
	// The api REVISION must NOT appear as app's provider (never app@digest -> api@digest).
	if findEdge(nb, string(app), string(apiRev)) != nil {
		t.Errorf("fabricated a revision->revision edge for an unlocked dependency")
	}
	if n := nodeByKey(nb, string(apiRev)); n != nil {
		t.Errorf("api revision node must not be present for an unlocked dependency: %+v", n)
	}
}

// TestRevisionProjection_UnresolvedSurfaced proves an unresolvable declared
// dependency is surfaced, not dropped or drawn as an edge.
func TestRevisionProjection_UnresolvedSurfaced(t *testing.T) {
	q := projectionFleet(t)
	web := revKeyFor(t, q, "web")
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: string(web), Perspective: PerspectiveRevision, Direction: DirectionDependencies})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range nb.UnresolvedDependencies.Items {
		if u.RequestedRef == "oci://x/ghost" {
			found = true
		}
	}
	if !found {
		t.Errorf("ghost dependency not surfaced as unresolved: %+v", nb.UnresolvedDependencies)
	}
}

// TestRevisionProjection_DependentsAreOnlyLockers proves a revision's dependents are
// only the revisions that lock its exact content; a consumer of its logical service
// (app) is NOT attributed to the revision.
func TestRevisionProjection_DependentsAreOnlyLockers(t *testing.T) {
	q := projectionFleet(t)
	api := revKeyFor(t, q, "api")
	web := revKeyFor(t, q, "web")
	app := revKeyFor(t, q, "app")
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: string(api), Perspective: PerspectiveRevision, Direction: DirectionDependents})
	if err != nil {
		t.Fatal(err)
	}
	if nodeByKey(nb, string(web)) == nil {
		t.Errorf("web (locks api) must be a dependent of api's revision")
	}
	if nodeByKey(nb, string(app)) != nil {
		t.Errorf("app (depends on api's logical service, not the exact revision) must NOT be a revision dependent")
	}
}

// TestTargetProjection_RunsAndDeps_NoTargetMesh proves a target links (runs) to its
// revision and depends on that revision's services, and NEVER draws a target->target
// edge.
func TestTargetProjection_RunsAndDeps_NoTargetMesh(t *testing.T) {
	q := projectionFleet(t)
	tk := targetKeyFor(t, q, "api-prod")
	apiRev := revKeyFor(t, q, "api")
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: string(tk), Perspective: PerspectiveTarget})
	if err != nil {
		t.Fatal(err)
	}
	if nb.Perspective != PerspectiveTarget {
		t.Fatalf("perspective = %q, want target", nb.Perspective)
	}
	// runs edge: target -> the revision it runs.
	runs := findEdge(nb, string(tk), string(apiRev))
	if runs == nil || runs.Relation != RelationRuns || runs.To.Kind != KindRevision {
		t.Fatalf("target->revision runs edge missing/wrong: %+v", runs)
	}
	// dependency edge: target -> db (a service the revision depends on).
	dep := findEdge(nb, string(tk), "db")
	if dep == nil || dep.Relation != RelationDependency || dep.To.Kind != KindService {
		t.Fatalf("target->db dependency edge missing/wrong: %+v", dep)
	}
	// the invariant: NO edge has BOTH endpoints as targets.
	for _, e := range nb.Edges {
		if e.From.Kind == KindTarget && e.To.Kind == KindTarget {
			t.Errorf("fabricated a target-to-target edge: %+v", e)
		}
	}
}

// TestTargetProjection_RunsOnlyWhenAuthoritative proves the runs edge appears only for
// an exact/inferred link. An unresolved link draws no runs edge.
func TestTargetProjection_RunsOnlyWhenAuthoritative(t *testing.T) {
	dep := func(name, ref string) contract.Dependency {
		return contract.Dependency{Name: name, Ref: ref, Required: true, Compatibility: "^1.0.0"}
	}
	api := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "api", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Workload: contract.WorkloadService, Dependencies: []contract.Dependency{dep("db", "oci://x/db")}, Readiness: readyContract()}
	db := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "db", Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Workload: contract.WorkloadService, Readiness: readyContract()}
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: db, FS: fstest.MapFS{}}, Digest: "sha256:db"},
			{Bundle: &contract.Bundle{Contract: api, FS: fstest.MapFS{}}, Digest: "sha256:api"},
		},
		// target references a digest that matches NO revision -> unresolved link.
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "mystery", Service: "api", Digest: "sha256:nomatch", Compliance: StatusUnknown}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	tk := targetKeyFor(t, q, "mystery")
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: string(tk), Perspective: PerspectiveTarget})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range nb.Edges {
		if e.Relation == RelationRuns {
			t.Errorf("drew a runs edge for an unresolved target link: %+v", e)
		}
	}
	// C1: an unresolved-link target must NOT inherit a revision's declared
	// dependencies, so there is NO target->db dependency edge; the honest gap is a
	// TARGET_REVISION_UNRESOLVED limitation instead.
	if findEdge(nb, string(tk), "db") != nil {
		t.Errorf("unresolved-link target must NOT attribute a revision's declared dependency to the concrete target")
	}
	for _, e := range nb.Edges {
		if e.Relation == RelationDependency && e.From.Key == string(tk) {
			t.Errorf("unresolved-link target must draw no dependency edges from the target: %+v", e)
		}
	}
	if !hasLimitation(nb.Limitations.Items, "TARGET_REVISION_UNRESOLVED") {
		t.Errorf("unresolved-link target must surface the TARGET_REVISION_UNRESOLVED limitation: %+v", nb.Limitations)
	}
}

// TestProjection_PerspectiveValidation proves each perspective rejects a focus kind it
// cannot honestly project, and an unknown perspective is rejected.
func TestProjection_PerspectiveValidation(t *testing.T) {
	q := projectionFleet(t)
	cases := []NeighborhoodQuery{
		{Kind: KindService, Key: "web", Perspective: PerspectiveRevision},
		{Kind: KindService, Key: "web", Perspective: PerspectiveTarget},
		{Kind: KindRevision, Key: string(revKeyFor(t, q, "web")), Perspective: PerspectiveTarget},
		{Kind: KindService, Key: "web", Perspective: Perspective("bogus")},
	}
	for _, nq := range cases {
		if _, err := q.Neighborhood(nq); err == nil {
			t.Errorf("query %+v: expected an error", nq)
		}
	}
}

// TestProjection_DefaultServicePerspectiveUnchanged proves the default perspective is
// still the logical-service projection with service nodes.
func TestProjection_DefaultServicePerspectiveUnchanged(t *testing.T) {
	q := projectionFleet(t)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if nb.Perspective != PerspectiveService {
		t.Errorf("default perspective = %q, want service", nb.Perspective)
	}
	for _, n := range nb.Nodes {
		if n.Ref.Kind != KindService {
			t.Errorf("service projection node is not a service: %+v", n.Ref)
		}
	}
}

// TestProjection_BoundedAndDeterministic proves the projections respect the node cap
// (truncation) and are deterministic across identical queries without mutating the
// snapshot.
func TestProjection_BoundedAndDeterministic(t *testing.T) {
	q := projectionFleet(t)
	web := string(revKeyFor(t, q, "web"))

	// A tiny node cap truncates to just the focus.
	capped, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, MaxNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.Nodes) != 1 || !capped.Truncated {
		t.Errorf("maxNodes=1 should yield 1 node + truncated; got %d nodes truncated=%v", len(capped.Nodes), capped.Truncated)
	}

	// Two identical queries are byte-identical (deterministic; no snapshot mutation).
	a, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionBoth})
	b, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionBoth})
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Errorf("revision projection is not deterministic across identical queries")
	}
}

// TestRevisionFocus_AllResolutions covers every focus resolution: a missing revision,
// a target with an exact link (maps to its revision), a target with an unresolved link
// (rejected), and a non-revision/non-target focus (rejected).
func TestRevisionFocus_AllResolutions(t *testing.T) {
	q := projectionFleet(t)
	apiProd := targetKeyFor(t, q, "api-prod")
	apiRev := revKeyFor(t, q, "api")
	mystery := targetKeyFor(t, q, "mystery")

	// missing revision.
	if _, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: "nope@sha256:x", Perspective: PerspectiveRevision}); err == nil {
		t.Error("missing revision focus should error")
	}
	// missing target (GetTarget error path) in a revision perspective.
	if _, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: "prod/k8s/nope", Perspective: PerspectiveRevision}); err == nil {
		t.Error("missing target focus should error")
	}
	// target with an exact link -> focuses that revision.
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: string(apiProd), Perspective: PerspectiveRevision, Direction: DirectionDependencies})
	if err != nil {
		t.Fatalf("target-with-exact-link revision focus errored: %v", err)
	}
	if nodeByKey(nb, string(apiRev)) == nil {
		t.Error("revision perspective on an exactly-linked target must focus that revision")
	}
	// target with an unresolved link -> rejected for a revision perspective.
	if _, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: string(mystery), Perspective: PerspectiveRevision}); err == nil {
		t.Error("revision perspective on an unresolved target link should error")
	}
	// a non-revision/non-target focus -> rejected.
	if _, err := q.Neighborhood(NeighborhoodQuery{Kind: KindOwner, Key: "t", Perspective: PerspectiveRevision}); err == nil {
		t.Error("owner focus in a revision perspective should error")
	}
}

// TestRevisionProjection_EdgeCapAndMerge covers the edge cap (a small maxEdges
// truncates) and the merge/dedupe of an edge reached from both directions (the
// depth-2 both walk re-derives web->api as a dependent of api).
func TestRevisionProjection_EdgeCapAndMerge(t *testing.T) {
	q := projectionFleet(t)
	api := string(revKeyFor(t, q, "api"))
	// api has two resolved dependency edges (db, cache); maxEdges=1 truncates to one.
	capped, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: api, Perspective: PerspectiveRevision, Direction: DirectionDependencies, MaxEdges: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.Edges) != 1 || !capped.Truncated {
		t.Errorf("maxEdges=1 on api should yield 1 edge + truncated; got %d edges truncated=%v", len(capped.Edges), capped.Truncated)
	}

	// A depth-2 both walk from web reaches web->api (dependency) and, expanding api's
	// dependents, re-derives web->api (a locker) -- the edge is merged, not duplicated.
	web := string(revKeyFor(t, q, "web"))
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Direction: DirectionBoth, Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range nb.Edges {
		if e.From.Key == web && e.To.Key == api {
			n++
		}
	}
	if n != 1 {
		t.Errorf("web->api edge should appear exactly once (merged), got %d", n)
	}
	// The unresolved deps come from BOTH web (ghost, ghost2) and api (missing1),
	// exercising the from-key and ref tie-breaks of the unresolved ordering.
	if nb.UnresolvedDependencies.Total < 3 {
		t.Errorf("expected web's and api's unresolved deps together, got %d", nb.UnresolvedDependencies.Total)
	}
}

// TestRevisionProjection_LeafHasNoResolvedDepExpansion covers a revision node with no
// resolved dependencies (db): it advertises no dependency expansion.
func TestRevisionProjection_LeafHasNoResolvedDepExpansion(t *testing.T) {
	q := projectionFleet(t)
	db := string(revKeyFor(t, q, "db"))
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: db, Perspective: PerspectiveRevision, Direction: DirectionBoth})
	if err != nil {
		t.Fatal(err)
	}
	focus := nodeByKey(nb, db)
	for _, e := range focus.Expansions {
		if e == DirectionDependencies {
			t.Errorf("db has no resolved dependency, so it must not advertise a dependencies expansion")
		}
	}
}

// TestTargetProjection_Dependents covers the dependents direction (services depending
// on the target's service, deduped across a consumer's multiple revisions) and the
// service-level dependency fallback for an unresolved target link.
func TestTargetProjection_Dependents(t *testing.T) {
	q := projectionFleet(t)
	apiProd := string(targetKeyFor(t, q, "api-prod"))
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: apiProd, Perspective: PerspectiveTarget, Direction: DirectionBoth})
	if err != nil {
		t.Fatal(err)
	}
	// C2: web and app depend on api's LOGICAL SERVICE. They are drawn as
	// consumer->service edges (never consumer->concrete-target), so the target is
	// never rendered as the specific routing endpoint. app appears ONCE despite two
	// revisions.
	apps := 0
	for _, n := range nb.Nodes {
		if n.Ref.Key == "app" {
			apps++
		}
	}
	if apps != 1 {
		t.Errorf("app (two revisions depending on api) must appear once as a dependent, got %d", apps)
	}
	if nodeByKey(nb, "web") == nil {
		t.Errorf("web must appear as a dependent of api's service")
	}
	// The dependent edges point to the LOGICAL SERVICE "api", not to the concrete
	// target api-prod, and no edge targets the concrete target as a dependency
	// provider endpoint.
	if findEdge(nb, "web", "api") == nil {
		t.Errorf("dependent edge must be consumer->logical-service (web->api), not consumer->target")
	}
	for _, e := range nb.Edges {
		if e.Relation == RelationDependency && e.To.Key == apiProd {
			t.Errorf("no dependency edge may point AT the concrete target as its provider endpoint: %+v", e)
		}
	}

	// The C1 no-inheritance counterexamples (unresolved and ambiguous targets) are
	// covered by TestTargetProjection_UnresolvedNoDependencyInheritance and
	// TestTargetProjection_AmbiguousNoDependencyInheritance in projection_views_test.go.
}

// TestTargetProjection_ObservedLimitation covers the target-perspective observation
// limitation branch.
func TestTargetProjection_ObservedLimitation(t *testing.T) {
	q := projectionFleet(t)
	apiProd := string(targetKeyFor(t, q, "api-prod"))
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindTarget, Key: apiProd, Perspective: PerspectiveTarget, Views: []KnowledgeView{ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	if nb.Limitations.Count == 0 {
		t.Errorf("target projection under an observed view must record the target-scope limitation")
	}
}

// TestProjection_ObservedLimitation proves a projection records the honest
// observation-scope limitation when an observation-bearing view is requested.
func TestProjection_ObservedLimitation(t *testing.T) {
	q := projectionFleet(t)
	web := string(revKeyFor(t, q, "web"))
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Views: []KnowledgeView{ViewDifferences}})
	if err != nil {
		t.Fatal(err)
	}
	if nb.Limitations.Count == 0 {
		t.Errorf("revision projection under a differences view must record the observed-scope limitation")
	}
	// Without an observation view there is no such limitation.
	nb2, _ := q.Neighborhood(NeighborhoodQuery{Kind: KindRevision, Key: web, Perspective: PerspectiveRevision, Views: []KnowledgeView{ViewExpected}})
	if nb2.Limitations.Count != 0 {
		t.Errorf("expected-only revision projection must not record an observation limitation")
	}
}
