package dashboard

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/diff"
	"github.com/trianalab/pacto/v2/pkg/graph"
	"github.com/trianalab/pacto/v2/pkg/validation"
)

func TestServiceFromContract(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Name:    "my-service",
			Version: "1.2.3",
			Owner:   contract.Owner{Team: "team-a"},
		},
	}

	svc := ServiceFromContract(c, "local")
	if svc.Name != "my-service" {
		t.Errorf("expected name 'my-service', got %q", svc.Name)
	}
	if svc.Version != "1.2.3" {
		t.Errorf("expected version '1.2.3', got %q", svc.Version)
	}
	if svc.Owner.DisplayString() != "team-a" {
		t.Errorf("expected owner 'team-a', got %q", svc.Owner.DisplayString())
	}
	if svc.Source != "local" {
		t.Errorf("expected source 'local', got %q", svc.Source)
	}
	if svc.ContractStatus != StatusUnknown {
		t.Errorf("expected status Unknown, got %q", svc.ContractStatus)
	}
}

func TestServiceDetailsFromBundle_Interfaces(t *testing.T) {
	port := 8080
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: &port, Visibility: "public", Contract: "openapi.yaml"},
		},
	}

	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(details.Interfaces))
	}
	iface := details.Interfaces[0]
	if iface.Name != "api" {
		t.Errorf("expected interface name 'api', got %q", iface.Name)
	}
	if !iface.HasContractFile {
		t.Error("expected HasContractFile to be true")
	}
}

func TestServiceDetailsFromBundle_Runtime(t *testing.T) {
	shutdown := 30
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Runtime: &contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:            "stateful",
				DataCriticality: "high",
				Persistence: contract.Persistence{
					Scope:      "shared",
					Durability: "persistent",
				},
			},
			Lifecycle: &contract.Lifecycle{
				UpgradeStrategy:         "rolling",
				GracefulShutdownSeconds: &shutdown,
			},
			Health: &contract.Health{
				Interface: "api",
				Path:      "/healthz",
			},
		},
	}

	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Runtime == nil {
		t.Fatal("expected runtime to be set")
	}
	if details.Runtime.Workload != "service" {
		t.Errorf("expected workload 'service', got %q", details.Runtime.Workload)
	}
	if details.Runtime.StateType != "stateful" {
		t.Errorf("expected state 'stateful', got %q", details.Runtime.StateType)
	}
	if details.Runtime.UpgradeStrategy != "rolling" {
		t.Errorf("expected strategy 'rolling', got %q", details.Runtime.UpgradeStrategy)
	}
	if *details.Runtime.GracefulShutdownSeconds != 30 {
		t.Errorf("expected shutdown 30, got %d", *details.Runtime.GracefulShutdownSeconds)
	}
}

func TestDiffResultFromEngine(t *testing.T) {
	r := &diff.Result{
		Classification: diff.Breaking,
		Changes: []diff.Change{
			{
				Path:           "service.version",
				Type:           diff.Modified,
				OldValue:       "1.0.0",
				NewValue:       "2.0.0",
				Classification: diff.Breaking,
				Reason:         "major version bump",
			},
		},
	}

	from := Ref{Name: "svc", Version: "1.0.0"}
	to := Ref{Name: "svc", Version: "2.0.0"}

	dr := DiffResultFromEngine(from, to, r)
	if dr.Classification != "BREAKING" {
		t.Errorf("expected BREAKING, got %q", dr.Classification)
	}
	if len(dr.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(dr.Changes))
	}
	if dr.Changes[0].Type != "modified" {
		t.Errorf("expected 'modified', got %q", dr.Changes[0].Type)
	}
}

// flattenValues / flattenSchemaProps behavior is now tested in pkg/schemax
// (shared with the operator); see schemax_test.go.

func TestServiceDetailsFromBundle_Dependencies(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			{Ref: "oci://ghcr.io/org/db:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Ref: "cache-svc", Required: false},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(details.Dependencies))
	}
	if details.Dependencies[0].Ref != "oci://ghcr.io/org/db:1.0.0" {
		t.Errorf("expected ref, got %q", details.Dependencies[0].Ref)
	}
	if !details.Dependencies[0].Required {
		t.Error("expected first dep required=true")
	}
	if details.Dependencies[0].Compatibility != "^1.0.0" {
		t.Errorf("expected compatibility '^1.0.0', got %q", details.Dependencies[0].Compatibility)
	}
}

