package contractview

import (
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

func TestGenerateInsights_NoopWhenPresent(t *testing.T) {
	d := &ServiceDetails{Insights: []Insight{{Severity: "info", Title: "existing"}}}
	d.GenerateInsights()
	if len(d.Insights) != 1 || d.Insights[0].Title != "existing" {
		t.Errorf("expected existing insight preserved, got %v", d.Insights)
	}
}

func TestGenerateInsights_ContractStatus(t *testing.T) {
	for _, tc := range []struct {
		status   ContractStatus
		severity string
	}{
		{StatusInvalid, "critical"},
		{StatusNonCompliant, "critical"},
		{StatusUnknown, "warning"},
		{StatusWarning, "warning"},
	} {
		d := &ServiceDetails{}
		d.ContractStatus = tc.status
		d.GenerateInsights()
		if len(d.Insights) == 0 || d.Insights[0].Severity != tc.severity {
			t.Errorf("status %s: expected %s insight, got %v", tc.status, tc.severity, d.Insights)
		}
	}
}

func TestGenerateInsights_Compliant(t *testing.T) {
	d := &ServiceDetails{}
	d.ContractStatus = StatusCompliant
	d.GenerateInsights()
	if len(d.Insights) != 0 {
		t.Errorf("expected no insights for compliant, got %v", d.Insights)
	}
}

func TestGenerateInsights_Validation(t *testing.T) {
	d := &ServiceDetails{
		Validation: &ValidationInfo{
			Errors:   []ValidationIssue{{Message: "bad field"}, {Message: "another"}},
			Warnings: []ValidationIssue{{Message: "check this"}},
		},
	}
	d.GenerateInsights()
	if len(d.Insights) != 2 {
		t.Fatalf("expected 2 insights, got %d: %v", len(d.Insights), d.Insights)
	}
	if d.Insights[0].Title != "2 validation errors" || d.Insights[0].Description != "bad field" {
		t.Errorf("unexpected error insight: %+v", d.Insights[0])
	}
	if d.Insights[1].Title != "1 validation warning" || d.Insights[1].Description != "check this" {
		t.Errorf("unexpected warning insight: %+v", d.Insights[1])
	}
}

func TestGenerateInsights_ValidationEmptyMessage(t *testing.T) {
	d := &ServiceDetails{Validation: &ValidationInfo{Errors: []ValidationIssue{{Code: "E001"}}}}
	d.GenerateInsights()
	if len(d.Insights) != 1 || d.Insights[0].Description != "" {
		t.Errorf("expected empty description, got %+v", d.Insights)
	}
}

func TestGenerateInsights_Resources(t *testing.T) {
	d := &ServiceDetails{Resources: &ResourcesInfo{ServiceExists: boolPtr(false), WorkloadExists: boolPtr(false)}}
	d.GenerateInsights()
	if len(d.Insights) != 2 {
		t.Fatalf("expected 2 resource insights, got %d", len(d.Insights))
	}

	d2 := &ServiceDetails{Resources: &ResourcesInfo{ServiceExists: boolPtr(true), WorkloadExists: boolPtr(true)}}
	d2.GenerateInsights()
	if len(d2.Insights) != 0 {
		t.Errorf("expected no insights for existing resources, got %v", d2.Insights)
	}
}

func TestGenerateInsights_Ports(t *testing.T) {
	d := &ServiceDetails{Ports: &PortsInfo{Missing: []int{8080, 9090}, Unexpected: []int{3000}}}
	d.GenerateInsights()
	if len(d.Insights) != 2 {
		t.Fatalf("expected 2 port insights, got %d", len(d.Insights))
	}
	if d.Insights[0].Title != "Missing ports: 8080, 9090" {
		t.Errorf("unexpected missing ports title: %s", d.Insights[0].Title)
	}
	if d.Insights[1].Title != "Unexpected ports: 3000" {
		t.Errorf("unexpected ports title: %s", d.Insights[1].Title)
	}
}

// boolPtr is a pointer helper for the optional booleans in ResourcesInfo.
func boolPtr(b bool) *bool { return &b }

func TestPlural(t *testing.T) {
	if plural(1) != "" {
		t.Error("expected empty for 1")
	}
	if plural(2) != "s" {
		t.Error("expected 's' for 2")
	}
	if plural(0) != "s" {
		t.Error("expected 's' for 0")
	}
}

func TestJoinInts(t *testing.T) {
	if got := joinInts([]int{1, 2, 3}); got != "1, 2, 3" {
		t.Errorf("expected '1, 2, 3', got %q", got)
	}
	if got := joinInts([]int{42}); got != "42" {
		t.Errorf("expected '42', got %q", got)
	}
}

// TestInterfacesFromContract_AsyncAPI covers the non-OpenAPI branch (line 262-266).
func TestInterfacesFromContract_AsyncAPI(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "events", Type: contract.InterfaceTypeAsyncAPI, Ref: "events.yaml"},
		},
	}
	fsys := fstest.MapFS{
		"events.yaml": &fstest.MapFile{Data: []byte("asyncapi: 2.0.0\nchannels:\n  user-signup:\n    publish:\n      message:\n        payload:\n          type: object\n")},
	}
	ifaces := interfacesFromContract(c, fsys)
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(ifaces))
	}
	if ifaces[0].Type != contract.InterfaceTypeAsyncAPI {
		t.Errorf("expected asyncapi, got %q", ifaces[0].Type)
	}
	if ifaces[0].ContractContent == "" {
		t.Error("expected contract content for asyncapi")
	}
}

