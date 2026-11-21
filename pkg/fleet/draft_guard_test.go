package fleet

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// draftGuardRange is the range a draft requests for audit; no published
// version of invoices declares it.
const draftGuardRange = "^1.4.0"

// draftGuardRevisions is invoices 1.0.0 (audit's range unknown), a draft of
// invoices whose contract and record request audit ^1.4.0, and audit 1.4.2.
func draftGuardRevisions(t *testing.T) []RawRevision {
	t.Helper()
	invoices := func(version, compat string) *contract.Contract {
		return &contract.Contract{PactoVersion: "2.5",
			Service:      contract.Service{Name: "invoices", Version: version, Owner: contract.Owner{Team: "billing"}},
			Dependencies: []contract.Dependency{{Name: "audit", Required: true, Compatibility: compat}}}
	}
	rec, err := contract.ParseInferredRecord(declaredRecord(t, contract.DeclarationDraft,
		contract.InferredSigner{Issuer: contract.SignerUnsigned, Class: contract.SignerClassAgent}))
	if err != nil {
		t.Fatal(err)
	}
	rec.Declaration.Requested = []contract.InferredAcceptance{{Field: "dependencies[audit].compatibility", Range: draftGuardRange}}
	draftFS := fstest.MapFS{"deploy/app.yaml": {Data: []byte(recordManifest)}, contract.InferredRecordFile: {Data: rec.Marshal()}}
	audit := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "audit", Version: "1.4.2", Owner: contract.Owner{Team: "t"}}}
	return []RawRevision{
		{Bundle: &contract.Bundle{Contract: invoices("1.0.0", contract.CompatibilityUnknown), FS: fstest.MapFS{}}, Domain: "acme", Digest: "sha256:invoices"},
		{Bundle: &contract.Bundle{Contract: invoices("1.1.0", draftGuardRange), FS: draftFS}, Domain: "acme", Digest: "sha256:draft"},
		{Bundle: &contract.Bundle{Contract: audit, FS: fstest.MapFS{}}, Domain: "acme", Digest: "sha256:audit"},
	}
}

// A draft's requested range is a request waiting for a person: the fleet
// builds no revision from the draft and the relationship keeps the declared
// unknown range.
func TestDraftRequestedRangeNeverDeclared(t *testing.T) {
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("oci", "oci", &Collection{Revisions: draftGuardRevisions(t)}))
	if err != nil {
		t.Fatal(err)
	}
	inv := NewServiceKeyDomain("acme", "invoices")
	if s := snap.Services[inv]; s == nil || len(s.Revisions) != 1 || !hasLimitation(snap.Limitations, LimitationDeclarationDraftIgnored) {
		t.Fatalf("invoices = %+v, limitations %+v; want one revision and the draft left out", s, snap.Limitations)
	}
	n := 0
	for _, r := range snap.Relationships {
		if r.FromService != inv || r.To != "audit" {
			continue
		}
		n++
		if r.Compatibility != contract.CompatibilityUnknown || r.RangeStatus != RangeUnknown {
			t.Errorf("relationship = %+v; want the declared unknown range", r)
		}
	}
	if n == 0 {
		t.Fatal("invoices -> audit relationship missing")
	}
}
