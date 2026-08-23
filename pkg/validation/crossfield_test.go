package validation

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

func validV20Contract() *contract.Contract {
	return &contract.Contract{
		PactoVersion: "2.0",
		Service: contract.Service{
			Name:    "my-svc",
			Version: "1.0.0",
			Owner:   contract.Owner{Team: "platform"},
		},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "openapi", Ref: "interfaces/openapi.yaml", Visibility: "internal"},
		},
	}
}

func TestValidateServiceVersion_InvalidSemver(t *testing.T) {
	c := validV20Contract()
	c.Service.Version = "not-semver"
	var result ValidationResult
	validateServiceVersion(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid semver")
	}
	if !hasErrorCode(result, "INVALID_SEMVER") {
		t.Errorf("expected INVALID_SEMVER, got %+v", result.Errors)
	}
}

func TestValidateServiceVersion_Valid(t *testing.T) {
	c := validV20Contract()
	c.Service.Version = "2.3.4"
	var result ValidationResult
	validateServiceVersion(c, &result)
	if !result.IsValid() {
		t.Errorf("expected valid semver, got errors: %v", result.Errors)
	}
}

func TestValidateInterfaceNamesUnique_Duplicate(t *testing.T) {
	c := validV20Contract()
	c.Interfaces = []contract.Interface{
		{Name: "api", Type: "openapi", Ref: "spec1.yaml"},
		{Name: "api", Type: "asyncapi", Ref: "spec2.yaml"},
	}
	var result ValidationResult
	validateInterfaceNamesUnique(c, &result)
	if result.IsValid() {
		t.Error("expected error for duplicate interface names")
	}
	if !hasErrorCode(result, "DUPLICATE_INTERFACE_NAME") {
		t.Errorf("expected DUPLICATE_INTERFACE_NAME, got %+v", result.Errors)
	}
}

// Both validators read the one contract-level rule, which reports duplicates of
// BOTH kinds. So each is given a contract that repeats a configuration name AND a
// policy name, and each has to report only its own kind: a configuration
// duplicate reported as a policy duplicate would point the reader at the wrong
// list, under the wrong index.
func duplicatesOfBothKinds() *contract.Contract {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json"},
		{Name: "app", Schema: "config/app2.json"},
	}
	c.Policies = []contract.Policy{
		{Name: "security", Schema: "policy/sec.json"},
		{Name: "security", Schema: "policy/sec2.json"},
	}
	return c
}

func TestValidateConfigurationNamesUnique_Duplicate(t *testing.T) {
	var result ValidationResult
	validateConfigurationNamesUnique(duplicatesOfBothKinds(), &result)
	if result.IsValid() {
		t.Error("expected error for duplicate config names")
	}
	if !hasErrorCode(result, "DUPLICATE_CONFIGURATION_NAME") {
		t.Errorf("expected DUPLICATE_CONFIGURATION_NAME, got %+v", result.Errors)
	}
	if hasErrorCode(result, "DUPLICATE_POLICY_NAME") {
		t.Errorf("configuration validator reported a policy duplicate: %+v", result.Errors)
	}
}

func TestValidatePolicyNamesUnique_Duplicate(t *testing.T) {
	var result ValidationResult
	validatePolicyNamesUnique(duplicatesOfBothKinds(), &result)
	if result.IsValid() {
		t.Error("expected error for duplicate policy names")
	}
	if !hasErrorCode(result, "DUPLICATE_POLICY_NAME") {
		t.Errorf("expected DUPLICATE_POLICY_NAME, got %+v", result.Errors)
	}
	if hasErrorCode(result, "DUPLICATE_CONFIGURATION_NAME") {
		t.Errorf("policy validator reported a configuration duplicate: %+v", result.Errors)
	}
}

func TestValidateDependencyNamesUnique_Duplicate(t *testing.T) {
	c := validV20Contract()
	c.Dependencies = []contract.Dependency{
		{Name: "cache", Ref: "oci://ghcr.io/acme/redis:1.0.0", Compatibility: "^1.0.0"},
		{Name: "cache", Ref: "oci://ghcr.io/acme/redis:2.0.0", Compatibility: "^2.0.0"},
	}
	var result ValidationResult
	validateDependencyNamesUnique(c, &result)
	if result.IsValid() {
		t.Error("expected error for duplicate dependency names")
	}
	if !hasErrorCode(result, "DUPLICATE_DEPENDENCY_NAME") {
		t.Errorf("expected DUPLICATE_DEPENDENCY_NAME, got %+v", result.Errors)
	}
}

func TestValidateInterfaces_ValidTypes(t *testing.T) {
	types := []string{"openapi", "asyncapi", "grpc"}
	for _, typ := range types {
		c := validV20Contract()
		c.Interfaces = []contract.Interface{{Name: "api", Type: typ, Ref: "spec.yaml"}}
		var result ValidationResult
		validateInterfaces(c, &result)
		if !result.IsValid() {
			t.Errorf("type %q should be valid, got errors: %v", typ, result.Errors)
		}
	}
}

