package fleet

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

// inferredRels is every inferred relationship of q from from.
func inferredRels(q *Query, from string) []Relationship {
	return slices.DeleteFunc(slices.Clone(q.snap.InferredRelationships), func(r Relationship) bool { return r.FromService != NewServiceKey(from) })
}

// noObservedRelationship fails when q holds an observed relationship.
func noObservedRelationship(t *testing.T, q *Query) {
	t.Helper()
	for _, rel := range q.snap.Relationships {
		if rel.Provenance == ProvenanceObserved || rel.Provenance == ProvenanceInferred && rel.ObservedCount > 0 {
			t.Errorf("a runtime edge with an inferred end entered Relationships: %+v", rel)
		}
	}
}

func TestFoldKeepsInferredEdge(t *testing.T) {
	at := fixedNow().Add(-time.Hour)
	k := call(cartName, ledgerName)
	k.FirstSeen, k.LastSeen = at, at.Add(time.Minute)
	m := ObservedEdge{From: cartName, To: ledgerName, Count: 2, FirstSeen: at.Add(-time.Hour), LastSeen: at}
	q := goldenWith(t, true, kernel(k), NewMemorySource("mesh", "observation", &Collection{Observed: []ObservedEdge{m}}))
	rels := inferredRels(q, cartName)
	if len(rels) != 1 {
		t.Fatalf("inferred relationships %+v", q.snap.InferredRelationships)
	}
	r := rels[0]
	if r.Provenance != ProvenanceInferred || r.Type != RelationshipDependency || r.ToService != NewServiceKey(ledgerName) || !r.Resolved || r.ObservedCount != 5 || r.Source != "" {
		t.Errorf("relationship %+v", r)
	}
	if !r.FirstSeen.Equal(m.FirstSeen) || !r.LastSeen.Equal(k.LastSeen) {
		t.Errorf("window %v %v", r.FirstSeen, r.LastSeen)
	}
	want := []ObservedSourceStat{
		{Source: "kernel", Count: 3, FirstSeen: &k.FirstSeen, LastSeen: &k.LastSeen},
		{Source: "mesh", Count: 2, FirstSeen: &m.FirstSeen, LastSeen: &m.LastSeen},
	}
	if !reflect.DeepEqual(r.ObservedSources, want) {
		t.Errorf("sources %+v", r.ObservedSources)
	}
	noObservedRelationship(t, q)
}

func TestFoldDeclaredToInferredEdge(t *testing.T) {
	q := goldenWith(t, true, kernel(call("orders", cartName)))
	orders := acmeService(q, "orders").Key
	if len(q.snap.InferredRelationships) != 1 {
		t.Fatalf("inferred relationships %+v", q.snap.InferredRelationships)
	}
	if r := q.snap.InferredRelationships[0]; r.FromService != orders || r.ToService != NewServiceKey(cartName) || r.Source != "kernel" {
		t.Errorf("relationship %+v", r)
	}
	if len(q.snap.UncontractedCallers) != 0 || limitationMentions(q.snap, LimitationObservedIdentityUnresolved, cartName) {
		t.Errorf("callers %+v limitations %+v", q.snap.UncontractedCallers, q.snap.Limitations)
	}
	noObservedRelationship(t, q)
}

func TestFoldInferredToDeclaredKeepsUncontractedCaller(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, "postgresql")))
	pg := acmeService(q, "postgresql").Key
	rels := inferredRels(q, cartName)
	if len(rels) != 1 || rels[0].ToService != pg {
		t.Fatalf("inferred relationships %+v", q.snap.InferredRelationships)
	}
	c := q.snap.UncontractedCallers
	if len(c) != 1 || c[0].Caller != cartName || c[0].Entity != NewServiceKey(cartName) || c[0].ToService != pg {
		t.Errorf("uncontracted callers %+v", c)
	}
	noObservedRelationship(t, q)
}

