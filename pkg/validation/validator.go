// Package validation checks a contract across three layers — structural (JSON
// Schema), cross-field consistency, and policy enforcement — and reports errors
// and warnings. It supports local-only policy resolution as well as recursive,
// ref-based resolution through a pluggable BundleResolver.
package validation

import (
	"context"
	"io/fs"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Validate runs all three validation layers in order on the given contract.
// If structural validation fails, subsequent layers are skipped.
// The rawYAML parameter is the original YAML bytes for JSON Schema validation.
// The bundleFS parameter provides access to bundle files for cross-field validation.
func Validate(c *contract.Contract, rawYAML []byte, bundleFS fs.FS) ValidationResult {
	return ValidateDocument(c, contract.DecodeDocument(rawYAML), bundleFS)
}

// ValidateDocument is [Validate] over a document a loader already decoded
// ([contract.ParseDocument]), so the YAML is not read twice.
func ValidateDocument(c *contract.Contract, doc *contract.Document, bundleFS fs.FS) ValidationResult {
	return runLayers(c, doc, bundleFS, func() ([]ResolvedPolicy, ValidationResult) {
		return ResolvePoliciesFromBundle(c, bundleFS)
	})
}

// ValidateWithResolver runs all three validation layers, using the provided
// BundleResolver for recursive ref-based policy resolution. If resolver is nil,
// any ref-based policies produce a hard POLICY_REF_UNRESOLVED error (fail closed).
func ValidateWithResolver(ctx context.Context, c *contract.Contract, rawYAML []byte, bundleFS fs.FS, resolver BundleResolver) ValidationResult {
	return runLayers(c, contract.DecodeDocument(rawYAML), bundleFS, func() ([]ResolvedPolicy, ValidationResult) {
		return ResolvePoliciesWithResolver(ctx, c, bundleFS, resolver)
	})
}

// runLayers runs the three-layer validation pipeline, short-circuiting on
// structural and cross-field failures. Layer 3 policy resolution is delegated to
// resolvePolicies, which is the only step that differs between the local-only
// Validate and the resolver-based ValidateWithResolver.
func runLayers(c *contract.Contract, doc *contract.Document, bundleFS fs.FS, resolvePolicies func() ([]ResolvedPolicy, ValidationResult)) ValidationResult {
	// Layer 1: Structural validation via JSON Schema.
	result := validateStructuralDoc(doc)
	if !result.IsValid() {
		return result
	}

	// Layer 2: Cross-field validation.
	result.Merge(ValidateCrossField(c, bundleFS))
	if !result.IsValid() {
		return result
	}

	// Layer 3: Policy resolution + enforcement.
	policies, policyResult := resolvePolicies()
	result.Merge(policyResult)
	if result.IsValid() {
		result.Merge(enforcePolicies(doc, policies))
	}

	return result
}

// ValidateStructuralRaw performs Layer 1 (JSON Schema) validation on raw YAML bytes.
func ValidateStructuralRaw(rawYAML []byte) ValidationResult {
	return validateStructuralDoc(contract.DecodeDocument(rawYAML))
}

func validateStructuralDoc(doc *contract.Document) ValidationResult {
	data, err := doc.JSON()
	if err != nil {
		var result ValidationResult
		result.AddError("", "YAML_PARSE_ERROR", err.Error())
		return result
	}
	return ValidateStructural(data)
}
