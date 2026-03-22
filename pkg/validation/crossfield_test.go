package validation

import (
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
)

func validContract() *contract.Contract {
	port := 8080
	return &contract.Contract{
		PactoVersion: "1.0",
		Service: contract.ServiceIdentity{
			Name:    "my-svc",
			Version: "1.0.0",
		},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: &port, Visibility: "internal"},
		},
		Runtime: &contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:            "stateless",
				Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
				DataCriticality: "low",
			},
			Health: &contract.Health{Interface: "api", Path: "/health"},
		},
	}
}

func TestValidateServiceVersion_InvalidSemver(t *testing.T) {
	c := validContract()
	c.Service.Version = "not-semver"
	var result ValidationResult
	validateServiceVersion(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid semver")
	}
}

func TestValidateInterfaceFiles_NilBundleFS(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = "openapi.yaml"
	var result ValidationResult
	validateInterfaceFiles(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error when bundleFS is nil")
	}
}

func TestValidateInterfaceFiles_FileNotFound(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = "openapi.yaml"
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateInterfaceFiles(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error when contract file not found")
	}
}

func TestValidateInterfaceFiles_FileExists(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = "openapi.yaml"
	bundleFS := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte("test")},
	}
	var result ValidationResult
	validateInterfaceFiles(c, bundleFS, &result)
	if !result.IsValid() {
		t.Error("expected no error when contract file exists")
	}
}

func TestValidateInterfaceFiles_EmptyContract(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = ""
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateInterfaceFiles(c, bundleFS, &result)
	if !result.IsValid() {
		t.Error("expected no error when contract path is empty")
	}
}

func TestValidateConfigFiles_NilConfig(t *testing.T) {
	c := validContract()
	c.Configuration = nil
	var result ValidationResult
	validateConfigFiles(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil config")
	}
}

func TestValidateConfigFiles_NilBundleFS(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "schema.json"}
	var result ValidationResult
	validateConfigFiles(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error when bundleFS is nil")
	}
}

func TestValidateConfigFiles_FileNotFound(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "schema.json"}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateConfigFiles(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error when schema file not found")
	}
}

func TestValidateConfigFiles_FileExists(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "schema.json"}
	bundleFS := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte("{}")},
	}
	var result ValidationResult
	validateConfigFiles(c, bundleFS, &result)
	if !result.IsValid() {
		t.Error("expected no error when schema file exists")
	}
}

func TestValidateConfigFiles_EmptySchema(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: ""}
	var result ValidationResult
	validateConfigFiles(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for empty schema path")
	}
}

func TestValidateDependencyRefs_InvalidOCIRef(t *testing.T) {
	c := validContract()
	c.Dependencies = []contract.Dependency{
		{Ref: "oci://invalid", Compatibility: "^1.0.0"},
	}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid OCI ref")
	}
}

func TestValidateDependencyRefs_NoTagWithCompatibility(t *testing.T) {
	c := validContract()
	c.Dependencies = []contract.Dependency{
		{Ref: "oci://ghcr.io/acme/svc", Compatibility: "^1.0.0"},
	}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if !result.IsValid() {
		t.Errorf("expected tagless ref with compatibility to be valid, got errors: %v", result.Errors)
	}
}

func TestValidateDependencyRefs_NoTagNoCompatibility(t *testing.T) {
	c := validContract()
	c.Dependencies = []contract.Dependency{
		{Ref: "oci://ghcr.io/acme/svc", Compatibility: ""},
	}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if result.IsValid() {
		t.Error("expected error for empty compatibility")
	}
}

func TestValidateDependencyRefs_LocalRef(t *testing.T) {
	c := validContract()
	c.Dependencies = []contract.Dependency{
		{Ref: "file://../local-dep", Compatibility: "^1.0.0"},
	}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if !result.IsValid() {
		t.Errorf("expected local ref to be valid, got errors: %v", result.Errors)
	}
}