func TestServiceDetailsFromBundle_Configuration(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{
			{
				Name:   "default",
				Schema: "config.schema.json",
				Ref:    "shared-config",
				Values: map[string]any{
					"port":    float64(8080),
					"enabled": true,
				},
			},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Configurations) != 1 {
		t.Fatal("expected 1 configuration entry")
	}
	cfg := details.Configurations[0]
	if !cfg.HasSchema {
		t.Error("expected HasSchema=true")
	}
	if cfg.Schema != "config.schema.json" {
		t.Errorf("expected schema path, got %q", cfg.Schema)
	}
	if cfg.Ref != "shared-config" {
		t.Errorf("expected ref 'shared-config', got %q", cfg.Ref)
	}
	if len(cfg.Values) != 2 {
		t.Fatalf("expected 2 config values, got %d", len(cfg.Values))
	}
}

func TestServiceDetailsFromBundle_Scaling(t *testing.T) {
	replicas := 3
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Scaling: &contract.Scaling{
			Replicas: &replicas,
			Min:      2,
			Max:      5,
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Scaling == nil {
		t.Fatal("expected scaling to be set")
	}
	if details.Scaling.Replicas == nil || *details.Scaling.Replicas != 3 {
		t.Error("expected replicas=3")
	}
	if details.Scaling.Min == nil || *details.Scaling.Min != 2 {
		t.Error("expected min=2")
	}
	if details.Scaling.Max == nil || *details.Scaling.Max != 5 {
		t.Error("expected max=5")
	}
}

func TestServiceDetailsFromBundle_Policy(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Policies: []contract.PolicySource{{
			Schema: "policy.schema.json",
			Ref:    "shared-policy",
		}},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Policies) != 1 {
		t.Fatal("expected 1 policy entry")
	}
	if !details.Policies[0].HasSchema {
		t.Error("expected HasSchema=true")
	}
	if details.Policies[0].Schema != "policy.schema.json" {
		t.Errorf("expected schema, got %q", details.Policies[0].Schema)
	}
	if details.Policies[0].Ref != "shared-policy" {
		t.Errorf("expected ref, got %q", details.Policies[0].Ref)
	}
}

func TestServiceDetailsFromBundle_Metadata(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Metadata: map[string]any{
			"team": "platform",
			"tier": "backend",
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Metadata == nil {
		t.Fatal("expected metadata to be set")
	}
	if details.Metadata["team"] != "platform" {
		t.Errorf("expected team='platform', got %q", details.Metadata["team"])
	}
	if details.Metadata["tier"] != "backend" {
		t.Errorf("expected tier='backend', got %q", details.Metadata["tier"])
	}
}

func TestServiceDetailsFromBundle_Metadata_NonStringSkipped(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Metadata: map[string]any{
			"team":  "platform",
			"count": 42, // non-string, should be skipped
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Metadata) != 1 {
		t.Errorf("expected 1 metadata entry (non-string skipped), got %d", len(details.Metadata))
	}
}

func TestServiceDetailsFromBundle_ImageAndChart(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Name:    "svc",
			Version: "1.0.0",
			Image:   &contract.Image{Ref: "ghcr.io/org/svc:1.0.0"},
			Chart:   &contract.Chart{Ref: "oci://charts/svc", Version: "1.0.0"},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "oci")
	if details.ImageRef != "ghcr.io/org/svc:1.0.0" {
		t.Errorf("expected imageRef, got %q", details.ImageRef)
	}
	if details.ChartRef != "oci://charts/svc" {
		t.Errorf("expected chartRef, got %q", details.ChartRef)
	}
}

func TestGraphFromResult_Nil(t *testing.T) {
	result := graphFromResult(nil)
	if result != nil {
		t.Error("expected nil for nil input")
	}
}

func TestGraphFromResult_NilRoot(t *testing.T) {
	result := graphFromResult(&graph.Result{Root: nil})
	if result != nil {
		t.Error("expected nil for nil root")
	}
}

func TestGraphFromResult_Basic(t *testing.T) {
	r := &graph.Result{
		Root: &graph.Node{
			Name:    "svc",
			Version: "1.0.0",
			Dependencies: []graph.Edge{
				{
					Ref:      "dep-svc",
					Required: true,
					Node:     &graph.Node{Name: "dep-svc", Version: "2.0.0"},
				},
			},
		},
		Cycles: [][]string{{"a", "b", "a"}},
		Conflicts: []graph.Conflict{
			{Name: "dep-svc", Versions: []string{"1.0.0", "2.0.0"}},
		},
	}

	g := graphFromResult(r)
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	if g.Root.Name != "svc" {
		t.Errorf("expected root name 'svc', got %q", g.Root.Name)
	}
	if len(g.Root.Dependencies) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(g.Root.Dependencies))
	}
	if g.Root.Dependencies[0].Node.Name != "dep-svc" {
		t.Errorf("expected dep name 'dep-svc', got %q", g.Root.Dependencies[0].Node.Name)
	}
	if len(g.Cycles) != 1 {
		t.Errorf("expected 1 cycle, got %d", len(g.Cycles))
	}
	if len(g.Conflicts) != 1 {
		t.Errorf("expected 1 conflict, got %d", len(g.Conflicts))
	}
}

