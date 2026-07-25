package dashboard

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

func TestExtractTar_ReadError(t *testing.T) {
	// Create a tar with a valid header but the data read will fail.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Write a header for a file with size 100 but don't write the data.
	_ = tw.WriteHeader(&tar.Header{
		Name: "bad.txt",
		Size: 100,
		Mode: 0644,
	})
	_ = tw.Flush()

	// Concatenate the buffer with an error reader to simulate read failure.
	combinedReader := io.MultiReader(bytes.NewReader(buf.Bytes()), &errorReader{})

	_, err := extractTar(combinedReader)
	if err == nil {
		t.Fatal("expected error from read failure in tar extraction")
	}
}

// errorReader always returns an error on Read.
type errorReader struct{}

func (e *errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestCacheSource_ScanWalkError(t *testing.T) {
	// Create a cache dir with an unreadable subdirectory to trigger walk error callback.
	root := t.TempDir()

	// Create a valid bundle first so there's something to scan.
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "2.0"
service:
  name: api
  version: 1.0.0
`)

	// Create an unreadable directory that will cause Walk to pass an error to the callback.
	badDir := filepath.Join(root, "ghcr.io/bad")
	if err := os.MkdirAll(badDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(badDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(badDir, 0o755) })

	src := NewCacheSource(root)
	// Should still find the valid bundle despite the walk error.
	if src.ServiceCount() != 1 {
		t.Fatalf("expected 1 service (skipping walk error), got %d", src.ServiceCount())
	}
}

func TestDetectCache_WithValidBundles(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "oci")

	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "2.0"
service:
  name: api
  version: 1.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/worker/2.0.0/bundle.tar.gz"),
		`pactoVersion: "2.0"
service:
  name: worker
  version: 2.0.0
`)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache(cacheDir)

	if result.Cache == nil {
		t.Fatal("expected cache source to be detected")
	}
	if result.Diagnostics.Cache.ServiceCount != 2 {
		t.Errorf("expected 2 services, got %d", result.Diagnostics.Cache.ServiceCount)
	}
	if result.Diagnostics.Cache.VersionCount != 2 {
		t.Errorf("expected 2 versions, got %d", result.Diagnostics.Cache.VersionCount)
	}
}

func TestDetectCache_HomeError(t *testing.T) {
	// Force the home-dir lookup to fail so the error path is exercised
	// deterministically (relying on an unset HOME is platform-dependent).
	orig := userHomeDir
	userHomeDir = func() (string, error) { return "", fmt.Errorf("no home dir") }
	t.Cleanup(func() { userHomeDir = orig })

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache("")

	if result.Diagnostics.Cache.Error == "" {
		t.Error("expected a diagnostics error when the home dir cannot be determined")
	}
	if result.Cache != nil {
		t.Error("expected nil cache when home dir fails")
	}
}

func TestExtractTar_PrefixDotSlash(t *testing.T) {
	// Entries prefixed with "./" should have the prefix stripped.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	data := []byte("content")
	_ = tw.WriteHeader(&tar.Header{
		Name: "./file.txt",
		Size: int64(len(data)),
		Mode: 0644,
	})
	_, _ = tw.Write(data)
	_ = tw.Close()

	fsys, err := extractTar(&buf)
	if err != nil {
		t.Fatal(err)
	}
	content, err := fs.ReadFile(fsys, "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "content" {
		t.Errorf("expected 'content', got %q", string(content))
	}
}

func TestExtractTar_DotDotInMiddle(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	data := []byte("sneaky")
	_ = tw.WriteHeader(&tar.Header{
		Name: "subdir/../secret.txt",
		Size: int64(len(data)),
		Mode: 0644,
	})
	_, _ = tw.Write(data)
	_ = tw.Close()

	_, err := extractTar(&buf)
	if err == nil {
		t.Fatal("expected error for path containing '..'")
	}
}

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

func TestLocalSource_FindBundle_SubdirInvalidYAMLThenValid(t *testing.T) {
	// This tests the `continue` path in findBundle when loadLocalBundle fails
	// for a subdirectory (line 115-116 in source_local.go).
	// The root has NO pacto.yaml. One subdir has invalid YAML, another has valid.
	root := t.TempDir()

	// Create a subdir with invalid pacto.yaml that will fail contract.Parse.
	badDir := filepath.Join(root, "aaa-bad")
	if err := os.MkdirAll(badDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "pacto.yaml"), []byte("not valid yaml: [[["), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a subdir with valid pacto.yaml.
	writeLocalPactoYAML(t, filepath.Join(root, "zzz-good"), "target-svc", "1.0.0")

	src := NewLocalSource(root)
	// findBundle iterates sorted entries: "aaa-bad" first (fails), then "zzz-good" (succeeds).
	details, err := src.GetService(t.Context(), "target-svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "target-svc" {
		t.Errorf("expected 'target-svc', got %q", details.Name)
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

// TestServiceDetailsFromK8sStatus_NilState covers line 667-674 nil check.
func TestServiceDetailsFromK8sStatus_NilState(t *testing.T) {
	r := &pactoResource{}
	r.Metadata.Name = "svc"
	r.Status.ContractStatus = "Compliant"
	r.Status.Contract = &k8sContractInfo{ServiceName: "svc", Version: "1.0.0"}
	r.Status.State = nil

	svc := serviceDetailsFromK8sStatus(r)
	if svc.State != nil {
		t.Errorf("expected nil state, got %+v", svc.State)
	}
}

// TestEnrichRuntimeFields_ContractHasValues covers the NOT-taken branches (356, 359, 362).
func TestEnrichRuntimeFields_ContractHasValues(t *testing.T) {
	contract := &ServiceDetails{
		Workload:     "service",
		State:        &StateInfo{Type: "stateless"},
		Capabilities: []CapabilityInfo{{Type: "health"}},
	}
	runtime := &ServiceDetails{
		Workload:     "worker",
		State:        &StateInfo{Type: "stateful"},
		Capabilities: []CapabilityInfo{{Type: "metrics"}},
	}
	enrichRuntimeFields(contract, runtime)
	// Contract values should NOT be overridden
	if contract.Workload != "service" {
		t.Errorf("workload overridden: got %q", contract.Workload)
	}
	if contract.State.Type != "stateless" {
		t.Errorf("state overridden: got %q", contract.State.Type)
	}
	if len(contract.Capabilities) != 1 || contract.Capabilities[0].Type != "health" {
		t.Errorf("capabilities overridden: got %+v", contract.Capabilities)
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

// TestServiceDetailsFromK8sStatus_NilSummary covers line 692-698 nil check.
func TestServiceDetailsFromK8sStatus_NilSummary(t *testing.T) {
	r := &pactoResource{}
	r.Metadata.Name = "svc"
	r.Status.ContractStatus = "Compliant"
	r.Status.Contract = &k8sContractInfo{ServiceName: "svc", Version: "1.0.0"}
	r.Status.Summary = nil

	svc := serviceDetailsFromK8sStatus(r)
	if svc.ChecksSummary != nil {
		t.Errorf("expected nil summary, got %+v", svc.ChecksSummary)
	}
}

// TestEnrichRuntimeFields_NilRuntimeFields covers nil checks in enrichRuntimeFields.
func TestEnrichRuntimeFields_NilRuntimeFields(t *testing.T) {
	contract := &ServiceDetails{}
	runtime := &ServiceDetails{
		Resources:       nil,
		Ports:           nil,
		Validation:      nil,
		ChecksSummary:   nil,
		ObservedRuntime: nil,
		Endpoints:       nil,
		Conditions:      nil,
		Insights:        nil,
		RuntimeDiff:     nil,
	}
	enrichRuntimeFields(contract, runtime)
	// All should remain nil
	if contract.Resources != nil || contract.Ports != nil || contract.Validation != nil {
		t.Error("expected nil fields to stay nil")
	}
}

// TestEnrichRuntimeFields_EmptySlices covers empty slice checks.
func TestEnrichRuntimeFields_EmptySlices(t *testing.T) {
	contract := &ServiceDetails{}
	runtime := &ServiceDetails{
		Endpoints:   []EndpointStatus{},
		Conditions:  []Condition{},
		Insights:    []Insight{},
		RuntimeDiff: []RuntimeDiffRow{},
	}
	enrichRuntimeFields(contract, runtime)
	// Empty slices should not be copied
	if len(contract.Endpoints) != 0 || len(contract.Conditions) != 0 {
		t.Error("expected empty slices to not be copied")
	}
}

// TestServiceDetailsFromK8sStatus_NilContract covers line 606 nil check.
func TestServiceDetailsFromK8sStatus_NilContract(t *testing.T) {
	r := &pactoResource{}
	r.Metadata.Name = "svc"
	r.Status.ContractStatus = "Compliant"
	r.Status.Contract = nil

	svc := serviceDetailsFromK8sStatus(r)
	if svc.ResolvedRef != "" || svc.CurrentRevision != "" {
		t.Error("expected empty refs when contract is nil")
	}
}

// TestServiceDetailsFromK8sStatus_EmptyResolutionPolicy covers line 609.
func TestServiceDetailsFromK8sStatus_EmptyResolutionPolicy(t *testing.T) {
	r := &pactoResource{}
	r.Metadata.Name = "svc"
	r.Status.ContractStatus = "Compliant"
	r.Status.Contract = &k8sContractInfo{
		ServiceName:      "svc",
		Version:          "1.0.0",
		ResolutionPolicy: "", // empty
	}

	svc := serviceDetailsFromK8sStatus(r)
	if svc.VersionPolicy != "" {
		t.Errorf("expected empty version policy, got %q", svc.VersionPolicy)
	}
}

// TestServiceDetailsFromK8sStatus_Capabilities covers line 676-681 (Capabilities loop).
func TestServiceDetailsFromK8sStatus_Capabilities(t *testing.T) {
	r := &pactoResource{}
	r.Metadata.Name = "svc"
	r.Status.ContractStatus = "Compliant"
	r.Status.Contract = &k8sContractInfo{ServiceName: "svc", Version: "1.0.0"}
	r.Status.Capabilities = flexSlice[k8sCapability]{
		{Type: "health"},
		{Type: "metrics", Ref: ""},
		{Type: "extension", Ref: "acme.io/backup"},
	}

	svc := serviceDetailsFromK8sStatus(r)
	if len(svc.Capabilities) != 3 {
		t.Fatalf("expected 3 capabilities, got %d", len(svc.Capabilities))
	}
	if svc.Capabilities[0].Type != "health" {
		t.Errorf("expected health, got %q", svc.Capabilities[0].Type)
	}
	if svc.Capabilities[2].Ref != "acme.io/backup" {
		t.Errorf("expected extension ref, got %q", svc.Capabilities[2].Ref)
	}
}

// TestEnrichRuntimeFields_EmptyContractCapabilities covers line 362-364.
func TestEnrichRuntimeFields_EmptyContractCapabilities(t *testing.T) {
	contract := &ServiceDetails{
		Capabilities: []CapabilityInfo{}, // empty
	}
	runtime := &ServiceDetails{
		Capabilities: []CapabilityInfo{{Type: "health"}, {Type: "metrics"}},
	}
	enrichRuntimeFields(contract, runtime)
	// Contract had no capabilities, should adopt runtime's
	if len(contract.Capabilities) != 2 {
		t.Fatalf("expected 2 capabilities from runtime, got %d", len(contract.Capabilities))
	}
	if contract.Capabilities[0].Type != "health" {
		t.Errorf("expected health, got %q", contract.Capabilities[0].Type)
	}
}
