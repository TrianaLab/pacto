package validation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// unreadableFS holds a file it will not open. It is what a root bundle carrying
// `chmod 000 policy/schema.json` looks like: layer 2's existence check passes and
// its content check returns silently, so layer 3 is the only thing standing between
// the operator and a contract reported valid with zero policies enforced. The inner
// MapFS is deliberately NOT embedded — promoting its ReadFile would route around
// the refusal. Stat IS delegated, so layer 2's fs.Stat existence check really does
// pass and the layer-3 hole is reached through the full pipeline, not only by
// calling the resolver directly.
type unreadableFS struct{ inner fstest.MapFS }

func (u unreadableFS) Open(name string) (fs.File, error) {
	if _, err := u.inner.Open(name); err != nil {
		return nil, err
	}
	return nil, fs.ErrPermission
}

func (u unreadableFS) Stat(name string) (fs.FileInfo, error) { return fs.Stat(u.inner, name) }

// The ROOT bundle fails closed too. Layer 2 covers only the schema files it can
// stat and read, and layer 3 never runs after a layer-2 failure, so everything left
// here is a hole nothing else reports.
func TestResolvePoliciesFromBundle_UnenforceablePolicyIsAnError(t *testing.T) {
	cases := []struct {
		name     string
		pol      contract.Policy
		bundleFS fs.FS
	}{
		{"schema is not valid JSON", contract.Policy{Name: "sec", Schema: "policy/sec.json"},
			fstest.MapFS{"policy/sec.json": &fstest.MapFile{Data: []byte(`not json`)}}},
		{"schema stats but will not read", contract.Policy{Name: "sec", Schema: "policy/sec.json"},
			unreadableFS{fstest.MapFS{"policy/sec.json": &fstest.MapFile{Data: []byte(`{}`)}}}},
		{"neither schema nor ref", contract.Policy{Name: "baseline"}, fstest.MapFS{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &contract.Contract{Policies: []contract.Policy{tc.pol}}
			policies, result := ResolvePoliciesFromBundle(c, tc.bundleFS)
			if len(policies) != 0 {
				t.Errorf("expected no policies, got %d", len(policies))
			}
			if result.IsValid() {
				t.Fatal("expected POLICY_REF_UNRESOLVED, got a clean result")
			}
			if result.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
				t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", result.Errors[0].Code)
			}
		})
	}
}

// inlineContractYAML is the shape the operator hands Validate for
// spec.contract.inline: a real 2.0 contract declaring a bundle-relative policy
// schema, with no bundle to read it from (loadInline returns BundleFS: nil in
// integrations/kubernetes/internal/loader/contract.go).
const inlineContractYAML = `pactoVersion: "2.0"
service:
  name: orders
  version: "1.0.0"
policies:
  - name: baseline
    target: contract
    schema: policy/schema.json
`

func inlineContract(t *testing.T) *contract.Contract {
	t.Helper()
	c, err := contract.Parse(strings.NewReader(inlineContractYAML))
	if err != nil {
		t.Fatalf("fixture must parse: %v", err)
	}
	return c
}

// The two halves of the nil-FS rule, together so nobody collapses them again.
//
// Half one: "there is no bundle" is a delivery mode, not an unenforced policy.
// An inline contract has no files by construction, so layer 3 resolves nothing and
// reports nothing. Hard-failing it would flip every inline Pacto CR's
// ContractValid condition True -> False on operator upgrade.
//
// Half two: once there IS a filesystem, a schema it cannot produce is exactly the
// silent hole layer 3 exists to close, and stays a hard POLICY_REF_UNRESOLVED.
func TestValidate_NilBundleFS_InlineContractStaysValid(t *testing.T) {
	c := inlineContract(t)

	result := Validate(c, []byte(inlineContractYAML), nil)
	if !result.IsValid() {
		t.Fatalf("an inline contract with no bundle must stay valid, got %+v", result.Errors)
	}
	// Findings() is what the operator projects into status, so check the codes there:
	// a POLICY_REF_NOT_ENFORCED warning is enough to flip contractStatus to Warning.
	for _, f := range result.Findings() {
		if strings.HasPrefix(string(f.Code), "POLICY_REF_") {
			t.Errorf("no POLICY_REF_* finding may be emitted without a bundle, got %q: %s", f.Code, f.Message)
		}
	}

	// Same contract, same policy path, but now a bundle that holds the file and
	// refuses to open it: fail closed.
	withFS := Validate(c, []byte(inlineContractYAML), unreadableFS{
		fstest.MapFS{"policy/schema.json": &fstest.MapFile{Data: []byte(`{}`)}},
	})
	if withFS.IsValid() {
		t.Fatal("an unreadable schema inside a real bundle must still fail closed")
	}
	if withFS.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
		t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", withFS.Errors[0].Code)
	}
}

