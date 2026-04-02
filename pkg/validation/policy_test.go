package validation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
)

// mockBundleResolver implements BundleResolver for tests.
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
	pol1 := mustResolvePolicy(t, "policies[0]", `{
		"type": "object",
		"required": ["service"]
	}`)
	pol2 := mustResolvePolicy(t, "policies[1]", `{
		"type": "object",
		"required": ["pactoVersion"]
	}`)
	rawYAML := []byte("pactoVersion: '1.0'\nservice:\n  name: my-svc\n")
	result := EnforcePolicies(rawYAML, []ResolvedPolicy{pol1, pol2})
	if !result.IsValid() {
		for _, e := range result.Errors {
			t.Errorf("unexpected error: [%s] %s", e.Code, e.Message)
		}
	}
}

func TestEnforcePolicies_ContradictoryPoliciesFail(t *testing.T) {
	pol1 := mustResolvePolicy(t, "policies[0]", `{
		"type": "object",
		"properties": {
			"service": {
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				}
			}
		}
	}`)
	pol2 := mustResolvePolicy(t, "policies[1]", `{
		"type": "object",
		"properties": {
			"service": {
				"type": "object",
				"properties": {
					"name": {"type": "number"}
				}
			}
		}
	}`)
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
	pol := mustResolvePolicy(t, "policies[0]", `{
		"type": "object",
		"required": ["zzz", "aaa"]
	}`)
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

func TestEnforcePolicies_NestedViolationsFlattened(t *testing.T) {
	// Use allOf to produce multiple independent violations that flatten to multiple leaves
	pol := mustResolvePolicy(t, "policies[0]", `{
		"allOf": [
			{"required": ["aaa"]},
			{"required": ["bbb"]}
		]
	}`)
	rawYAML := []byte("foo: bar\n")
	result := EnforcePolicies(rawYAML, []ResolvedPolicy{pol})
	if result.IsValid() {
		t.Error("expected violations")
	}
	if len(result.Errors) < 2 {
		t.Errorf("expected at least 2 flattened violations, got %d", len(result.Errors))
	}
}

func TestCompilePolicySchema_ValidSchema(t *testing.T) {
	s, err := compilePolicySchema([]byte(`{"type": "object"}`), "mem:///test.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Error("expected non-nil schema")
	}
}

func TestCompilePolicySchema_InvalidJSON(t *testing.T) {
	_, err := compilePolicySchema([]byte(`not json`), "mem:///test.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCompilePolicySchema_InvalidSchema(t *testing.T) {
	_, err := compilePolicySchema([]byte(`{"type": 123}`), "mem:///test.json")
	if err == nil {
		t.Error("expected error for invalid schema")
	}
}

func TestCompilePolicySchema_AddResourceError(t *testing.T) {
	// Using a metaschema URL triggers AddResource to fail with ResourceExistsError.
	_, err := compilePolicySchema([]byte(`{"type": "object"}`), "https://json-schema.org/draft/2020-12/schema")
	if err == nil {
		t.Error("expected error for metaschema URL")
	}
}

func TestResolvePoliciesFromBundle_NilBundleFS(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Schema: "policy.json"}},
	}
	policies, result := ResolvePoliciesFromBundle(c, nil)
	if policies != nil {
		t.Error("expected nil policies with nil bundleFS")
	}
	if !result.IsValid() {
		t.Error("expected no errors")
	}
}

func TestResolvePoliciesFromBundle_NoPolicies(t *testing.T) {
	c := &contract.Contract{}
	bundleFS := fstest.MapFS{}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if len(policies) != 0 {
		t.Error("expected no policies")
	}
	if !result.IsValid() {
		t.Error("expected no errors")
	}
}

func TestResolvePoliciesFromBundle_LocalSchemaResolved(t *testing.T) {
	bundleFS := fstest.MapFS{
		"policy.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
	}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Schema: "policy.json"}},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if !result.IsValid() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	if policies[0].Origin != "policies[0]" {
		t.Errorf("expected origin policies[0], got %s", policies[0].Origin)
	}
}

func TestResolvePoliciesFromBundle_MultiplePolicies(t *testing.T) {
	bundleFS := fstest.MapFS{
		"policy-a.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
		"policy-b.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["pactoVersion"]}`)},
	}
	c := &contract.Contract{
		Policies: []contract.PolicySource{
			{Schema: "policy-a.json"},
			{Schema: "policy-b.json"},
		},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if !result.IsValid() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(policies))
	}
}

