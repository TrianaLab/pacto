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

// Each state bucket of sourceCounts leads to exactly as many rows as it
// counts, with an advisory source in that state left out of both.
func TestSourceCountBucketsMatchTheirFilter(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("oci", "oci", &Collection{State: &SourceState{Status: SourcePartial}}),
		NewMemorySource("edge", "oci", &Collection{State: &SourceState{Status: SourceUnavailable}}),
		advisoryMesh(),
		NewMemorySource("mesh-down", SourceKindMesh, &Collection{Advisory: true, State: &SourceState{Status: SourceUnavailable}}))
	q := NewQuery(snap)
	c := sourceCounts(snap.Sources)
	for health, want := range map[string]int{string(SourcePartial): c.Partial, string(SourceUnavailable): c.Unavailable,
		string(SourceStale): c.Stale, string(SourceAvailable): c.Available} {
		l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, SourceHealth: health})
		if err != nil {
			t.Fatal(err)
		}
		if l.Total != want {
			t.Errorf("sourceHealth=%s lists %d rows; its bucket counts %d", health, l.Total, want)
		}
	}
	if c.Partial != 1 || c.Unavailable != 1 {
		t.Errorf("counts %+v", c)
	}
}
