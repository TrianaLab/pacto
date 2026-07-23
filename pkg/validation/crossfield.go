package validation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/graph"
	"gopkg.in/yaml.v3"
)

// readinessDateLayout is the strict YYYY-MM-DD layout for readiness expiry dates.
const readinessDateLayout = "2006-01-02"

// ValidateCrossField performs Layer 2 validation: cross-field consistency,
// file existence, reference validation, and semantic rules that cannot be
// expressed in JSON Schema alone.
func ValidateCrossField(c *contract.Contract, bundleFS fs.FS) ValidationResult {
	var result ValidationResult

	validateServiceVersion(c, &result)
	validateInterfaceNamesUnique(c, &result)
	validateConfigurationNamesUnique(c, &result)
	validatePolicyNamesUnique(c, &result)
	validateDependencyNamesUnique(c, &result)
	validateInterfaces(c, bundleFS, &result)
	validateCapabilities(c, &result)
	validateInterfaceFiles(c, bundleFS, &result)
	validateInterfaceFileContent(c, bundleFS, &result)
	validateConfigFiles(c, bundleFS, &result)
	validateConfigSchemaContent(c, bundleFS, &result)
	validateConfigRef(c, &result)
	validatePolicyFields(c, bundleFS, &result)
	validatePolicySchemaContent(c, bundleFS, &result)
	validatePolicyTarget(c, &result)
	validateDependencyRefs(c, &result)
	validateConfigValues(c, bundleFS, &result)
	validateStatePersistenceInvariants(c, &result)
	validateReadiness(c, &result)

	return result
}

// validateReadiness enforces the readiness rules that JSON Schema cannot express:
// the assessment-level expires (and each revision date) must be a strict
// canonical YYYY-MM-DD date, revision version/author/description must not be
// blank, readiness claim IDs must be unique, and evidence and (when present)
// description must not be whitespace-only. Shape rules (id pattern, type/status
// enum, weight range, evidence length) are enforced by the structural schema and
// are deliberately not duplicated here.
func validateReadiness(c *contract.Contract, result *ValidationResult) {
	if c.Readiness == nil {
		return
	}
	r := c.Readiness

	if !isCanonicalDate(r.Expires) {
		result.AddError(
			"readiness.expires",
			"INVALID_READINESS_EXPIRES",
			fmt.Sprintf("expires %q must be a valid YYYY-MM-DD date", r.Expires),
		)
	}

	for i, rev := range r.History {
		base := fmt.Sprintf("readiness.history[%d]", i)
		if !isCanonicalDate(rev.Date) {
			result.AddError(base+".date", "INVALID_READINESS_REVISION", "revision date must be YYYY-MM-DD")
		}
		if strings.TrimSpace(rev.Version) == "" {
			result.AddError(base+".version", "INVALID_READINESS_REVISION", "revision version must not be blank")
		}
		if strings.TrimSpace(rev.Author) == "" {
			result.AddError(base+".author", "INVALID_READINESS_REVISION", "revision author must not be blank")
		}
		if strings.TrimSpace(rev.Description) == "" {
			result.AddError(base+".description", "INVALID_READINESS_REVISION", "revision description must not be blank")
		}
	}

	seen := make(map[string]int)
	for i, claim := range r.Claims {
		base := fmt.Sprintf("readiness.claims[%d]", i)
		if prev, exists := seen[claim.ID]; exists {
			result.AddError(
				base+".id",
				"DUPLICATE_READINESS_ID",
				fmt.Sprintf("readiness claim id %q is already declared at readiness.claims[%d]", claim.ID, prev),
			)
		}
		seen[claim.ID] = i

		if strings.TrimSpace(claim.Evidence) == "" {
			result.AddError(
				base+".evidence",
				"EMPTY_READINESS_EVIDENCE",
				fmt.Sprintf("readiness claim %q has blank evidence", claim.ID),
			)
		}

		if claim.Description != "" && strings.TrimSpace(claim.Description) == "" {
			result.AddError(
				base+".description",
				"EMPTY_READINESS_DESCRIPTION",
				fmt.Sprintf("readiness claim %q has a blank description", claim.ID),
			)
		}
	}
}

// isCanonicalDate reports whether s is a valid date in the strict canonical
// YYYY-MM-DD layout (rejecting non-canonical inputs like "2026-1-1").
func isCanonicalDate(s string) bool {
	t, err := time.Parse(readinessDateLayout, s)
	return err == nil && t.Format(readinessDateLayout) == s
}

func validateSemver(version, field, code string, result *ValidationResult) {
	if _, err := semver.NewVersion(version); err != nil {
		result.AddError(field, code, fmt.Sprintf("%q is not valid semver: %v", version, err))
	}
}