func TestResolvePoliciesFromBundle_FileNotFound(t *testing.T) {
	bundleFS := fstest.MapFS{}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Schema: "missing.json"}},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if !result.IsValid() {
		t.Error("expected no errors (file-not-found handled by crossfield)")
	}
	if len(policies) != 0 {
		t.Error("expected no policies when file not found")
	}
}

func TestResolvePoliciesFromBundle_InvalidJSON(t *testing.T) {
	bundleFS := fstest.MapFS{
		"policy.json": &fstest.MapFile{Data: []byte(`not valid json`)},
	}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Schema: "policy.json"}},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if !result.IsValid() {
		t.Error("expected no errors (invalid JSON handled by crossfield)")
	}
	if len(policies) != 0 {
		t.Error("expected no policies when JSON invalid")
	}
}

func TestResolvePoliciesFromBundle_InvalidSchema(t *testing.T) {
	bundleFS := fstest.MapFS{
		"policy.json": &fstest.MapFile{Data: []byte(`{"type": 123}`)},
	}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Schema: "policy.json"}},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if !result.IsValid() {
		t.Error("expected no errors (schema compilation errors handled by crossfield)")
	}
	if len(policies) != 0 {
		t.Error("expected no policies when schema invalid")
	}
}

func TestResolvePoliciesFromBundle_RefBasedSkipped(t *testing.T) {
	bundleFS := fstest.MapFS{}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if !result.IsValid() {
		t.Error("expected no errors for ref-based policies")
	}
	if len(policies) != 0 {
		t.Error("expected ref-based policies to be skipped")
	}
}

func TestResolvePoliciesFromBundle_MixedSchemaAndRef(t *testing.T) {
	bundleFS := fstest.MapFS{
		"policy.json": &fstest.MapFile{Data: []byte(`{"type": "object"}`)},
	}
	c := &contract.Contract{
		Policies: []contract.PolicySource{
			{Schema: "policy.json"},
			{Ref: "oci://example.com/policy:1.0"},
		},
	}
	policies, result := ResolvePoliciesFromBundle(c, bundleFS)
	if !result.IsValid() {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 resolved policy (ref skipped), got %d", len(policies))
	}
	if policies[0].Origin != "policies[0]" {
		t.Errorf("expected origin policies[0], got %s", policies[0].Origin)
	}
}

// --- ResolvePoliciesWithResolver tests ---

func TestResolvePoliciesWithResolver_NilResolverRefProducesHardError(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, nil)
	if result.IsValid() {
		t.Fatal("expected POLICY_REF_UNRESOLVED error")
	}
	if len(policies) != 0 {
		t.Errorf("expected no policies, got %d", len(policies))
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_UNRESOLVED" {
			found = true
		}
	}
	if !found {
		t.Error("expected error code POLICY_REF_UNRESOLVED")
	}
}

func TestResolvePoliciesWithResolver_RefResolvesToPolicySchema(t *testing.T) {
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if !result.IsValid() {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_RefBundleMissingPolicySchema(t *testing.T) {
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{},
		FS:       fstest.MapFS{},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected error when bundle missing policy/schema.json")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_UNRESOLVED" && strings.Contains(e.Message, PolicySchemaPath) {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_REF_UNRESOLVED mentioning policy/schema.json")
	}
}

func TestResolvePoliciesWithResolver_RefBundleInvalidJSON(t *testing.T) {
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`not json`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected error for invalid JSON in policy/schema.json")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_UNRESOLVED" && strings.Contains(e.Message, "not valid JSON") {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_REF_UNRESOLVED with 'not valid JSON'")
	}
}

func TestResolvePoliciesWithResolver_RefBundleInvalidSchema(t *testing.T) {
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": 123}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected error for invalid schema in policy/schema.json")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_UNRESOLVED" && strings.Contains(e.Message, "failed to compile") {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_REF_UNRESOLVED with 'failed to compile'")
	}
}

func TestResolvePoliciesWithResolver_RefResolutionError(t *testing.T) {
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/missing:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected error for unresolvable ref")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_UNRESOLVED" && strings.Contains(e.Message, "failed to resolve") {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_REF_UNRESOLVED with 'failed to resolve'")
	}
}

func TestResolvePoliciesWithResolver_RefNilBundle(t *testing.T) {
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": nil,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected error for nil bundle")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_UNRESOLVED" && strings.Contains(e.Message, "empty bundle") {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_REF_UNRESOLVED with 'empty bundle'")
	}
}

