package fleet

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestProviderCurrentRevisionSeam(t *testing.T) {
	q := ownerProvenanceFleet(t, contract.Owner{Team: "pay"})
	if r := q.ProviderCurrentRevision(NewServiceKey("pay")); r == nil || r.Contract == nil || r.Contract.Service.Name != "pay" {
		t.Fatalf("current revision = %+v, want pay's revision", r)
	}
	if r := q.ProviderCurrentRevision(NewServiceKey("ghost")); r != nil {
		t.Errorf("unknown service has a current revision: %+v", r)
	}
}

func TestOwnerClaimCountSeam(t *testing.T) {
	q := ownerProvenanceFleet(t, contract.Owner{}, payTarget("a", contract.Owner{Team: "pay"}), payTarget("b", contract.Owner{Team: "ops"}))
	if n := q.OwnerClaimCount(NewServiceKey("pay")); n != 2 {
		t.Errorf("claims = %d, want 2", n)
	}
	if n := q.OwnerClaimCount(NewServiceKey("ghost")); n != 0 {
		t.Errorf("unknown service claims = %d, want 0", n)
	}
}

func TestHasContractSourceSeam(t *testing.T) {
	key := NewServiceKey("pay")
	if !ownerProvenanceFleet(t, contract.Owner{Team: "pay"}).HasContractSource(key) {
		t.Error("a revision is a contract source")
	}
	read := payTarget("a", contract.Owner{})
	read.BusinessDomainKnown = true
	if !ownerProvenanceFleet(t, contract.Owner{}, read).HasContractSource(key) {
		t.Error("a target whose source read the contract is a contract source")
	}
	if !ownerProvenanceFleet(t, contract.Owner{}, payTarget("a", contract.Owner{Team: "pay"})).HasContractSource(key) {
		t.Error("a target reporting the owner of the contract it runs is a contract source, whatever its status format")
	}
	q := ownerProvenanceFleet(t, contract.Owner{}, payTarget("a", contract.Owner{}))
	if q.HasContractSource(key) {
		t.Error("a target with no read contract is no contract source")
	}
	if q.HasContractSource(NewServiceKey("ghost")) {
		t.Error("an unknown service has a contract source")
	}
	// A service whose only revision is invalid still has a contract speaking for it.
	invalid := ownerProvenanceFleet(t, contract.Owner{Team: "pay"})
	r := invalid.ProviderCurrentRevision(key)
	r.validated, r.Valid = true, false
	if !invalid.HasContractSource(key) {
		t.Error("an invalid revision is still a contract source")
	}
}