func TestValidationInfoFromResult_WithErrors(t *testing.T) {
	// Test that validation errors and warnings are mapped correctly
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
	}
	yaml := `invalid yaml content here: [[[`
	bundle := &contract.Bundle{Contract: c, RawYAML: []byte(yaml)}
	details := ServiceDetailsFromBundle(bundle, "local")
	// The validation result should exist regardless
	if details.Validation == nil {
		t.Fatal("expected validation to be present")
	}
}

func TestServiceDetailsFromBundle_InterfaceNilPort(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: nil, Visibility: "public"},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(details.Interfaces))
	}
	if details.Interfaces[0].Port != nil {
		t.Error("expected nil port")
	}
}

func TestServiceDetailsFromBundle_ScalingNilFields(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Scaling: &contract.Scaling{
			Replicas: nil,
			Min:      0,
			Max:      0,
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Scaling == nil {
		t.Fatal("expected scaling to be set")
	}
	if details.Scaling.Replicas != nil {
		t.Error("expected nil replicas")
	}
	if details.Scaling.Min != nil {
		t.Error("expected nil min (0 means not set)")
	}
	if details.Scaling.Max != nil {
		t.Error("expected nil max (0 means not set)")
	}
}

func TestServiceDetailsFromBundle_ConfigurationSchemaExtract(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{
			{Name: "default", Schema: "config.schema.json"},
		},
	}
	fsys := fstest.MapFS{
		"config.schema.json": &fstest.MapFile{
			Data: []byte(`{
				"type": "object",
				"properties": {
					"port": {"type": "integer", "default": 8080}
				}
			}`),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if len(details.Configurations) != 1 {
		t.Fatal("expected 1 configuration entry")
	}
	cfg := details.Configurations[0]
	if len(cfg.Values) != 1 {
		t.Fatalf("expected 1 config value from schema, got %d", len(cfg.Values))
	}
	if cfg.Values[0].Key != "port" {
		t.Errorf("expected key 'port', got %q", cfg.Values[0].Key)
	}
}

func TestServiceDetailsFromBundle_PolicyContent(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Policies: []contract.PolicySource{{
			Ref: "policy.yaml",
		}},
	}
	fsys := fstest.MapFS{
		"policy.yaml": &fstest.MapFile{
			Data: []byte("enforce: true\nlevel: strict\n"),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if len(details.Policies) != 1 {
		t.Fatal("expected 1 policy entry")
	}
	if details.Policies[0].Content == "" {
		t.Error("expected policy content to be populated")
	}
	if len(details.Policies[0].Values) == 0 {
		t.Error("expected policy values parsed from content")
	}
}

func TestServiceDetailsFromBundle_PolicySchemaFallback(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Policies: []contract.PolicySource{{
			Schema: "policy.schema.json",
		}},
	}
	fsys := fstest.MapFS{
		"policy.schema.json": &fstest.MapFile{
			Data: []byte(`{
				"type": "object",
				"properties": {
					"enforce": {"type": "boolean", "default": true}
				}
			}`),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if len(details.Policies) != 1 {
		t.Fatal("expected 1 policy entry")
	}
	if len(details.Policies[0].Values) != 1 {
		t.Fatalf("expected 1 value from schema, got %d", len(details.Policies[0].Values))
	}
}

func TestServiceDetailsFromBundle_PolicyProviderAutoDetect(t *testing.T) {
	// Bundle has policy/schema.json but no policies declared in contract.
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "shared-policy", Version: "1.0.0"},
	}
	fsys := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{
			Data: []byte(`{
				"title": "Platform HTTP Policy",
				"description": "Enforces platform standards",
				"type": "object",
				"properties": {
					"service": {"type": "object", "required": ["owner"]}
				},
				"required": ["service"]
			}`),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "oci")
	if len(details.Policies) != 1 {
		t.Fatalf("expected 1 auto-detected policy, got %d", len(details.Policies))
	}
	if !details.Policies[0].HasSchema {
		t.Error("expected HasSchema=true")
	}
	if details.Policies[0].Schema != validation.PolicySchemaPath {
		t.Errorf("expected schema=%q, got %q", validation.PolicySchemaPath, details.Policies[0].Schema)
	}
	if details.Policies[0].Content == "" {
		t.Error("expected content to be populated")
	}
	if len(details.Policies[0].Values) == 0 {
		t.Error("expected values extracted from schema")
	}
	if details.Policies[0].Title != "Platform HTTP Policy" {
		t.Errorf("expected title, got %q", details.Policies[0].Title)
	}
	if details.Policies[0].Description != "Enforces platform standards" {
		t.Errorf("expected description, got %q", details.Policies[0].Description)
	}
}

func TestServiceDetailsFromBundle_PolicyProviderAutoDetectNoProperties(t *testing.T) {
	// Auto-detected policy schema with no "properties" — falls back to parseContentAsValues.
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "shared-policy", Version: "1.0.0"},
	}
	fsys := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{
			Data: []byte(`{"title": "Simple", "type": "boolean"}`),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "oci")
	if len(details.Policies) != 1 {
		t.Fatalf("expected 1 auto-detected policy, got %d", len(details.Policies))
	}
	if details.Policies[0].Title != "Simple" {
		t.Errorf("expected title 'Simple', got %q", details.Policies[0].Title)
	}
}

