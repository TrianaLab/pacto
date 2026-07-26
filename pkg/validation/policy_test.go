package validation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

type mockBundleResolver struct {
	bundles map[string]*contract.Bundle
	err     error
}

func (m *mockBundleResolver) ResolveBundle(_ context.Context, ref string) (*contract.Bundle, error) {
	if m.err != nil {
		return nil, m.err
	}
	b, ok := m.bundles[ref]
	if !ok {
		return nil, fmt.Errorf("bundle not found: %s", ref)
	}
	return b, nil
}

func mustResolvePolicy(t *testing.T, origin, schemaJSON string) ResolvedPolicy {
	t.Helper()
	s, err := compilePolicySchema([]byte(schemaJSON), "mem:///test-policy.json")
	if err != nil {
		t.Fatalf("failed to compile policy schema: %v", err)
	}
	return ResolvedPolicy{Origin: origin, Schema: s}
}

func TestEnforcePolicies_NoPolicies(t *testing.T) {
	result := EnforcePolicies([]byte(`{}`), nil)
	if !result.IsValid() {
		t.Error("expected no errors with empty policies")
	}
}

func TestEnforcePolicies_SinglePolicySatisfied(t *testing.T) {
	pol := mustResolvePolicy(t, "policies[0]", `{
		"type": "object",
		"properties": {
			"service": {
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				},
				"required": ["name"]
			}
		},
		"required": ["service"]
	}`)
	rawYAML := []byte("service:\n  name: my-svc\n")
	result := EnforcePolicies(rawYAML, []ResolvedPolicy{pol})
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s", e.Code, e.Message)
		}
	}
}

func TestEnforcePolicies_SinglePolicyViolated(t *testing.T) {
	pol := mustResolvePolicy(t, "policies[0]", `{
		"type": "object",
		"properties": {
			"service": {
				"type": "object",
				"required": ["owner"]
			}
		},
		"required": ["service"]
	}`)
	rawYAML := []byte("service:\n  name: my-svc\n")
	result := EnforcePolicies(rawYAML, []ResolvedPolicy{pol})
	if result.IsValid() {
		t.Error("expected policy violation")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_VIOLATION" && strings.Contains(e.Message, "policies[0]") {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_VIOLATION with origin policies[0]")
	}
}

func TestEnforcePolicies_MultiplePoliciesAllSatisfied(t *testing.T) {
	pol1 := mustResolvePolicy(t, "policies[0]", `{"type": "object","required": ["service"]}`)
	pol2 := mustResolvePolicy(t, "policies[1]", `{"type": "object","required": ["pactoVersion"]}`)
	rawYAML := []byte("pactoVersion: '2.0'\nservice:\n  name: my-svc\n")
	result := EnforcePolicies(rawYAML, []ResolvedPolicy{pol1, pol2})
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s", e.Code, e.Message)
		}
	}
}

func TestEnforcePolicies_ContradictoryPoliciesFail(t *testing.T) {
	pol1 := mustResolvePolicy(t, "policies[0]", `{"type": "object","properties": {"service": {"type": "object","properties": {"name": {"type": "string"}}}}}`)
	pol2 := mustResolvePolicy(t, "policies[1]", `{"type": "object","properties": {"service": {"type": "object","properties": {"name": {"type": "number"}}}}}`)
	rawYAML := []byte("service:\n  name: my-svc\n")
	result := EnforcePolicies(rawYAML, []ResolvedPolicy{pol1, pol2})
	if result.IsValid() {
		t.Error("expected contradictory policy to fail")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_VIOLATION" && strings.Contains(e.Message, "policies[1]") {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_VIOLATION from policies[1]")
	}
}

func TestEnforcePolicies_InvalidYAML(t *testing.T) {
	pol := mustResolvePolicy(t, "policies[0]", `{"type": "object"}`)
	result := EnforcePolicies([]byte(":\n  bad: yaml: [[["), []ResolvedPolicy{pol})
	if result.IsValid() {
		t.Error("expected error for invalid YAML")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_ENFORCEMENT_ERROR" {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_ENFORCEMENT_ERROR")
	}
}