func TestValidateInterfaces_InvalidType(t *testing.T) {
	c := validV20Contract()
	c.Interfaces = []contract.Interface{{Name: "api", Type: "http", Ref: "spec.yaml"}}
	var result ValidationResult
	validateInterfaces(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid interface type")
	}
	if !hasErrorCode(result, "INVALID_INTERFACE_TYPE") {
		t.Errorf("expected INVALID_INTERFACE_TYPE, got %+v", result.Errors)
	}
}

func TestValidateInterfaces_MissingRef(t *testing.T) {
	c := validV20Contract()
	c.Interfaces = []contract.Interface{{Name: "api", Type: "openapi", Ref: ""}}
	var result ValidationResult
	validateInterfaces(c, &result)
	if result.IsValid() {
		t.Error("expected error for missing ref")
	}
	if !hasErrorCode(result, "INTERFACE_REF_REQUIRED") {
		t.Errorf("expected INTERFACE_REF_REQUIRED, got %+v", result.Errors)
	}
}

func TestValidateCapabilities_BindingInterfaceUnknown(t *testing.T) {
	c := validV20Contract()
	c.Interfaces = []contract.Interface{{Name: "public-api", Type: "openapi", Ref: "i.json"}}
	c.Capabilities = []contract.Capability{{Type: "health", Binding: &contract.CapabilityBinding{Type: "http", Interface: "nope", Path: "/healthz"}}}
	var result ValidationResult
	validateCapabilities(c, &result)
	if !hasErrorCode(result, "CAPABILITY_INTERFACE_UNKNOWN") {
		t.Errorf("expected CAPABILITY_INTERFACE_UNKNOWN, got %+v", result.Errors)
	}
}

func TestValidateCapabilities_BindingInterfaceKnown(t *testing.T) {
	c := validV20Contract()
	c.Interfaces = []contract.Interface{{Name: "public-api", Type: "openapi", Ref: "i.json"}}
	c.Capabilities = []contract.Capability{{Type: "health", Binding: &contract.CapabilityBinding{Type: "http", Interface: "public-api", Path: "/healthz"}}}
	var result ValidationResult
	validateCapabilities(c, &result)
	if hasErrorCode(result, "CAPABILITY_INTERFACE_UNKNOWN") || hasErrorCode(result, "CAPABILITY_PATH_INVALID") {
		t.Errorf("valid binding must produce no binding errors, got %+v", result.Errors)
	}
}

func TestValidateCapabilities_BindingPathInvalid(t *testing.T) {
	for _, bad := range []string{"//evil.example", "/%2Fevil.example", "/%2f%2fevil.example", "http://evil.example", "https://evil.example", "user@host", "#fragment", "relative/no/slash", "/\x7f"} {
		c := validV20Contract()
		c.Interfaces = []contract.Interface{{Name: "public-api", Type: "openapi", Ref: "i.json"}}
		c.Capabilities = []contract.Capability{{Type: "health", Binding: &contract.CapabilityBinding{Type: "http", Interface: "public-api", Path: bad}}}
		var result ValidationResult
		validateCapabilities(c, &result)
		if !hasErrorCode(result, "CAPABILITY_PATH_INVALID") {
			t.Errorf("path %q must be rejected as CAPABILITY_PATH_INVALID, got %+v", bad, result.Errors)
		}
	}
}

func TestValidateCapabilities_BindingPathValid(t *testing.T) {
	for _, ok := range []string{"/healthz", "/metrics", "/a/b/c"} {
		c := validV20Contract()
		c.Interfaces = []contract.Interface{{Name: "public-api", Type: "openapi", Ref: "i.json"}}
		c.Capabilities = []contract.Capability{{Type: "health", Binding: &contract.CapabilityBinding{Type: "http", Interface: "public-api", Path: ok}}}
		var result ValidationResult
		validateCapabilities(c, &result)
		if hasErrorCode(result, "CAPABILITY_PATH_INVALID") {
			t.Errorf("path %q must be accepted, got %+v", ok, result.Errors)
		}
	}
}

func TestValidateCapabilities_DuplicateExtensionRef(t *testing.T) {
	c := validV20Contract()
	c.Capabilities = []contract.Capability{
		{Type: "extension", Ref: "acme.io/backup"},
		{Type: "extension", Ref: "acme.io/backup"},
	}
	var result ValidationResult
	validateCapabilities(c, &result)
	if !hasErrorCode(result, "DUPLICATE_CAPABILITY") {
		t.Errorf("duplicate extension ref must be DUPLICATE_CAPABILITY, got %+v", result.Errors)
	}
}

func TestValidateCapabilities_DistinctExtensionRefs_OK(t *testing.T) {
	c := validV20Contract()
	c.Capabilities = []contract.Capability{
		{Type: "extension", Ref: "acme.io/backup"},
		{Type: "extension", Ref: "acme.io/security-scan"},
	}
	var result ValidationResult
	validateCapabilities(c, &result)
	if hasErrorCode(result, "DUPLICATE_CAPABILITY") {
		t.Errorf("distinct extension refs must NOT collide, got %+v", result.Errors)
	}
}

