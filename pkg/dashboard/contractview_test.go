package dashboard

import (
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/contractview"
	depgraph "github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// The seven functions in contractview.go exist only to keep released v3 call
// sites compiling now that the contract-view half lives in pkg/contractview.
// Their whole contract is "forward, unchanged", so each assertion below is
// exactly that: call both spellings with the same input and require the same
// answer. Anything weaker would pass while a wrapper dropped an argument, and
// anything stronger would be a second copy of pkg/contractview's own tests.

func testContract() *contract.Contract {
	c := &contract.Contract{}
	c.Service.Name = "payments"
	c.Service.Version = "1.4.0"
	return c
}

func TestServiceFromContract_ForwardsToContractview(t *testing.T) {
	c := testContract()
	if got, want := ServiceFromContract(c, "local"), contractview.ServiceFromContract(c, "local"); !reflect.DeepEqual(got, want) {
		t.Errorf("ServiceFromContract = %+v, want %+v", got, want)
	}
}

func TestServiceDetailsFromBundle_ForwardsToContractview(t *testing.T) {
	b := &contract.Bundle{Contract: testContract()}
	if got, want := ServiceDetailsFromBundle(b, "oci"), contractview.ServiceDetailsFromBundle(b, "oci"); !reflect.DeepEqual(got, want) {
		t.Errorf("ServiceDetailsFromBundle = %+v, want %+v", got, want)
	}
}

func TestGlobalGraphFromResult_ForwardsToContractview(t *testing.T) {
	gr := &depgraph.Result{Root: &depgraph.Node{Name: "payments", Version: "1.4.0"}}
	root := &ServiceDetails{Service: Service{Name: "payments"}}
	if got, want := GlobalGraphFromResult(gr, root), contractview.GlobalGraphFromResult(gr, root); !reflect.DeepEqual(got, want) {
		t.Errorf("GlobalGraphFromResult = %+v, want %+v", got, want)
	}
}

func TestNormalizeContractStatus_ForwardsToContractview(t *testing.T) {
	// A status the canonicaliser rewrites, so a wrapper that returned its own
	// argument untouched would still fail this.
	for _, in := range []ContractStatus{"Passing", StatusCompliant, ""} {
		if got, want := NormalizeContractStatus(in), contractview.NormalizeContractStatus(in); got != want {
			t.Errorf("NormalizeContractStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComputeCompliance_ForwardsToContractview(t *testing.T) {
	conds := []Condition{{Type: "SchemaValid", Status: "True"}}
	if got, want := ComputeCompliance(StatusCompliant, conds), contractview.ComputeCompliance(StatusCompliant, conds); !reflect.DeepEqual(got, want) {
		t.Errorf("ComputeCompliance = %+v, want %+v", got, want)
	}
}

func TestLookupValidation_ForwardsToContractview(t *testing.T) {
	for _, in := range []string{"SchemaValid", "no-such-condition"} {
		if got, want := LookupValidation(in), contractview.LookupValidation(in); !reflect.DeepEqual(got, want) {
			t.Errorf("LookupValidation(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestApplyLock_ForwardsToContractview(t *testing.T) {
	// ApplyLock mutates in place and returns nothing, so the two spellings are
	// compared by the state they leave behind on identical inputs.
	l := &lock.Lock{
		LockVersion:  lock.CurrentLockVersion,
		Dependencies: []lock.Entry{{Name: "ledger", Source: "oci", Version: "2.0.0", Digest: "sha256:d"}},
	}
	deps := func() []DependencyInfo { return []DependencyInfo{{Name: "ledger"}} }

	viaShim := &ServiceDetails{Dependencies: deps()}
	ApplyLock(viaShim, l)

	direct := &ServiceDetails{Dependencies: deps()}
	contractview.ApplyLock(direct, l)

	if !reflect.DeepEqual(viaShim, direct) {
		t.Errorf("ApplyLock left %+v, want %+v", viaShim, direct)
	}
	if viaShim.Lock == nil {
		t.Fatal("ApplyLock populated nothing, so this test would pass on a wrapper with an empty body")
	}
}
