package metrics

import (
	"fmt"
	"testing"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
)

func TestMust_PanicsOnError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from must")
		}
	}()
	must(0, fmt.Errorf("test error"))
}

// v2: RecordContractStatus and RecordReadiness are the primary metrics functions used by the controller.
// RecordValidation is kept for backward compat but not called in v2 flow.

func TestRecordContractStatus_Compliant(t *testing.T) {
	RecordContractStatus("default", "my-pacto", pactov1alpha1.ContractStatusCompliant)
}

func TestRecordContractStatus_Warning(t *testing.T) {
	RecordContractStatus("default", "my-pacto", pactov1alpha1.ContractStatusWarning)
}

func TestRecordContractStatus_NonCompliant(t *testing.T) {
	RecordContractStatus("default", "my-pacto", pactov1alpha1.ContractStatusNonCompliant)
}

func TestRecordContractStatus_Reference(t *testing.T) {
	RecordContractStatus("default", "my-pacto", pactov1alpha1.ContractStatusReference)
}

func TestRecordContractStatus_Unknown(t *testing.T) {
	RecordContractStatus("default", "my-pacto", pactov1alpha1.ContractStatusUnknown)
}

func TestRecordReadiness_Nil(t *testing.T) {
	RecordReadiness("default", "my-pacto", nil, "")
}

func TestRecordReadiness_Passing(t *testing.T) {
	rs := &pactov1alpha1.ReadinessStatus{
		Score: 100, Passing: true, DoneCount: 3,
	}
	RecordReadiness("default", "my-pacto", rs, pactov1alpha1.ReasonReadinessSatisfied)
}

func TestRecordReadiness_BelowMinScore(t *testing.T) {
	rs := &pactov1alpha1.ReadinessStatus{
		Score: 60, Passing: false, DoneCount: 1, PartialCount: 1, NotDoneCount: 1, DeferredCount: 1,
	}
	RecordReadiness("test-ns", "below-min", rs, pactov1alpha1.ReasonReadinessBelowMinScore)
}

func TestRecordReadiness_Expired(t *testing.T) {
	rs := &pactov1alpha1.ReadinessStatus{
		Score: 0, Passing: false, NotDoneCount: 2, Expired: true,
	}
	RecordReadiness("test-ns", "expired", rs, pactov1alpha1.ReasonReadinessExpired)
}

func TestRecordReadiness_AllZeros(t *testing.T) {
	rs := &pactov1alpha1.ReadinessStatus{
		Score: 0, Passing: false, DoneCount: 0, PartialCount: 0, NotDoneCount: 0, DeferredCount: 0,
	}
	RecordReadiness("test-ns", "zero", rs, pactov1alpha1.ReasonReadinessExpired)
}

func TestRecordReadiness_Mixed(t *testing.T) {
	rs := &pactov1alpha1.ReadinessStatus{
		Score: 75, Passing: false, DoneCount: 2, PartialCount: 1, NotDoneCount: 1, DeferredCount: 0,
	}
	RecordReadiness("test-ns", "mixed", rs, pactov1alpha1.ReasonReadinessBelowMinScore)
}