func TestEnforcePolicies_MultipleViolationsSorted(t *testing.T) {
	pol := mustResolvePolicy(t, "policies[0]", `{"type": "object","required": ["zzz", "aaa"]}`)
	rawYAML := []byte("foo: bar\n")
	result := EnforcePolicies(rawYAML, []ResolvedPolicy{pol})
	if result.IsValid() {
		t.Error("expected violations")
	}
	var msgs []string
	for _, e := range result.Errors {
		msgs = append(msgs, e.Message)
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i] < msgs[i-1] {
			t.Errorf("violations not sorted: %q before %q", msgs[i-1], msgs[i])
		}
	}
}

func TestCollectPolicyViolations_NonValidationError(t *testing.T) {
	var result ValidationResult
	collectPolicyViolations(&result, "test-origin", errors.New("some generic error"))
	if result.IsValid() {
		t.Error("expected error")
	}
	if !strings.Contains(result.Errors[0].Message, "test-origin") {
		t.Error("expected origin in message")
	}
}

func TestResolvePoliciesFromBundle_NoBundleFS(t *testing.T) {
	c := &contract.Contract{}
	policies, result := ResolvePoliciesFromBundle(c, nil)
	if !result.IsValid() {
		t.Errorf("expected no errors, got %+v", result.Errors)
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
}

func TestResolvePoliciesFromBundle_LocalSchema(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "sec", Schema: "policy/sec.json"}},
	}
	bundleFS := fstest.MapFS{
		"policy/sec.json": &fstest.MapFile{Data: []byte(`{"type":"object","required":["service"]}`)},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if !result.IsValid() {
		t.Errorf("expected no errors, got %+v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	if policies[0].Origin != `policies["sec"]` {
		t.Errorf("expected origin policies[\"sec\"], got %q", policies[0].Origin)
	}
}

func TestResolvePoliciesFromBundle_RefWarning(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	bundleFS := fstest.MapFS{}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if len(policies) != 0 {
		t.Errorf("expected no policies for ref without resolver, got %d", len(policies))
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected warning for ref policy")
	}
	if result.Warnings[0].Code != "POLICY_REF_NOT_ENFORCED" {
		t.Errorf("expected POLICY_REF_NOT_ENFORCED, got %q", result.Warnings[0].Code)
	}
}

func TestResolvePoliciesFromBundle_InvalidSchemaIgnored(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "sec", Schema: "policy/sec.json"}},
	}
	bundleFS := fstest.MapFS{
		"policy/sec.json": &fstest.MapFile{Data: []byte(`not json`)},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if len(policies) != 0 {
		t.Errorf("expected no policies for invalid schema, got %d", len(policies))
	}
	if !result.IsValid() {
		t.Errorf("expected no errors (invalid schema is silently skipped), got %+v", result.Errors)
	}
}