// The resolver-based entry point takes the same nil-FS rule: an inline contract's
// local-schema policy resolves to nothing rather than to a hard error, while its
// ref policies still resolve through the resolver.
func TestResolvePoliciesWithResolver_NilBundleFS_LocalSchemaIsSilent(t *testing.T) {
	c := inlineContract(t)
	policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, &mockBundleResolver{})
	if len(policies) != 0 {
		t.Errorf("expected no policies without a bundle, got %d", len(policies))
	}
	if !result.IsValid() || len(result.Warnings) != 0 {
		t.Errorf("expected nothing reported, got %+v / %+v", result.Errors, result.Warnings)
	}
}

// A contract with no policies has nothing to enforce, and no filesystem to read it
// from is not a defect.
//
// The ref-only case is what makes ResolvePoliciesFromBundle's own nil-FS guard
// load-bearing rather than a duplicate of resolveLocalPolicy's: without it the loop
// runs and an inline contract's ref policy earns a POLICY_REF_NOT_ENFORCED warning,
// which DeriveStatus ranks as Warning and the operator writes to
// status.contract.status — a Compliant-to-Warning flip on upgrade.
func TestResolvePoliciesFromBundle_NilFSReportsNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    *contract.Contract
	}{
		{"no policies at all", &contract.Contract{}},
		{"local schema policy", &contract.Contract{
			Policies: []contract.Policy{{Name: "baseline", Schema: "policy/schema.json"}},
		}},
		{"ref policy", &contract.Contract{
			Policies: []contract.Policy{{Name: "platform", Ref: "oci://ghcr.io/acme/platform:1.0.0"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policies, result := ResolvePoliciesFromBundle(tc.c, nil)
			if len(policies) != 0 {
				t.Errorf("expected no policies, got %d", len(policies))
			}
			if len(result.Errors) != 0 || len(result.Warnings) != 0 {
				t.Errorf("expected nothing to report, got %+v / %+v", result.Errors, result.Warnings)
			}
		})
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

// A referenced bundle gets neither layer 1 nor layer 2 — contract.Parse does not
// enforce the schema's policies oneOf, and cross-field validation only ever runs on
// the root contract — so every way its policy entry can fail to name an enforceable
// schema is only ever caught here. Dropping any of them silently would let the
// consumer's validate and push pass with zero policies enforced.
func TestResolvePoliciesWithResolver_ReferencedBundlePolicyUnenforceable(t *testing.T) {
	cases := []struct {
		name string
		pol  contract.Policy
		fs   fstest.MapFS
	}{
		{"missing file", contract.Policy{Name: "tls", Schema: "policy/tls.json"}, fstest.MapFS{}},
		{"not json", contract.Policy{Name: "tls", Schema: "policy/tls.json"},
			fstest.MapFS{"policy/tls.json": &fstest.MapFile{Data: []byte(`not json`)}}},
		{"uncompilable", contract.Policy{Name: "tls", Schema: "policy/tls.json"},
			fstest.MapFS{"policy/tls.json": &fstest.MapFile{Data: []byte(`{"type": 12345}`)}}},
		// The published platform bundle whose policies[] entry has a name and nothing
		// else. It matches neither the schema branch nor the ref branch, and the
		// legacy policy/schema.json fallback is already out of reach because the
		// bundle DOES declare policies[].
		{"neither schema nor ref", contract.Policy{Name: "baseline"},
			fstest.MapFS{"policy/schema.json": &fstest.MapFile{Data: []byte(`{"required": ["nope"]}`)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &contract.Contract{
				Policies: []contract.Policy{{Name: "platform", Ref: "oci://ghcr.io/acme/platform:1.0.0"}},
			}
			resolver := &mockBundleResolver{
				bundles: map[string]*contract.Bundle{
					"oci://ghcr.io/acme/platform:1.0.0": {
						Contract: &contract.Contract{Policies: []contract.Policy{tc.pol}},
						FS:       tc.fs,
					},
				},
			}
			policies, result := ResolvePoliciesWithResolver(context.Background(), c, nil, resolver)
			if result.IsValid() {
				t.Fatal("expected POLICY_REF_UNRESOLVED, got a clean result")
			}
			if result.Errors[0].Code != "POLICY_REF_UNRESOLVED" {
				t.Errorf("expected POLICY_REF_UNRESOLVED, got %q", result.Errors[0].Code)
			}
			if len(policies) != 0 {
				t.Errorf("expected no policies, got %d", len(policies))
			}
		})
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
