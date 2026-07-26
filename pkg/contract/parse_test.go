package contract_test

import (
	"os"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
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

	if c.PactoVersion != "2.0" {
		t.Errorf("expected pactoVersion 2.0, got %s", c.PactoVersion)
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
	if c.Interfaces[0].Type != contract.InterfaceTypeOpenAPI {
		t.Errorf("expected interface type openapi, got %s", c.Interfaces[0].Type)
	}
	if c.Interfaces[0].Ref != "interfaces/openapi.json" {
		t.Errorf("expected interface ref interfaces/openapi.json, got %s", c.Interfaces[0].Ref)
	}
	if c.State == nil {
		t.Fatal("expected state to be present")
	}
	if c.State.Type != "stateless" {
		t.Errorf("expected state type stateless, got %s", c.State.Type)
	}
	if c.State.Persistence.Durability != "ephemeral" {
		t.Errorf("expected persistence durability ephemeral, got %s", c.State.Persistence.Durability)
	}
	if c.State.DataCriticality != "low" {
		t.Errorf("expected dataCriticality low, got %s", c.State.DataCriticality)
	}
	if len(c.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(c.Capabilities))
	}
	if c.Capabilities[0].Type != contract.CapabilityHealth {
		t.Errorf("expected capability type health, got %s", c.Capabilities[0].Type)
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

	if c.Service.Name != "payments-api" {
		t.Errorf("expected service name payments-api, got %s", c.Service.Name)
	}
	if c.Service.Version != "2.1.0" {
		t.Errorf("expected service version 2.1.0, got %s", c.Service.Version)
	}
	if c.Service.Owner.Team != "Platform" {
		t.Errorf("expected owner team Platform, got %s", c.Service.Owner.Team)
	}
	if c.Service.Owner.DRI != "alice@example.com" {
		t.Errorf("expected owner dri alice@example.com, got %s", c.Service.Owner.DRI)
	}
	if len(c.Interfaces) != 3 {
		t.Errorf("expected 3 interfaces, got %d", len(c.Interfaces))
	}
	if len(c.Configurations) != 1 {
		t.Errorf("expected 1 configuration, got %d", len(c.Configurations))
	}
	if c.Configurations[0].Schema != "configuration/schema.json" {
		t.Errorf("expected configuration schema, got %s", c.Configurations[0].Schema)
	}
	if len(c.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies, got %d", len(c.Dependencies))
	}
}

func TestParse_ValidFull_Interfaces(t *testing.T) {
	c := parseFullContract(t)
	if c.Interfaces[0].Type != contract.InterfaceTypeOpenAPI {
		t.Errorf("expected first interface type openapi, got %s", c.Interfaces[0].Type)
	}
	if c.Interfaces[1].Type != contract.InterfaceTypeAsyncAPI {
		t.Errorf("expected second interface type asyncapi, got %s", c.Interfaces[1].Type)
	}
	if c.Interfaces[2].Type != contract.InterfaceTypeGRPC {
		t.Errorf("expected third interface type grpc, got %s", c.Interfaces[2].Type)
	}
	if c.Interfaces[0].Visibility != contract.VisibilityPublic {
		t.Errorf("expected first interface visibility public, got %s", c.Interfaces[0].Visibility)
	}
}

func TestParse_ValidFull_State(t *testing.T) {
	c := parseFullContract(t)
	if c.State == nil {
		t.Fatal("expected state to be present")
	}
	if c.State.Type != "stateful" {
		t.Errorf("expected state type stateful, got %s", c.State.Type)
	}
	if c.State.DataCriticality != "high" {
		t.Errorf("expected dataCriticality high, got %s", c.State.DataCriticality)
	}
	if c.State.Persistence.Scope != "local" {
		t.Errorf("expected persistence scope local, got %s", c.State.Persistence.Scope)
	}
	if c.State.Persistence.Durability != "persistent" {
		t.Errorf("expected persistence durability persistent, got %s", c.State.Persistence.Durability)
	}
}

func TestParse_ValidFull_Workload(t *testing.T) {
	c := parseFullContract(t)
	if c.Workload != contract.WorkloadService {
		t.Errorf("expected workload service, got %s", c.Workload)
	}
}

func TestParse_ValidFull_Capabilities(t *testing.T) {
	c := parseFullContract(t)
	if len(c.Capabilities) != 2 {
		t.Fatalf("expected 2 capabilities, got %d", len(c.Capabilities))
	}
	if c.Capabilities[0].Type != contract.CapabilityHealth {
		t.Errorf("expected first capability health, got %s", c.Capabilities[0].Type)
	}
	if c.Capabilities[1].Type != contract.CapabilityMetrics {
		t.Errorf("expected second capability metrics, got %s", c.Capabilities[1].Type)
	}
}

func TestParse_ValidFull_Policies(t *testing.T) {
	c := parseFullContract(t)
	if len(c.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(c.Policies))
	}
	if c.Policies[0].Name != "security" {
		t.Errorf("expected policy name security, got %s", c.Policies[0].Name)
	}
	if c.Policies[0].Target != contract.PolicyTargetContract {
		t.Errorf("expected policy target contract, got %s", c.Policies[0].Target)
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

func TestParse_UnsupportedVersion(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
`)
	_, err := contract.Parse(r)
	if err == nil {
		t.Fatal("expected error for unsupported pactoVersion")
	}
	pe, ok := err.(*contract.ParseError)
	if !ok {
		t.Fatalf("expected ParseError, got %T", err)
	}
	if pe.Path != "pactoVersion" {
		t.Errorf("expected path pactoVersion, got %s", pe.Path)
	}
	if !strings.Contains(pe.Message, "unsupported") || !strings.Contains(pe.Message, "2.0") {
		t.Errorf("expected unsupported version message, got %s", pe.Message)
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
pactoVersion: "2.0"
service:
  name: my-svc
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

func TestParse_ValidReadiness(t *testing.T) {
	f, err := os.Open("testdata/valid_readiness.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	c, err := contract.Parse(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.PactoVersion != "2.0" {
		t.Errorf("expected pactoVersion 2.0, got %s", c.PactoVersion)
	}
	if c.Readiness == nil {
		t.Fatal("expected readiness to be present")
	}
	if c.Readiness.Expires != "2099-12-31" {
		t.Errorf("unexpected assessment expires: %s", c.Readiness.Expires)
	}
	if len(c.Readiness.Claims) != 3 {
		t.Fatalf("expected 3 readiness claims, got %d", len(c.Readiness.Claims))
	}
	first := c.Readiness.Claims[0]
	type field struct {
		label string
		got   any
		want  any
	}
	firstFields := []field{
		{"first.ID", first.ID, "dashboard"},
		{"first.Type", first.Type, "url"},
		{"first.Weight", first.Weight, 20},
		{"first.Category", first.Category, "observability"},
		{"first.Status", first.Status, contract.StatusDone},
		{"first.Evidence", first.Evidence, "https://grafana.company.com/payment-api"},
		{"first.Description", first.Description, "Main production dashboard"},
	}
	for _, f := range firstFields {
		if f.got != f.want {
			t.Errorf("%s: got %v, want %v", f.label, f.got, f.want)
		}
	}
	if c.Readiness.Claims[1].Status != contract.StatusPartial {
		t.Errorf("expected partial status on second claim, got %s", c.Readiness.Claims[1].Status)
	}
	if c.Readiness.Claims[2].Status != contract.StatusNotDone {
		t.Errorf("expected not-done status on third claim, got %s", c.Readiness.Claims[2].Status)
	}
}

func TestParse_ReadinessUnknownFieldRejected(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "2.0"
service:
  name: my-svc
  version: "1.0.0"
readiness:
  expires: 2026-12-31
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://x
      weight: 20
      bogusField: nope
`)
	_, err := contract.Parse(r)
	if err == nil {
		t.Fatal("expected error for unknown readiness claim field")
	}
	if _, ok := err.(*contract.ParseError); !ok {
		t.Fatalf("expected ParseError, got %T", err)
	}
}

func TestParse_OptionalFields(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "2.0"
service:
  name: my-svc
  version: "1.0.0"
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("optional fields should be allowed, got error: %v", err)
	}
	if len(c.Interfaces) != 0 {
		t.Errorf("expected 0 interfaces, got %d", len(c.Interfaces))
	}
	if c.State != nil {
		t.Errorf("expected nil state, got %+v", c.State)
	}
	if c.Workload != "" {
		t.Errorf("expected empty workload, got %s", c.Workload)
	}
}