// TestInterfacesFromContract_GRPC covers the gRPC branch.
func TestInterfacesFromContract_GRPC(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "rpc", Type: contract.InterfaceTypeGRPC, Ref: "service.proto"},
		},
	}
	fsys := fstest.MapFS{
		"service.proto": &fstest.MapFile{Data: []byte("syntax = \"proto3\";\nservice Greeter {\n  rpc SayHello (HelloRequest) returns (HelloReply) {}\n}\n")},
	}
	ifaces := interfacesFromContract(c, fsys)
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(ifaces))
	}
	if ifaces[0].Type != contract.InterfaceTypeGRPC {
		t.Errorf("expected grpc, got %q", ifaces[0].Type)
	}
	if ifaces[0].ContractContent == "" {
		t.Error("expected contract content for grpc")
	}
}

// TestInterfacesFromContract_OpenAPIParseFailure covers line 257-261 (OpenAPI parse error fallback).
func TestInterfacesFromContract_OpenAPIParseFailure(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "broken.yaml"},
		},
	}
	fsys := fstest.MapFS{
		"broken.yaml": &fstest.MapFile{Data: []byte("invalid: yaml: [[[")},
	}
	ifaces := interfacesFromContract(c, fsys)
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(ifaces))
	}
	// Should have contract content from fallback path
	if ifaces[0].ContractContent == "" {
		t.Error("expected contract content from fallback when OpenAPI parse fails")
	}
}

// TestInterfacesFromContract_OpenAPIEmptyEndpoints covers the empty endpoints branch.
func TestInterfacesFromContract_OpenAPIEmptyEndpoints(t *testing.T) {
	c := &contract.Contract{
		Interfaces: []contract.Interface{
			{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "no-paths.json"},
		},
	}
	fsys := fstest.MapFS{
		"no-paths.json": &fstest.MapFile{Data: []byte(`{"openapi":"3.0.0","info":{"title":"API","version":"1.0.0"}}`)},
	}
	ifaces := interfacesFromContract(c, fsys)
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(ifaces))
	}
	// Should have contract content from fallback when no endpoints
	if ifaces[0].ContractContent == "" {
		t.Error("expected contract content from fallback when no endpoints")
	}
}