func TestServiceDetailsFromBundle_PolicyProviderNoAutoDetectWhenDeclared(t *testing.T) {
	// Bundle has policy/schema.json AND explicit policies — use declared only.
	c := &contract.Contract{
		Service:  contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Policies: []contract.PolicySource{{Ref: "other-policy"}},
	}
	fsys := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{
			Data: []byte(`{"type":"object"}`),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if len(details.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(details.Policies))
	}
	if details.Policies[0].Ref != "other-policy" {
		t.Errorf("expected declared policy ref, got %q", details.Policies[0].Ref)
	}
}

func TestValidationInfoFromResult_WithWarnings(t *testing.T) {
	r := validation.ValidationResult{
		Warnings: []contract.ValidationWarning{
			{Code: "W001", Path: "service.owner", Message: "owner is recommended"},
		},
	}
	vi := validationInfoFromResult(r)
	if !vi.Valid {
		t.Error("expected valid=true (no errors)")
	}
	if len(vi.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(vi.Warnings))
	}
	if vi.Warnings[0].Code != "W001" {
		t.Errorf("expected warning code 'W001', got %q", vi.Warnings[0].Code)
	}
}

func TestValidationInfoFromResult_ValidNoIssues(t *testing.T) {
	r := validation.ValidationResult{}
	vi := validationInfoFromResult(r)
	if !vi.Valid {
		t.Error("expected valid=true")
	}
	if len(vi.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(vi.Errors))
	}
	if len(vi.Warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d", len(vi.Warnings))
	}
}

func TestMapGraphNode_NilEdgeNode(t *testing.T) {
	n := &graph.Node{
		Name:    "root",
		Version: "1.0.0",
		Dependencies: []graph.Edge{
			{
				Ref:      "missing-dep",
				Required: true,
				Error:    "not found",
				Node:     nil,
			},
		},
	}
	gn := mapGraphNode(n)
	if gn == nil {
		t.Fatal("expected non-nil graph node")
	}
	if len(gn.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(gn.Dependencies))
	}
	if gn.Dependencies[0].Node != nil {
		t.Error("expected nil node for edge with nil source node")
	}
	if gn.Dependencies[0].Error != "not found" {
		t.Errorf("expected error 'not found', got %q", gn.Dependencies[0].Error)
	}
}

func TestServiceDetailsFromBundle_InterfaceContractContentFallback(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "grpc", Contract: "service.proto"},
		},
	}
	fsys := fstest.MapFS{
		"service.proto": &fstest.MapFile{
			Data: []byte("syntax = \"proto3\";\nservice MyService {}"),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if len(details.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(details.Interfaces))
	}
	if details.Interfaces[0].ContractContent == "" {
		t.Error("expected raw contract content as fallback")
	}
}

func TestExtractSchemaProperties_PropertiesNotMapValue(t *testing.T) {
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{
			Data: []byte(`{"type": "object", "properties": [1, 2, 3]}`),
		},
	}
	values := extractSchemaProperties(fsys, "schema.json")
	if len(values) != 0 {
		t.Errorf("expected 0 values when properties is an array, got %d", len(values))
	}
}

func TestServiceDetailsFromBundle_ValidationValid(t *testing.T) {
	yamlContent := `pactoVersion: "1.0"
service:
  name: svc
  version: 1.0.0
`
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c, RawYAML: []byte(yamlContent)}, "local")
	if details.Validation == nil {
		t.Fatal("expected validation to be set")
	}
}