func TestFoldInferredToExternal(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, "api.stripe.com")))
	rels := inferredRels(q, cartName)
	if len(rels) != 1 || rels[0].ToExternal == "" || rels[0].ToService != "" || rels[0].Resolved {
		t.Fatalf("inferred relationships %+v", q.snap.InferredRelationships)
	}
	rec := q.snap.Externals[rels[0].ToExternal]
	if rec == nil || !slices.ContainsFunc(rec.Sources, func(s ExternalSource) bool {
		return s.Service == NewServiceKey(cartName) && s.Provenance == ProvenanceInferred && s.Kind == ExternalSourceObserved
	}) {
		t.Errorf("external record %+v", rec)
	}
	if limitationMentions(q.snap, LimitationObservedIdentityUnresolved, cartName) {
		t.Errorf("limitations %+v", q.snap.Limitations)
	}
	noObservedRelationship(t, q)
}

func TestFoldUnresolvedEndStillLimitation(t *testing.T) {
	gone := "k8s:shop/Deployment/gone"
	q := goldenWith(t, true, kernel(call(cartName, gone), call("k8s:ops/Job/other", sweepName), call(cartName, cartName)))
	if len(q.snap.InferredRelationships) != 0 {
		t.Errorf("an edge with an unresolved end or a self-loop was kept: %+v", q.snap.InferredRelationships)
	}
	for _, n := range []string{gone, "k8s:ops/Job/other"} {
		if !limitationMentions(q.snap, LimitationObservedIdentityUnresolved, n+" could not be mapped") {
			t.Errorf("no unknown limitation for %s: %+v", n, q.snap.Limitations)
		}
	}
	for _, n := range []string{cartName + " could", sweepName} {
		if limitationMentions(q.snap, LimitationObservedIdentityUnresolved, n) {
			t.Errorf("the inferred %s is reported unresolved", n)
		}
	}
}

func TestInferredEdgesStayOutOfIndexes(t *testing.T) {
	calls := kernel(call(cartName, ledgerName), call("orders", cartName), call(cartName, "postgresql"))
	q := goldenWith(t, true, calls)
	base := goldenWith(t, true)
	if !reflect.DeepEqual(q.snap.forwardDeps, base.snap.forwardDeps) || !reflect.DeepEqual(q.snap.reverseDeps, base.snap.reverseDeps) {
		t.Errorf("declared indexes changed: %v %v", q.snap.forwardDeps, q.snap.reverseDeps)
	}
	if len(q.snap.observedForward) != 0 || len(q.snap.observedReverse) != 0 {
		t.Errorf("observed indexes %v %v", q.snap.observedForward, q.snap.observedReverse)
	}
	cart, orders, pg := NewServiceKey(cartName), acmeService(q, "orders").Key, acmeService(q, "postgresql").Key
	if got := q.snap.inferredForward[cart]; !slices.Equal(got, []ServiceKey{pg, NewServiceKey(ledgerName)}) && !slices.Equal(got, []ServiceKey{NewServiceKey(ledgerName), pg}) {
		t.Errorf("inferred forward of cart %v", got)
	}
	if got := q.snap.inferredReverse[cart]; !slices.Equal(got, []ServiceKey{orders}) {
		t.Errorf("inferred reverse of cart %v", got)
	}
	noObservedRelationship(t, q)
}

func TestSnapshotIDCoversInferredEdges(t *testing.T) {
	a := goldenWith(t, true, kernel(call(cartName, ledgerName)))
	b := goldenWith(t, true, kernel(ObservedEdge{From: cartName, To: ledgerName, Count: 4}))
	if a.snap.SnapshotID == b.snap.SnapshotID {
		t.Error("two inferred edge counts share a snapshot id")
	}
}

func TestRestrictHidesInferredEdgeCountsOnce(t *testing.T) {
	q := inferredRestrictFleet(t)
	web := NewServiceKey("web")
	if !slices.ContainsFunc(q.snap.InferredRelationships, func(r Relationship) bool { return r.FromService == NewServiceKey(cartName) && r.ToService == web }) {
		t.Fatalf("no inferred cart edge to web: %+v", q.snap.InferredRelationships)
	}
	r := q.Restrict(anyCluster(everyNamespace), anyClusterKind(func(_, kind string) bool { return kind != "Deployment" }))
	s := r.SnapshotForSerialization()
	if slices.ContainsFunc(s.InferredRelationships, func(r Relationship) bool { return r.FromService == NewServiceKey(cartName) }) {
		t.Errorf("the hidden cart keeps its edge: %+v", s.InferredRelationships)
	}
	if got := s.Services[web].HiddenDependents; got != 1 {
		t.Errorf("web counts %d hidden dependents, want cart once", got)
	}
	if r.snap.inferredForward[NewServiceKey(cartName)] != nil || slices.Contains(r.snap.inferredReverse[web], NewServiceKey(cartName)) {
		t.Errorf("the inferred adjacency keeps the hidden cart: %v %v", r.snap.inferredForward, r.snap.inferredReverse)
	}
}

