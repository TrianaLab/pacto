package fleet

import "testing"

// advisoryMesh is an advisory mesh source that read part of the cluster and
// saw cart call ledger.
func advisoryMesh(edges ...ObservedEdge) Source {
	return NewMemorySource("mesh", SourceKindMesh, &Collection{
		Advisory: true, Observed: edges, ObservedUndeclaredOnly: true,
		State: &SourceState{Status: SourcePartial},
	})
}

func TestMeshPartialKeepsSnapshotComplete(t *testing.T) {
	cl := clusterOf(clusterA, "eu", ClusterNameFromContext)
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: cl, Targets: []RawTarget{workload("prod", "payments")}}),
		workloadsSource("k8s-workloads", cl, inferredCart(), rawInferred("shop", "StatefulSet", "ledger")),
		advisoryMesh(call(kindlessCart, kindlessLedger)))
	if rels := inferredRels(NewQuery(snap), cartName); len(rels) != 1 {
		t.Fatalf("the advisory source's edge did not fold: %+v", snap.InferredRelationships)
	}
	if snap.Completeness != CompletenessComplete || snap.ConsumerCompleteness() != CompletenessComplete {
		t.Errorf("completeness %s / %s", snap.Completeness, snap.ConsumerCompleteness())
	}
	if lims := degradedLimitations(snap); len(lims) != 0 {
		t.Errorf("degraded limitations %+v", lims)
	}
	if st := sourceState(t, snap, "mesh"); st.Status != SourcePartial || !st.Advisory {
		t.Errorf("mesh state %+v", st)
	}
}

func TestIncompleteSourcesListSkipsAdvisoryNotAvailable(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("oci", "oci", &Collection{State: &SourceState{Status: SourcePartial}}),
		advisoryMesh())
	q := NewQuery(snap)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, SourceHealth: SourceHealthIncomplete})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entities) != 1 || l.Entities[0].Key != "oci" {
		t.Errorf("incomplete sources %+v", l.Entities)
	}
	if got := q.Overview().Summary; l.Total != got.DegradedSources+got.StaleSources+got.UnavailableSources {
		t.Errorf("list total %d, tile %+v", l.Total, got)
	}
	if all, _ := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}}); all.Total != 2 {
		t.Errorf("unfiltered sources %+v", all.Entities)
	}
}