func TestResolvePoliciesWithResolver_RefNilFS(t *testing.T) {
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": {Contract: &contract.Contract{}},
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected error for nil FS")
	}
}

func TestResolvePoliciesWithResolver_CycleDetection(t *testing.T) {
	// A references B, B references A → cycle
	bundleB := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.PolicySource{{Ref: "oci://example.com/a:1.0"}},
		},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object"}`)},
		},
	}
	bundleA := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.PolicySource{{Ref: "oci://example.com/b:1.0"}},
		},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object"}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/a:1.0": bundleA,
		"oci://example.com/b:1.0": bundleB,
	}}

	rootContract := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/a:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), rootContract, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected cycle detection error")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_CYCLE" {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_REF_CYCLE error")
	}
}

func TestResolvePoliciesWithResolver_NHopChain(t *testing.T) {
	// Root → A → B (3 hops, all with policy/schema.json)
	bundleB := &contract.Bundle{
		Contract: &contract.Contract{},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["pactoVersion"]}`)},
		},
	}
	bundleA := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.PolicySource{{Ref: "oci://example.com/b:1.0"}},
		},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/a:1.0": bundleA,
		"oci://example.com/b:1.0": bundleB,
	}}

	rootContract := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/a:1.0"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), rootContract, fstest.MapFS{}, resolver)
	if !result.IsValid() {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	// A has explicit policies[], so recursion is used (no fixed-path duplication).
	// B has no policies[], so its policy/schema.json is read via fixed-path fallback.
	// Result: 1 policy (B's schema only, resolved via A's policies[0].ref → B).
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy from N-hop chain (A uses recursion, B uses fallback), got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_MixedLocalAndRef_ANDSemantics(t *testing.T) {
	// Local policy requires "service", ref policy requires "pactoVersion"
	// Both must be satisfied (AND semantics)
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["pactoVersion"]}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}

	bundleFS := fstest.MapFS{
		"local-policy.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
	}
	c := &contract.Contract{
		Policies: []contract.PolicySource{
			{Schema: "local-policy.json"},
			{Ref: "oci://example.com/policy:1.0"},
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, bundleFS, resolver)
	if !result.IsValid() {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies (local + ref), got %d", len(policies))
	}

	// Enforce against a doc missing "pactoVersion" — should fail
	rawYAML := []byte("service:\n  name: test\n")
	enforceResult := EnforcePolicies(rawYAML, policies)
	if enforceResult.IsValid() {
		t.Error("expected AND semantics: missing pactoVersion should fail")
	}

	// Enforce against a complete doc — should pass
	rawYAMLFull := []byte("pactoVersion: '1.0'\nservice:\n  name: test\n")
	enforceResultFull := EnforcePolicies(rawYAMLFull, policies)
	if !enforceResultFull.IsValid() {
		t.Errorf("expected full doc to pass AND semantics: %v", enforceResultFull.Errors)
	}
}

func TestResolvePoliciesWithResolver_DeterministicOutput(t *testing.T) {
	// Same input should produce same output order
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object"}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	bundleFS := fstest.MapFS{
		"local.json": &fstest.MapFile{Data: []byte(`{"type": "object"}`)},
	}
	c := &contract.Contract{
		Policies: []contract.PolicySource{
			{Schema: "local.json"},
			{Ref: "oci://example.com/policy:1.0"},
		},
	}

	// Run twice, verify same order
	p1, _ := ResolvePoliciesWithResolver(context.Background(), c, bundleFS, resolver)
	p2, _ := ResolvePoliciesWithResolver(context.Background(), c, bundleFS, resolver)
	if len(p1) != len(p2) {
		t.Fatalf("different policy counts: %d vs %d", len(p1), len(p2))
	}
	for i := range p1 {
		if p1[i].Origin != p2[i].Origin {
			t.Errorf("origin mismatch at %d: %s vs %s", i, p1[i].Origin, p2[i].Origin)
		}
	}
}

func TestResolvePoliciesWithResolver_LocalSchemaWithNilBundleFS(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Schema: "policy.json"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, nil)
	if !result.IsValid() {
		t.Error("expected no errors")
	}
	if len(policies) != 0 {
		t.Error("expected no policies with nil bundleFS")
	}
}

func TestResolvePoliciesWithResolver_NoPolicies(t *testing.T) {
	c := &contract.Contract{}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, nil)
	if !result.IsValid() {
		t.Error("expected no errors")
	}
	if len(policies) != 0 {
		t.Error("expected no policies")
	}
}

func TestResolvePoliciesWithResolver_ContradictoryRefPolicies(t *testing.T) {
	// Two refs with contradictory policies: one requires name=string, other requires name=number
	refA := &contract.Bundle{
		Contract: &contract.Contract{},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{
				"type": "object",
				"properties": {"service": {"type": "object", "properties": {"name": {"type": "string"}}}}
			}`)},
		},
	}
	refB := &contract.Bundle{
		Contract: &contract.Contract{},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{
				"type": "object",
				"properties": {"service": {"type": "object", "properties": {"name": {"type": "number"}}}}
			}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/a:1.0": refA,
		"oci://example.com/b:1.0": refB,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{
			{Ref: "oci://example.com/a:1.0"},
			{Ref: "oci://example.com/b:1.0"},
		},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if !result.IsValid() {
		t.Fatalf("resolution should succeed, enforcement should fail: %v", result.Errors)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(policies))
	}
	// Enforcement against string name should fail (policy B contradicts)
	rawYAML := []byte("service:\n  name: test\n")
	enforceResult := EnforcePolicies(rawYAML, policies)
	if enforceResult.IsValid() {
		t.Error("expected contradictory policies to fail enforcement")
	}
}

