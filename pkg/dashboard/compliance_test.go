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
	info := ComputeCompliance(PhaseReference, nil)
	if info.Status != ComplianceReference {
		t.Errorf("expected REFERENCE, got %q", info.Status)
	}
}

func TestComputeCompliance_Invalid(t *testing.T) {
	info := ComputeCompliance(PhaseInvalid, nil)
	if info.Status != ComplianceError {
		t.Errorf("expected ERROR, got %q", info.Status)
	}
}

func TestComputeCompliance_AllPassed(t *testing.T) {
	conds := []Condition{
		{Type: "ContractValid", Status: "True"},
		{Type: "ServiceExists", Status: "True"},
	}
	info := ComputeCompliance(PhaseHealthy, conds)
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
	info := ComputeCompliance(PhaseHealthy, conds)
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
	info := ComputeCompliance(PhaseHealthy, conds)
	if info.Status != ComplianceError {
		t.Errorf("expected ERROR, got %q", info.Status)
	}
	if info.Summary.Errors != 1 {
		t.Errorf("expected 1 error, got %d", info.Summary.Errors)
	}
}

func TestComputeCompliance_ExplicitSeverity(t *testing.T) {
	conds := []Condition{
		{Type: "ContractValid", Status: "True"},
		{Type: "ServiceExists", Status: "False", Severity: "warning"},
	}
	info := ComputeCompliance(PhaseHealthy, conds)
	// ServiceExists normally severity=error, but explicit severity=warning overrides.
	if info.Status != ComplianceWarning {
		t.Errorf("expected WARNING, got %q", info.Status)
	}
	if info.Summary.Warnings != 1 {
		t.Errorf("expected 1 warning, got %d", info.Summary.Warnings)
	}
}

func TestComputeCompliance_NoConds(t *testing.T) {
	info := ComputeCompliance(PhaseHealthy, nil)
	if info.Status != ComplianceOK {
		t.Errorf("expected OK, got %q", info.Status)
	}
	if info.Score != nil {
		t.Errorf("expected nil score with no conditions, got %v", info.Score)
	}
}

func TestComputeRuntimeDiff_BothNil(t *testing.T) {
	rows := ComputeRuntimeDiff(nil, nil)
	if rows != nil {
		t.Errorf("expected nil, got %v", rows)
	}
}

func TestComputeRuntimeDiff_Match(t *testing.T) {
	rt := &RuntimeInfo{
		Workload:        "service",
		UpgradeStrategy: "RollingUpdate",
	}
	obs := &ObservedRuntime{
		WorkloadKind:       "Deployment",
		DeploymentStrategy: "RollingUpdate",
	}
	rows := ComputeRuntimeDiff(rt, obs)
	if len(rows) == 0 {
		t.Fatal("expected rows")
	}
	// Workload: service -> Deployment, observed Deployment -> match
	if rows[0].Status != "match" {
		t.Errorf("expected match for workload, got %q", rows[0].Status)
	}
	// Upgrade strategy: RollingUpdate == RollingUpdate -> match
	if rows[1].Status != "match" {
		t.Errorf("expected match for upgrade strategy, got %q", rows[1].Status)
	}
}

func TestComputeRuntimeDiff_Mismatch(t *testing.T) {
	rt := &RuntimeInfo{
		Workload: "service",
	}
	obs := &ObservedRuntime{
		WorkloadKind: "StatefulSet",
	}
	rows := ComputeRuntimeDiff(rt, obs)
	// Workload: service -> Deployment, observed StatefulSet -> mismatch
	if rows[0].Status != "mismatch" {
		t.Errorf("expected mismatch for workload, got %q", rows[0].Status)
	}
}

func TestComputeRuntimeDiff_NilRuntime(t *testing.T) {
	obs := &ObservedRuntime{WorkloadKind: "Deployment"}
	rows := ComputeRuntimeDiff(nil, obs)
	if len(rows) == 0 {
		t.Fatal("expected rows even with nil runtime")
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