func validateOCIRef(ref, field, code string, result *ValidationResult) {
	if _, err := contract.ParseOCIReference(ref); err != nil {
		result.AddError(field, code, fmt.Sprintf("invalid OCI reference %q: %v", ref, err))
	}
}

func validateServiceVersion(c *contract.Contract, result *ValidationResult) {
	validateSemver(c.Service.Version, "service.version", "INVALID_SEMVER", result)
}

func validateInterfaceNamesUnique(c *contract.Contract, result *ValidationResult) {
	seen := make(map[string]int)
	for i, iface := range c.Interfaces {
		if prev, exists := seen[iface.Name]; exists {
			result.AddError(
				fmt.Sprintf("interfaces[%d].name", i),
				"DUPLICATE_INTERFACE_NAME",
				fmt.Sprintf("interface name %q is already declared at interfaces[%d]", iface.Name, prev),
			)
		}
		seen[iface.Name] = i
	}
}

func validateConfigurationNamesUnique(c *contract.Contract, result *ValidationResult) {
	seen := make(map[string]int)
	for i, cfg := range c.Configurations {
		if prev, exists := seen[cfg.Name]; exists {
			result.AddError(
				fmt.Sprintf("configurations[%d].name", i),
				"DUPLICATE_CONFIGURATION_NAME",
				fmt.Sprintf("configuration name %q is already declared at configurations[%d]", cfg.Name, prev),
			)
		}
		seen[cfg.Name] = i
	}
}

func validatePolicyNamesUnique(c *contract.Contract, result *ValidationResult) {
	seen := make(map[string]int)
	for i, pol := range c.Policies {
		if prev, exists := seen[pol.Name]; exists {
			result.AddError(
				fmt.Sprintf("policies[%d].name", i),
				"DUPLICATE_POLICY_NAME",
				fmt.Sprintf("policy name %q is already declared at policies[%d]", pol.Name, prev),
			)
		}
		seen[pol.Name] = i
	}
}

func validateDependencyNamesUnique(c *contract.Contract, result *ValidationResult) {
	seen := make(map[string]int)
	for i, dep := range c.Dependencies {
		if prev, exists := seen[dep.Name]; exists {
			result.AddError(
				fmt.Sprintf("dependencies[%d].name", i),
				"DUPLICATE_DEPENDENCY_NAME",
				fmt.Sprintf("dependency name %q is already declared at dependencies[%d]", dep.Name, prev),
			)
		}
		seen[dep.Name] = i
	}
}

// validateInterfaces validates v2 interfaces: type in enum, ref required, ref file exists and parses.
func validateInterfaces(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	validTypes := map[string]bool{
		"openapi":  true,
		"asyncapi": true,
		"grpc":     true,
	}
	for i, iface := range c.Interfaces {
		if !validTypes[iface.Type] {
			result.AddError(
				fmt.Sprintf("interfaces[%d].type", i),
				"INVALID_INTERFACE_TYPE",
				fmt.Sprintf("interface type %q is invalid; must be openapi, asyncapi, or grpc", iface.Type),
			)
		}
		if iface.Ref == "" {
			result.AddError(
				fmt.Sprintf("interfaces[%d].ref", i),
				"INTERFACE_REF_REQUIRED",
				fmt.Sprintf("interface %q requires a ref to the spec file", iface.Name),
			)
		}
	}
}

// validateCapabilities validates v2 capabilities: type in enum, extension requires namespaced ref, standard types must not have ref, no duplicate standard types.
func validateCapabilities(c *contract.Contract, result *ValidationResult) {
	validTypes := map[string]bool{
		"health":    true,
		"metrics":   true,
		"extension": true,
	}
	seen := make(map[string]int)
	for i, cap := range c.Capabilities {
		if !validTypes[cap.Type] {
			result.AddError(
				fmt.Sprintf("capabilities[%d].type", i),
				"INVALID_CAPABILITY_TYPE",
				fmt.Sprintf("capability type %q is invalid; must be health, metrics, or extension", cap.Type),
			)
		}
		if cap.Type == "extension" {
			if cap.Ref == "" {
				result.AddError(
					fmt.Sprintf("capabilities[%d].ref", i),
					"CAPABILITY_REF_REQUIRED",
					"extension capabilities require a namespaced ref",
				)
			} else if !strings.Contains(cap.Ref, "/") || !strings.Contains(strings.Split(cap.Ref, "/")[0], ".") {
				result.AddError(
					fmt.Sprintf("capabilities[%d].ref", i),
					"CAPABILITY_REF_INVALID",
					fmt.Sprintf("extension capability ref %q must be namespaced (e.g. example.com/custom)", cap.Ref),
				)
			}
		}
		if cap.Type == "health" || cap.Type == "metrics" {
			if prev, exists := seen[cap.Type]; exists {
				result.AddError(
					fmt.Sprintf("capabilities[%d].type", i),
					"DUPLICATE_CAPABILITY",
					fmt.Sprintf("duplicate standard capability %q (already declared at capabilities[%d])", cap.Type, prev),
				)
			}
			seen[cap.Type] = i
		}
	}
}

