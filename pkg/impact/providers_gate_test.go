package impact

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestNoGateReadsProviders: two snapshots that differ only by detected
// provider rows give the same impact answer, status items, readiness and
// compliance tallies. A detected row is a hint and never clears a gate.
func TestNoGateReadsProviders(t *testing.T) {
	build := func(detected bool) *fleet.FleetSnapshot {
		srcs := []fleet.Source{fleet.NewMemorySource("delivery", "kubernetes", &fleet.Collection{
			Revisions: []fleet.RawRevision{gwRev(authDep)},
			Targets:   []fleet.RawTarget{gwTarget(&evidenceNow)},
		})}
		if detected {
			at := evidenceNow
			srcs = append(srcs, fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true, Providers: []fleet.Provider{
				{ID: "registry", Name: "Registry", Evidence: "ghcr.io/x", Provenance: fleet.ProvenanceInferred, AsOf: &at, Hint: "oci://ghcr.io/x/<service>:<version>"},
				{ID: "cloud", Name: "Cloud", Evidence: "unknown", Status: fleet.SourceUnavailable, Provenance: fleet.ProvenanceInferred, AsOf: &at, Scope: fleet.ScopeCluster},
			}}))
		}
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, srcs...)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	without, with := build(false), build(true)
	if len(with.Providers) != 2 {
		t.Fatalf("providers = %+v", with.Providers)
	}
	a, b := analyzeAuth(without, Options{}), analyzeAuth(with, Options{})
	if !reflect.DeepEqual(a.Consumers, b.Consumers) || a.Gate != b.Gate || a.Completeness != b.Completeness || !reflect.DeepEqual(a.Limitations, b.Limitations) {
		t.Errorf("impact changed with detected rows: %+v %s %s, want %+v %s %s", b.Consumers, b.Gate, b.Completeness, a.Consumers, a.Gate, a.Completeness)
	}
	qa, qb := fleet.NewQuery(without), fleet.NewQuery(with)
	if sa, sb := qa.Status(fleet.StatusQuery{NeedsAttention: true}), qb.Status(fleet.StatusQuery{NeedsAttention: true}); !reflect.DeepEqual(sa.Items, sb.Items) || sa.Total != sb.Total {
		t.Errorf("status items changed: %+v, want %+v", sb.Items, sa.Items)
	}
	oa, ob := qa.Overview().Summary, qb.Overview().Summary
	if !reflect.DeepEqual(oa.Readiness, ob.Readiness) || oa.CompliantTargets != ob.CompliantTargets || oa.NonCompliantTargets != ob.NonCompliantTargets || oa.UnknownTargets != ob.UnknownTargets {
		t.Errorf("readiness or compliance tallies changed: %+v, want %+v", ob, oa)
	}
}
