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
		Runtime: contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:            "stateless",
				Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
				DataCriticality: "low",
			},
			Health: contract.Health{Interface: "api", Path: "/health"},
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
	c.Runtime.Health = contract.Health{Interface: "grpc", Path: "/health"}
	var result ValidationResult
	validateHealthInterface(c, &result)
	if len(result.Warnings) == 0 {
		t.Error("expected HEALTH_PATH_IGNORED warning for gRPC interface with path")
	}
}

func TestValidateHealthInterface_HTTPWithoutPath(t *testing.T) {
	c := validContract()
	c.Runtime.Health = contract.Health{Interface: "api", Path: ""}
	var result ValidationResult
	validateHealthInterface(c, &result)
	if result.IsValid() {
		t.Error("expected error for HTTP health interface without path")
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
