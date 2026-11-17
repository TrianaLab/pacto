package fleet

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// derivedFleet builds payments published at oci://ghcr.io/acme/payments and a
// consumer in the same domain whose one dependency names no service: its name
// is derived from ref.
func derivedFleet(t *testing.T, ref string) Relationship {
	t.Helper()
	pay := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "payments", Version: "1.2.0", Owner: contract.Owner{Team: "t"}}}
	dep := contract.Dependency{Name: contract.DependencyName(ref), NameFromRef: true, Ref: ref, Compatibility: "^1.0.0", Required: true}
	shop := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "shop", Version: "1.0.0", Owner: contract.Owner{Team: "t"}},
		Dependencies: []contract.Dependency{dep}}
	src := NewMemorySource("oci", "oci", &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: pay, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme", RequestedRef: "oci://ghcr.io/acme/payments:1.2.0", Digest: "sha256:pay"},
		{Bundle: &contract.Bundle{Contract: shop, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme", RequestedRef: "oci://ghcr.io/acme/shop:1.0.0", Digest: "sha256:shop"},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Relationships {
		if r.Type == RelationshipDependency && r.Provenance == ProvenanceDeclared {
			return r
		}
	}
	t.Fatal("no declared dependency edge")
	return Relationship{}
}

func TestDerivedDependencyNameDoesNotResolveForeignRepo(t *testing.T) {
	r := derivedFleet(t, "oci://ghcr.io/other-org/payments")
	if r.Resolved || r.ToService != "" || r.RangeStatus != "" || !r.NameFromRef ||
		r.Reason != "the name was derived from the ref and no service in the fleet is published at ghcr.io/other-org/payments" {
		t.Fatalf("edge = %+v, want unresolved with the derived-name reason", r)
	}
}

func TestDerivedDependencyNameResolvesSameRepo(t *testing.T) {
	r := derivedFleet(t, "oci://ghcr.io/acme/payments:1.2.0")
	if !r.Resolved || r.ToService != NewServiceKeyDomain("ghcr.io/acme", "payments") || r.RangeStatus != RangeSatisfied || !r.NameFromRef {
		t.Fatalf("edge = %+v, want resolved to the payments published at that repository", r)
	}
}

func TestDerivedLocalRefNeverResolves(t *testing.T) {
	for _, ref := range []string{"file://../payments", "../payments"} {
		if r := derivedFleet(t, ref); r.Resolved || r.ToService != "" ||
			r.Reason != "the name was derived from a local ref, which names no published repository: declare the name" {
			t.Errorf("%s: edge = %+v, want unresolved", ref, r)
		}
	}
}

func TestDerivedNameCarriedToUnresolvedRow(t *testing.T) {
	r := derivedFleet(t, "oci://ghcr.io/other-org/payments")
	if row := unresolvedDep(EntityRef{}, r); !row.NameFromRef {
		t.Errorf("row = %+v, want nameFromRef", row)
	}
}

// TestDerivedDependencyNameSameDomainOtherRepo: payments-pacto finds payments by
// the bundle-name suffix rule, but payments was read from another repository.
func TestDerivedDependencyNameSameDomainOtherRepo(t *testing.T) {
	if r := derivedFleet(t, "oci://ghcr.io/acme/payments-pacto"); r.Resolved {
		t.Fatalf("edge = %+v, want unresolved", r)
	}
}
