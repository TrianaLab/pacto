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
}
