package fleet

import (
	"slices"
	"strings"
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

// A kindless name from a source with no cluster in a multi-cluster snapshot
// matches no cluster's workload: it stays unknown, never guessed.
func TestObservedKindlessNameNoClusterStaysUnknown(t *testing.T) {
	var srcs []Source
	for _, id := range []string{clusterA, clusterB} {
		srcs = append(srcs, workloadsSource("w-"+id[:4], clusterOf(id, id[:4], ClusterNameFromContext), inferredCart(), rawInferred("shop", "StatefulSet", "ledger")))
	}
	srcs = append(srcs, NewMemorySource("mesh", "observation", &Collection{Observed: []ObservedEdge{call(kindlessCart, kindlessLedger)}}))
	snap := buildClusterSnap(t, srcs...)
	if len(snap.InferredRelationships) != 0 || len(snap.Relationships) != 0 {
		t.Errorf("a clusterless name was matched: %+v %+v", snap.InferredRelationships, snap.Relationships)
	}
	if k, res := snap.ObservedNameResolver()(kindlessCart); k != "" || res != ObservedUnknown {
		t.Errorf("resolved %s %v", k, res)
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

// A kindless name ambiguous only because of a workload of a kind the reader
// may not list never tells the reader that workload exists.
func TestObservedKindlessAmbiguityHiddenByKind(t *testing.T) {
	snap := kindlessSnap(t, []RawInferredService{rawInferred("shop", "Deployment", "cache"), rawInferred("shop", "StatefulSet", "cache")},
		call("k8s:shop//cache", kindlessLedger))
	view := NewQuery(snap).Restrict(func(string, string) bool { return true },
		func(_, _, kind string) bool { return kind != "StatefulSet" })
	for _, l := range view.Snapshot().Limitations {
		if strings.Contains(l.Message, "k8s:shop//cache") {
			t.Errorf("limitation %+v names the hidden workload's name", l)
		}
	}
}

// One known workload of a name is not the only one when the workload source
// did not read every workload: the kindless name stays unknown.
func TestObservedKindlessNameUnknownWhileWorkloadsPartial(t *testing.T) {
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{workload("prod", "payments")}}),
		NewMemorySource("k8s-workloads", SourceKindKubernetesWorkloads, &Collection{Cluster: cl, InferredServices: []RawInferredService{inferredCart()},
			State: &SourceState{Status: SourcePartial}}))
	if k, res := snap.ObservedNameResolver()(kindlessCart); k != "" || res != ObservedUnknown {
		t.Errorf("resolved %s %v while the workload list was partial", k, res)
	}
}

// In a multi-cluster snapshot only the cluster whose workload list is
// incomplete leaves its kindless names unknown.
func TestObservedKindlessPartialWorkloadsPerCluster(t *testing.T) {
	var srcs []Source
	for i, id := range []string{clusterA, clusterB} {
		col := &Collection{Cluster: clusterOf(id, id[:4], ClusterNameFromContext), InferredServices: []RawInferredService{inferredCart()}}
		if i == 0 {
			col.State = &SourceState{Status: SourcePartial}
		}
		srcs = append(srcs, NewMemorySource("w-"+id[:4], SourceKindKubernetesWorkloads, col))
	}
	resolve := buildClusterSnap(t, srcs...).ObservedNameResolver()
	if _, res := resolve(K8sCaller(clusterA, "shop", "", "cart")); res != ObservedUnknown {
		t.Errorf("partial cluster resolved %v", res)
	}
	if k, res := resolve(K8sCaller(clusterB, "shop", "", "cart")); res != ObservedInferred || k != NewServiceKey(K8sCaller(clusterB, "shop", "Deployment", "cart")) {
		t.Errorf("complete cluster resolved %s %v", k, res)
	}
}