func TestValidateDependencyRefs_TagNotDigestWarning(t *testing.T) {
	c := validContract()
	c.Dependencies = []contract.Dependency{
		{Ref: "oci://ghcr.io/acme/svc:1.0.0", Compatibility: "^1.0.0"},
	}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if len(result.Warnings) == 0 {
		t.Error("expected TAG_NOT_DIGEST warning")
	}
}

func TestValidateDependencyRefs_EmptyCompatibility(t *testing.T) {
	c := validContract()
	c.Dependencies = []contract.Dependency{
		{Ref: "oci://ghcr.io/acme/svc@sha256:abc123", Compatibility: ""},
	}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if result.IsValid() {
		t.Error("expected error for empty compatibility")
	}
}

func TestValidateDependencyRefs_InvalidCompatibility(t *testing.T) {
	c := validContract()
	c.Dependencies = []contract.Dependency{
		{Ref: "oci://ghcr.io/acme/svc@sha256:abc123", Compatibility: "not-a-range"},
	}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid compatibility range")
	}
}

func TestValidateDependencyRefs_Valid(t *testing.T) {
	c := validContract()
	c.Dependencies = []contract.Dependency{
		{Ref: "oci://ghcr.io/acme/svc@sha256:abc123", Compatibility: "^1.0.0"},
	}
	var result ValidationResult
	validateDependencyRefs(c, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid dependency, got %v", result.Errors)
	}
}

func TestValidateStatePersistenceInvariants_Conflict(t *testing.T) {
	c := validContract()
	c.Runtime.State.Type = "stateless"
	c.Runtime.State.Persistence.Durability = "persistent"
	var result ValidationResult
	validateStatePersistenceInvariants(c, &result)
	if result.IsValid() {
		t.Error("expected error for stateless with persistent durability")
	}
}

func TestValidateStatePersistenceInvariants_NoConflict(t *testing.T) {
	c := validContract()
	c.Runtime.State.Type = "stateful"
	c.Runtime.State.Persistence.Durability = "persistent"
	var result ValidationResult
	validateStatePersistenceInvariants(c, &result)
	if !result.IsValid() {
		t.Error("expected no error for stateful with persistent durability")
	}
}

func TestValidateInterfacePorts_EventWithPort(t *testing.T) {
	c := validContract()
	port := 8080
	c.Interfaces = append(c.Interfaces, contract.Interface{
		Name: "events", Type: "event", Port: &port, Contract: "events.proto",
	})
	var result ValidationResult
	validateInterfacePorts(c, &result)
	if len(result.Warnings) == 0 {
		t.Error("expected PORT_IGNORED warning for event interface with port")
	}
}

func TestValidateInterfaceContracts_GRPCWithoutContract(t *testing.T) {
	c := validContract()
	grpcPort := 9090
	c.Interfaces = append(c.Interfaces, contract.Interface{
		Name: "grpc", Type: "grpc", Port: &grpcPort,
	})
	var result ValidationResult
	validateInterfaceContracts(c, &result)
	if result.IsValid() {
		t.Error("expected error for gRPC interface without contract")
	}
}

func TestValidateHealthInterface_EventInterface(t *testing.T) {
	c := validContract()
	c.Interfaces = []contract.Interface{
		{Name: "events", Type: "event", Contract: "events.proto"},
	}
	c.Runtime.Health.Interface = "events"
	var result ValidationResult
	validateHealthInterface(c, &result)
	if result.IsValid() {
		t.Error("expected error for event health interface")
	}
}

func TestValidateHealthInterface_GRPCWithPath(t *testing.T) {
	c := validContract()
	grpcPort := 9090
	c.Interfaces = []contract.Interface{
		{Name: "grpc", Type: "grpc", Port: &grpcPort, Contract: "service.proto"},
	}
	c.Runtime.Health = &contract.Health{Interface: "grpc", Path: "/health"}
	var result ValidationResult
	validateHealthInterface(c, &result)
	if len(result.Warnings) == 0 {
		t.Error("expected HEALTH_PATH_IGNORED warning for gRPC interface with path")
	}
}

