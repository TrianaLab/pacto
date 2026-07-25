package dashboard

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

func TestComputeRuntimeDiff_BothNil(t *testing.T) {
	rows := ComputeRuntimeDiff("", nil, nil)
	if rows != nil {
		t.Errorf("expected nil, got %v", rows)
	}
}

func TestComputeRuntimeDiff_Match(t *testing.T) {
	state := &StateInfo{
		Type: "stateless",
	}
	obs := &ObservedRuntime{
		WorkloadKind: "Deployment",
	}
	rows := ComputeRuntimeDiff("service", state, obs)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	// Workload: service -> Deployment, observed Deployment -> match
	if rows[0].Status != "match" {
		t.Errorf("expected match for workload, got %q", rows[0].Status)
	}
	if rows[0].Field != "Workload Type" {
		t.Errorf("expected field 'Workload Type', got %q", rows[0].Field)
	}
	// State: stateless, no PVC/emptyDir -> stateless -> match
	if rows[1].Status != "match" {
		t.Errorf("expected match for state, got %q", rows[1].Status)
	}
}

func TestComputeRuntimeDiff_Mismatch(t *testing.T) {
	state := &StateInfo{
		Type: "stateless",
	}
	obs := &ObservedRuntime{
		WorkloadKind: "StatefulSet",
	}
	rows := ComputeRuntimeDiff("service", state, obs)
	// Workload: service -> Deployment, observed StatefulSet -> mismatch
	if rows[0].Status != "mismatch" {
		t.Errorf("expected mismatch for workload, got %q", rows[0].Status)
	}
}

func TestComputeRuntimeDiff_NilState(t *testing.T) {
	obs := &ObservedRuntime{WorkloadKind: "Deployment"}
	rows := ComputeRuntimeDiff("service", nil, obs)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows even with nil state, got %d", len(rows))
	}
}

func TestMapWorkloadToDeclared(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"service", "Deployment"},
		{"Service", "Deployment"},
		{"job", "Job"},
		{"scheduled", "CronJob"},
		{"custom", "custom"},
		{"", ""},
	}
	for _, c := range cases {
		got := mapWorkloadToDeclared(c.in)
		if got != c.want {
			t.Errorf("mapWorkloadToDeclared(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStorageState(t *testing.T) {
	tr := true
	fa := false

	if s := storageState(nil); s != "" {
		t.Errorf("expected empty, got %q", s)
	}
	if s := storageState(&ObservedRuntime{}); s != "stateless" {
		t.Errorf("expected 'stateless', got %q", s)
	}
	if s := storageState(&ObservedRuntime{HasPVC: &tr}); s != "PVC" {
		t.Errorf("expected 'PVC', got %q", s)
	}
	if s := storageState(&ObservedRuntime{HasEmptyDir: &tr}); s != "emptyDir" {
		t.Errorf("expected 'emptyDir', got %q", s)
	}
	if s := storageState(&ObservedRuntime{HasPVC: &tr, HasEmptyDir: &tr}); s != "PVC, emptyDir" {
		t.Errorf("expected 'PVC, emptyDir', got %q", s)
	}
	if s := storageState(&ObservedRuntime{HasPVC: &fa}); s != "stateless" {
		t.Errorf("expected 'stateless', got %q", s)
	}
}

func TestIntPtrToString(t *testing.T) {
	if s := intPtrToString(nil); s != "" {
		t.Errorf("expected empty, got %q", s)
	}
	v := 30
	if s := intPtrToString(&v); s != "30" {
		t.Errorf("expected '30', got %q", s)
	}
}
