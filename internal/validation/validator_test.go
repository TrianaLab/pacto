package validation_test

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/internal/validation"
	"github.com/trianalab/pacto/pkg/contract"
)

func loadFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	return data
}

func parseFixture(t *testing.T, path string) ([]byte, *contract.Contract) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open fixture %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	c, err := contract.Parse(f)
	if err != nil {
		t.Fatalf("failed to parse fixture %s: %v", path, err)
	}

	return data, c
}

func parseString(t *testing.T, s string) ([]byte, *contract.Contract) {
	t.Helper()
	data := []byte(s)
	c, err := contract.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	return data, c
}

func TestValidate_ValidMinimal(t *testing.T) {
	data, c := parseFixture(t, "testdata/valid_minimal.yaml")
	result := validation.Validate(c, data, nil)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_ValidStateful(t *testing.T) {
	data, c := parseFixture(t, "testdata/valid_stateful.yaml")
	result := validation.Validate(c, data, nil)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_MissingRuntime(t *testing.T) {
	data := loadFixture(t, "testdata/invalid_missing_runtime.yaml")

	f, err := os.Open("testdata/invalid_missing_runtime.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	c, err := contract.Parse(f)
	if err != nil {
		t.Fatal(err)
	}

	result := validation.Validate(c, data, nil)
	if !result.IsValid() {
		t.Errorf("runtime should be optional, got errors: %v", result.Errors)
	}
}

func TestValidate_InvalidBadEnum(t *testing.T) {
	data := loadFixture(t, "testdata/invalid_bad_kind.yaml")

	f, err := os.Open("testdata/invalid_bad_kind.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	c, err := contract.Parse(f)
	if err != nil {
		t.Fatal(err)
	}

	result := validation.Validate(c, data, nil)
	if result.IsValid() {
		t.Error("expected validation to fail for invalid enum value")
	}
}

func TestValidate_StatelessPersistentConflict(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: bad-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: persistent
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	result := validation.Validate(c, data, nil)

	// The JSON Schema conditional should catch this at structural level,
	// but if it passes through, the CLI cross-field check catches it too.
	if result.IsValid() {
		t.Error("expected validation to fail for stateless + persistent durability")
		return
	}

	// Check for either structural schema violation or cross-field error.
	foundStructural := false
	foundCrossField := false
	for _, e := range result.Errors {
		if e.Code == "SCHEMA_VIOLATION" {
			foundStructural = true
		}
		if e.Code == "STATELESS_PERSISTENT_CONFLICT" {
			foundCrossField = true
		}
	}
	if !foundStructural && !foundCrossField {
		t.Error("expected SCHEMA_VIOLATION or STATELESS_PERSISTENT_CONFLICT error")
		for _, e := range result.Errors {
			t.Logf("  error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidateStructural_InvalidServiceName(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: "INVALID_NAME!"
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	result := validation.Validate(c, data, nil)
	if result.IsValid() {
		t.Error("expected validation to fail for invalid service name")
	}
}

func TestValidate_HealthPathRequired(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
`)
	result := validation.Validate(c, data, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "HEALTH_PATH_REQUIRED" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected HEALTH_PATH_REQUIRED error")
		for _, e := range result.Errors {
			t.Logf("  got: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_HealthInterfaceNotFound(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: nonexistent
    path: /health
`)
	result := validation.Validate(c, data, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "HEALTH_INTERFACE_NOT_FOUND" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected HEALTH_INTERFACE_NOT_FOUND error")
	}
}

func TestValidate_PortRequiredForHTTP(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	result := validation.Validate(c, data, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "PORT_REQUIRED" || e.Code == "SCHEMA_VIOLATION" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected PORT_REQUIRED or SCHEMA_VIOLATION error")
	}
}

func TestValidate_EventInterfaceNoPort(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
  - name: events
    type: event
    contract: interfaces/events.yaml
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	result := validation.Validate(c, data, nil)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_ContractRequiredForGRPC(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: grpc
    port: 9090
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
`)
	result := validation.Validate(c, data, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "CONTRACT_REQUIRED" || e.Code == "SCHEMA_VIOLATION" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected CONTRACT_REQUIRED or SCHEMA_VIOLATION error for grpc interface without contract")
		for _, e := range result.Errors {
			t.Logf("  got: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_ContractRequiredForEvent(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
  - name: events
    type: event
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	result := validation.Validate(c, data, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "CONTRACT_REQUIRED" || e.Code == "SCHEMA_VIOLATION" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected CONTRACT_REQUIRED or SCHEMA_VIOLATION error for event interface without contract")
		for _, e := range result.Errors {
			t.Logf("  got: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_BundleWithDocs(t *testing.T) {
	// A bundle containing an optional docs/ directory must validate successfully.
	// The docs/ directory is informational metadata with no contract semantics.
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	// Provide a bundle FS that includes docs/ with various documentation files.
	bundleFS := fstest.MapFS{
		"pacto.yaml":           &fstest.MapFile{Data: data},
		"docs":                 &fstest.MapFile{Mode: fs.ModeDir | 0755},
		"docs/README.md":       &fstest.MapFile{Data: []byte("# Service Overview")},
		"docs/architecture.md": &fstest.MapFile{Data: []byte("# Architecture Notes")},
		"docs/runbook.md":      &fstest.MapFile{Data: []byte("# Operational Runbook")},
		"docs/integration.md":  &fstest.MapFile{Data: []byte("# Integration Guide")},
	}
	result := validation.Validate(c, data, bundleFS)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_BundleWithoutDocs(t *testing.T) {
	// A bundle without docs/ must remain valid — docs/ is strictly optional.
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	bundleFS := fstest.MapFS{
		"pacto.yaml": &fstest.MapFile{Data: data},
	}
	result := validation.Validate(c, data, bundleFS)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_ScalingMinExceedsMax(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
scaling:
  min: 10
  max: 2
`)
	result := validation.Validate(c, data, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "SCALING_MIN_EXCEEDS_MAX" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SCALING_MIN_EXCEEDS_MAX error")
		for _, e := range result.Errors {
			t.Logf("  got: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_DuplicateInterfaceNames(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-service
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
  - name: api
    type: http
    port: 9090
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)
	result := validation.Validate(c, data, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "DUPLICATE_INTERFACE_NAME" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected DUPLICATE_INTERFACE_NAME error")
		for _, e := range result.Errors {
			t.Logf("  got: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_JobScalingNotAllowed(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "1.0"
service:
  name: my-job
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: job
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
scaling:
  min: 1
  max: 3
`)
	result := validation.Validate(c, data, nil)
	found := false
	for _, e := range result.Errors {
		if e.Code == "JOB_SCALING_NOT_ALLOWED" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected JOB_SCALING_NOT_ALLOWED error")
		for _, e := range result.Errors {
			t.Logf("  got: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}