func TestServiceDetailsFromBundle_RuntimeMetrics(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Runtime: &contract.Runtime{
			Workload: "service",
			Metrics: &contract.Metrics{
				Interface: "api",
				Path:      "/metrics",
			},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Runtime == nil {
		t.Fatal("expected runtime")
	}
	if details.Runtime.MetricsInterface != "api" {
		t.Errorf("expected metrics interface 'api', got %q", details.Runtime.MetricsInterface)
	}
	if details.Runtime.MetricsPath != "/metrics" {
		t.Errorf("expected metrics path '/metrics', got %q", details.Runtime.MetricsPath)
	}
}

func TestExtractSchemaProperties_ValidSchema(t *testing.T) {
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{
			Data: []byte(`{
				"type": "object",
				"properties": {
					"port": {"type": "integer", "default": 8080},
					"host": {"type": "string"}
				}
			}`),
		},
	}
	values := extractSchemaProperties(fsys, "schema.json")
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
}

func TestExtractSchemaProperties_NoProperties(t *testing.T) {
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{
			Data: []byte(`{"type": "object"}`),
		},
	}
	values := extractSchemaProperties(fsys, "schema.json")
	if len(values) != 0 {
		t.Errorf("expected 0 values (no properties key), got %d", len(values))
	}
}

func TestExtractSchemaProperties_FileNotFound(t *testing.T) {
	fsys := fstest.MapFS{}
	values := extractSchemaProperties(fsys, "missing.json")
	if len(values) != 0 {
		t.Errorf("expected 0 values for missing file, got %d", len(values))
	}
}

func TestExtractSchemaProperties_InvalidJSON(t *testing.T) {
	fsys := fstest.MapFS{
		"schema.json": &fstest.MapFile{Data: []byte(`not json`)},
	}
	values := extractSchemaProperties(fsys, "schema.json")
	if len(values) != 0 {
		t.Errorf("expected 0 values for invalid JSON, got %d", len(values))
	}
}

func TestParseContentAsValues_YAML(t *testing.T) {
	data := []byte("port: 8080\nhost: localhost\n")
	values := parseContentAsValues(data, "config.yaml")
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
}

func TestParseContentAsValues_JSON(t *testing.T) {
	data := []byte(`{"port": 8080, "host": "localhost"}`)
	values := parseContentAsValues(data, "config.json")
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
}

func TestParseContentAsValues_EmptyContent(t *testing.T) {
	data := []byte("")
	values := parseContentAsValues(data, "config.yaml")
	if values != nil {
		t.Errorf("expected nil for empty content, got %v", values)
	}
}

func TestServiceDetailsFromBundle_ConfigurationValuesWithKeys(t *testing.T) {
	// Ensure ValueKeys is populated when Configuration has inline Values.
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{
			{
				Name: "default",
				Values: map[string]any{
					"port": float64(8080),
				},
			},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Configurations) != 1 {
		t.Fatal("expected 1 configuration entry")
	}
	if len(details.Configurations[0].ValueKeys) != 1 {
		t.Fatalf("expected 1 value key, got %d", len(details.Configurations[0].ValueKeys))
	}
}

func TestServiceDetailsFromBundle_LargeContractContentTruncated(t *testing.T) {
	// Test that large contract content (>10KB) is truncated in fallback.
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "grpc", Contract: "big.proto"},
		},
	}
	// Create a file larger than 10KB
	bigContent := make([]byte, 11000)
	for i := range bigContent {
		bigContent[i] = 'x'
	}
	fsys := fstest.MapFS{
		"big.proto": &fstest.MapFile{Data: bigContent},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if len(details.Interfaces[0].ContractContent) != 10240+len("\n... (truncated)") {
		t.Errorf("expected truncated content at 10240+suffix, got length %d", len(details.Interfaces[0].ContractContent))
	}
}

func TestServiceDetailsFromBundle_LargePolicyContentTruncated(t *testing.T) {
	// Test that large policy content (>10KB) is truncated.
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Policies: []contract.PolicySource{{
			Ref: "policy.yaml",
		}},
	}
	bigContent := make([]byte, 11000)
	for i := range bigContent {
		bigContent[i] = 'y'
	}
	fsys := fstest.MapFS{
		"policy.yaml": &fstest.MapFile{Data: bigContent},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if len(details.Policies) != 1 {
		t.Fatal("expected 1 policy entry")
	}
	if len(details.Policies[0].Content) != 10240+len("\n... (truncated)") {
		t.Errorf("expected truncated content, got length %d", len(details.Policies[0].Content))
	}
}

func TestServiceDetailsFromBundle_InterfaceOpenAPIEndpoints(t *testing.T) {
	// Test interface with a valid OpenAPI spec that yields endpoints.
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Contract: "openapi.yaml"},
		},
	}
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{
			Data: []byte(`openapi: "3.0.0"
info:
  title: Test API
  version: "1.0.0"
paths:
  /health:
    get:
      summary: Health check
`),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if len(details.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(details.Interfaces))
	}
	if len(details.Interfaces[0].Endpoints) == 0 {
		t.Error("expected endpoints parsed from OpenAPI spec")
	}
}

