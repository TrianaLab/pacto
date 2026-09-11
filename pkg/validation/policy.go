package validation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/trianalab/pacto/v3/pkg/contract"
)

// ResolvedPolicy holds a compiled policy schema and its origin for error reporting.
type ResolvedPolicy struct {
	Origin string             // human-readable origin (e.g., "policies[0]", "oci://ghcr.io/acme/policy:1.0.0")
	Schema *jsonschema.Schema // compiled JSON Schema
}

// BundleResolver resolves a ref string (OCI or local) into a contract Bundle.
// This abstracts away whether the ref is oci:// or file://.
//
// It cannot express WHO declared the ref, so a resolver implementing only this
// reads every local ref from the process working directory however deep the
// recursion went. Implement [OriginBundleResolver] as well to resolve refs the
// way the lock builder, the catalog and the dependency graph already do.
type BundleResolver interface {
	ResolveBundle(ctx context.Context, ref string) (*contract.Bundle, error)
}

// OriginBundleResolver is a [BundleResolver] that is told which contract
// declared the ref it is resolving.
//
// Two things depend on knowing that, and neither can be recovered from the ref
// text alone: a relative local ref means "next to the contract that wrote it",
// not "next to wherever Pacto was invoked"; and a contract that arrived from a
// registry must not be able to name a local directory AT ALL, or the remote
// party chooses which of your files a policy is read from.
type OriginBundleResolver interface {
	BundleResolver
	// RootBase is where the ROOT contract's own refs resolve from -- its
	// directory, or [graph.OCIBase] when it came from a registry.
	RootBase() string
	// ResolveBundleFrom resolves ref as declared by a contract whose base is
	// base, and reports the base the RESOLVED bundle's own refs resolve
	// against: its directory for a local bundle, [graph.OCIBase] for one
	// pulled from a registry.
	ResolveBundleFrom(ctx context.Context, base, ref string) (*contract.Bundle, string, error)
}

// resolveFrom is the port policy resolution actually calls: the widened one,
// with a shim standing in for a resolver that only implements [BundleResolver].
type resolveFrom func(ctx context.Context, base, ref string) (*contract.Bundle, string, error)

// resolvePort adapts whichever port the caller's resolver implements to the one
// this package uses, and reports the root's base.
//
// A plain [BundleResolver] gets a shim that discards the declarer and reports no
// base for what it returns, which is exactly the pre-origin behaviour: every ref
// resolves the way that resolver already resolved it. A nil resolver stays nil,
// so [resolveRefPolicy] still fails a ref-based policy closed.
func resolvePort(resolver BundleResolver) (resolveFrom, string) {
	if resolver == nil {
		return nil, ""
	}
	if or, ok := resolver.(OriginBundleResolver); ok {
		return or.ResolveBundleFrom, or.RootBase()
	}
	return func(ctx context.Context, _, ref string) (*contract.Bundle, string, error) {
		b, err := resolver.ResolveBundle(ctx, ref)
		return b, "", err
	}, ""
}

// declaredRef is one link in the chain from the root contract to the bundle
// being resolved: the ref text a contract declared, paired with the base that
// contract resolved its own refs from.
//
// The pair is the identity, not the text. "./platform-policy" declared in two
// directories names two different bundles, so keying the chain on text alone
// reports a cycle where there is none -- the same reason pkg/catalog keys its
// memo on {Base, Ref, Constraint} and pkg/graph on the matching depKey.
type declaredRef struct{ Base, Ref string }

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

	// No bundle at all is a delivery mode, not a failure: the operator validates
	// spec.contract.inline with a nil FS (integrations/kubernetes/internal/loader/
	// contract.go loadInline). Local resolution has nothing to read and no resolver
	// to defer to, so it resolves nothing and says nothing — neither the fail-closed
	// error below nor the POLICY_REF_NOT_ENFORCED warning, which would flip an inline
	// contract's status on upgrade. This is the same silence layer 2 keeps for a nil
	// FS (validateInterfaceFiles, validateConfigFiles, validateJSONSchemaFile).
	if bundleFS == nil {
		return nil, result
	}

	for i, pol := range c.Policies {
		origin := policyOrigin(pol, i)

		// Ref-based policies require an external resolver. Surface this explicitly
		// so local validation does not silently report success while leaving a
		// referenced policy unenforced.
		if pol.Schema == "" && pol.Ref != "" {
			result.AddWarning(
				fmt.Sprintf("policies[%d].ref", i),
				"POLICY_REF_NOT_ENFORCED",
				fmt.Sprintf("ref-based policy %q is not enforced by local validation; run validate with a resolver to enforce it", pol.Ref),
			)
			continue
		}
		// Everything else must produce a local schema, the entry declaring neither
		// field included.
		policies = resolveLocalPolicy(policies, &result, bundleFS, pol.Schema, origin, i)
	}

	return policies, result
}

// ResolvePoliciesWithResolver resolves all policy sources, including ref-based
// policies, using the provided BundleResolver. It recurses into referenced bundles'
// own policies with cycle detection. If resolver is nil, ref-based policies produce
// a hard POLICY_REF_UNRESOLVED error (fail closed).
func ResolvePoliciesWithResolver(ctx context.Context, c *contract.Contract, bundleFS fs.FS, resolver BundleResolver) ([]ResolvedPolicy, ValidationResult) {
	resolve, rootBase := resolvePort(resolver)
	return resolvePoliciesRecursive(ctx, c, bundleFS, resolve, rootBase, nil)
}

