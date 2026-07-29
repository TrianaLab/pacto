package fleet

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// immutBundle builds a bundle whose contract carries a mutable map and a
// dependency, so mutation attempts have something to target.
func immutBundle(name string) *contract.Bundle {
	c := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Dependencies: []contract.Dependency{{Name: "dep", Ref: "oci://x/dep", Required: true, Compatibility: "^1.0.0"}},
		Metadata:     map[string]any{"tier": "gold"},
	}
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{}}
}

func immutSnapshot(t *testing.T) (*FleetSnapshot, *Collection) {
	t.Helper()
	col := &Collection{
		Revisions: []RawRevision{{Bundle: immutBundle("svc"), Digest: "sha256:svc"}},
		Targets: []RawTarget{{
			Scope: "prod", Kind: "k8s", Name: "ns/svc", Service: "svc",
			Compliance: StatusNonCompliant, Labels: map[string]string{"env": "prod"},
			ObservedRuntime: map[string]any{"replicas": 3},
			Coverage:        &Coverage{Evaluated: 2, Required: 3},
			Readiness:       &readiness.Result{Score: 80, Passing: true},
		}},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap, col
}

// TestBuild_OwnsSourceData: mutating the source's contract/collection after Build
// must not change the snapshot.
func TestBuild_OwnsSourceData(t *testing.T) {
	snap, col := immutSnapshot(t)
	before := mustJSON(t, snap)

	// Mutate everything the source still holds.
	col.Revisions[0].Bundle.Contract.Metadata["tier"] = "bronze"
	col.Revisions[0].Bundle.Contract.Service.Name = "hacked"
	col.Revisions[0].Bundle.Contract.Dependencies[0].Name = "evil"
	col.Targets[0].Labels["env"] = "hacked"
	col.Targets[0].ObservedRuntime["replicas"] = 99
	col.Targets[0].Compliance = StatusCompliant

	if after := mustJSON(t, snap); after != before {
		t.Error("mutating the source after Build changed the snapshot")
	}
}

// TestQuery_ResultsAreDefensiveCopies: mutating any returned result must not
// affect the snapshot or a subsequent query.
func TestQuery_ResultsAreDefensiveCopies(t *testing.T) {
	snap, _ := immutSnapshot(t)
	q := NewQuery(snap)
	before := mustJSON(t, snap)

	sv, err := q.GetService("svc")
	if err != nil {
		t.Fatal(err)
	}
	sv.Service.Name = "hacked"
	sv.Service.Status = "hacked"
	if len(sv.Revisions) > 0 {
		sv.Revisions[0].Version = "9.9.9"
		if sv.Revisions[0].Contract != nil {
			sv.Revisions[0].Contract.Metadata["tier"] = "bronze"
		}
	}
	if len(sv.Targets) > 0 {
		sv.Targets[0].Labels["env"] = "hacked"
		sv.Targets[0].Compliance = "hacked"
	}

	tv, err := q.GetTarget("ns/svc")
	if err != nil {
		t.Fatal(err)
	}
	tv.Target.Compliance = "hacked"
	tv.Target.Labels["env"] = "hacked"
	if tv.Revision != nil {
		tv.Revision.Version = "0.0.0"
	}

	res, err := q.Search(SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range res.Services {
		res.Services[i].Name = "hacked"
		res.Services[i].Sources = append(res.Services[i].Sources, "hacked")
	}

	if q.SnapshotID() != snap.SnapshotID || q.SnapshotID() == "" {
		t.Error("SnapshotID accessor must match the snapshot id")
	}
	snapCopy := q.Snapshot()
	snapCopy.Services[NewServiceKey("svc")].Name = "hacked"
	delete(snapCopy.Revisions, snapCopy.Revisions[firstRevKey(t, snapCopy)].Key)

	if after := mustJSON(t, snap); after != before {
		t.Error("mutating a query result changed the snapshot")
	}
	// A second query still sees the original values.
	sv2, err := q.GetService("svc")
	if err != nil || sv2.Service.Name != "svc" {
		t.Errorf("second query saw mutated state: %+v err=%v", sv2.Service, err)
	}
}

// TestQuery_ConcurrentReadsRaceSafe drives many concurrent queries (with -race)
// while each mutates its own returned results.
func TestQuery_ConcurrentReadsRaceSafe(t *testing.T) {
	snap, _ := immutSnapshot(t)
	q := NewQuery(snap)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sv, err := q.GetService("svc"); err == nil && sv.Service != nil {
				sv.Service.Name = "local-mutation"
			}
			_, _ = q.Search(SearchFilter{Text: "svc"})
			_, _ = q.Graph(GraphQuery{Service: "svc", Transitive: true})
			_ = q.Status(StatusQuery{NeedsAttention: true})
		}()
	}
	wg.Wait()
	if got := snap.Services[NewServiceKey("svc")].Name; got != "svc" {
		t.Errorf("concurrent readers mutated the snapshot: %q", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func firstRevKey(t *testing.T, snap *FleetSnapshot) RevisionKey {
	t.Helper()
	for k := range snap.Revisions {
		return k
	}
	t.Fatal("no revisions")
	return ""
}