func TestContractStatusFromBundle(t *testing.T) {
	t.Run("nil RawYAML returns unknown", func(t *testing.T) {
		b := &contract.Bundle{
			Contract: &contract.Contract{
				Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			},
		}
		if got := contractStatusFromBundle(b); got != StatusUnknown {
			t.Errorf("expected StatusUnknown, got %v", got)
		}
	})

	t.Run("valid contract returns compliant", func(t *testing.T) {
		raw := []byte(`pactoVersion: "1.0"
service:
  name: svc
  version: 1.0.0
`)
		c, _ := contract.Parse(bytes.NewReader(raw))
		b := &contract.Bundle{Contract: c, RawYAML: raw}
		if got := contractStatusFromBundle(b); got != StatusCompliant {
			t.Errorf("expected StatusCompliant, got %v", got)
		}
	})

	t.Run("invalid contract returns non-compliant", func(t *testing.T) {
		// Missing required service.version field triggers validation error.
		raw := []byte(`pactoVersion: "1.0"
service:
  name: svc
`)
		c := &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: "svc"},
		}
		b := &contract.Bundle{Contract: c, RawYAML: raw}
		if got := contractStatusFromBundle(b); got != StatusNonCompliant {
			t.Errorf("expected StatusNonCompliant, got %v", got)
		}
	})
}

func TestServiceFromContract_StructuredOwner(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Name:    "my-service",
			Version: "1.0.0",
			Owner:   contract.Owner{Team: "foundations", DRI: "alice"},
		},
	}

	svc := ServiceFromContract(c, "oci")
	if svc.Owner.Team != "foundations" {
		t.Errorf("expected team 'foundations', got %q", svc.Owner.Team)
	}
	if svc.Owner.DRI != "alice" {
		t.Errorf("expected dri 'alice', got %q", svc.Owner.DRI)
	}
	if svc.Owner.DisplayString() != "foundations" {
		t.Errorf("expected display 'foundations', got %q", svc.Owner.DisplayString())
	}
}

func TestServiceFromContract_EmptyOwner(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Name:    "my-service",
			Version: "1.0.0",
		},
	}

	svc := ServiceFromContract(c, "local")
	if !svc.Owner.IsEmpty() {
		t.Error("expected empty owner")
	}
	if svc.Owner.DisplayString() != "" {
		t.Errorf("expected empty display, got %q", svc.Owner.DisplayString())
	}
}

func TestServiceDetailsFromBundle_MultiConfig(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{
			{Name: "app", Schema: "config/app.json", Values: map[string]any{"PORT": float64(8080)}},
			{Name: "db", Ref: "oci://ghcr.io/acme/db-config:1.0.0"},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Configurations) != 2 {
		t.Fatalf("expected 2 configuration entries, got %d", len(details.Configurations))
	}
	app := details.Configurations[0]
	if app.Name != "app" {
		t.Errorf("expected name 'app', got %q", app.Name)
	}
	if !app.HasSchema {
		t.Error("expected HasSchema=true for app config")
	}
	if len(app.Values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(app.Values))
	}

	db := details.Configurations[1]
	if db.Name != "db" {
		t.Errorf("expected name 'db', got %q", db.Name)
	}
	if db.Ref != "oci://ghcr.io/acme/db-config:1.0.0" {
		t.Errorf("expected ref, got %q", db.Ref)
	}
}

func TestServiceDetailsFromBundle_NilConfiguration(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Configurations) != 0 {
		t.Errorf("expected 0 configurations, got %d", len(details.Configurations))
	}
}