func TestValidateCapabilities_ValidStandardTypes(t *testing.T) {
	types := []string{"health", "metrics"}
	for _, typ := range types {
		c := validV20Contract()
		c.Capabilities = []contract.Capability{{Type: typ}}
		var result ValidationResult
		validateCapabilities(c, &result)
		if !result.IsValid() {
			t.Errorf("capability type %q should be valid, got errors: %v", typ, result.Errors)
		}
	}
}

func TestValidateCapabilities_Extension_Valid(t *testing.T) {
	c := validV20Contract()
	c.Capabilities = []contract.Capability{{Type: "extension", Ref: "example.com/custom"}}
	var result ValidationResult
	validateCapabilities(c, &result)
	if !result.IsValid() {
		t.Errorf("extension capability should be valid, got errors: %v", result.Errors)
	}
}

func TestValidateCapabilities_Extension_MissingRef(t *testing.T) {
	c := validV20Contract()
	c.Capabilities = []contract.Capability{{Type: "extension"}}
	var result ValidationResult
	validateCapabilities(c, &result)
	if result.IsValid() {
		t.Error("expected error for extension without ref")
	}
	if !hasErrorCode(result, "CAPABILITY_REF_REQUIRED") {
		t.Errorf("expected CAPABILITY_REF_REQUIRED, got %+v", result.Errors)
	}
}

func TestValidateCapabilities_Extension_InvalidRef(t *testing.T) {
	c := validV20Contract()
	c.Capabilities = []contract.Capability{{Type: "extension", Ref: "bad-ref"}}
	var result ValidationResult
	validateCapabilities(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid extension ref")
	}
	if !hasErrorCode(result, "CAPABILITY_REF_INVALID") {
		t.Errorf("expected CAPABILITY_REF_INVALID, got %+v", result.Errors)
	}
}

func TestValidateCapabilities_DuplicateStandard(t *testing.T) {
	c := validV20Contract()
	c.Capabilities = []contract.Capability{
		{Type: "health"},
		{Type: "health"},
	}
	var result ValidationResult
	validateCapabilities(c, &result)
	if result.IsValid() {
		t.Error("expected error for duplicate health capability")
	}
	if !hasErrorCode(result, "DUPLICATE_CAPABILITY") {
		t.Errorf("expected DUPLICATE_CAPABILITY, got %+v", result.Errors)
	}
}

func TestValidateCapabilities_InvalidType(t *testing.T) {
	c := validV20Contract()
	c.Capabilities = []contract.Capability{{Type: "monitoring"}}
	var result ValidationResult
	validateCapabilities(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid capability type")
	}
	if !hasErrorCode(result, "INVALID_CAPABILITY_TYPE") {
		t.Errorf("expected INVALID_CAPABILITY_TYPE, got %+v", result.Errors)
	}
}

func TestValidateInterfaceFiles_FileNotFound(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0].Ref = "missing.yaml"
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateInterfaceFiles(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error when interface file not found")
	}
	if !hasErrorCode(result, "FILE_NOT_FOUND") {
		t.Errorf("expected FILE_NOT_FOUND, got %+v", result.Errors)
	}
}

func TestValidateInterfaceFiles_FileExists(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0].Ref = "interfaces/openapi.yaml"
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte("test")},
	}
	var result ValidationResult
	validateInterfaceFiles(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error when file exists, got %v", result.Errors)
	}
}

func TestValidateInterfaceFiles_NilBundleFS(t *testing.T) {
	c := validV20Contract()
	var result ValidationResult
	validateInterfaceFiles(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error when bundleFS is nil")
	}
}

func TestValidateInterfaceFileContent_ValidYAML(t *testing.T) {
	c := validV20Contract()
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte("openapi: '3.0.0'\n")},
	}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid YAML, got %v", result.Errors)
	}
}

func TestValidateInterfaceFileContent_InvalidYAML(t *testing.T) {
	c := validV20Contract()
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte("\t\t\tinvalid:\n\t-broken")},
	}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error for invalid YAML")
	}
	if !hasErrorCode(result, "INVALID_INTERFACE_SPEC") {
		t.Errorf("expected INVALID_INTERFACE_SPEC, got %+v", result.Errors)
	}
}

func TestValidateInterfaceFileContent_InvalidJSON(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0].Ref = "interfaces/openapi.json"
	bundleFS := fstest.MapFS{
		"interfaces/openapi.json": &fstest.MapFile{Data: []byte("{not valid")},
	}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error for invalid JSON")
	}
	if !hasErrorCode(result, "INVALID_INTERFACE_SPEC") {
		t.Errorf("expected INVALID_INTERFACE_SPEC, got %+v", result.Errors)
	}
}

func TestValidateInterfaceFileContent_ValidJSON(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0].Ref = "interfaces/openapi.json"
	bundleFS := fstest.MapFS{
		"interfaces/openapi.json": &fstest.MapFile{Data: []byte(`{"openapi":"3.0.0"}`)},
	}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid JSON, got %v", result.Errors)
	}
}

// One missing interface spec must produce exactly one finding: validateInterfaces
// used to repeat the existence check owned by validateInterfaceFiles.
func TestValidateCrossField_MissingInterfaceFileReportedOnce(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0].Ref = "interfaces/missing.json"
	result := ValidateCrossField(c, fstest.MapFS{})
	if got := countErrorCode(result, "FILE_NOT_FOUND"); got != 1 {
		t.Errorf("expected 1 FILE_NOT_FOUND, got %d: %+v", got, result.Errors)
	}
}