func validateInterfaceFiles(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if bundleFS == nil {
		return
	}
	for i, iface := range c.Interfaces {
		if iface.Ref == "" {
			continue
		}
		if _, err := fs.Stat(bundleFS, iface.Ref); err != nil {
			result.AddError(
				fmt.Sprintf("interfaces[%d].ref", i),
				"FILE_NOT_FOUND",
				fmt.Sprintf("interface spec file %q not found in bundle", iface.Ref),
			)
		}
	}
}

func validateConfigFiles(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if bundleFS == nil {
		return
	}

	for i, cfg := range c.Configurations {
		if cfg.Schema == "" {
			continue
		}
		fieldPath := fmt.Sprintf("configurations[%d].schema", i)
		if _, err := fs.Stat(bundleFS, cfg.Schema); err != nil {
			result.AddError(
				fieldPath,
				"FILE_NOT_FOUND",
				fmt.Sprintf("configuration schema file %q not found in bundle", cfg.Schema),
			)
		}
	}
}

func validateConfigRef(c *contract.Contract, result *ValidationResult) {
	for i, cfg := range c.Configurations {
		if cfg.Ref == "" {
			continue
		}
		fieldPath := fmt.Sprintf("configurations[%d].ref", i)
		parsed := graph.ParseDependencyRef(cfg.Ref)
		if parsed.IsOCI() {
			validateOCIRef(parsed.Location, fieldPath, "INVALID_CONFIG_REF", result)
		}
	}
}

func validatePolicyFields(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	for i, pol := range c.Policies {
		if pol.Schema != "" && bundleFS != nil {
			if _, err := fs.Stat(bundleFS, pol.Schema); err != nil {
				result.AddError(
					fmt.Sprintf("policies[%d].schema", i),
					"FILE_NOT_FOUND",
					fmt.Sprintf("policy schema file %q not found in bundle", pol.Schema),
				)
			}
		}
		if pol.Ref != "" {
			parsed := graph.ParseDependencyRef(pol.Ref)
			if parsed.IsOCI() {
				validateOCIRef(parsed.Location, fmt.Sprintf("policies[%d].ref", i), "INVALID_POLICY_REF", result)
			}
		}
	}
}

func validateDependencyRefs(c *contract.Contract, result *ValidationResult) {
	for i, dep := range c.Dependencies {
		parsed := graph.ParseDependencyRef(dep.Ref)

		if parsed.IsOCI() {
			field := fmt.Sprintf("dependencies[%d].ref", i)
			ref, err := contract.ParseOCIReference(parsed.Location)
			if err != nil {
				result.AddError(field, "INVALID_OCI_REF", fmt.Sprintf("invalid OCI reference %q: %v", dep.Ref, err))
				continue
			}

			if ref.Digest == "" && ref.Tag != "" {
				result.AddWarning(field, "TAG_NOT_DIGEST",
					fmt.Sprintf("dependency %q uses a tag instead of a digest; digest pinning is recommended", dep.Ref))
			}
		}

		if dep.Compatibility == "" {
			result.AddError(
				fmt.Sprintf("dependencies[%d].compatibility", i),
				"EMPTY_COMPATIBILITY",
				"compatibility range must not be empty",
			)
		} else if _, err := contract.ParseRange(dep.Compatibility); err != nil {
			result.AddError(
				fmt.Sprintf("dependencies[%d].compatibility", i),
				"INVALID_COMPATIBILITY",
				fmt.Sprintf("invalid compatibility range %q: %v", dep.Compatibility, err),
			)
		}
	}
}

// validatePolicyTarget validates that policy target is supported (contract only).
func validatePolicyTarget(c *contract.Contract, result *ValidationResult) {
	for i, pol := range c.Policies {
		if pol.Target != "" && pol.Target != "contract" {
			result.AddError(
				fmt.Sprintf("policies[%d].target", i),
				"UNSUPPORTED_POLICY_TARGET",
				fmt.Sprintf("policy target %q is not supported; only 'contract' is allowed", pol.Target),
			)
		}
	}
}

func validateConfigValues(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	for i, cfg := range c.Configurations {
		if len(cfg.Values) == 0 {
			continue
		}
		fieldPath := fmt.Sprintf("configurations[%d].values", i)
		validateSingleConfigValues(cfg, fieldPath, bundleFS, result)
	}
}

