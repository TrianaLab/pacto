package contract_test

import (
	"os"
	"strings"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestParse_ValidMinimal(t *testing.T) {
	f, err := os.Open("testdata/valid_minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	c, err := contract.Parse(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if c.PactoVersion != "1.0" {
		t.Errorf("expected pactoVersion 1.0, got %s", c.PactoVersion)
	}
	if c.Service.Name != "my-service" {
		t.Errorf("expected service name my-service, got %s", c.Service.Name)
	}
	if c.Service.Version != "1.0.0" {
		t.Errorf("expected service version 1.0.0, got %s", c.Service.Version)
	}
	if len(c.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(c.Interfaces))
	}
	if c.Interfaces[0].Port == nil || *c.Interfaces[0].Port != 8080 {
		t.Error("expected interface port 8080")
	}
	if c.Runtime.Workload != "service" {
		t.Errorf("expected workload service, got %s", c.Runtime.Workload)
	}
	if c.Runtime.State.Type != "stateless" {
		t.Errorf("expected state type stateless, got %s", c.Runtime.State.Type)
	}
	if c.Runtime.State.Persistence.Durability != "ephemeral" {
		t.Errorf("expected persistence durability ephemeral, got %s", c.Runtime.State.Persistence.Durability)
	}
	if c.Runtime.State.DataCriticality != "low" {
		t.Errorf("expected dataCriticality low, got %s", c.Runtime.State.DataCriticality)
	}
	if c.Runtime.Health.Interface != "api" {
		t.Errorf("expected health interface api, got %s", c.Runtime.Health.Interface)
	}
}

func parseFullContract(t *testing.T) *contract.Contract {
	t.Helper()
	f, err := os.Open("testdata/valid_full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	c, err := contract.Parse(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return c
}

func TestParse_ValidFull_Service(t *testing.T) {
	c := parseFullContract(t)

	if len(c.Interfaces) != 3 {
		t.Errorf("expected 3 interfaces, got %d", len(c.Interfaces))
	}
	if c.Service.Image == nil {
		t.Fatal("expected image to be present")
	}
	if c.Service.Image.Ref != "ghcr.io/acme/payments-api:2.1.0" {
		t.Errorf("expected image ref, got %s", c.Service.Image.Ref)
	}
	if !c.Service.Image.Private {
		t.Error("expected image to be private")
	}
	if c.Configuration == nil {
		t.Fatal("expected configuration to be present")
	}
	if c.Configuration.Schema != "configuration/schema.json" {
		t.Errorf("expected configuration schema, got %s", c.Configuration.Schema)
	}
	if len(c.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies, got %d", len(c.Dependencies))
	}
}

func TestParse_ValidFull_Workload(t *testing.T) {
	c := parseFullContract(t)
	if c.Runtime.Workload != "service" {
		t.Errorf("expected workload service, got %s", c.Runtime.Workload)
	}
}

func TestParse_ValidFull_State(t *testing.T) {
	c := parseFullContract(t)
	if c.Runtime.State.Type != "stateful" {
		t.Errorf("expected state type stateful, got %s", c.Runtime.State.Type)
	}
	if c.Runtime.State.DataCriticality != "high" {
		t.Errorf("expected dataCriticality high, got %s", c.Runtime.State.DataCriticality)
	}
	if c.Runtime.State.Persistence.Scope != "local" {
		t.Errorf("expected persistence scope local, got %s", c.Runtime.State.Persistence.Scope)
	}
	if c.Runtime.State.Persistence.Durability != "persistent" {
		t.Errorf("expected persistence durability persistent, got %s", c.Runtime.State.Persistence.Durability)
	}
}

func TestParse_ValidFull_Lifecycle(t *testing.T) {
	c := parseFullContract(t)
	if c.Runtime.Lifecycle == nil {
		t.Fatal("expected lifecycle to be present")
	}
	if c.Runtime.Lifecycle.UpgradeStrategy != "ordered" {
		t.Errorf("expected upgradeStrategy ordered, got %s", c.Runtime.Lifecycle.UpgradeStrategy)
	}
	if c.Runtime.Lifecycle.GracefulShutdownSeconds == nil {
		t.Fatal("expected gracefulShutdownSeconds to be present")
	}
	if *c.Runtime.Lifecycle.GracefulShutdownSeconds != 30 {
		t.Errorf("expected gracefulShutdownSeconds 30, got %d", *c.Runtime.Lifecycle.GracefulShutdownSeconds)
	}
}

func TestParse_ValidFull_HealthAndScaling(t *testing.T) {
	c := parseFullContract(t)
	if c.Runtime.Health.InitialDelaySeconds == nil {
		t.Fatal("expected health initialDelaySeconds to be present")
	}
	if *c.Runtime.Health.InitialDelaySeconds != 15 {
		t.Errorf("expected health initialDelaySeconds 15, got %d", *c.Runtime.Health.InitialDelaySeconds)
	}
	if c.Scaling == nil {
		t.Fatal("expected scaling to be present")
	}
	if c.Scaling.Min != 2 {
		t.Errorf("expected scaling min 2, got %d", c.Scaling.Min)
	}
}

func TestParse_MissingPactoVersion(t *testing.T) {
	f, err := os.Open("testdata/invalid_missing_version.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	_, err = contract.Parse(f)
	if err == nil {
		t.Fatal("expected error for missing pactoVersion")
	}
	pe, ok := err.(*contract.ParseError)
	if !ok {
		t.Fatalf("expected ParseError, got %T", err)
	}
	if pe.Path != "pactoVersion" {
		t.Errorf("expected path pactoVersion, got %s", pe.Path)
	}
}

func TestParse_MissingServiceName(t *testing.T) {
	f, err := os.Open("testdata/invalid_missing_name.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	_, err = contract.Parse(f)
	if err == nil {
		t.Fatal("expected error for missing service.name")
	}
	pe, ok := err.(*contract.ParseError)
	if !ok {
		t.Fatalf("expected ParseError, got %T", err)
	}
	if pe.Path != "service.name" {
		t.Errorf("expected path service.name, got %s", pe.Path)
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	f, err := os.Open("testdata/invalid_yaml.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	_, err = contract.Parse(f)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if _, ok := err.(*contract.ParseError); !ok {
		t.Fatalf("expected ParseError, got %T", err)
	}
}

func TestParse_MissingServiceVersion(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
interfaces:
  - name: api
    type: http
    port: 8080
`)
	_, err := contract.Parse(r)
	if err == nil {
		t.Fatal("expected error for missing service.version")
	}
	pe, ok := err.(*contract.ParseError)
	if !ok {
		t.Fatalf("expected ParseError, got %T", err)
	}
	if pe.Path != "service.version" {
		t.Errorf("expected path service.version, got %s", pe.Path)
	}
}

func TestParse_ScalingReplicasNormalized(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
scaling:
  replicas: 3
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Scaling == nil {
		t.Fatal("expected scaling to be present")
	}
	if c.Scaling.Replicas == nil || *c.Scaling.Replicas != 3 {
		t.Error("expected replicas=3")
	}
	if c.Scaling.Min != 3 {
		t.Errorf("expected min=3 (normalized), got %d", c.Scaling.Min)
	}
	if c.Scaling.Max != 3 {
		t.Errorf("expected max=3 (normalized), got %d", c.Scaling.Max)
	}
}

func TestParse_MissingInterfaces(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("interfaces should be optional, got error: %v", err)
	}
	if len(c.Interfaces) != 0 {
		t.Errorf("expected 0 interfaces, got %d", len(c.Interfaces))
	}
}
