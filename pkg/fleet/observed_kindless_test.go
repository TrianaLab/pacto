package fleet

import (
	"slices"
	"testing"
)

const (
	kindlessCart   = "k8s:shop//cart"
	kindlessLedger = "k8s:shop//ledger"
)

// kindlessSnap is one cluster with cart (Deployment) and ledger (StatefulSet)
// inferred in shop plus extra workloads, the declared payments in prod and a
// mesh that saw edges by workload name only.
func kindlessSnap(t *testing.T, extra []RawInferredService, edges ...ObservedEdge) *FleetSnapshot {
	t.Helper()
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	in := append([]RawInferredService{inferredCart(), rawInferred("shop", "StatefulSet", "ledger")}, extra...)
	return buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{workload("prod", "payments")}}),
		workloadsSource("k8s-workloads", cl, in...),
		NewMemorySource("mesh", "observation", &Collection{Observed: edges}))
}

func TestObservedKindlessNameResolvesTheOneWorkload(t *testing.T) {
	snap := kindlessSnap(t, nil, call(kindlessCart, kindlessLedger))
	rels := inferredRels(NewQuery(snap), cartName)
	if len(rels) != 1 || rels[0].ToService != NewServiceKey(ledgerName) {
		t.Fatalf("inferred relationships %+v", snap.InferredRelationships)
	}
	if lims := limitationsWith(snap, LimitationObservedIdentityUnresolved); len(lims) != 0 {
		t.Errorf("limitations %+v", lims)
	}
}

func TestObservedKindlessNameResolvesReplacedWorkload(t *testing.T) {
	tgt := workload("prod", "payments")
	tgt.Workload = &WorkloadRef{Kind: "Deployment", Name: "payments-v2"}
	snap := buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu", ClusterNameFromContext), Targets: []RawTarget{tgt}}))
	if k, res := snap.ObservedNameResolver()("k8s:prod//payments-v2"); k != NewServiceKey("payments") || res != ObservedResolved {
		t.Errorf("resolved %s %v", k, res)
	}
}

func TestObservedKindlessNameTwoKindsIsAmbiguous(t *testing.T) {
	snap := kindlessSnap(t, []RawInferredService{rawInferred("shop", "Deployment", "cache"), rawInferred("shop", "StatefulSet", "cache")},
		call("k8s:shop//cache", kindlessLedger))
	if k, res := snap.ObservedNameResolver()("k8s:shop//cache"); k != "" || res != ObservedAmbiguous {
		t.Errorf("resolved %s %v", k, res)
	}
	if len(snap.InferredRelationships) != 0 || len(snap.Relationships) != 0 {
		t.Errorf("an ambiguous edge was kept: %+v %+v", snap.InferredRelationships, snap.Relationships)
	}
	want := "observed service k8s:shop//cache could not be mapped to a unique fleet service (ambiguous: more than one workload of this name); "
	if !limitationMentions(snap, LimitationObservedIdentityUnresolved, want) || limitationMentions(snap, LimitationObservedIdentityUnresolved, "across domains") {
		t.Errorf("limitations %+v", snap.Limitations)
	}
}

func TestObservedKindlessNameNoWorkloadIsUnknown(t *testing.T) {
	snap := kindlessSnap(t, nil, call("k8s:shop//ghost", "payments"))
	want := []UncontractedCaller{{Caller: "k8s:shop//ghost", ToService: NewServiceKey("payments"), Sources: []string{"mesh"}}}
	if !slices.EqualFunc(snap.UncontractedCallers, want, callerEqual) {
		t.Errorf("callers %+v", snap.UncontractedCallers)
	}
	if k, res := snap.ObservedNameResolver()("k8s:shop//ghost"); k != "" || res != ObservedUnknown {
		t.Errorf("resolved %s %v", k, res)
	}
}

func TestObservedKindlessCallerTakesWorkloadName(t *testing.T) {
	snap := kindlessSnap(t, nil, call(kindlessCart, "payments"))
	want := []UncontractedCaller{{Caller: cartName, ToService: NewServiceKey("payments"), Sources: []string{"mesh"}, Entity: NewServiceKey(cartName)}}
	if !slices.EqualFunc(snap.UncontractedCallers, want, callerEqual) {
		t.Errorf("callers %+v", snap.UncontractedCallers)
	}
}

func TestObservedKindlessNameQualifiedPerCluster(t *testing.T) {
	var srcs []Source
	for _, id := range []string{clusterA, clusterB} {
		cl := clusterOf(id, id[:4], ClusterNameFromContext)
		srcs = append(srcs,
			workloadsSource("w-"+id[:4], cl, inferredCart(), rawInferred("shop", "StatefulSet", "ledger")),
			NewMemorySource("mesh-"+id[:4], "observation", &Collection{Cluster: cl, Observed: []ObservedEdge{call(kindlessCart, kindlessLedger)}}))
	}
	snap := buildClusterSnap(t, srcs...)
	if len(snap.InferredRelationships) != 2 {
		t.Fatalf("inferred relationships %+v", snap.InferredRelationships)
	}
	for _, id := range []string{clusterA, clusterB} {
		from, to := NewServiceKey(K8sCaller(id, "shop", "Deployment", "cart")), NewServiceKey(K8sCaller(id, "shop", "StatefulSet", "ledger"))
		if !slices.ContainsFunc(snap.InferredRelationships, func(r Relationship) bool { return r.FromService == from && r.ToService == to }) {
			t.Errorf("no %s -> %s in %+v", from, to, snap.InferredRelationships)
		}
	}
}

func TestObservedKindlessInferredEdgeStaysInferred(t *testing.T) {
	snap := kindlessSnap(t, nil, call(kindlessCart, kindlessLedger))
	q := NewQuery(snap)
	noObservedRelationship(t, q)
	if len(snap.Relationships) != 0 {
		t.Errorf("relationships %+v", snap.Relationships)
	}
	ledger := NewServiceKey(ledgerName)
	if len(snap.reverseDeps[ledger]) != 0 || len(snap.observedReverse[ledger]) != 0 || len(snap.forwardDeps[NewServiceKey(cartName)]) != 0 {
		t.Errorf("a declared or observed index holds the edge: %v %v %v", snap.reverseDeps, snap.observedReverse, snap.forwardDeps)
	}
}

func TestObservedKindlessRestrictHidesCaller(t *testing.T) {
	q := NewQuery(kindlessSnap(t, nil, call(kindlessCart, "payments")))
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "shop" }), anyClusterKind(everyWorkload))
	s := r.SnapshotForSerialization()
	if len(s.UncontractedCallers) != 0 {
		t.Errorf("the hidden cart still calls payments: %+v", s.UncontractedCallers)
	}
	if p := s.Services[NewServiceKey("payments")]; p == nil || p.HiddenDependents != 1 {
		t.Errorf("payments %+v", p)
	}
}

// callerEqual compares two uncontracted callers field by field.
func callerEqual(a, b UncontractedCaller) bool {
	return a.Caller == b.Caller && a.ToService == b.ToService && a.Ambiguous == b.Ambiguous && a.Entity == b.Entity && slices.Equal(a.Sources, b.Sources)
}
