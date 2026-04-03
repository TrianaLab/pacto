package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/trianalab/pacto/pkg/contract"
)

// ResolvedPolicy holds a compiled policy schema and its origin for error reporting.
type ResolvedPolicy struct {
	Origin string             // human-readable origin (e.g., "policies[0]", "oci://ghcr.io/acme/policy:1.0.0")
	Schema *jsonschema.Schema // compiled JSON Schema
}

// BundleResolver resolves a ref string (OCI or local) into a contract Bundle.
// This abstracts away whether the ref is oci:// or file://.
type BundleResolver interface {
	ResolveBundle(ctx context.Context, ref string) (*contract.Bundle, error)
}

// PolicySchemaPath is the fixed path where policy schemas are located
// inside referenced bundles, as documented in the JSON Schema specification.
const PolicySchemaPath = "policy/schema.json"

// EnforcePolicies validates the contract document against all resolved policy schemas.
// Each policy is applied independently with strict AND semantics: the contract must
// satisfy every policy. Contradictory policies naturally fail — no precedence or
// override logic is applied.
func EnforcePolicies(rawYAML []byte, policies []ResolvedPolicy) ValidationResult {
	var result ValidationResult
	if len(policies) == 0 {
		return result
	}

	// Parse contract YAML into generic form for JSON Schema validation.
	contractDoc, err := yamlToGeneric(rawYAML)
	if err != nil {
		result.AddError("", "POLICY_ENFORCEMENT_ERROR",
			fmt.Sprintf("failed to parse contract for policy enforcement: %v", err))
		return result
	}

	for _, pol := range policies {
		if err := pol.Schema.Validate(contractDoc); err != nil {
			collectPolicyViolations(&result, pol.Origin, err)
		}
	}

	return result
}

// collectPolicyViolations extracts individual violations from a JSON Schema validation error.
func collectPolicyViolations(result *ValidationResult, origin string, err error) {
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		result.AddError("", "POLICY_VIOLATION",
			fmt.Sprintf("policy %s: %v", origin, err))
		return
	}
	violations := flattenViolations(ve)
	// Sort for deterministic output.
	slices.Sort(violations)
	for _, v := range violations {
		result.AddError("", "POLICY_VIOLATION",
			fmt.Sprintf("policy %s: %s", origin, v))
	}
}

// flattenViolations recursively collects leaf error messages from a JSON Schema validation error.
func flattenViolations(ve *jsonschema.ValidationError) []string {
	if len(ve.Causes) == 0 {
		return []string{ve.Error()}
	}
	var msgs []string
	for _, cause := range ve.Causes {
		msgs = append(msgs, flattenViolations(cause)...)
	}
	return msgs
}

// ResolvePoliciesFromBundle resolves policy sources from a contract using only the
// local bundle filesystem. This is the default resolver used when no external
// resolver (OCI/file) is configured. It compiles local schema files and skips
// external refs (which are validated structurally but not enforced without a resolver).
func ResolvePoliciesFromBundle(c *contract.Contract, bundleFS fs.FS) ([]ResolvedPolicy, ValidationResult) {
	var policies []ResolvedPolicy
	var result ValidationResult

	if bundleFS == nil {
		return nil, result
	}

	for i, pol := range c.Policies {
		origin := policyOrigin(pol, i)

		if pol.Schema != "" {
			rp := resolveLocalPolicySchema(bundleFS, pol.Schema, origin, i)
			if rp != nil {
				policies = append(policies, *rp)
			}
		}
		// Ref-based policies require an external resolver — skipped here.
	}

	return policies, result
}

// ResolvePoliciesWithResolver resolves all policy sources, including ref-based
// policies, using the provided BundleResolver. It recurses into referenced bundles'
// own policies with cycle detection. If resolver is nil, ref-based policies produce
// a hard POLICY_REF_UNRESOLVED error (fail closed).
func ResolvePoliciesWithResolver(ctx context.Context, c *contract.Contract, bundleFS fs.FS, resolver BundleResolver) ([]ResolvedPolicy, ValidationResult) {
	visited := map[string]bool{}
	return resolvePoliciesRecursive(ctx, c, bundleFS, resolver, visited, nil)
}