// resolvePoliciesRecursive is the internal recursive implementation.
//
// base is where THIS contract resolves its own relative refs from, so it changes
// at every hop; path tracks the chain of {base, ref} links from the root to here
// and is used for cycle detection. A cycle exists only when a link reappears
// within its own chain (A→B→A). Links reached via distinct chains (diamonds,
// shared siblings) resolve independently and are not cycles.
func resolvePoliciesRecursive(ctx context.Context, c *contract.Contract, bundleFS fs.FS, resolve resolveFrom, base string, path []declaredRef) ([]ResolvedPolicy, ValidationResult) {
	var policies []ResolvedPolicy
	var result ValidationResult

	for i, pol := range c.Policies {
		origin := policyOrigin(pol, i)
		if len(path) > 0 {
			origin = fmt.Sprintf("%s → %s", path[len(path)-1].Ref, policyOrigin(pol, i))
		}

		if pol.Schema == "" && pol.Ref != "" {
			resolved, refResult := resolveRefPolicy(ctx, pol.Ref, base, origin, resolve, path)
			policies = append(policies, resolved...)
			result.Merge(refResult)
			continue
		}
		policies = resolveLocalPolicy(policies, &result, bundleFS, pol.Schema, origin, i)
	}

	return policies, result
}

// resolveLocalPolicy compiles one policy entry's local schema and appends it, or
// records why that entry enforces nothing. Given a bundle, both callers fail closed
// here, because a policy that resolves to no schema enforces nothing and reporting
// success for it is the whole defect.
//
// Layer 2 hard-errors on a root schema it can stat and read (FILE_NOT_FOUND,
// INVALID_POLICY_JSON, INVALID_POLICY_SCHEMA) and layer 3 never runs after a
// layer-2 failure, so what reaches here from a root bundle is a file that stats
// but will not read. A REFERENCED bundle gets no layer 2 and no layer 1, so
// everything reaches here — including an empty schemaPath, the entry declaring
// neither schema nor ref that the structural oneOf would have rejected had
// anything validated that bundle.
func resolveLocalPolicy(policies []ResolvedPolicy, result *ValidationResult, bundleFS fs.FS, schemaPath, origin string, index int) []ResolvedPolicy {
	// Same nil-FS reasoning as ResolvePoliciesFromBundle, for the resolver-based
	// path: an inline contract has no files, so its local-schema entries resolve to
	// nothing rather than to a hard error. Its ref entries still resolve, through the
	// resolver. Referenced bundles never reach here with a nil FS — resolveRefPolicy
	// rejects bundle.FS == nil before recursing. Fail-closed below applies once there
	// IS a filesystem that cannot produce a schema.
	if bundleFS == nil {
		return policies
	}
	rp, err := resolveLocalPolicySchema(bundleFS, schemaPath, origin, index)
	if err != nil {
		result.AddError("", "POLICY_REF_UNRESOLVED", fmt.Sprintf("policy %s: %v", origin, err))
		return policies
	}
	return append(policies, *rp)
}

// resolveLocalPolicySchema reads and compiles a local schema file from the bundle FS.
// Every way of failing to produce an enforceable schema is an error, the empty path
// included: an entry naming no schema is an entry enforcing nothing.
func resolveLocalPolicySchema(bundleFS fs.FS, schemaPath, origin string, index int) (*ResolvedPolicy, error) {
	if schemaPath == "" {
		return nil, errors.New("declares neither a schema nor a ref, so it enforces nothing")
	}
	data, err := fs.ReadFile(bundleFS, schemaPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read schema %q: %w", schemaPath, err)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("schema %q is not valid JSON", schemaPath)
	}
	schema, err := compilePolicySchema(data, fmt.Sprintf("mem:///policy-%d.json", index))
	if err != nil {
		return nil, fmt.Errorf("schema %q failed to compile: %w", schemaPath, err)
	}
	return &ResolvedPolicy{Origin: origin, Schema: schema}, nil
}

// resolveRefPolicy fetches a referenced bundle and extracts its policy schema,
// then recurses into the referenced bundle's own policies. base is where the
// contract that DECLARED ref resolves its refs from.
func resolveRefPolicy(ctx context.Context, ref, base, origin string, resolve resolveFrom, path []declaredRef) ([]ResolvedPolicy, ValidationResult) {
	var result ValidationResult

	if resolve == nil {
		result.AddError("", "POLICY_REF_UNRESOLVED",
			fmt.Sprintf("policy %s: ref %q cannot be resolved (no resolver configured)", origin, ref))
		return nil, result
	}

	// Cycle detection: a cycle exists only when this link already appears in the
	// chain from the root to here (A→B→A). Links reached via distinct chains
	// (shared siblings, diamonds) are not cycles and resolve independently.
	link := declaredRef{Base: base, Ref: ref}
	if slices.Contains(path, link) {
		result.AddError("", "POLICY_REF_CYCLE",
			fmt.Sprintf("policy %s: cycle detected resolving ref %q (chain: %v)", origin, ref, refChain(path, ref)))
		return nil, result
	}

	bundle, childBase, err := resolve(ctx, base, ref)
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
		childPath := append(append([]declaredRef{}, path...), link)
		return resolvePoliciesRecursive(ctx, bundle.Contract, bundle.FS, resolve, childBase, childPath)
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

// refChain renders the chain that led to ref as the ref texts a human wrote.
// The bases each link resolved against are what makes the chain an identity, but
// they are absolute machine paths and the reader already knows where they came
// from -- the message names the loop, not the filesystem.
func refChain(path []declaredRef, ref string) []string {
	out := make([]string, 0, len(path)+1)
	for _, link := range path {
		out = append(out, link.Ref)
	}
	return append(out, ref)
}

// policyOrigin returns a human-readable origin string for a policy source.
// It uses the policy name if available, falling back to the index.
func policyOrigin(pol contract.Policy, index int) string {
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
	compiler.AddResource(url, schemaDoc) //nolint:errcheck // AddResource does not fail for valid JSON
	return compiler.Compile(url)
}
