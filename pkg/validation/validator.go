// Package validation checks a contract across four layers — structural (JSON
// Schema), cross-field consistency, semantic rules, and policy enforcement — and
// reports errors and warnings. It supports local-only policy resolution as well
// as recursive, ref-based resolution through a pluggable BundleResolver.
package validation

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"gopkg.in/yaml.v3"
)

// Validate runs all four validation layers in order on the given contract.
// If structural validation fails, subsequent layers are skipped.
// The rawYAML parameter is the original YAML bytes for JSON Schema validation.
// The bundleFS parameter provides access to bundle files for cross-field validation.
func Validate(c *contract.Contract, rawYAML []byte, bundleFS fs.FS) ValidationResult {
	return runLayers(c, rawYAML, bundleFS, func() ([]ResolvedPolicy, ValidationResult) {
		return ResolvePoliciesFromBundle(c, bundleFS)
	})
}

// ValidateWithResolver runs all four validation layers, using the provided
// BundleResolver for recursive ref-based policy resolution. If resolver is nil,
// any ref-based policies produce a hard POLICY_REF_UNRESOLVED error (fail closed).
func ValidateWithResolver(ctx context.Context, c *contract.Contract, rawYAML []byte, bundleFS fs.FS, resolver BundleResolver) ValidationResult {
	return runLayers(c, rawYAML, bundleFS, func() ([]ResolvedPolicy, ValidationResult) {
		return ResolvePoliciesWithResolver(ctx, c, bundleFS, resolver)
	})
}

// runLayers runs the four-layer validation pipeline, short-circuiting on
// structural and cross-field failures. Layer 4 policy resolution is delegated to
// resolvePolicies, which is the only step that differs between the local-only
// Validate and the resolver-based ValidateWithResolver.
func runLayers(c *contract.Contract, rawYAML []byte, bundleFS fs.FS, resolvePolicies func() ([]ResolvedPolicy, ValidationResult)) ValidationResult {
	// Layer 1: Structural validation via JSON Schema.
	result := ValidateStructuralRaw(rawYAML)
	if !result.IsValid() {
		return result
	}

	// Layer 2: Cross-field validation.
	result.Merge(ValidateCrossField(c, bundleFS))
	if !result.IsValid() {
		return result
	}

	// Layer 3: Semantic validation.
	result.Merge(ValidateSemantic(c))

	// Layer 4: Policy resolution + enforcement.
	policies, policyResult := resolvePolicies()
	result.Merge(policyResult)
	if result.IsValid() {
		result.Merge(EnforcePolicies(rawYAML, policies))
	}

	return result
}

// ValidateStructuralRaw performs Layer 1 (JSON Schema) validation on raw YAML bytes.
// It converts the YAML to a generic interface{} and validates against the schema.
func ValidateStructuralRaw(rawYAML []byte) ValidationResult {
	data, err := yamlToGeneric(rawYAML)
	if err != nil {
		var result ValidationResult
		result.AddError("", "YAML_PARSE_ERROR", err.Error())
		return result
	}
	return ValidateStructural(data)
}

// Function variable for testing.
var jsonUnmarshalFn = json.Unmarshal

// yamlToGeneric converts YAML bytes to a generic interface{} suitable for
// JSON Schema validation. It goes through JSON to ensure type compatibility
// with the JSON Schema library.
func yamlToGeneric(data []byte) (interface{}, error) {
	var yamlObj interface{}
	if err := yaml.Unmarshal(data, &yamlObj); err != nil {
		return nil, err
	}

	// Convert map[string]interface{} (yaml uses map[interface{}]interface{} for nested)
	converted := convertYAMLToJSON(yamlObj)

	// Round-trip through JSON to ensure types match JSON Schema expectations
	jsonBytes, err := json.Marshal(converted)
	if err != nil {
		return nil, err
	}

	var result interface{}
	if err := jsonUnmarshalFn(jsonBytes, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// convertYAMLToJSON recursively converts YAML-style maps to JSON-compatible maps.
func convertYAMLToJSON(v interface{}) interface{} {
	switch v := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		for key, val := range v {
			result[key] = convertYAMLToJSON(val)
		}
		return result
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(v))
		for key, val := range v {
			strKey, ok := key.(string)
			if !ok {
				strKey = fmt.Sprintf("%v", key)
			}
			result[strKey] = convertYAMLToJSON(val)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, val := range v {
			result[i] = convertYAMLToJSON(val)
		}
		return result
	default:
		return v
	}
}
