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
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/graph"
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
	validateInterfaceLifecycle(c, &result)
	validateConfigurationNamesUnique(c, &result)
	validatePolicyNamesUnique(c, &result)
	validateDependencyNamesUnique(c, &result)
	validateCapabilities(c, &result)
	validateInterfaceFiles(c, bundleFS, &result)
	validateInterfaceFileContent(c, bundleFS, &result)
	validateConfigFiles(c, bundleFS, &result)
	validateConfigSchemaContent(c, bundleFS, &result)
	validateConfigRef(c, &result)
	validatePolicyFields(c, bundleFS, &result)
	validatePolicySchemaContent(c, bundleFS, &result)
	validateDependencyRefs(c, &result)
	validateConfigValues(c, bundleFS, &result)
	validateEnvironmentFiles(c, bundleFS, &result)
	validateStatePersistenceInvariants(c, &result)
	validateReadiness(c, &result)
	validatePublish(c, &result)
	validateMCPInterfaces(c, bundleFS, &result)
	validateChecks(c, bundleFS, &result)

	return result
}

// validatePublish enforces what the schema cannot say about publish: artifact
// names are unique, deploy names a manifests artifact (a Kustomization applies
// manifests, never a chart or an image), the dockerfile builder and sbom
// apply to images only, and an sbom needs a command build, which writes it.
func validatePublish(c *contract.Contract, result *ValidationResult) {
	if c.Publish == nil {
		return
	}
	types := map[string]string{}
	for i, a := range c.Publish.Artifacts {
		path := fmt.Sprintf("publish.artifacts[%d]", i)
		if _, dup := types[a.Name]; dup {
			result.AddError(path+".name", "DUPLICATE_PUBLISH_ARTIFACT", fmt.Sprintf("artifact name %q is used more than once", a.Name))
		}
		types[a.Name] = a.Type
		switch {
		case a.SBOM != "" && a.Type != contract.ArtifactImage:
			result.AddError(path+".sbom", "PUBLISH_SBOM_NOT_IMAGE", fmt.Sprintf("%s is a %s; an SBOM is attached to images only", a.Name, a.Type))
		case a.SBOM != "" && a.Build.Dockerfile != nil:
			result.AddError(path+".sbom", "PUBLISH_SBOM_DOCKERFILE", fmt.Sprintf("%s is built from a Dockerfile, which writes no SBOM file; build it with a command that writes %s, or drop sbom", a.Name, a.SBOM))
		}
		if a.Build.Dockerfile != nil && a.Type != contract.ArtifactImage {
			result.AddError(path+".build.dockerfile", "PUBLISH_DOCKERFILE_NOT_IMAGE", fmt.Sprintf("%s is a %s; the dockerfile builder builds images only, use command", a.Name, a.Type))
		}
	}
	if d := c.Publish.Deploy; d != "" && types[d] != contract.ArtifactManifests {
		result.AddError("publish.deploy", "PUBLISH_DEPLOY_NOT_MANIFESTS", fmt.Sprintf("deploy %q must name a manifests artifact: Flux applies it through a Kustomization", d))
	}
}

// validateMCPInterfaces checks what the schema cannot: an mcp interface file
// is a tools/list result with named tools, so a diff can tell a removed tool
// from a changed one.
func validateMCPInterfaces(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if bundleFS == nil {
		return
	}
	for i, iface := range c.Interfaces {
		if iface.Type != contract.InterfaceTypeMCP {
			continue
		}
		data, err := fs.ReadFile(bundleFS, iface.Ref)
		if err != nil {
			continue // validateInterfaceFiles reports a missing file
		}
		if _, err := contract.ParseMCPTools(data); err != nil {
			result.AddError(fmt.Sprintf("interfaces[%d].ref", i), "INVALID_MCP_TOOLS", fmt.Sprintf("mcp interface file %q %v", iface.Ref, err))
		}
	}
}