func TestRestrictInferredEdges(t *testing.T) {
	a, b, c, d := ServiceKey("a"), ServiceKey("b"), ServiceKey("c"), ServiceKey("d")
	rels := []Relationship{
		{FromService: a, ToService: b}, // both visible: kept
		{FromService: c, ToService: b}, // hidden caller: counted on b
		{FromService: a, ToService: d}, // hidden callee: counted on a
		{FromService: c, ToService: d}, // both hidden: dropped uncounted
		{FromService: c, ToExternal: "stripe"},
	}
	deps, dependents := map[ServiceKey]map[ServiceKey]bool{}, map[ServiceKey]map[ServiceKey]bool{}
	got := restrictInferredEdges(rels, map[ServiceKey]bool{c: true, d: true}, deps, dependents)
	if !reflect.DeepEqual(got, rels[:1]) {
		t.Errorf("kept %+v", got)
	}
	if !reflect.DeepEqual(dependents, map[ServiceKey]map[ServiceKey]bool{b: {c: true}}) || !reflect.DeepEqual(deps, map[ServiceKey]map[ServiceKey]bool{a: {d: true}}) {
		t.Errorf("dependents %v deps %v", dependents, deps)
	}
}

func TestFoldQualifiesInferredCalleeAcrossClusters(t *testing.T) {
	a, b := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	cart, ledger := K8sCaller("", "shop", "Deployment", "cart"), K8sCaller("", "shop", "Deployment", "ledger")
	snap := buildClusterSnap(t,
		workloadsSource("wa", a, rawInferred("shop", "Deployment", "cart"), rawInferred("shop", "Deployment", "ledger")),
		workloadsSource("wb", b),
		NewMemorySource("hubble", "observation", &Collection{Cluster: a, Observed: []ObservedEdge{call(cart, ledger)}}))
	from, to := K8sCaller(clusterA, "shop", "Deployment", "cart"), K8sCaller(clusterA, "shop", "Deployment", "ledger")
	if len(snap.InferredRelationships) != 1 || snap.InferredRelationships[0].FromService != NewServiceKey(from) ||
		snap.InferredRelationships[0].ToService != NewServiceKey(to) {
		t.Fatalf("inferred relationships %+v", snap.InferredRelationships)
	}
	if lims := limitationsWith(snap, LimitationObservedIdentityUnresolved); len(lims) != 0 {
		t.Errorf("a qualified callee stays unresolved: %+v", lims)
	}
}

func TestExternalGraphAndEvidenceShowInferredCaller(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, "api.stripe.com")))
	cart := NewServiceKey(cartName)
	g, err := q.Graph(GraphQuery{Service: "external:stripe", Direction: DirectionDependents, IncludeObserved: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(g.Nodes, func(n GraphNode) bool { return n.Key == cart }) ||
		!slices.ContainsFunc(g.Edges, func(r Relationship) bool { return r.FromService == cart && r.Provenance == ProvenanceInferred }) {
		t.Errorf("external graph nodes %+v edges %+v", g.Nodes, g.Edges)
	}
	declared, err := q.Graph(GraphQuery{Service: "external:stripe", Direction: DirectionDependents})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(declared.Nodes, func(n GraphNode) bool { return n.Key == cart }) {
		t.Errorf("an inferred caller shows without observed edges: %+v", declared.Nodes)
	}
	ext, err := q.GetExternal("stripe")
	if err != nil {
		t.Fatal(err)
	}
	if ext.ObservedSourcesTotal != 1 || ext.ObservedSources[0].Service != cart || ext.ObservedSources[0].Count != 3 {
		t.Errorf("external evidence %+v", ext.ExternalEvidence)
	}
}
