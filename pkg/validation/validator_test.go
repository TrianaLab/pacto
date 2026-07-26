package validation_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

type testBundleResolver struct {
	bundles map[string]*contract.Bundle
}

func (r *testBundleResolver) ResolveBundle(_ context.Context, ref string) (*contract.Bundle, error) {
	b, ok := r.bundles[ref]
	if !ok {
		return nil, fmt.Errorf("not found: %s", ref)
	}
	return b, nil
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
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte("openapi: '3.0.0'\n")},
	}
	result := validation.Validate(c, data, bundleFS)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_ValidStateful(t *testing.T) {
	data, c := parseFixture(t, "testdata/valid_stateful.yaml")
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml":       &fstest.MapFile{Data: []byte("openapi: '3.0.0'\n")},
		"configuration/app.schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"replicas":{"type":"integer"},"timeout":{"type":"integer"}}}`)},
	}
	result := validation.Validate(c, data, bundleFS)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidate_UnsupportedVersion(t *testing.T) {
	data := []byte(`pactoVersion: "1.0"
service:
  name: legacy
  version: "1.0.0"
`)
	result := validation.ValidateStructuralRaw(data)
	if result.IsValid() {
		t.Error("expected validation to fail for unsupported pactoVersion")
	}
	if !hasErrorCode(result, "UNSUPPORTED_PACTO_VERSION") {
		t.Errorf("expected UNSUPPORTED_PACTO_VERSION, got %+v", result.Errors)
	}
}

func TestValidate_InvalidInterfaceType(t *testing.T) {
	data := []byte(`pactoVersion: "2.0"
service:
  name: bad-iface
  version: "1.0.0"
  owner:
    team: platform
interfaces:
  - name: api
    type: http
    ref: interfaces/spec.yaml
`)
	result := validation.ValidateStructuralRaw(data)
	if result.IsValid() {
		t.Error("expected validation to fail for invalid interface type")
	}
	if !hasErrorCode(result, "SCHEMA_VIOLATION") {
		t.Errorf("expected SCHEMA_VIOLATION, got %+v", result.Errors)
	}
}

func TestValidate_StatelessPersistentConflict(t *testing.T) {
	data := []byte(`pactoVersion: "2.0"
service:
  name: bad-service
  version: "1.0.0"
  owner:
    team: backend
state:
  type: stateless
  persistence:
    scope: local
    durability: persistent
  dataCriticality: low
`)
	result := validation.ValidateStructuralRaw(data)
	if result.IsValid() {
		t.Error("expected validation to fail for stateless+persistent")
	}
	if !hasErrorCode(result, "SCHEMA_VIOLATION") {
		t.Errorf("expected SCHEMA_VIOLATION, got %+v", result.Errors)
	}
}

func TestValidate_MissingInterfaceFile(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
  owner:
    team: platform
interfaces:
  - name: api
    type: openapi
    ref: interfaces/missing.yaml
`)
	bundleFS := fstest.MapFS{}
	result := validation.Validate(c, data, bundleFS)
	if result.IsValid() {
		t.Error("expected validation to fail for missing interface file")
	}
	if !hasErrorCode(result, "FILE_NOT_FOUND") {
		t.Errorf("expected FILE_NOT_FOUND, got %+v", result.Errors)
	}
}

func TestValidateWithResolver_LocalPolicyOnly(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
  owner:
    team: platform
policies:
  - name: security
    schema: policy/sec.json
`)
	bundleFS := fstest.MapFS{
		"policy/sec.json": &fstest.MapFile{Data: []byte(`{"type":"object","required":["service"],"properties":{"service":{"type":"object","required":["owner"]}}}`)},
	}
	result := validation.ValidateWithResolver(context.Background(), c, data, bundleFS, nil)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidateWithResolver_RefPolicy_NoResolver(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
  owner:
    team: platform
policies:
  - name: external
    ref: oci://ghcr.io/acme/policy:1.0.0
`)
	result := validation.ValidateWithResolver(context.Background(), c, data, nil, nil)
	if result.IsValid() {
		t.Error("expected validation to fail when resolver is nil for ref policy")
	}
	if !hasErrorCode(result, "POLICY_REF_UNRESOLVED") {
		t.Errorf("expected POLICY_REF_UNRESOLVED, got %+v", result.Errors)
	}
}

func TestValidateWithResolver_RefPolicy_Resolved(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
  owner:
    team: platform
policies:
  - name: external
    ref: oci://ghcr.io/acme/policy:1.0.0
`)
	policyFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","required":["service"]}`)},
	}
	resolver := &testBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy:1.0.0": {FS: policyFS},
		},
	}
	result := validation.ValidateWithResolver(context.Background(), c, data, nil, resolver)
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s: %s", e.Code, e.Path, e.Message)
		}
	}
}

func TestValidateWithResolver_PolicyViolation(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
  owner:
    team: platform
`)
	bundleFS := fstest.MapFS{
		"policy/sec.json": &fstest.MapFile{Data: []byte(`{"type":"object","required":["interfaces"],"properties":{"interfaces":{"type":"array","minItems":1}}}`)},
	}
	c.Policies = []contract.Policy{{Name: "sec", Schema: "policy/sec.json"}}
	result := validation.Validate(c, data, bundleFS)
	if result.IsValid() {
		t.Error("expected policy violation for missing interfaces")
	}
	if !hasErrorCode(result, "POLICY_VIOLATION") {
		t.Errorf("expected POLICY_VIOLATION, got %+v", result.Errors)
	}
}

func TestValidate_LayerShortCircuit(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "2.0"
service:
  name: test-svc
  version: "not-semver"
  owner:
    team: platform
`)
	result := validation.Validate(c, data, nil)
	if result.IsValid() {
		t.Error("expected validation to fail")
	}
	if hasErrorCode(result, "POLICY_VIOLATION") {
		t.Error("policy layer should not run when crossfield fails")
	}
}

func TestValidate_WithRefPolicyWarning(t *testing.T) {
	data, c := parseString(t, `
pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
  owner:
    team: platform
policies:
  - name: external
    ref: oci://ghcr.io/acme/policy:1.0.0
`)
	bundleFS := fstest.MapFS{}
	result := validation.Validate(c, data, bundleFS)
	if !hasWarningCode(result, "POLICY_REF_NOT_ENFORCED") {
		t.Errorf("expected POLICY_REF_NOT_ENFORCED warning, got %+v", result.Warnings)
	}
}

func hasErrorCode(r validation.ValidationResult, code string) bool {
	for _, e := range r.Errors {
		if e.Code == code {
			return true
		}
	}
	return false
}

func hasWarningCode(r validation.ValidationResult, code string) bool {
	for _, w := range r.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