// resolvePoliciesRecursive is the internal recursive implementation.
// path tracks the chain of refs for cycle detection.
func resolvePoliciesRecursive(ctx context.Context, c *contract.Contract, bundleFS fs.FS, resolver BundleResolver, visited map[string]bool, path []string) ([]ResolvedPolicy, ValidationResult) {
	var policies []ResolvedPolicy
	var result ValidationResult

	for i, pol := range c.Policies {
		origin := policyOrigin(pol, i)
		if len(path) > 0 {
			origin = fmt.Sprintf("%s → %s", path[len(path)-1], policyOrigin(pol, i))
		}

		if pol.Schema != "" {
			rp := resolveLocalPolicySchema(bundleFS, pol.Schema, origin, i)
			if rp != nil {
				policies = append(policies, *rp)
			}
			continue
		}

		if pol.Ref != "" {
			resolved, refResult := resolveRefPolicy(ctx, pol.Ref, origin, resolver, visited, path)
			policies = append(policies, resolved...)
			result.Merge(refResult)
		}
	}

	return policies, result
}

// resolveLocalPolicySchema reads and compiles a local schema file from the bundle FS.
func resolveLocalPolicySchema(bundleFS fs.FS, schemaPath, origin string, index int) *ResolvedPolicy {
	if bundleFS == nil {
		return nil
	}
	data, err := fs.ReadFile(bundleFS, schemaPath)
	if err != nil {
		return nil
	}
	if !json.Valid(data) {
		return nil
	}
	schema, err := compilePolicySchema(data, fmt.Sprintf("mem:///policy-%d.json", index))
	if err != nil {
		return nil
	}
	return &ResolvedPolicy{Origin: origin, Schema: schema}
}

// resolveRefPolicy fetches a referenced bundle and extracts its policy schema,
// then recurses into the referenced bundle's own policies.
func resolveRefPolicy(ctx context.Context, ref, origin string, resolver BundleResolver, visited map[string]bool, path []string) ([]ResolvedPolicy, ValidationResult) {
	var result ValidationResult

	if resolver == nil {
		result.AddError("", "POLICY_REF_UNRESOLVED",
			fmt.Sprintf("policy %s: ref %q cannot be resolved (no resolver configured)", origin, ref))
		return nil, result
	}

	// Cycle detection.
	if visited[ref] {
		result.AddError("", "POLICY_REF_CYCLE",
			fmt.Sprintf("policy %s: cycle detected resolving ref %q (chain: %v)", origin, ref, append(path, ref)))
		return nil, result
	}
	visited[ref] = true

	bundle, err := resolver.ResolveBundle(ctx, ref)
	if err != nil {
		result.AddError("", "POLICY_REF_UNRESOLVED",
			fmt.Sprintf("policy %s: failed to resolve ref %q: %v", origin, ref, err))
		return nil, result
	}
	if bundle == nil || bundle.FS == nil {
		result.AddError("", "POLICY_REF_UNRESOLVED",
			fmt.Sprintf("policy %s: ref %q resolved to empty bundle", origin, ref))
		return nil, result
	}

	// If the referenced bundle explicitly declares policies[], use those
	// (recursion only). Otherwise, fall back to the fixed-path policy/schema.json
	// for backward compatibility.
	if bundle.Contract != nil && len(bundle.Contract.Policies) > 0 {
		childPath := append(append([]string{}, path...), ref)
		return resolvePoliciesRecursive(ctx, bundle.Contract, bundle.FS, resolver, visited, childPath)
	}

	// Legacy fallback: read fixed-path policy schema.
	data, err := fs.ReadFile(bundle.FS, PolicySchemaPath)
	if err != nil {
		result.AddError("", "POLICY_REF_UNRESOLVED",
			fmt.Sprintf("policy %s: ref %q bundle does not contain %s", origin, ref, PolicySchemaPath))
		return nil, result
	}
	if !json.Valid(data) {
		result.AddError("", "POLICY_REF_UNRESOLVED",
			fmt.Sprintf("policy %s: ref %q %s is not valid JSON", origin, ref, PolicySchemaPath))
		return nil, result
	}
	schema, err := compilePolicySchema(data, fmt.Sprintf("mem:///ref-policy-%s.json", ref))
	if err != nil {
		result.AddError("", "POLICY_REF_UNRESOLVED",
			fmt.Sprintf("policy %s: ref %q %s failed to compile: %v", origin, ref, PolicySchemaPath, err))
		return nil, result
	}

	return []ResolvedPolicy{{Origin: origin, Schema: schema}}, result
}

// policyOrigin returns a human-readable origin string for a policy source.
// It uses the policy name if available, falling back to the index.
func policyOrigin(pol contract.PolicySource, index int) string {
	if pol.Name != "" {
		return fmt.Sprintf("policies[%q]", pol.Name)
	}
	return fmt.Sprintf("policies[%d]", index)
}

// compilePolicySchema compiles a JSON Schema from raw bytes for policy enforcement.
func compilePolicySchema(data []byte, url string) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	var schemaDoc any
	if err := json.Unmarshal(data, &schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to parse: %w", err)
	}
	if err := compiler.AddResource(url, schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to add resource: %w", err)
	}
	return compiler.Compile(url)
}