// The de-duplication must not collapse findings about genuinely different files.
func TestValidateCrossField_TwoMissingInterfaceFilesReportedTwice(t *testing.T) {
	c := validV20Contract()
	c.Interfaces = []contract.Interface{
		{Name: "api", Type: "openapi", Ref: "interfaces/one.json"},
		{Name: "events", Type: "asyncapi", Ref: "interfaces/two.yaml"},
	}
	result := ValidateCrossField(c, fstest.MapFS{})
	if got := countErrorCode(result, "FILE_NOT_FOUND"); got != 2 {
		t.Errorf("expected 2 FILE_NOT_FOUND, got %d: %+v", got, result.Errors)
	}
}

// Same root cause as the duplicate FILE_NOT_FOUND: the spec-content check ran twice.
func TestValidateCrossField_InvalidInterfaceSpecReportedOnce(t *testing.T) {
	c := validV20Contract()
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte("paths: [ this is: broken")},
	}
	result := ValidateCrossField(c, bundleFS)
	if got := countErrorCode(result, "INVALID_INTERFACE_SPEC"); got != 1 {
		t.Errorf("expected 1 INVALID_INTERFACE_SPEC, got %d: %+v", got, result.Errors)
	}
}

// A grpc .proto ref has no format this layer can parse; JSON-parsing it reported
// every valid proto file as an invalid spec.
func TestValidateCrossField_ProtoRefNotParsed(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0] = contract.Interface{Name: "rpc", Type: "grpc", Ref: "interfaces/service.proto"}
	bundleFS := fstest.MapFS{
		"interfaces/service.proto": &fstest.MapFile{Data: []byte("syntax = \"proto3\";\n")},
	}
	result := ValidateCrossField(c, bundleFS)
	if !result.IsValid() {
		t.Errorf("expected valid proto ref, got %+v", result.Errors)
	}
}

// A ref pointing at a directory satisfies fs.Stat but nothing downstream can
// read it, so pack would ship a bundle whose declared file does not exist.
func TestCrossField_DirectoryRefIsNotAFile(t *testing.T) {
	// fstest.MapFS synthesises the parent directory of any file it holds.
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml/inner.txt": &fstest.MapFile{Data: []byte("x")},
		"config/app.json/inner.txt":         &fstest.MapFile{Data: []byte("x")},
		"policy/sec.json/inner.txt":         &fstest.MapFile{Data: []byte("x")},
	}
	tests := []struct {
		name  string
		check func(*contract.Contract, fs.FS, *ValidationResult)
		setup func(*contract.Contract)
	}{
		{"interface spec", validateInterfaceFiles, func(c *contract.Contract) {}},
		{"configuration schema", validateConfigFiles, func(c *contract.Contract) {
			c.Configurations = []contract.Configuration{{Name: "app", Schema: "config/app.json"}}
		}},
		{"policy schema", validatePolicyFields, func(c *contract.Contract) {
			c.Policies = []contract.Policy{{Name: "sec", Schema: "policy/sec.json"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validV20Contract()
			tt.setup(c)
			var result ValidationResult
			tt.check(c, bundleFS, &result)
			if got := countErrorCode(result, "FILE_NOT_FOUND"); got != 1 {
				t.Errorf("expected 1 FILE_NOT_FOUND for a directory ref, got %d: %+v", got, result.Errors)
			}
		})
	}
}

func TestValidateConfigFiles_FileNotFound(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Schema: "config/app.json"}}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateConfigFiles(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error when config schema file not found")
	}
	if !hasErrorCode(result, "FILE_NOT_FOUND") {
		t.Errorf("expected FILE_NOT_FOUND, got %+v", result.Errors)
	}
}

func TestValidateConfigFiles_FileExists(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Schema: "config/app.json"}}
	bundleFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte("{}")},
	}
	var result ValidationResult
	validateConfigFiles(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error when file exists, got %v", result.Errors)
	}
}

func TestValidateConfigSchemaContent_InvalidJSON(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Schema: "config/app.json"}}
	bundleFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte("not json")},
	}
	var result ValidationResult
	validateConfigSchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error for invalid JSON")
	}
	if !hasErrorCode(result, "INVALID_CONFIG_JSON") {
		t.Errorf("expected INVALID_CONFIG_JSON, got %+v", result.Errors)
	}
}

func TestValidateConfigSchemaContent_InvalidSchema(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Schema: "config/app.json"}}
	bundleFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte(`{"type": 12345}`)},
	}
	var result ValidationResult
	validateConfigSchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error for invalid JSON Schema")
	}
	if !hasErrorCode(result, "INVALID_CONFIG_SCHEMA") {
		t.Errorf("expected INVALID_CONFIG_SCHEMA, got %+v", result.Errors)
	}
}