// validateSingleConfigValues validates a single config's values against its schema.
func validateSingleConfigValues(cfg contract.Configuration, fieldPath string, bundleFS fs.FS, result *ValidationResult) {
	if cfg.Schema == "" && cfg.Ref == "" {
		result.AddError(
			fieldPath,
			"VALUES_WITHOUT_SCHEMA",
			"configuration values require a configuration schema to validate against",
		)
		return
	}
	if cfg.Schema == "" {
		// Schema is external (ref) — values validation deferred to runtime resolution.
		return
	}
	if bundleFS == nil {
		return
	}
	schemaData, err := fs.ReadFile(bundleFS, cfg.Schema)
	if err != nil {
		// File-not-found is already caught by validateConfigFiles; skip here.
		return
	}

	schema, err := compileConfigSchema(schemaData)
	if err != nil {
		// Schema compilation errors are already caught by validateConfigSchemaContent.
		return
	}

	// Round-trip through JSON to normalize types (e.g. YAML int → JSON float64).
	valuesJSON, _ := json.Marshal(cfg.Values)
	var valuesGeneric any
	json.Unmarshal(valuesJSON, &valuesGeneric) //nolint:errcheck // round-trip of valid data

	if err := schema.Validate(valuesGeneric); err != nil {
		result.AddError(
			fieldPath,
			"CONFIG_VALUES_VALIDATION_FAILED",
			fmt.Sprintf("configuration values do not match schema: %v", err),
		)
	}
}

// compileConfigSchema parses and compiles a JSON Schema from raw bytes.
func compileConfigSchema(data []byte) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	var schemaDoc any
	if err := json.Unmarshal(data, &schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to parse: %w", err)
	}
	compiler.AddResource("mem:///config-schema.json", schemaDoc) //nolint:errcheck // AddResource does not fail for valid JSON
	return compiler.Compile("mem:///config-schema.json")
}

// isYAMLFile reports whether the file path has a YAML extension.
func isYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func validateInterfaceFileContent(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if bundleFS == nil {
		return
	}
	for i, iface := range c.Interfaces {
		if iface.Ref == "" {
			continue
		}
		data, err := fs.ReadFile(bundleFS, iface.Ref)
		if err != nil {
			// File-not-found is already caught by validateInterfaceFiles.
			continue
		}
		if !isYAMLFile(iface.Ref) {
			continue
		}
		var parsed any
		if err := yaml.Unmarshal(data, &parsed); err != nil {
			result.AddError(
				fmt.Sprintf("interfaces[%d].ref", i),
				"INVALID_INTERFACE_SPEC",
				fmt.Sprintf("interface spec file %q is not valid YAML: %v", iface.Ref, err),
			)
		}
	}
}

// validateJSONSchemaFile reads a JSON file from the bundle, validates it is
// valid JSON, and compiles it as a JSON Schema. It reports errors at the given
// field path using the given error codes.
func validateJSONSchemaFile(bundleFS fs.FS, path, field, invalidJSONCode, invalidSchemaCode string, result *ValidationResult) {
	if bundleFS == nil || path == "" {
		return
	}
	data, err := fs.ReadFile(bundleFS, path)
	if err != nil {
		// File-not-found is already caught by other validators.
		return
	}
	if !json.Valid(data) {
		result.AddError(field, invalidJSONCode,
			fmt.Sprintf("file %q is not valid JSON", path))
		return
	}
	if _, err := compileConfigSchema(data); err != nil {
		result.AddError(field, invalidSchemaCode,
			fmt.Sprintf("file %q is not a valid JSON Schema: %v", path, err))
	}
}

func validateConfigSchemaContent(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	for i, cfg := range c.Configurations {
		if cfg.Schema == "" {
			continue
		}
		fieldPath := fmt.Sprintf("configurations[%d].schema", i)
		validateJSONSchemaFile(bundleFS, cfg.Schema,
			fieldPath, "INVALID_CONFIG_JSON", "INVALID_CONFIG_SCHEMA", result)
	}
}

func validatePolicySchemaContent(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	for i, pol := range c.Policies {
		if pol.Schema == "" {
			continue
		}
		validateJSONSchemaFile(bundleFS, pol.Schema,
			fmt.Sprintf("policies[%d].schema", i), "INVALID_POLICY_JSON", "INVALID_POLICY_SCHEMA", result)
	}
}

func validateStatePersistenceInvariants(c *contract.Contract, result *ValidationResult) {
	if c.State == nil {
		return
	}
	// Invariant: stateless services must use ephemeral durability.
	if c.State.Type == contract.StateStateless &&
		c.State.Persistence.Durability == contract.DurabilityPersistent {
		result.AddError(
			"state.persistence.durability",
			"STATELESS_PERSISTENT_CONFLICT",
			"stateless services must use ephemeral durability; persistent durability requires stateful or hybrid",
		)
	}
}