func TestValidateHealthInterface_HTTPWithoutPath(t *testing.T) {
	c := validContract()
	c.Runtime.Health = &contract.Health{Interface: "api", Path: ""}
	var result ValidationResult
	validateHealthInterface(c, &result)
	if result.IsValid() {
		t.Error("expected error for HTTP health interface without path")
	}
}

func TestValidateMetricsInterface_NotFound(t *testing.T) {
	c := validContract()
	c.Runtime.Metrics = &contract.Metrics{Interface: "nonexistent", Path: "/metrics"}
	var result ValidationResult
	validateMetricsInterface(c, &result)
	if result.IsValid() {
		t.Error("expected error for metrics interface not found")
	}
}

func TestValidateMetricsInterface_EventInterface(t *testing.T) {
	c := validContract()
	c.Interfaces = []contract.Interface{
		{Name: "events", Type: "event", Contract: "events.proto"},
	}
	c.Runtime.Metrics = &contract.Metrics{Interface: "events", Path: "/metrics"}
	var result ValidationResult
	validateMetricsInterface(c, &result)
	if result.IsValid() {
		t.Error("expected error for event metrics interface")
	}
}

func TestValidateMetricsInterface_HTTPWithoutPath(t *testing.T) {
	c := validContract()
	c.Runtime.Metrics = &contract.Metrics{Interface: "api", Path: ""}
	var result ValidationResult
	validateMetricsInterface(c, &result)
	if result.IsValid() {
		t.Error("expected error for HTTP metrics interface without path")
	}
}

func TestValidateMetricsInterface_GRPCWithPath(t *testing.T) {
	c := validContract()
	grpcPort := 9090
	c.Interfaces = []contract.Interface{
		{Name: "grpc", Type: "grpc", Port: &grpcPort, Contract: "service.proto"},
	}
	c.Runtime.Metrics = &contract.Metrics{Interface: "grpc", Path: "/metrics"}
	var result ValidationResult
	validateMetricsInterface(c, &result)
	if len(result.Warnings) == 0 {
		t.Error("expected METRICS_PATH_IGNORED warning for gRPC interface with path")
	}
}

func TestValidateMetricsInterface_Valid(t *testing.T) {
	c := validContract()
	c.Runtime.Metrics = &contract.Metrics{Interface: "api", Path: "/metrics"}
	var result ValidationResult
	validateMetricsInterface(c, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid metrics interface, got %v", result.Errors)
	}
}

func TestValidateMetricsInterface_NilRuntime(t *testing.T) {
	c := validContract()
	c.Runtime = nil
	var result ValidationResult
	validateMetricsInterface(c, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil runtime")
	}
}

func TestValidateMetricsInterface_NilMetrics(t *testing.T) {
	c := validContract()
	c.Runtime.Metrics = nil
	var result ValidationResult
	validateMetricsInterface(c, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil metrics")
	}
}

func TestValidateImageRef_InvalidRef(t *testing.T) {
	c := validContract()
	c.Service.Image = &contract.Image{Ref: "invalid"}
	var result ValidationResult
	validateImageRef(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid image ref")
	}
}

func TestValidateImageRef_NilImage(t *testing.T) {
	c := validContract()
	c.Service.Image = nil
	var result ValidationResult
	validateImageRef(c, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil image")
	}
}

func TestValidateUpgradeStrategyConsistency_OrderedStateless(t *testing.T) {
	c := validContract()
	c.Runtime.Lifecycle = &contract.Lifecycle{UpgradeStrategy: "ordered"}
	c.Runtime.State.Type = "stateless"
	var result ValidationResult
	validateUpgradeStrategyConsistency(c, &result)
	if len(result.Warnings) == 0 {
		t.Error("expected warning for ordered upgrade strategy with stateless service")
	}
}

func TestValidateUpgradeStrategyConsistency_NilLifecycle(t *testing.T) {
	c := validContract()
	c.Runtime.Lifecycle = nil
	var result ValidationResult
	validateUpgradeStrategyConsistency(c, &result)
	if len(result.Warnings) != 0 {
		t.Error("expected no warning for nil lifecycle")
	}
}