func TestValidateConfigRef_InvalidOCI(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Ref: "oci://bad"}}
	var result ValidationResult
	validateConfigRef(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid OCI ref")
	}
	if !hasErrorCode(result, "INVALID_CONFIG_REF") {
		t.Errorf("expected INVALID_CONFIG_REF, got %+v", result.Errors)
	}
}

func TestValidateConfigRef_ValidOCI(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Ref: "oci://ghcr.io/acme/config:1.0.0"}}
	var result ValidationResult
	validateConfigRef(c, &result)
	if !result.IsValid() {
		t.Errorf("expected valid OCI ref, got errors: %v", result.Errors)
	}
}

func TestValidatePolicyFields_FileNotFound(t *testing.T) {
	c := validV20Contract()
	c.Policies = []contract.Policy{{Name: "sec", Schema: "policy/sec.json"}}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validatePolicyFields(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error when policy schema file not found")
	}
	if !hasErrorCode(result, "FILE_NOT_FOUND") {
		t.Errorf("expected FILE_NOT_FOUND, got %+v", result.Errors)
	}
}

func TestValidatePolicyFields_InvalidRef(t *testing.T) {
	c := validV20Contract()
	c.Policies = []contract.Policy{{Name: "sec", Ref: "oci://bad"}}
	var result ValidationResult
	validatePolicyFields(c, nil, &result)
	if result.IsValid() {
		t.Error("expected error for invalid policy ref")
	}
	if !hasErrorCode(result, "INVALID_POLICY_REF") {
		t.Errorf("expected INVALID_POLICY_REF, got %+v", result.Errors)
	}
}

func TestValidatePolicySchemaContent_InvalidJSON(t *testing.T) {
	c := validV20Contract()
	c.Policies = []contract.Policy{{Name: "sec", Schema: "policy/sec.json"}}
	bundleFS := fstest.MapFS{
		"policy/sec.json": &fstest.MapFile{Data: []byte("not json")},
	}
	var result ValidationResult
	validatePolicySchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error for invalid JSON")
	}
	if !hasErrorCode(result, "INVALID_POLICY_JSON") {
		t.Errorf("expected INVALID_POLICY_JSON, got %+v", result.Errors)
	}
}

func TestValidatePolicySchemaContent_InvalidSchema(t *testing.T) {
	c := validV20Contract()
	c.Policies = []contract.Policy{{Name: "sec", Schema: "policy/sec.json"}}
	bundleFS := fstest.MapFS{
		"policy/sec.json": &fstest.MapFile{Data: []byte(`{"type": 12345}`)},
	}
	var result ValidationResult
	validatePolicySchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error for invalid JSON Schema")
	}
	if !hasErrorCode(result, "INVALID_POLICY_SCHEMA") {
		t.Errorf("expected INVALID_POLICY_SCHEMA, got %+v", result.Errors)
	}
}

func TestValidatePolicyTarget_Contract(t *testing.T) {
	c := validV20Contract()
	c.Policies = []contract.Policy{{Name: "sec", Target: "contract", Schema: "policy/sec.json"}}
	var result ValidationResult
	validatePolicyTarget(c, &result)
	if !result.IsValid() {
		t.Errorf("expected contract target to be valid, got errors: %v", result.Errors)
	}
}

func TestValidatePolicyTarget_Unsupported(t *testing.T) {
	c := validV20Contract()
	c.Policies = []contract.Policy{{Name: "sec", Target: "runtime", Schema: "policy/sec.json"}}
	var result ValidationResult
	validatePolicyTarget(c, &result)
	if result.IsValid() {
		t.Error("expected error for unsupported policy target")
	}
	if !hasErrorCode(result, "UNSUPPORTED_POLICY_TARGET") {
		t.Errorf("expected UNSUPPORTED_POLICY_TARGET, got %+v", result.Errors)
	}
}

func TestValidateDependencyRefs_InvalidOCI(t *testing.T) {
	c := validV20Contract()
	c.Dependencies = []contract.Dependency{{Name: "cache", Ref: "oci://bad", Compatibility: "^1.0.0"}}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid OCI ref")
	}
	if !hasErrorCode(result, "INVALID_OCI_REF") {
		t.Errorf("expected INVALID_OCI_REF, got %+v", result.Errors)
	}
}

func TestValidateDependencyRefs_TagNotDigestWarning(t *testing.T) {
	c := validV20Contract()
	c.Dependencies = []contract.Dependency{{Name: "cache", Ref: "oci://ghcr.io/acme/redis:1.0.0", Compatibility: "^1.0.0"}}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if !hasWarningCode(result, "TAG_NOT_DIGEST") {
		t.Errorf("expected TAG_NOT_DIGEST warning, got %+v", result.Warnings)
	}
}

