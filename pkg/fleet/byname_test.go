package fleet

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestRefLessDependencyResolvesByName: a 2.5 consumer that names its provider
// with no ref resolves to the provider in its own domain, with an unknown
// range.
func TestRefLessDependencyResolvesByName(t *testing.T) {
	reports := &contract.Contract{
		PactoVersion: "2.5",
		Service:      contract.Service{Name: "reports", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Dependencies: []contract.Dependency{{Name: "ledger", Required: true, Compatibility: contract.CompatibilityUnknown}},
	}
	ledger := bundleFor(t, "ledger")
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("fin", "oci", &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: reports, FS: fstest.MapFS{}}, Domain: "finance", Digest: "sha256:reports"},
		{Bundle: ledger, Domain: "finance", Digest: "sha256:ledger"},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	rel := relFrom(snap.Relationships, string(NewServiceKeyDomain("finance", "reports")), "ledger")
	if rel == nil || !rel.Resolved || rel.ToService != NewServiceKeyDomain("finance", "ledger") || rel.RangeStatus != RangeUnknown || rel.RequestedRef != "" {
		t.Fatalf("relationship = %+v", rel)
	}
}