func TestResolvePoliciesWithResolver_ResolverError(t *testing.T) {
	resolver := &mockBundleResolver{err: fmt.Errorf("network error")}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected error from resolver")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_UNRESOLVED" && strings.Contains(e.Message, "network error") {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_REF_UNRESOLVED with resolver error")
	}
}

func TestResolveLocalPolicySchema_NilBundleFS(t *testing.T) {
	rp := resolveLocalPolicySchema(nil, "policy.json", "test", 0)
	if rp != nil {
		t.Error("expected nil for nil bundleFS")
	}
}

func TestResolvePoliciesWithResolver_RefBundleWithExplicitPolicies(t *testing.T) {
	// Provider declares policies: [{schema: policy/schema.json}] explicitly.
	// Consumer should get exactly 1 policy (not 2 from double enforcement).
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.PolicySource{{Schema: "policy/schema.json"}},
		},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if !result.IsValid() {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy (no double enforcement), got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_RefBundleCustomSchemaPath(t *testing.T) {
	// Provider uses custom path policies: [{schema: policy/custom.json}]
	// with NO policy/schema.json. Should resolve successfully via recursion.
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.PolicySource{{Schema: "policy/custom.json"}},
		},
		FS: fstest.MapFS{
			"policy/custom.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if !result.IsValid() {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy from custom schema path, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_RefBundleMultiplePolicies(t *testing.T) {
	// Provider declares 2 policy schemas. Consumer should get both.
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.PolicySource{
				{Schema: "policy/scaling.json"},
				{Schema: "policy/naming.json"},
			},
		},
		FS: fstest.MapFS{
			"policy/scaling.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
			"policy/naming.json":  &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["pactoVersion"]}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if !result.IsValid() {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies from multi-schema provider, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_RefBundleEmptyPoliciesSlice(t *testing.T) {
	// Provider has Policies: [] (empty slice) — should fall back to fixed-path.
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{
			Policies: []contract.PolicySource{},
		},
		FS: fstest.MapFS{
			"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type": "object", "required": ["service"]}`)},
		},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if !result.IsValid() {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy from fixed-path fallback, got %d", len(policies))
	}
}

func TestResolvePoliciesWithResolver_RefBundleNoPoliciesNoFixedPath(t *testing.T) {
	// Provider has no policies[] AND no policy/schema.json — should error.
	refBundle := &contract.Bundle{
		Contract: &contract.Contract{},
		FS:       fstest.MapFS{},
	}
	resolver := &mockBundleResolver{bundles: map[string]*contract.Bundle{
		"oci://example.com/policy:1.0": refBundle,
	}}
	c := &contract.Contract{
		Policies: []contract.PolicySource{{Ref: "oci://example.com/policy:1.0"}},
	}
	_, result := ResolvePoliciesWithResolver(context.Background(), c, fstest.MapFS{}, resolver)
	if result.IsValid() {
		t.Fatal("expected error when no policies[] and no fixed-path schema")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "POLICY_REF_UNRESOLVED" {
			found = true
		}
	}
	if !found {
		t.Error("expected POLICY_REF_UNRESOLVED error")
	}
}