func TestValidateInterfacePorts_HTTPWithoutPort(t *testing.T) {
	c := validContract()
	c.Interfaces = []contract.Interface{
		{Name: "api", Type: "http", Port: nil},
	}
	var result ValidationResult
	validateInterfacePorts(c, &result)
	if result.IsValid() {
		t.Error("expected PORT_REQUIRED error for HTTP interface without port")
	}
}

func TestValidateChartRef_NilChart(t *testing.T) {
	c := validContract()
	c.Service.Chart = nil
	var result ValidationResult
	validateChartRef(c, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil chart")
	}
}

func TestValidateChartRef_ValidLocal(t *testing.T) {
	c := validContract()
	c.Service.Chart = &contract.Chart{Ref: "./charts/my-chart", Version: "1.0.0"}
	var result ValidationResult
	validateChartRef(c, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid local chart, got %v", result.Errors)
	}
}

func TestValidateChartRef_ValidOCI(t *testing.T) {
	c := validContract()
	c.Service.Chart = &contract.Chart{Ref: "oci://ghcr.io/acme/chart", Version: "1.0.0"}
	var result ValidationResult
	validateChartRef(c, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid OCI chart, got %v", result.Errors)
	}
}

func TestValidateChartRef_InvalidOCIRef(t *testing.T) {
	c := validContract()
	c.Service.Chart = &contract.Chart{Ref: "oci://invalid", Version: "1.0.0"}
	var result ValidationResult
	validateChartRef(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid OCI chart ref")
	}
}

func TestValidateChartRef_InvalidVersion(t *testing.T) {
	c := validContract()
	c.Service.Chart = &contract.Chart{Ref: "./charts/my-chart", Version: "not-semver"}
	var result ValidationResult
	validateChartRef(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid chart version")
	}
}

func TestValidateConfigValues_NoConfig(t *testing.T) {
	c := validContract()
	c.Configuration = nil
	var result ValidationResult
	validateConfigValues(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil config")
	}
}

func TestValidateConfigValues_NoValues(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "schema.json"}
	var result ValidationResult
	validateConfigValues(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for config without values")
	}
}

func TestValidateConfigValues_ValuesWithoutSchema(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{
		Values: map[string]interface{}{"key": "val"},
	}
	var result ValidationResult
	validateConfigValues(c, nil, &result)
	if result.IsValid() {
		t.Error("expected error for values without schema")
	}
}

func TestValidateConfigValues_NilBundleFS(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{
		Schema: "config-schema.json",
		Values: map[string]interface{}{"key": "val"},
	}
	var result ValidationResult
	validateConfigValues(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error when bundleFS is nil")
	}
}

func TestValidateConfigValues_Valid(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{
		Schema: "config-schema.json",
		Values: map[string]interface{}{"DB_HOST": "localhost"},
	}
	bundleFS := fstest.MapFS{
		"config-schema.json": &fstest.MapFile{Data: []byte(`{
			"type": "object",
			"properties": {
				"DB_HOST": {"type": "string"}
			}
		}`)},
	}
	var result ValidationResult
	validateConfigValues(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid config values, got %v", result.Errors)
	}
}

func TestValidateConfigValues_SchemaFileNotFound(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{
		Schema: "missing-schema.json",
		Values: map[string]interface{}{"key": "val"},
	}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateConfigValues(c, bundleFS, &result)
	// File-not-found is caught by validateConfigFiles, not here.
	if !result.IsValid() {
		t.Error("expected no error for missing schema file (handled elsewhere)")
	}
}

func TestValidateConfigValues_InvalidSchemaJSON(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{
		Schema: "bad-schema.json",
		Values: map[string]interface{}{"key": "val"},
	}
	bundleFS := fstest.MapFS{
		"bad-schema.json": &fstest.MapFile{Data: []byte("not valid json")},
	}
	var result ValidationResult
	// Invalid schema JSON is now caught by validateConfigSchemaContent;
	// validateConfigValues silently skips when compilation fails.
	validateConfigValues(c, bundleFS, &result)
	if !result.IsValid() {
		t.Error("expected no error from validateConfigValues (caught by validateConfigSchemaContent)")
	}
}

