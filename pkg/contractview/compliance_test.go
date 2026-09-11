package contractview

import "testing"

func TestLookupValidation_Known(t *testing.T) {
	entry := LookupValidation("ContractValid")
	if entry.Category != "contract" {
		t.Errorf("expected category 'contract', got %q", entry.Category)
	}
	if entry.Severity != "error" {
		t.Errorf("expected severity 'error', got %q", entry.Severity)
	}
	if entry.Label != "Contract Structure" {
		t.Errorf("expected label 'Contract Structure', got %q", entry.Label)
	}
}

func TestLookupValidation_Unknown(t *testing.T) {
	entry := LookupValidation("SomethingNew")
	if entry.Category != "other" {
		t.Errorf("expected category 'other', got %q", entry.Category)
	}
	if entry.Severity != "error" {
		t.Errorf("expected severity 'error', got %q", entry.Severity)
	}
	if entry.Label != "SomethingNew" {
		t.Errorf("expected label 'SomethingNew', got %q", entry.Label)
	}
}

func TestComputeCompliance_Reference(t *testing.T) {
	info := ComputeCompliance(StatusReference, nil)
	if info.Status != ComplianceReference {
		t.Errorf("expected REFERENCE, got %q", info.Status)
	}
}

func TestComputeCompliance_Invalid(t *testing.T) {
	info := ComputeCompliance(StatusInvalid, nil)
	if info.Status != ComplianceError {
		t.Errorf("expected ERROR, got %q", info.Status)
	}
}

func TestComputeCompliance_NonCompliant(t *testing.T) {
	info := ComputeCompliance(StatusNonCompliant, nil)
	if info.Status != ComplianceError {
		t.Errorf("expected ERROR, got %q", info.Status)
	}
}

func TestComputeCompliance_AllPassed(t *testing.T) {
	conds := []Condition{
		{Type: "ContractValid", Status: "True"},
		{Type: "ServiceExists", Status: "True"},
	}
	info := ComputeCompliance(StatusCompliant, conds)
	if info.Status != ComplianceOK {
		t.Errorf("expected OK, got %q", info.Status)
	}
	if info.Score == nil || *info.Score != 100 {
		t.Errorf("expected score 100, got %v", info.Score)
	}
	if info.Summary.Total != 2 || info.Summary.Passed != 2 || info.Summary.Failed != 0 {
		t.Errorf("unexpected summary: %+v", info.Summary)
	}
}

func TestComputeCompliance_WithWarnings(t *testing.T) {
	conds := []Condition{
		{Type: "ContractValid", Status: "True"},
		{Type: "UpgradeStrategyMatch", Status: "False"},
	}
	info := ComputeCompliance(StatusCompliant, conds)
	if info.Status != ComplianceWarning {
		t.Errorf("expected WARNING, got %q", info.Status)
	}
	if info.Score == nil || *info.Score != 50 {
		t.Errorf("expected score 50, got %v", info.Score)
	}
	if info.Summary.Warnings != 1 {
		t.Errorf("expected 1 warning, got %d", info.Summary.Warnings)
	}
}

func TestComputeCompliance_WithErrors(t *testing.T) {
	conds := []Condition{
		{Type: "ContractValid", Status: "True"},
		{Type: "ServiceExists", Status: "False"},
	}
	info := ComputeCompliance(StatusCompliant, conds)
	if info.Status != ComplianceError {
		t.Errorf("expected ERROR, got %q", info.Status)
	}
	if info.Summary.Errors != 1 {
		t.Errorf("expected 1 error, got %d", info.Summary.Errors)
	}
}

func TestComputeCompliance_WithUnknown(t *testing.T) {
	conds := []Condition{
		{Type: "ContractValid", Status: "True"},
		{Type: "SomeCheck", Status: "Unknown"},
	}
	info := ComputeCompliance(StatusCompliant, conds)
	if info.Status != ComplianceUnknown {
		t.Errorf("expected UNKNOWN, got %q", info.Status)
	}
	if info.Summary.Unknown != 1 {
		t.Errorf("expected 1 unknown, got %d", info.Summary.Unknown)
	}
	// Unknown is inconclusive, NOT a violation: Failed excludes it (errors+warnings=0),
	// so Failed must differ from the old total-passed folding (which would be 1).
	if info.Summary.Failed != 0 {
		t.Errorf("expected Failed=0 (Unknown excluded), got %d", info.Summary.Failed)
	}
	if info.Summary.Failed == info.Summary.Total-info.Summary.Passed {
		t.Errorf("Failed (%d) must not fold Unknown into total-passed (%d)",
			info.Summary.Failed, info.Summary.Total-info.Summary.Passed)
	}
	// Check secondary metrics per B-2.
	if info.Summary.RuntimeEvaluated != 2 {
		t.Errorf("expected RuntimeEvaluated=2, got %d", info.Summary.RuntimeEvaluated)
	}
	if info.Summary.Conclusive != 1 {
		t.Errorf("expected Conclusive=1, got %d", info.Summary.Conclusive)
	}
}

func TestComputeCompliance_UnknownStatus(t *testing.T) {
	info := ComputeCompliance(StatusUnknown, nil)
	if info.Status != ComplianceUnknown {
		t.Errorf("expected UNKNOWN, got %q", info.Status)
	}
}

func TestComputeCompliance_NotEvaluated(t *testing.T) {
	info := ComputeCompliance(StatusNotEvaluated, nil)
	if info.Status != ComplianceReference {
		t.Errorf("expected REFERENCE (excluded from denominator), got %q", info.Status)
	}
}

func TestComputeCompliance_ExplicitSeverity(t *testing.T) {
	conds := []Condition{
		{Type: "ContractValid", Status: "True"},
		{Type: "ServiceExists", Status: "False", Severity: "warning"},
	}
	info := ComputeCompliance(StatusCompliant, conds)
	// ServiceExists normally severity=error, but explicit severity=warning overrides.
	if info.Status != ComplianceWarning {
		t.Errorf("expected WARNING, got %q", info.Status)
	}
	if info.Summary.Warnings != 1 {
		t.Errorf("expected 1 warning, got %d", info.Summary.Warnings)
	}
}

func TestComputeCompliance_NoConds(t *testing.T) {
	info := ComputeCompliance(StatusCompliant, nil)
	if info.Status != ComplianceOK {
		t.Errorf("expected OK, got %q", info.Status)
	}
	if info.Score != nil {
		t.Errorf("expected nil score with no conditions, got %v", info.Score)
	}
}