func TestValidateDependencyRefs_DigestOK(t *testing.T) {
	c := validV20Contract()
	c.Dependencies = []contract.Dependency{{Name: "cache", Ref: "oci://ghcr.io/acme/redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Compatibility: "^1.0.0"}}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if !result.IsValid() {
		t.Errorf("expected valid digest ref, got errors: %v", result.Errors)
	}
	if hasWarningCode(result, "TAG_NOT_DIGEST") {
		t.Error("expected no TAG_NOT_DIGEST warning for digest ref")
	}
}

func TestValidateDependencyRefs_EmptyCompatibility(t *testing.T) {
	c := validV20Contract()
	c.Dependencies = []contract.Dependency{{Name: "cache", Ref: "oci://ghcr.io/acme/redis:1.0.0", Compatibility: ""}}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if result.IsValid() {
		t.Error("expected error for empty compatibility")
	}
	if !hasErrorCode(result, "EMPTY_COMPATIBILITY") {
		t.Errorf("expected EMPTY_COMPATIBILITY, got %+v", result.Errors)
	}
}

func TestValidateDependencyRefs_InvalidCompatibility(t *testing.T) {
	c := validV20Contract()
	c.Dependencies = []contract.Dependency{{Name: "cache", Ref: "oci://ghcr.io/acme/redis:1.0.0", Compatibility: "not-a-range"}}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid compatibility range")
	}
	if !hasErrorCode(result, "INVALID_COMPATIBILITY") {
		t.Errorf("expected INVALID_COMPATIBILITY, got %+v", result.Errors)
	}
}

func TestValidateConfigValues_WithoutSchema(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Values: map[string]any{"x": 1}}}
	var result ValidationResult
	validateConfigValues(c, nil, &result)
	if result.IsValid() {
		t.Error("expected error for values without schema")
	}
	if !hasErrorCode(result, "VALUES_WITHOUT_SCHEMA") {
		t.Errorf("expected VALUES_WITHOUT_SCHEMA, got %+v", result.Errors)
	}
}

func TestValidateConfigValues_WithSchema_Valid(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json", Values: map[string]any{"replicas": 3}},
	}
	bundleFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"replicas":{"type":"integer"}}}`)},
	}
	var result ValidationResult
	validateConfigValues(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected valid config values, got errors: %v", result.Errors)
	}
}

func TestValidateConfigValues_WithSchema_Invalid(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json", Values: map[string]any{"replicas": "three"}},
	}
	bundleFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"replicas":{"type":"integer"}}}`)},
	}
	var result ValidationResult
	validateConfigValues(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error for config values that don't match schema")
	}
	if !hasErrorCode(result, "CONFIG_VALUES_VALIDATION_FAILED") {
		t.Errorf("expected CONFIG_VALUES_VALIDATION_FAILED, got %+v", result.Errors)
	}
}

func TestValidateStatePersistenceInvariants_StatelessPersistent(t *testing.T) {
	c := validV20Contract()
	c.State = &contract.State{
		Type:            contract.StateStateless,
		Persistence:     contract.Persistence{Scope: "local", Durability: contract.DurabilityPersistent},
		DataCriticality: "low",
	}
	var result ValidationResult
	validateStatePersistenceInvariants(c, &result)
	if result.IsValid() {
		t.Error("expected error for stateless with persistent durability")
	}
	if !hasErrorCode(result, "STATELESS_PERSISTENT_CONFLICT") {
		t.Errorf("expected STATELESS_PERSISTENT_CONFLICT, got %+v", result.Errors)
	}
}

func TestValidateStatePersistenceInvariants_StatefulPersistent(t *testing.T) {
	c := validV20Contract()
	c.State = &contract.State{
		Type:            contract.StateStateful,
		Persistence:     contract.Persistence{Scope: "shared", Durability: contract.DurabilityPersistent},
		DataCriticality: "high",
	}
	var result ValidationResult
	validateStatePersistenceInvariants(c, &result)
	if !result.IsValid() {
		t.Errorf("expected valid stateful+persistent, got errors: %v", result.Errors)
	}
}

func TestValidateReadiness_InvalidExpires(t *testing.T) {
	c := validV20Contract()
	c.Readiness = &contract.Readiness{
		Expires: "2026-1-1",
		Claims:  []contract.ReadinessClaim{{ID: "a", Type: "url", Status: "done", Evidence: "x", Weight: 10}},
	}
	var result ValidationResult
	validateReadiness(c, &result)
	if result.IsValid() {
		t.Error("expected error for non-canonical date")
	}
	if !hasErrorCode(result, "INVALID_READINESS_EXPIRES") {
		t.Errorf("expected INVALID_READINESS_EXPIRES, got %+v", result.Errors)
	}
}

func TestValidateReadiness_ValidExpires(t *testing.T) {
	c := validV20Contract()
	c.Readiness = &contract.Readiness{
		Expires: "2026-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "a", Type: "url", Status: "done", Evidence: "x", Weight: 10}},
	}
	var result ValidationResult
	validateReadiness(c, &result)
	if !result.IsValid() {
		t.Errorf("expected valid readiness, got errors: %v", result.Errors)
	}
}

func TestValidateReadiness_DuplicateClaimID(t *testing.T) {
	c := validV20Contract()
	c.Readiness = &contract.Readiness{
		Expires: "2026-12-31",
		Claims: []contract.ReadinessClaim{
			{ID: "a", Type: "url", Status: "done", Evidence: "x", Weight: 10},
			{ID: "a", Type: "ticket", Status: "done", Evidence: "y", Weight: 10},
		},
	}
	var result ValidationResult
	validateReadiness(c, &result)
	if result.IsValid() {
		t.Error("expected error for duplicate claim ID")
	}
	if !hasErrorCode(result, "DUPLICATE_READINESS_ID") {
		t.Errorf("expected DUPLICATE_READINESS_ID, got %+v", result.Errors)
	}
}