func TestValidateConfigValues_InvalidSchemaCompile(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{
		Schema: "bad-compile.json",
		Values: map[string]interface{}{"key": "val"},
	}
	// Valid JSON but references a non-existent $ref — should fail compilation.
	bundleFS := fstest.MapFS{
		"bad-compile.json": &fstest.MapFile{Data: []byte(`{
			"type": "object",
			"properties": {
				"key": {"$ref": "nonexistent://bad-ref"}
			}
		}`)},
	}
	var result ValidationResult
	// Schema compilation errors are now caught by validateConfigSchemaContent;
	// validateConfigValues silently skips when compilation fails.
	validateConfigValues(c, bundleFS, &result)
	if !result.IsValid() {
		t.Error("expected no error from validateConfigValues (caught by validateConfigSchemaContent)")
	}
}

func TestValidateConfigValues_InvalidValue(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{
		Schema: "config-schema.json",
		Values: map[string]interface{}{"DB_PORT": "not-a-number"},
	}
	bundleFS := fstest.MapFS{
		"config-schema.json": &fstest.MapFile{Data: []byte(`{
			"type": "object",
			"properties": {
				"DB_PORT": {"type": "integer"}
			}
		}`)},
	}
	var result ValidationResult
	validateConfigValues(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error for config value type mismatch")
	}
}

func TestValidateConfigValues_ExternalRef(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{
		Ref:    "oci://ghcr.io/acme/config-pacto:1.0.0",
		Values: map[string]interface{}{"key": "val"},
	}
	var result ValidationResult
	validateConfigValues(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error when using external ref with values")
	}
}

func TestValidateConfigRef_NilConfig(t *testing.T) {
	c := validContract()
	c.Configuration = nil
	var result ValidationResult
	validateConfigRef(c, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil config")
	}
}

func TestValidateConfigRef_EmptyRef(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "schema.json"}
	var result ValidationResult
	validateConfigRef(c, &result)
	if !result.IsValid() {
		t.Error("expected no error for empty ref")
	}
}

func TestValidateConfigRef_ValidOCI(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Ref: "oci://ghcr.io/acme/config-pacto:1.0.0"}
	var result ValidationResult
	validateConfigRef(c, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid OCI ref, got %v", result.Errors)
	}
}

func TestValidateConfigRef_InvalidOCI(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Ref: "oci://invalid"}
	var result ValidationResult
	validateConfigRef(c, &result)
	if result.IsValid() {
		t.Error("expected error for invalid OCI config ref")
	}
}

func TestValidateConfigRef_LocalRef(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Ref: "file://../config"}
	var result ValidationResult
	validateConfigRef(c, &result)
	if !result.IsValid() {
		t.Error("expected no error for local config ref")
	}
}

func TestValidatePolicyFields_NilPolicy(t *testing.T) {
	c := validContract()
	c.Policy = nil
	var result ValidationResult
	validatePolicyFields(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil policy")
	}
}

func TestValidatePolicyFields_EmptyPolicy(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{}
	var result ValidationResult
	validatePolicyFields(c, nil, &result)
	if result.IsValid() {
		t.Error("expected error for empty policy")
	}
}

func TestValidatePolicyFields_SchemaFileNotFound(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Schema: "policy/schema.json"}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validatePolicyFields(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error when policy schema file not found")
	}
}

func TestValidatePolicyFields_SchemaFileExists(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Schema: "policy/schema.json"}
	bundleFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte("{}")},
	}
	var result ValidationResult
	validatePolicyFields(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error when policy schema file exists, got %v", result.Errors)
	}
}

func TestValidatePolicyFields_SchemaNilBundleFS(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Schema: "policy/schema.json"}
	var result ValidationResult
	validatePolicyFields(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error when bundleFS is nil")
	}
}

