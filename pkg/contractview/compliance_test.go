package contractview

import "testing"

func TestComputeCompliance(t *testing.T) {
	cases := map[ContractStatus]ComplianceStatus{
		StatusReference:    ComplianceReference,
		StatusNotEvaluated: ComplianceReference,
		StatusInvalid:      ComplianceError,
		StatusNonCompliant: ComplianceError,
		StatusUnknown:      ComplianceUnknown,
		StatusWarning:      ComplianceWarning,
		StatusCompliant:    ComplianceOK,
		"":                 ComplianceOK,
	}
	for cs, want := range cases {
		if got := ComputeCompliance(cs); got.Status != want {
			t.Errorf("ComputeCompliance(%q) = %q, want %q", cs, got.Status, want)
		}
	}
}
