package validation

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/graph"
	"gopkg.in/yaml.v3"
)

// ValidateCrossField performs Layer 2 validation: cross-field consistency,
// file existence, reference validation, and semantic rules that cannot be
// expressed in JSON Schema alone.
func ValidateCrossField(c *contract.Contract, bundleFS fs.FS) ValidationResult {
	var result ValidationResult

	validateServiceVersion(c, &result)
	validateInterfaceNamesUnique(c, &result)
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

	return result
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
	healthIface := c.Runtime.Health.Interface

	var found *contract.Interface
	for i := range c.Interfaces {
		if c.Interfaces[i].Name == healthIface {
			found = &c.Interfaces[i]
			break
		}
	}

	if found == nil {
		result.AddError(
			"runtime.health.interface",
			"HEALTH_INTERFACE_NOT_FOUND",
			fmt.Sprintf("health interface %q does not match any declared interface", healthIface),
		)
		return
	}

	if found.Type == contract.InterfaceTypeEvent {
		result.AddError(
			"runtime.health.interface",
			"HEALTH_INTERFACE_INVALID",
			fmt.Sprintf("health interface %q is an event interface; health checks require http or grpc", healthIface),
		)
		return
	}

	if found.Type == contract.InterfaceTypeHTTP && c.Runtime.Health.Path == "" {
		result.AddError(
			"runtime.health.path",
			"HEALTH_PATH_REQUIRED",
			"health check path is required when the health interface type is http",
		)
	}

	if found.Type == contract.InterfaceTypeGRPC && c.Runtime.Health.Path != "" {
		result.AddWarning(
			"runtime.health.path",
			"HEALTH_PATH_IGNORED",
			"health check path is not used for grpc interfaces; gRPC uses the standard health protocol",
		)
	}
}

func validateMetricsInterface(c *contract.Contract, result *ValidationResult) {
	if c.Runtime == nil || c.Runtime.Metrics == nil {
		return
	}
	metricsIface := c.Runtime.Metrics.Interface

	var found *contract.Interface
	for i := range c.Interfaces {
		if c.Interfaces[i].Name == metricsIface {
			found = &c.Interfaces[i]
			break
		}
	}

	if found == nil {
		result.AddError(
			"runtime.metrics.interface",
			"METRICS_INTERFACE_NOT_FOUND",
			fmt.Sprintf("metrics interface %q does not match any declared interface", metricsIface),
		)
		return
	}

	if found.Type == contract.InterfaceTypeEvent {
		result.AddError(
			"runtime.metrics.interface",
			"METRICS_INTERFACE_INVALID",
			fmt.Sprintf("metrics interface %q is an event interface; metrics require http or grpc", metricsIface),
		)
		return
	}

	if found.Type == contract.InterfaceTypeHTTP && c.Runtime.Metrics.Path == "" {
		result.AddError(
			"runtime.metrics.path",
			"METRICS_PATH_REQUIRED",
			"metrics path is required when the metrics interface type is http",
		)
	}

	if found.Type == contract.InterfaceTypeGRPC && c.Runtime.Metrics.Path != "" {
		result.AddWarning(
			"runtime.metrics.path",
			"METRICS_PATH_IGNORED",
			"metrics path is not used for grpc interfaces",
		)
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
	if c.Configuration == nil || c.Configuration.Schema == "" {
		return
	}
	if bundleFS == nil {
		return
	}
	if _, err := fs.Stat(bundleFS, c.Configuration.Schema); err != nil {
		result.AddError(
			"configuration.schema",
			"FILE_NOT_FOUND",
			fmt.Sprintf("configuration schema file %q not found in bundle", c.Configuration.Schema),
		)
	}
}

func validateConfigRef(c *contract.Contract, result *ValidationResult) {
	if c.Configuration == nil || c.Configuration.Ref == "" {
		return
	}
	parsed := graph.ParseDependencyRef(c.Configuration.Ref)
	if parsed.IsOCI() {
		validateOCIRef(parsed.Location, "configuration.ref", "INVALID_CONFIG_REF", result)
	}
}

func validatePolicyFields(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if c.Policy == nil {
		return
	}
	if c.Policy.Schema == "" && c.Policy.Ref == "" {
		result.AddError(
			"policy",
			"POLICY_EMPTY",
			"policy must specify at least one of schema or ref",
		)
		return
	}
	if c.Policy.Schema != "" && bundleFS != nil {
		if _, err := fs.Stat(bundleFS, c.Policy.Schema); err != nil {
			result.AddError(
				"policy.schema",
				"FILE_NOT_FOUND",
				fmt.Sprintf("policy schema file %q not found in bundle", c.Policy.Schema),
			)
		}
	}
	if c.Policy.Ref != "" {
		parsed := graph.ParseDependencyRef(c.Policy.Ref)
		if parsed.IsOCI() {
			validateOCIRef(parsed.Location, "policy.ref", "INVALID_POLICY_REF", result)
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
	if c.Configuration == nil || len(c.Configuration.Values) == 0 {
		return
	}
	if c.Configuration.Schema == "" && c.Configuration.Ref == "" {
		result.AddError(
			"configuration.values",
			"VALUES_WITHOUT_SCHEMA",
			"configuration values require a configuration schema to validate against",
		)
		return
	}
	if c.Configuration.Schema == "" {
		// Schema is external (ref) — values validation deferred to runtime resolution.
		return
	}
	if bundleFS == nil {
		return
	}
	schemaData, err := fs.ReadFile(bundleFS, c.Configuration.Schema)
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
	valuesJSON, _ := json.Marshal(c.Configuration.Values)
	var valuesGeneric interface{}
	json.Unmarshal(valuesJSON, &valuesGeneric) //nolint:errcheck // round-trip of valid data

	if err := schema.Validate(valuesGeneric); err != nil {
		result.AddError(
			"configuration.values",
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
	if c.Configuration == nil || c.Configuration.Schema == "" {
		return
	}
	validateJSONSchemaFile(bundleFS, c.Configuration.Schema,
		"configuration.schema", "INVALID_CONFIG_JSON", "INVALID_CONFIG_SCHEMA", result)
}

func validatePolicySchemaContent(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if c.Policy == nil || c.Policy.Schema == "" {
		return
	}
	validateJSONSchemaFile(bundleFS, c.Policy.Schema,
		"policy.schema", "INVALID_POLICY_JSON", "INVALID_POLICY_SCHEMA", result)
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