func TestValidatePolicyFields_ValidOCIRef(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Ref: "oci://ghcr.io/acme/policy-pacto:1.0.0"}
	var result ValidationResult
	validatePolicyFields(c, nil, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid OCI policy ref, got %v", result.Errors)
	}
}

func TestValidatePolicyFields_InvalidOCIRef(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Ref: "oci://invalid"}
	var result ValidationResult
	validatePolicyFields(c, nil, &result)
	if result.IsValid() {
		t.Error("expected error for invalid OCI policy ref")
	}
}

func TestValidatePolicyFields_LocalRef(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Ref: "file://../policy"}
	var result ValidationResult
	validatePolicyFields(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for local policy ref")
	}
}

func TestValidatePolicyFields_BothSchemaAndRef(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{
		Schema: "policy/schema.json",
		Ref:    "oci://ghcr.io/acme/policy-pacto:1.0.0",
	}
	bundleFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte("{}")},
	}
	var result ValidationResult
	validatePolicyFields(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for policy with both schema and ref, got %v", result.Errors)
	}
}

// --- Interface file content validation ---

func TestValidateInterfaceFileContent_ValidYAML(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = "interfaces/openapi.yaml"
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte("openapi: '3.0.0'\ninfo:\n  title: test\n  version: '1.0'\n")},
	}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid YAML, got %v", result.Errors)
	}
}

func TestValidateInterfaceFileContent_InvalidYAML(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = "interfaces/openapi.yaml"
	bundleFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(":\ninvalid:\n  - [yaml\n")},
	}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected INVALID_CONTRACT_FILE error for invalid YAML")
	}
	if result.Errors[0].Code != "INVALID_CONTRACT_FILE" {
		t.Errorf("expected code INVALID_CONTRACT_FILE, got %s", result.Errors[0].Code)
	}
}

func TestValidateInterfaceFileContent_NonYAMLSkipped(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = "interfaces/service.proto"
	bundleFS := fstest.MapFS{
		"interfaces/service.proto": &fstest.MapFile{Data: []byte("not yaml content")},
	}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Error("expected no error for non-YAML file")
	}
}

func TestValidateInterfaceFileContent_NilBundleFS(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = "interfaces/openapi.yaml"
	var result ValidationResult
	validateInterfaceFileContent(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error when bundleFS is nil")
	}
}

func TestValidateInterfaceFileContent_EmptyContract(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = ""
	var result ValidationResult
	validateInterfaceFileContent(c, fstest.MapFS{}, &result)
	if !result.IsValid() {
		t.Error("expected no error for empty contract path")
	}
}

func TestValidateInterfaceFileContent_MissingFileSkipped(t *testing.T) {
	c := validContract()
	c.Interfaces[0].Contract = "interfaces/openapi.yaml"
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateInterfaceFileContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Error("expected no error for missing file (handled by validateInterfaceFiles)")
	}
}

// --- Config schema content validation ---

func TestValidateConfigSchemaContent_ValidJSON(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "config/schema.json"}
	bundleFS := fstest.MapFS{
		"config/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)},
	}
	var result ValidationResult
	validateConfigSchemaContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid JSON Schema, got %v", result.Errors)
	}
}

func TestValidateConfigSchemaContent_InvalidJSON(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "config/schema.json"}
	bundleFS := fstest.MapFS{
		"config/schema.json": &fstest.MapFile{Data: []byte("not json")},
	}
	var result ValidationResult
	validateConfigSchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected INVALID_CONFIG_JSON error")
	}
	if result.Errors[0].Code != "INVALID_CONFIG_JSON" {
		t.Errorf("expected code INVALID_CONFIG_JSON, got %s", result.Errors[0].Code)
	}
}

func TestValidateConfigSchemaContent_InvalidSchema(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "config/schema.json"}
	bundleFS := fstest.MapFS{
		"config/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"k":{"$ref":"nonexistent://bad"}}}`)},
	}
	var result ValidationResult
	validateConfigSchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected INVALID_CONFIG_SCHEMA error")
	}
	if result.Errors[0].Code != "INVALID_CONFIG_SCHEMA" {
		t.Errorf("expected code INVALID_CONFIG_SCHEMA, got %s", result.Errors[0].Code)
	}
}

