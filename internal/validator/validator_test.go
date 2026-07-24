package validator

import (
	"testing"
)

// v2: validator package is deprecated — all validation happens via pacto v2.
// Keep only types (Check, Result, PortsResult) for backward compat.
// No validation logic to test.

func TestCheckType(t *testing.T) {
	c := Check{
		Name:     "TestCondition",
		Passed:   true,
		Reason:   "AllGood",
		Message:  "Check passed successfully",
		Severity: "info",
	}
	if c.Name != "TestCondition" {
		t.Errorf("expected Name=TestCondition, got %s", c.Name)
	}
	if !c.Passed {
		t.Error("expected Passed=true")
	}
	if c.Severity != "info" {
		t.Errorf("expected Severity=info, got %s", c.Severity)
	}
}

func TestResultType(t *testing.T) {
	r := Result{
		Checks: []Check{
			{Name: "Check1", Passed: true},
			{Name: "Check2", Passed: false, Severity: "error"},
		},
		Ports: PortsResult{
			Expected:   []int32{8080, 9090},
			Observed:   []int32{8080, 3000},
			Missing:    []int32{9090},
			Unexpected: []int32{3000},
		},
	}
	if len(r.Checks) != 2 {
		t.Errorf("expected 2 checks, got %d", len(r.Checks))
	}
	if len(r.Ports.Expected) != 2 {
		t.Errorf("expected 2 expected ports, got %d", len(r.Ports.Expected))
	}
	if len(r.Ports.Missing) != 1 || r.Ports.Missing[0] != 9090 {
		t.Errorf("expected missing=[9090], got %v", r.Ports.Missing)
	}
	if len(r.Ports.Unexpected) != 1 || r.Ports.Unexpected[0] != 3000 {
		t.Errorf("expected unexpected=[3000], got %v", r.Ports.Unexpected)
	}
}

func TestPortsResultZeroValues(t *testing.T) {
	p := PortsResult{}
	if p.Expected != nil {
		t.Error("expected Expected=nil for zero value")
	}
	if p.Observed != nil {
		t.Error("expected Observed=nil for zero value")
	}
	if p.Missing != nil {
		t.Error("expected Missing=nil for zero value")
	}
	if p.Unexpected != nil {
		t.Error("expected Unexpected=nil for zero value")
	}
}

func TestCheckDefaultSeverity(t *testing.T) {
	c := Check{Name: "Test", Passed: false, Severity: ""}
	if c.Severity != "" {
		t.Errorf("expected empty Severity to remain empty (backward compat defaults to error), got %s", c.Severity)
	}
}
