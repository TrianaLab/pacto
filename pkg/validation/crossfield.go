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
	validateInterfacePorts(c, &result)
	validateInterfaceContracts(c, &result)
	validateHealthInterface(c, &result)
	validateMetricsInterface(c, &result)
	validateInterfaceFiles(c, bundleFS, &result)
	validateInterfaceFileContent(c, bundleFS, &result)
	validateConfigFiles(c, bundleFS, &result)
	validateConfigSchemaContent(c, bundleFS, &result)
	validateConfigRef(c, &result)
	validatePolicyFields(c, bundleFS, &result)
	validatePolicySchemaContent(c, bundleFS, &result)
	validateDependencyRefs(c, &result)
	validateImageRef(c, &result)
	validateChartRef(c, &result)
	validateConfigValues(c, bundleFS, &result)
	validateScaling(c, &result)
	validateJobScaling(c, &result)
	validateStatePersistenceInvariants(c, &result)
	validateReadiness(c, &result)

	return result
}

// validateReadiness enforces the readiness rules that JSON Schema cannot express:
// the assessment-level expires (and each revision date) must be a strict
// canonical YYYY-MM-DD date, revision version/author/description must not be
// blank, readiness check IDs must be unique, and evidence and (when present)
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
	for i, check := range r.Checks {
		base := fmt.Sprintf("readiness.checks[%d]", i)
		if prev, exists := seen[check.ID]; exists {
			result.AddError(
				base+".id",
				"DUPLICATE_READINESS_ID",
				fmt.Sprintf("readiness check id %q is already declared at readiness.checks[%d]", check.ID, prev),
			)
		}
		seen[check.ID] = i

		if strings.TrimSpace(check.Evidence) == "" {
			result.AddError(
				base+".evidence",
				"EMPTY_READINESS_EVIDENCE",
				fmt.Sprintf("readiness check %q has blank evidence", check.ID),
			)
		}

		if check.Description != "" && strings.TrimSpace(check.Description) == "" {
			result.AddError(
				base+".description",
				"EMPTY_READINESS_DESCRIPTION",
				fmt.Sprintf("readiness check %q has a blank description", check.ID),
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

func validateInterfacePorts(c *contract.Contract, result *ValidationResult) {
	for i, iface := range c.Interfaces {
		switch iface.Type {
		case contract.InterfaceTypeHTTP, contract.InterfaceTypeGRPC:
			if iface.Port == nil {
				result.AddError(
					fmt.Sprintf("interfaces[%d].port", i),
					"PORT_REQUIRED",
					fmt.Sprintf("port is required for %s interface %q", iface.Type, iface.Name),
				)
			}
		case contract.InterfaceTypeEvent:
			if iface.Port != nil {
				result.AddWarning(
					fmt.Sprintf("interfaces[%d].port", i),
					"PORT_IGNORED",
					fmt.Sprintf("port is not applicable for event interface %q", iface.Name),
				)
			}
		}
	}
}

func validateInterfaceContracts(c *contract.Contract, result *ValidationResult) {
	for i, iface := range c.Interfaces {
		switch iface.Type {
		case contract.InterfaceTypeGRPC, contract.InterfaceTypeEvent:
			if iface.Contract == "" {
				result.AddError(
					fmt.Sprintf("interfaces[%d].contract", i),
					"CONTRACT_REQUIRED",
					fmt.Sprintf("contract is required for %s interface %q", iface.Type, iface.Name),
				)
			}
		}
	}
}

func validateHealthInterface(c *contract.Contract, result *ValidationResult) {
	if c.Runtime == nil || c.Runtime.Health == nil {
		return
	}
	validateProbeInterface(c, result, "health", c.Runtime.Health.Interface, c.Runtime.Health.Path)
}

func validateMetricsInterface(c *contract.Contract, result *ValidationResult) {
	if c.Runtime == nil || c.Runtime.Metrics == nil {
		return
	}
	validateProbeInterface(c, result, "metrics", c.Runtime.Metrics.Interface, c.Runtime.Metrics.Path)
}

// validateProbeInterface validates a runtime probe (health or metrics) interface
// reference: it must name a declared http/grpc interface, http probes require a
// path, and grpc probes must not set one. kind drives the field path, error
// codes, and messages so health and metrics share one implementation.
func validateProbeInterface(c *contract.Contract, result *ValidationResult, kind, iface, path string) {
	field := "runtime." + kind
	code := strings.ToUpper(kind)

	var found *contract.Interface
	for i := range c.Interfaces {
		if c.Interfaces[i].Name == iface {
			found = &c.Interfaces[i]
			break
		}
	}

	if found == nil {
		result.AddError(field+".interface", code+"_INTERFACE_NOT_FOUND",
			fmt.Sprintf("%s interface %q does not match any declared interface", kind, iface))
		return
	}

	if found.Type == contract.InterfaceTypeEvent {
		result.AddError(field+".interface", code+"_INTERFACE_INVALID",
			fmt.Sprintf("%s interface %q is an event interface; %s checks require http or grpc", kind, iface, kind))
		return
	}

	if found.Type == contract.InterfaceTypeHTTP && path == "" {
		result.AddError(field+".path", code+"_PATH_REQUIRED",
			fmt.Sprintf("%s path is required when the %s interface type is http", kind, kind))
	}

	if found.Type == contract.InterfaceTypeGRPC && path != "" {
		result.AddWarning(field+".path", code+"_PATH_IGNORED",
			fmt.Sprintf("%s path is not used for grpc interfaces", kind))
	}
}

func validateInterfaceFiles(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if bundleFS == nil {
		return
	}
	for i, iface := range c.Interfaces {
		if iface.Contract == "" {
			continue
		}
		if _, err := fs.Stat(bundleFS, iface.Contract); err != nil {
			result.AddError(
				fmt.Sprintf("interfaces[%d].contract", i),
				"FILE_NOT_FOUND",
				fmt.Sprintf("interface contract file %q not found in bundle", iface.Contract),
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

func validateImageRef(c *contract.Contract, result *ValidationResult) {
	if c.Service.Image == nil {
		return
	}
	validateOCIRef(c.Service.Image.Ref, "service.image.ref", "INVALID_IMAGE_REF", result)
}

func validateScaling(c *contract.Contract, result *ValidationResult) {
	if c.Scaling == nil {
		return
	}
	if c.Scaling.Min > c.Scaling.Max {
		result.AddError(
			"scaling",
			"SCALING_MIN_EXCEEDS_MAX",
			fmt.Sprintf("scaling min (%d) must not exceed max (%d)", c.Scaling.Min, c.Scaling.Max),
		)
	}
}

func validateJobScaling(c *contract.Contract, result *ValidationResult) {
	if c.Runtime != nil && c.Runtime.Workload == contract.WorkloadTypeJob && c.Scaling != nil {
		result.AddError(
			"scaling",
			"JOB_SCALING_NOT_ALLOWED",
			"scaling must not be applied to job workloads",
		)
	}
}

func validateChartRef(c *contract.Contract, result *ValidationResult) {
	if c.Service.Chart == nil {
		return
	}
	ref := c.Service.Chart.Ref
	parsed := graph.ParseDependencyRef(ref)
	if parsed.IsOCI() {
		validateOCIRef(parsed.Location, "service.chart.ref", "INVALID_CHART_REF", result)
	}
	// Version presence and minLength are enforced by JSON Schema (structural validation).
	// Here we validate semver format, which JSON Schema cannot express.
	validateSemver(c.Service.Chart.Version, "service.chart.version", "INVALID_CHART_VERSION", result)
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
func validateSingleConfigValues(cfg contract.ConfigurationSource, fieldPath string, bundleFS fs.FS, result *ValidationResult) {
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
	var valuesGeneric interface{}
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
	var schemaDoc interface{}
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
		if iface.Contract == "" {
			continue
		}
		data, err := fs.ReadFile(bundleFS, iface.Contract)
		if err != nil {
			// File-not-found is already caught by validateInterfaceFiles.
			continue
		}
		if !isYAMLFile(iface.Contract) {
			continue
		}
		var parsed interface{}
		if err := yaml.Unmarshal(data, &parsed); err != nil {
			result.AddError(
				fmt.Sprintf("interfaces[%d].contract", i),
				"INVALID_CONTRACT_FILE",
				fmt.Sprintf("interface contract file %q is not valid YAML: %v", iface.Contract, err),
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
	if c.Runtime == nil {
		return
	}
	// Invariant: stateless services must use ephemeral durability.
	if c.Runtime.State.Type == contract.StateStateless &&
		c.Runtime.State.Persistence.Durability == contract.DurabilityPersistent {
		result.AddError(
			"runtime.state.persistence.durability",
			"STATELESS_PERSISTENT_CONFLICT",
			"stateless services must use ephemeral durability; persistent durability requires stateful or hybrid",
		)
	}
}
