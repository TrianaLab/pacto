package fleet

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestUndeclaredOnlyObservationKeepsDeclaredInsufficient(t *testing.T) {
	a := bundleFor(t, "a")
	a.Contract.Dependencies = []contract.Dependency{{Name: "b", Compatibility: "^1.0.0", Required: true}}
	var revs []RawRevision
	for _, b := range []*contract.Bundle{a, bundleFor(t, "b"), bundleFor(t, "c"), bundleFor(t, "x"), bundleFor(t, "y")} {
		revs = append(revs, RawRevision{Bundle: b, Digest: "sha256:" + b.Contract.Service.Name})
	}
	declared := NewMemorySource("defs", "local", &Collection{Revisions: revs})
	undeclaredOnly := NewMemorySource("k8s", SourceKindKubernetes, &Collection{
		ObservedUndeclaredOnly: true,
		Observed:               []ObservedEdge{{From: "a", To: "c", Count: 1}},
	})
	reconciliationOfAB := func(sources ...Source) string {
		t.Helper()
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
		if err != nil {
			t.Fatal(err)
		}
		if r := relFrom(snap.Relationships, "a", "b"); r != nil {
			return r.Reconciliation
		}
		t.Fatal("no declared a->b relationship")
		return ""
	}
	if got := reconciliationOfAB(declared, undeclaredOnly); got != ReconciliationInsufficient {
		t.Fatalf("undeclared-only observation: a->b = %q, want insufficient", got)
	}
	regular := NewMemorySource("traces", "observation", &Collection{Observed: []ObservedEdge{{From: "x", To: "y", Count: 1}}})
	if got := reconciliationOfAB(declared, undeclaredOnly, regular); got != ReconciliationDeclaredNotObserved {
		t.Fatalf("regular observation: a->b = %q, want declared-not-observed", got)
	}
	// The same edge from both kinds of source: the regular one still counts.
	shared := NewMemorySource("traces", "observation", &Collection{Observed: []ObservedEdge{{From: "a", To: "c", Count: 1}}})
	if got := reconciliationOfAB(declared, undeclaredOnly, shared); got != ReconciliationDeclaredNotObserved {
		t.Fatalf("shared edge: a->b = %q, want declared-not-observed", got)
	}
}

// miraLikeSnapshot builds declared a->b and a->d plus an undeclared-only source of
// kind mira that witnessed a->b and a->c.
func miraLikeSnapshot(t *testing.T) *FleetSnapshot {
	t.Helper()
	a := bundleFor(t, "a")
	a.Contract.Dependencies = []contract.Dependency{
		{Name: "b", Compatibility: "^1.0.0", Required: true},
		{Name: "d", Compatibility: "^1.0.0", Required: true},
	}
	var revs []RawRevision
	for _, b := range []*contract.Bundle{a, bundleFor(t, "b"), bundleFor(t, "c"), bundleFor(t, "d")} {
		revs = append(revs, RawRevision{Bundle: b, Digest: "sha256:" + b.Contract.Service.Name})
	}
	declared := NewMemorySource("defs", "local", &Collection{Revisions: revs})
	mira := NewMemorySource("mira", SourceKindMira, &Collection{
		ObservedUndeclaredOnly: true,
		Observed:               []ObservedEdge{{From: "a", To: "b", Count: 3}, {From: "a", To: "c", Count: 1}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, declared, mira)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestWitnessedDeclaredEdgeMatches(t *testing.T) {
	got := map[string]string{}
	for _, r := range miraLikeSnapshot(t).Relationships {
		if r.Provenance == ProvenanceDeclared {
			got[string(r.FromService)+"->"+r.To] = r.Reconciliation
		}
	}
	if got["a->b"] != ReconciliationMatched || got["a->d"] != ReconciliationInsufficient {
		t.Fatalf("declared reconciliation = %v, want a->b matched and a->d insufficient", got)
	}
}

func TestMiraObservationNeverMakesDeclaredUnobserved(t *testing.T) {
	for _, r := range miraLikeSnapshot(t).Relationships {
		if r.Reconciliation == ReconciliationDeclaredNotObserved {
			t.Fatalf("%s->%s is declared-not-observed from a partial-coverage source", r.FromService, r.ToService)
		}
	}
}