func TestServiceDetailsFromBundle_EmptyConfiguration(t *testing.T) {
	// Configurations present but empty slice → returns empty.
	c := &contract.Contract{
		Service:        contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Configurations: []contract.ConfigurationSource{},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(details.Configurations) != 0 {
		t.Errorf("expected 0 configurations for empty Configuration, got %d", len(details.Configurations))
	}
}

// --- Readiness mapping ---

func pinDashboardTime(t *testing.T, at time.Time) {
	t.Helper()
	old := timeNow
	timeNow = func() time.Time { return at }
	t.Cleanup(func() { timeNow = old })
}

func TestReadinessFromContract_Nil(t *testing.T) {
	if got := readinessFromContract(&contract.Contract{}, nil); got != nil {
		t.Errorf("expected nil readiness for contract without readiness, got %+v", got)
	}
}

func TestServiceDetailsFromBundle_Readiness(t *testing.T) {
	pinDashboardTime(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	c := &contract.Contract{
		PactoVersion: "1.2",
		Service:      contract.ServiceIdentity{Name: "payment-api", Version: "1.4.0"},
		Readiness: &contract.Readiness{
			Expires: "2099-12-31",
			Checks: []contract.ReadinessCheck{
				{ID: "dashboard", Type: "url", Category: "observability", Status: "done", Evidence: "https://x", Weight: 60, Description: "Main"},
				{ID: "security-review", Type: "ticket", Status: "not-done", Evidence: "SEC-1", Weight: 40},
			},
			History: []contract.ReadinessRevision{
				{Date: "2026-06-21", Version: "2.1.0", Author: "ed", Description: "initial"},
			},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Readiness == nil {
		t.Fatal("expected readiness info to be present")
	}
	r := details.Readiness
	type field struct {
		label string
		got   any
		want  any
	}
	fields := []field{
		{"score", r.Score, 60},
		{"totalWeight", r.TotalWeight, 100},
		{"earnedWeight", r.EarnedWeight, 60},
		{"expires", r.Expires, "2099-12-31"},
		{"expired", r.Expired, false},
		{"minScore", r.MinScore, 100},
		{"passing", r.Passing, false},
		{"doneCount", r.DoneCount, 1},
		{"notDoneCount", r.NotDoneCount, 1},
		{"partialCount", r.PartialCount, 0},
		{"deferredCount", r.DeferredCount, 0},
		{"checks length", len(r.Checks), 2},
	}
	for _, f := range fields {
		if f.got != f.want {
			t.Errorf("%s: got %v, want %v", f.label, f.got, f.want)
		}
	}
	if len(r.Checks) > 0 && (r.Checks[0].Status != "done" || r.Checks[0].Category != "observability" || r.Checks[0].EarnedWeight != 60) {
		t.Errorf("unexpected first check: %+v", r.Checks[0])
	}
	if len(r.Checks) > 1 && (r.Checks[1].Status != "not-done" || r.Checks[1].EarnedWeight != 0) {
		t.Errorf("unexpected second check: %+v", r.Checks[1])
	}
	if len(r.Revisions) != 1 || r.Revisions[0].Author != "ed" {
		t.Errorf("expected mapped revision history, got %+v", r.Revisions)
	}
}

// --- in-bundle docs ---

// readFailDocsFS lists files (via embedded MapFS ReadDir) but fails ReadFile for
// .md files, to exercise the "skip unreadable doc" path.
type readFailDocsFS struct{ fstest.MapFS }

func (f readFailDocsFS) ReadFile(name string) ([]byte, error) {
	if strings.HasSuffix(name, ".md") {
		return nil, fmt.Errorf("read denied: %s", name)
	}
	return f.MapFS.ReadFile(name)
}

func TestDocsFromContract_NilFS(t *testing.T) {
	if got := docsFromContract(nil); got != nil {
		t.Errorf("expected nil for nil FS, got %+v", got)
	}
}

func TestDocsFromContract_NoDocsDir(t *testing.T) {
	fsys := fstest.MapFS{"pacto.yaml": &fstest.MapFile{Data: []byte("x")}}
	if got := docsFromContract(fsys); len(got) != 0 {
		t.Errorf("expected no docs, got %+v", got)
	}
}

func TestDocsFromContract_ReadsSortedTitlesIgnoresNonMd(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/overview.md":        &fstest.MapFile{Data: []byte("# Overview\n\nhi")},
		"docs/runbooks/deploy.md": &fstest.MapFile{Data: []byte("no h1 here")},
		"docs/notes.txt":          &fstest.MapFile{Data: []byte("ignored")},
	}
	got := docsFromContract(fsys)
	if len(got) != 2 {
		t.Fatalf("expected 2 md docs, got %d (%+v)", len(got), got)
	}
	if got[0].Path != "docs/overview.md" || got[0].Title != "Overview" {
		t.Errorf("unexpected first doc: %+v", got[0])
	}
	if got[0].Content == "" || got[0].Truncated {
		t.Errorf("unexpected content/truncation: %+v", got[0])
	}
	if got[1].Path != "docs/runbooks/deploy.md" || got[1].Title != "deploy" {
		t.Errorf("expected filename title 'deploy', got %+v", got[1])
	}
}

func TestDocsFromContract_PerDocTruncation(t *testing.T) {
	old := maxDocBytes
	maxDocBytes = 5
	defer func() { maxDocBytes = old }()
	fsys := fstest.MapFS{"docs/big.md": &fstest.MapFile{Data: []byte("0123456789")}}
	got := docsFromContract(fsys)
	if len(got) != 1 || !got[0].Truncated || got[0].Content != "01234" {
		t.Errorf("expected truncated '01234', got %+v", got)
	}
}

func TestDocsFromContract_CountCap(t *testing.T) {
	old := maxDocCount
	maxDocCount = 1
	defer func() { maxDocCount = old }()
	fsys := fstest.MapFS{
		"docs/a.md": &fstest.MapFile{Data: []byte("# A")},
		"docs/b.md": &fstest.MapFile{Data: []byte("# B")},
	}
	if got := docsFromContract(fsys); len(got) != 1 {
		t.Errorf("expected count cap to 1, got %d", len(got))
	}
}

func TestDocsFromContract_TotalCap(t *testing.T) {
	old := maxTotalDocBytes
	maxTotalDocBytes = 6
	defer func() { maxTotalDocBytes = old }()
	fsys := fstest.MapFS{
		"docs/a.md": &fstest.MapFile{Data: []byte("aaaa")},
		"docs/b.md": &fstest.MapFile{Data: []byte("bbbb")},
	}
	got := docsFromContract(fsys)
	if len(got) != 2 {
		t.Fatalf("expected 2 docs, got %d (%+v)", len(got), got)
	}
	if got[1].Content != "bb" || !got[1].Truncated {
		t.Errorf("expected second doc trimmed to 'bb' truncated, got %+v", got[1])
	}
}

func TestDocsFromContract_UnreadableFileSkipped(t *testing.T) {
	fsys := readFailDocsFS{fstest.MapFS{"docs/a.md": &fstest.MapFile{Data: []byte("# A")}}}
	if got := docsFromContract(fsys); len(got) != 0 {
		t.Errorf("expected unreadable doc skipped, got %+v", got)
	}
}

func TestServiceDetailsFromBundle_ReadinessDocPath(t *testing.T) {
	pinDashboardTime(t, time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	c := &contract.Contract{
		PactoVersion: "1.2",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Readiness: &contract.Readiness{
			Expires: "2099-12-31",
			Checks: []contract.ReadinessCheck{
				{ID: "runbook", Type: "document", Status: "done", Evidence: "docs/runbooks/deploy.md", Weight: 50},
				{ID: "dashboard", Type: "url", Status: "done", Evidence: "https://grafana/x", Weight: 50},
			},
		},
	}
	fsys := fstest.MapFS{"docs/runbooks/deploy.md": &fstest.MapFile{Data: []byte("# Deploy")}}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c, FS: fsys}, "local")
	if len(details.Docs) != 1 || details.Docs[0].Path != "docs/runbooks/deploy.md" {
		t.Fatalf("expected 1 doc, got %+v", details.Docs)
	}
	if details.Readiness == nil || details.Readiness.Checks[0].DocPath != "docs/runbooks/deploy.md" {
		t.Errorf("expected runbook DocPath set, got %+v", details.Readiness)
	}
	if details.Readiness.Checks[1].DocPath != "" {
		t.Errorf("expected url check DocPath empty, got %q", details.Readiness.Checks[1].DocPath)
	}
}

func mustParse(t *testing.T, data []byte) *contract.Contract {
	t.Helper()
	c, err := contract.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestServiceDetailsFromBundle_PopulatesSBOM(t *testing.T) {
	spdx := `{"spdxVersion":"SPDX-2.3","packages":[{"name":"libfoo","versionInfo":"1.2.3","licenseConcluded":"MIT"}]}`
	fsys := fstest.MapFS{
		"pacto.yaml":            &fstest.MapFile{Data: []byte("pactoVersion: \"1.2\"\nservice:\n  name: svc\n  version: 1.0.0\n")},
		"sbom/sbom.spdx.json":   &fstest.MapFile{Data: []byte(spdx)},
	}
	c, err := contract.Parse(bytes.NewReader(fsys["pacto.yaml"].Data))
	if err != nil {
		t.Fatal(err)
	}
	b := &contract.Bundle{Contract: c, RawYAML: fsys["pacto.yaml"].Data, FS: fsys}
	d := ServiceDetailsFromBundle(b, "local")
	if d.SBOM == nil {
		t.Fatal("expected SBOM populated")
	}
	if d.SBOM.Format != "spdx" || len(d.SBOM.Packages) != 1 || d.SBOM.Packages[0].Name != "libfoo" {
		t.Fatalf("unexpected SBOM: %+v", d.SBOM)
	}
}

func TestServiceDetailsFromBundle_NoSBOM(t *testing.T) {
	fsys := fstest.MapFS{
		"pacto.yaml": &fstest.MapFile{Data: []byte("pactoVersion: \"1.2\"\nservice:\n  name: svc\n  version: 1.0.0\n")},
	}
	b := &contract.Bundle{Contract: mustParse(t, fsys["pacto.yaml"].Data), RawYAML: fsys["pacto.yaml"].Data, FS: fsys}
	d := ServiceDetailsFromBundle(b, "local")
	if d.SBOM != nil {
		t.Fatalf("expected nil SBOM, got %+v", d.SBOM)
	}
}