func TestResolvePoliciesWithResolver_NilResolver_RefError(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, nil)
	if result.IsValid() {
		t.Error("expected error for ref policy with nil resolver")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
	if result.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
		t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", result.Errors[0].Code)
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_Resolved(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	policyFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","required":["service"]}`)},
	}
	resolver := &mockBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy:1.0.0": {FS: policyFS},
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if !result.IsValid() {
		t.Errorf("expected no errors, got %+v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_ResolverError(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	resolver := &mockBundleResolver{err: fmt.Errorf("network error")}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if result.IsValid() {
		t.Error("expected error from resolver")
	}
	if result.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
		t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", result.Errors[0].Code)
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_EmptyBundle(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	resolver := &mockBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy:1.0.0": {},
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if result.IsValid() {
		t.Error("expected error for empty bundle")
	}
	if result.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
		t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", result.Errors[0].Code)
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_MissingPolicySchema(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	policyFS := fstest.MapFS{}
	resolver := &mockBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy:1.0.0": {FS: policyFS},
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if result.IsValid() {
		t.Error("expected error for missing policy schema")
	}
	if result.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
		t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", result.Errors[0].Code)
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_InvalidSchemaJSON(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	policyFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`not json`)},
	}
	resolver := &mockBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy:1.0.0": {FS: policyFS},
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if result.IsValid() {
		t.Error("expected error for invalid JSON")
	}
	if result.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
		t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", result.Errors[0].Code)
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_InvalidSchemaCompilation(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	policyFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": 12345}`)},
	}
	resolver := &mockBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy:1.0.0": {FS: policyFS},
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if result.IsValid() {
		t.Error("expected error for invalid schema")
	}
	if result.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
		t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", result.Errors[0].Code)
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_RecursiveRef(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy-a:1.0.0"}},
	}
	policyABundle := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.Policy{{Name: "ext2", Ref: "oci://ghcr.io/acme/policy-b:1.0.0"}},
		},
		FS: fstest.MapFS{},
	}
	policyBBundle := &contract.Bundle{
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","required":["service"]}`)},
		},
	}
	resolver := &mockBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy-a:1.0.0": policyABundle,
			"oci://ghcr.io/acme/policy-b:1.0.0": policyBBundle,
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if !result.IsValid() {
		t.Errorf("expected no errors, got %+v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy (recursively resolved), got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_Cycle(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "ext", Ref: "oci://ghcr.io/acme/policy-a:1.0.0"}},
	}
	policyABundle := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.Policy{{Name: "ext2", Ref: "oci://ghcr.io/acme/policy-a:1.0.0"}},
		},
		FS: fstest.MapFS{},
	}
	resolver := &mockBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy-a:1.0.0": policyABundle,
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if result.IsValid() {
		t.Error("expected error for cycle")
	}
	if result.Errors[0].Code != "POLICY_REF_CYCLE" {
		t.Errorf("expected POLICY_REF_CYCLE, got %q", result.Errors[0].Code)
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_Diamond(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{
			{Name: "ext1", Ref: "oci://ghcr.io/acme/policy-a:1.0.0"},
			{Name: "ext2", Ref: "oci://ghcr.io/acme/policy-b:1.0.0"},
		},
	}
	sharedPolicyFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","required":["service"]}`)},
	}
	policyABundle := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.Policy{{Name: "shared", Ref: "oci://ghcr.io/acme/policy-shared:1.0.0"}},
		},
		FS: fstest.MapFS{},
	}
	policyBBundle := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.Policy{{Name: "shared", Ref: "oci://ghcr.io/acme/policy-shared:1.0.0"}},
		},
		FS: fstest.MapFS{},
	}
	sharedBundle := &contract.Bundle{FS: sharedPolicyFS}
	resolver := &mockBundleResolver{
		bundles: map[string]*contract.Bundle{
			"oci://ghcr.io/acme/policy-a:1.0.0":      policyABundle,
			"oci://ghcr.io/acme/policy-b:1.0.0":      policyBBundle,
			"oci://ghcr.io/acme/policy-shared:1.0.0": sharedBundle,
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
	if !result.IsValid() {
		t.Errorf("expected no errors for diamond (not a cycle), got %+v", result.Errors)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies (shared resolved twice), got %d", len(policies))
	}
}

func TestPolicyOrigin_WithName(t *testing.T) {
	pol := contract.Policy{Name: "security"}
	origin := policyOrigin(pol, 0)
	if origin != `policies["security"]` {
		t.Errorf("expected policies[\"security\"], got %q", origin)
	}
}

func TestPolicyOrigin_WithoutName(t *testing.T) {
	pol := contract.Policy{}
	origin := policyOrigin(pol, 3)
	if origin != "policies[3]" {
		t.Errorf("expected policies[3], got %q", origin)
	}
}

func TestCompilePolicySchema_CompileError(t *testing.T) {
	_, err := compilePolicySchema([]byte(`{"$ref": "#/missing"}`), "mem:///test.json")
	if err == nil {
		t.Error("expected error for schema with unresolved $ref")
	}
}