// validateChecks enforces that check names are unique, so a delivery gate
// names one suite, and that each suite file is in the bundle, so it ships
// with the version.
func validateChecks(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	seen := map[string]int{}
	for i, ch := range c.Checks {
		if prev, dup := seen[ch.Name]; dup {
			result.AddError(fmt.Sprintf("checks[%d].name", i), "DUPLICATE_CHECK_NAME", fmt.Sprintf("check name %q is already declared at checks[%d]", ch.Name, prev))
		}
		seen[ch.Name] = i
		if bundleFS != nil && missingFile(bundleFS, ch.File) {
			result.AddError(fmt.Sprintf("checks[%d].file", i), "FILE_NOT_FOUND", fmt.Sprintf("check %q: file %q not found in bundle", ch.Name, ch.File))
		}
	}
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

// The declaration-uniqueness rule itself lives on the contract
// (contract.DuplicateDeclarations), because `pacto lock` enforces the same rule
// without running validation. Reporting it is this layer's half.
func validateConfigurationNamesUnique(c *contract.Contract, result *ValidationResult) {
	for _, d := range c.DuplicateDeclarations() {
		if d.Kind != contract.ReferenceKindConfig {
			continue
		}
		result.AddError(
			fmt.Sprintf("configurations[%d].name", d.Index),
			"DUPLICATE_CONFIGURATION_NAME",
			fmt.Sprintf("configuration name %q is already declared at configurations[%d]", d.Name, d.First),
		)
	}
}

func validatePolicyNamesUnique(c *contract.Contract, result *ValidationResult) {
	for _, d := range c.DuplicateDeclarations() {
		if d.Kind != contract.ReferenceKindPolicy {
			continue
		}
		result.AddError(
			fmt.Sprintf("policies[%d].name", d.Index),
			"DUPLICATE_POLICY_NAME",
			fmt.Sprintf("policy name %q is already declared at policies[%d]", d.Name, d.First),
		)
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

// validateCapabilities validates the capability rules JSON Schema cannot express:
// the binding must name a declared interface, the binding path must be SSRF-safe,
// an extension ref must be namespaced and assertion keys must be unique. Shape
// rules (type enum, ref presence and minLength, binding/ref exclusivity) belong to
// the structural schema and are deliberately not duplicated here — layer 1
// short-circuits layer 2, so a copy here could never fire.
func validateCapabilities(c *contract.Contract, result *ValidationResult) {
	declaredIfaces := make(map[string]bool, len(c.Interfaces))
	for _, iface := range c.Interfaces {
		declaredIfaces[iface.Name] = true
	}
	seen := make(map[string]int)
	for i, cap := range c.Capabilities {
		if cap.Binding != nil {
			if cap.Binding.Interface != "" && !declaredIfaces[cap.Binding.Interface] {
				result.AddError(
					fmt.Sprintf("capabilities[%d].binding.interface", i),
					"CAPABILITY_INTERFACE_UNKNOWN",
					fmt.Sprintf("capability binding references interface %q, which is not declared in interfaces[]", cap.Binding.Interface),
				)
			}
			if cap.Binding.Path != "" && !contract.ValidCapabilityPath(cap.Binding.Path) {
				result.AddError(
					fmt.Sprintf("capabilities[%d].binding.path", i),
					"CAPABILITY_PATH_INVALID",
					fmt.Sprintf("capability binding path %q must start with a single '/' and contain no scheme, host, userinfo or fragment", cap.Binding.Path),
				)
			}
		}
		if cap.Type == "extension" && (!strings.Contains(cap.Ref, "/") || !strings.Contains(strings.Split(cap.Ref, "/")[0], ".")) {
			result.AddError(
				fmt.Sprintf("capabilities[%d].ref", i),
				"CAPABILITY_REF_INVALID",
				fmt.Sprintf("extension capability ref %q must be namespaced (e.g. example.com/custom)", cap.Ref),
			)
		}
		// Uniqueness on the canonical assertion key (standard type OR extension ref) — one rule covering
		// duplicate standard types, duplicate extension refs and any cross-collision.
		key := cap.AssertionKey()
		if prev, exists := seen[key]; exists {
			result.AddError(
				fmt.Sprintf("capabilities[%d]", i),
				"DUPLICATE_CAPABILITY",
				fmt.Sprintf("duplicate capability assertion %q (already declared at capabilities[%d])", key, prev),
			)
		}
		seen[key] = i
	}
}

// missingFile reports whether a bundle-relative ref fails to resolve to a
// readable file. A directory satisfies fs.Stat but is not a spec or a schema,
// and nothing downstream can read one — pack would ship a bundle whose declared
// file does not exist.
func missingFile(bundleFS fs.FS, path string) bool {
	info, err := fs.Stat(bundleFS, path)
	return err != nil || info.IsDir()
}

func validateInterfaceFiles(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	if bundleFS == nil {
		return
	}
	for i, iface := range c.Interfaces {
		if iface.Ref == "" {
			continue
		}
		if missingFile(bundleFS, iface.Ref) {
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
		if missingFile(bundleFS, cfg.Schema) {
			result.AddError(
				fieldPath,
				"FILE_NOT_FOUND",
				fmt.Sprintf("configuration schema file %q not found in bundle", cfg.Schema),
			)
		}
	}
}

// validateEnvironmentFiles checks that environment names are unique and that
// each environment's values file exists in the bundle. What the file holds is
// the environments stage's to check.
func validateEnvironmentFiles(c *contract.Contract, bundleFS fs.FS, result *ValidationResult) {
	seen := make(map[string]int)
	for i, env := range c.Environments {
		if prev, exists := seen[env.Name]; exists {
			result.AddError(fmt.Sprintf("environments[%d].name", i), "DUPLICATE_ENVIRONMENT_NAME",
				fmt.Sprintf("environment name %q is already declared at environments[%d]", env.Name, prev))
		}
		seen[env.Name] = i
		if bundleFS != nil && missingFile(bundleFS, env.File) {
			result.AddError(fmt.Sprintf("environments[%d].file", i), "FILE_NOT_FOUND",
				fmt.Sprintf("environment values file %q not found in bundle", env.File))
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
			if missingFile(bundleFS, pol.Schema) {
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

// validateDependencyForm enforces the two shapes a dependency takes: a ref to
// its contract with a compatibility range, or, for a dependency this Pacto
// system does not deliver and nobody wrote a contract for, external: true with
// the free-text version it runs against. It reports false when the shape is
// wrong, so the ref and range checks do not pile on.
func validateDependencyForm(i int, dep contract.Dependency, result *ValidationResult) bool {
	base := fmt.Sprintf("dependencies[%d]", i)
	switch {
	case dep.Ref == "" && !dep.External:
		result.AddError(base+".ref", "DEPENDENCY_REF_REQUIRED", fmt.Sprintf("dependency %q has no ref: set ref to its contract (for a contract not published yet, the repository it will be published at, with the versions you accept in compatibility), or, if this Pacto system does not deliver it, set external: true and the version you run against", dep.Name))
	case dep.Ref == "" && dep.Version == "":
		result.AddError(base+".version", "EXTERNAL_VERSION_REQUIRED", fmt.Sprintf("external dependency %q has no ref, so it needs a version: write the version you run against, for example version: \"2025-01\"", dep.Name))
	case dep.Ref == "" && dep.Compatibility != "":
		result.AddError(base+".compatibility", "COMPATIBILITY_WITHOUT_REF", fmt.Sprintf("dependency %q has a compatibility range but no ref to compare it with: add the ref of its contract, or drop compatibility and keep version", dep.Name))
	case dep.Ref != "" && dep.Version != "":
		result.AddError(base+".version", "VERSION_WITH_REF", fmt.Sprintf("dependency %q has a ref, so Pacto reads its version from its contract: drop version and say which versions work in compatibility", dep.Name))
	default:
		return true
	}
	return false
}

func validateDependencyRefs(c *contract.Contract, result *ValidationResult) {
	for i, dep := range c.Dependencies {
		if !validateDependencyForm(i, dep, result) || dep.Ref == "" {
			continue
		}
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
				fmt.Sprintf("dependency %q has a ref, so it needs compatibility: the versions that work, for example ^2.0.0", dep.Name),
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
		// Only structured specs are parseable here; a grpc .proto ref carries no
		// format this layer can check, so it is skipped rather than failed.
		var parsed any
		var perr error
		format := "YAML"
		switch {
		case isYAMLFile(iface.Ref):
			perr = yaml.Unmarshal(data, &parsed)
		case strings.EqualFold(filepath.Ext(iface.Ref), ".json"):
			format = "JSON"
			perr = json.Unmarshal(data, &parsed)
		default:
			continue
		}
		if perr != nil {
			result.AddError(
				fmt.Sprintf("interfaces[%d].ref", i),
				"INVALID_INTERFACE_SPEC",
				fmt.Sprintf("interface spec file %q is not valid %s: %v", iface.Ref, format, perr),
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