func TestValidateReadiness_EmptyEvidence(t *testing.T) {
	c := validV20Contract()
	c.Readiness = &contract.Readiness{
		Expires: "2026-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "a", Type: "url", Status: "done", Evidence: "  ", Weight: 10}},
	}
	var result ValidationResult
	validateReadiness(c, &result)
	if result.IsValid() {
		t.Error("expected error for blank evidence")
	}
	if !hasErrorCode(result, "EMPTY_READINESS_EVIDENCE") {
		t.Errorf("expected EMPTY_READINESS_EVIDENCE, got %+v", result.Errors)
	}
}

func TestValidateReadiness_BlankDescription(t *testing.T) {
	c := validV20Contract()
	c.Readiness = &contract.Readiness{
		Expires: "2026-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "a", Type: "url", Status: "done", Evidence: "e", Weight: 10, Description: "   "}},
	}
	var result ValidationResult
	validateReadiness(c, &result)
	if result.IsValid() {
		t.Error("expected error for blank description")
	}
	if !hasErrorCode(result, "EMPTY_READINESS_DESCRIPTION") {
		t.Errorf("expected EMPTY_READINESS_DESCRIPTION, got %+v", result.Errors)
	}
}

func TestValidateReadiness_InvalidRevisionDate(t *testing.T) {
	c := validV20Contract()
	c.Readiness = &contract.Readiness{
		Expires: "2026-12-31",
		History: []contract.ReadinessRevision{{Date: "2026-1-1", Version: "1.0.0", Author: "ed", Description: "init"}},
		Claims:  []contract.ReadinessClaim{{ID: "a", Type: "url", Status: "done", Evidence: "e", Weight: 10}},
	}
	var result ValidationResult
	validateReadiness(c, &result)
	if result.IsValid() {
		t.Error("expected error for non-canonical revision date")
	}
	if !hasErrorCode(result, "INVALID_READINESS_REVISION") {
		t.Errorf("expected INVALID_READINESS_REVISION, got %+v", result.Errors)
	}
}

func TestValidateReadiness_BlankRevisionFields(t *testing.T) {
	c := validV20Contract()
	c.Readiness = &contract.Readiness{
		Expires: "2026-12-31",
		History: []contract.ReadinessRevision{{Date: "2026-06-21", Version: "", Author: "ed", Description: "init"}},
		Claims:  []contract.ReadinessClaim{{ID: "a", Type: "url", Status: "done", Evidence: "e", Weight: 10}},
	}
	var result ValidationResult
	validateReadiness(c, &result)
	if result.IsValid() {
		t.Error("expected error for blank revision version")
	}
	if !hasErrorCode(result, "INVALID_READINESS_REVISION") {
		t.Errorf("expected INVALID_READINESS_REVISION, got %+v", result.Errors)
	}
}

func hasErrorCode(r ValidationResult, code string) bool {
	for _, e := range r.Errors {
		if e.Code == code {
			return true
		}
	}
	return false
}

func countErrorCode(r ValidationResult, code string) int {
	n := 0
	for _, e := range r.Errors {
		if e.Code == code {
			n++
		}
	}
	return n
}

func hasWarningCode(r ValidationResult, code string) bool {
	for _, w := range r.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func TestValidateInterfaceFiles_EmptyRef(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0].Ref = ""
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateInterfaceFiles(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for empty ref, got %v", result.Errors)
	}
}

func TestValidateConfigFiles_EmptySchema(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Schema: ""}}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateConfigFiles(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for empty schema, got %v", result.Errors)
	}
}

func TestValidateConfigValues_WithRef_Deferred(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{
		{Name: "app", Ref: "oci://ghcr.io/acme/config:1.0.0", Values: map[string]any{"x": 1}},
	}
	var result ValidationResult
	validateConfigValues(c, nil, &result)
	if !result.IsValid() {
		t.Errorf("expected values with ref to be deferred, got errors: %v", result.Errors)
	}
}

func TestValidateConfigValues_FileReadError_Silent(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{
		{Name: "app", Schema: "missing.json", Values: map[string]any{"x": 1}},
	}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateConfigValues(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected file-not-found to be skipped (caught elsewhere), got errors: %v", result.Errors)
	}
}

func TestValidateConfigValues_InvalidSchema_Silent(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json", Values: map[string]any{"x": 1}},
	}
	bundleFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte(`{"type": 12345}`)},
	}
	var result ValidationResult
	validateConfigValues(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected invalid schema to be skipped (caught elsewhere), got errors: %v", result.Errors)
	}
}

func TestValidateInterfaceFileContent_NonYAMLSkipped(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0].Ref = "interfaces/spec.proto"
	bundleFS := fstest.MapFS{
		"interfaces/spec.proto": &fstest.MapFile{Data: []byte("syntax = \"proto3\";")},
	}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected non-YAML to be skipped, got errors: %v", result.Errors)
	}
}