func TestValidateConfigSchemaContent_NilConfig(t *testing.T) {
	c := validContract()
	c.Configuration = nil
	var result ValidationResult
	validateConfigSchemaContent(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil config")
	}
}

func TestValidateConfigSchemaContent_EmptySchema(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: ""}
	var result ValidationResult
	validateConfigSchemaContent(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for empty schema")
	}
}

func TestValidateConfigSchemaContent_MissingFileSkipped(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "missing.json"}
	bundleFS := fstest.MapFS{}
	var result ValidationResult
	validateConfigSchemaContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Error("expected no error for missing file (handled by validateConfigFiles)")
	}
}

func TestValidateConfigSchemaContent_WithoutValues(t *testing.T) {
	c := validContract()
	c.Configuration = &contract.Configuration{Schema: "config/schema.json"}
	bundleFS := fstest.MapFS{
		"config/schema.json": &fstest.MapFile{Data: []byte("not json")},
	}
	var result ValidationResult
	validateConfigSchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected error even without values — schema must always be valid")
	}
}

// --- Policy schema content validation ---

func TestValidatePolicySchemaContent_ValidJSON(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Schema: "policy/schema.json"}
	bundleFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)},
	}
	var result ValidationResult
	validatePolicySchemaContent(c, bundleFS, &result)
	if !result.IsValid() {
		t.Errorf("expected no error for valid policy JSON Schema, got %v", result.Errors)
	}
}

func TestValidatePolicySchemaContent_InvalidJSON(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Schema: "policy/schema.json"}
	bundleFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte("not json")},
	}
	var result ValidationResult
	validatePolicySchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected INVALID_POLICY_JSON error")
	}
	if result.Errors[0].Code != "INVALID_POLICY_JSON" {
		t.Errorf("expected code INVALID_POLICY_JSON, got %s", result.Errors[0].Code)
	}
}

func TestValidatePolicySchemaContent_InvalidSchema(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Schema: "policy/schema.json"}
	bundleFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"k":{"$ref":"nonexistent://bad"}}}`)},
	}
	var result ValidationResult
	validatePolicySchemaContent(c, bundleFS, &result)
	if result.IsValid() {
		t.Error("expected INVALID_POLICY_SCHEMA error")
	}
	if result.Errors[0].Code != "INVALID_POLICY_SCHEMA" {
		t.Errorf("expected code INVALID_POLICY_SCHEMA, got %s", result.Errors[0].Code)
	}
}

func TestValidatePolicySchemaContent_NilPolicy(t *testing.T) {
	c := validContract()
	c.Policy = nil
	var result ValidationResult
	validatePolicySchemaContent(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for nil policy")
	}
}

func TestValidatePolicySchemaContent_EmptySchema(t *testing.T) {
	c := validContract()
	c.Policy = &contract.Policy{Ref: "oci://ghcr.io/acme/policy:1.0.0"}
	var result ValidationResult
	validatePolicySchemaContent(c, nil, &result)
	if !result.IsValid() {
		t.Error("expected no error for policy with ref only")
	}
}

// --- validateJSONSchemaFile guard ---

func TestValidateJSONSchemaFile_NilBundleFS(t *testing.T) {
	var result ValidationResult
	validateJSONSchemaFile(nil, "schema.json", "field", "CODE1", "CODE2", &result)
	if !result.IsValid() {
		t.Error("expected no error when bundleFS is nil")
	}
}

// --- isYAMLFile helper ---

func TestIsYAMLFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"openapi.yaml", true},
		{"openapi.yml", true},
		{"openapi.YAML", true},
		{"schema.json", false},
		{"service.proto", false},
		{"noext", false},
	}
	for _, tt := range tests {
		if got := isYAMLFile(tt.path); got != tt.want {
			t.Errorf("isYAMLFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