func TestValidateInterfaceFileContent_FileReadError_Silent(t *testing.T) {
	c := validV20Contract()
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected file-not-found to be skipped (caught elsewhere), got errors: %v", result.Errors)
	}
}

func TestValidateReadiness_AllRevisionFields(t *testing.T) {
	c := validV20Contract()
	c.Readiness = &contract.Readiness{
		Expires: "2026-12-31",
		History: []contract.ReadinessRevision{
			{Date: "2026-06-21", Version: "1.0.0", Author: "ed", Description: "init"},
			{Date: "2026-6-21", Version: "", Author: "", Description: ""},
		},
		Claims: []contract.ReadinessClaim{{ID: "a", Type: "url", Status: "done", Evidence: "e", Weight: 10}},
	}
	var result ValidationResult
	validateReadiness(c, &result)
	if result.IsValid() {
		t.Error("expected errors for bad revision fields")
	}
	if !hasErrorCode(result, "INVALID_READINESS_REVISION") {
		t.Errorf("expected INVALID_READINESS_REVISION, got %+v", result.Errors)
	}
}

func TestCompileConfigSchema_InvalidJSON(t *testing.T) {
	_, err := compileConfigSchema([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestValidateJSONSchemaFile_NoBundleFS(t *testing.T) {
	var result ValidationResult
	validateJSONSchemaFile(nil, "schema.json", "field", "CODE_JSON", "CODE_SCHEMA", &result)
	if !result.IsValid() {
		t.Errorf("expected no error when bundleFS is nil, got %v", result.Errors)
	}
}

func TestValidateJSONSchemaFile_EmptyPath(t *testing.T) {
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateJSONSchemaFile(bundleFS, "", "field", "CODE_JSON", "CODE_SCHEMA", &result)
	if !result.IsValid() {
		t.Errorf("expected no error for empty path, got %v", result.Errors)
	}
}

func TestValidateJSONSchemaFile_FileNotFound_Silent(t *testing.T) {
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateJSONSchemaFile(bundleFS, "missing.json", "field", "CODE_JSON", "CODE_SCHEMA", &result)
	if !result.IsValid() {
		t.Errorf("expected file-not-found to be skipped (caught elsewhere), got %v", result.Errors)
	}
}

func TestResolveLocalPolicySchema_NilBundleFS(t *testing.T) {
	rp := resolveLocalPolicySchema(nil, "policy/sec.json", "origin", 0)
	if rp != nil {
		t.Error("expected nil for nil bundleFS")
	}
}

func TestResolveLocalPolicySchema_FileNotFound(t *testing.T) {
	bundleFS := fstest.MapFS{}
	rp := resolveLocalPolicySchema(bundleFS, "missing.json", "origin", 0)
	if rp != nil {
		t.Error("expected nil for missing file")
	}
}

func TestResolveLocalPolicySchema_InvalidJSON(t *testing.T) {
	bundleFS := fstest.MapFS{
		"policy/sec.json": &fstest.MapFile{Data: []byte("not json")},
	}
	rp := resolveLocalPolicySchema(bundleFS, "policy/sec.json", "origin", 0)
	if rp != nil {
		t.Error("expected nil for invalid JSON")
	}
}

func TestResolveLocalPolicySchema_InvalidSchema(t *testing.T) {
	bundleFS := fstest.MapFS{
		"policy/sec.json": &fstest.MapFile{Data: []byte(`{"type": 12345}`)},
	}
	rp := resolveLocalPolicySchema(bundleFS, "policy/sec.json", "origin", 0)
	if rp != nil {
		t.Error("expected nil for invalid schema")
	}
}

func TestCompilePolicySchema_InvalidJSON(t *testing.T) {
	_, err := compilePolicySchema([]byte("not json"), "mem:///test.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCompilePolicySchema_AddResourceError(t *testing.T) {
	_, err := compilePolicySchema([]byte(`{"type": 12345}`), "mem:///test.json")
	if err == nil {
		t.Error("expected error for invalid schema")
	}
}

func TestValidateConfigValues_NoValues(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Schema: "config/app.json"}}
	bundleFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)},
	}
	var result ValidationResult
	validateConfigValues(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for config without values, got %v", result.Errors)
	}
}

func TestValidateConfigSchemaContent_EmptySchema(t *testing.T) {
	c := validV20Contract()
	c.Configurations = []contract.Configuration{{Name: "app", Schema: ""}}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateConfigSchemaContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for empty schema, got %v", result.Errors)
	}
}

func TestValidateInterfaceFileContent_EmptyRef(t *testing.T) {
	c := validV20Contract()
	c.Interfaces[0].Ref = ""
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for empty ref, got %v", result.Errors)
	}
}

func TestValidateSingleConfigValues_NilBundleFS(t *testing.T) {
	cfg := contract.Configuration{
		Name:   "app",
		Schema: "config/app.json",
		Values: map[string]any{"x": 1},
	}
	var result ValidationResult
	validateSingleConfigValues(cfg, "configurations[0].values", nil, &result)
	if !result.IsValid() {
		t.Errorf("expected no error when bundleFS is nil, got %v", result.Errors)
	}
}
