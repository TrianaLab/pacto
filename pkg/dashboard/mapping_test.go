package dashboard

import (
	"bytes"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/diff"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/validation"
)

func TestServiceFromContract(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{
			Name:    "my-service",
			Version: "1.2.3",
			Owner:   "team-a",
		},
	}

	svc := ServiceFromContract(c, "local")
	if svc.Name != "my-service" {
		t.Errorf("expected name 'my-service', got %q", svc.Name)
	}
	if svc.Version != "1.2.3" {
		t.Errorf("expected version '1.2.3', got %q", svc.Version)
	}
	if svc.Owner != "team-a" {
		t.Errorf("expected owner 'team-a', got %q", svc.Owner)
	}
	if svc.Source != "local" {
		t.Errorf("expected source 'local', got %q", svc.Source)
	}
	if svc.Phase != PhaseUnknown {
		t.Errorf("expected phase Unknown, got %q", svc.Phase)
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

func TestFlattenValues_StringValue(t *testing.T) {
	m := map[string]interface{}{"name": "hello"}
	values := flattenValues(m)
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0].Key != "name" {
		t.Errorf("expected key 'name', got %q", values[0].Key)
	}
	if values[0].Value != "hello" {
		t.Errorf("expected value 'hello', got %q", values[0].Value)
	}
	if values[0].Type != "string" {
		t.Errorf("expected type 'string', got %q", values[0].Type)
	}
}

func TestFlattenValues_NumberValue(t *testing.T) {
	m := map[string]interface{}{"count": float64(42)}
	values := flattenValues(m)
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0].Type != "number" {
		t.Errorf("expected type 'number', got %q", values[0].Type)
	}
	if values[0].Value != "42" {
		t.Errorf("expected value '42', got %q", values[0].Value)
	}
}

func TestFlattenValues_IntValue(t *testing.T) {
	m := map[string]interface{}{"count": 7}
	values := flattenValues(m)
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0].Type != "number" {
		t.Errorf("expected type 'number', got %q", values[0].Type)
	}
}

func TestFlattenValues_BoolValue(t *testing.T) {
	m := map[string]interface{}{"enabled": true}
	values := flattenValues(m)
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0].Type != "boolean" {
		t.Errorf("expected type 'boolean', got %q", values[0].Type)
	}
	if values[0].Value != "true" {
		t.Errorf("expected value 'true', got %q", values[0].Value)
	}
}

func TestFlattenValues_NilValue(t *testing.T) {
	m := map[string]interface{}{"optional": nil}
	values := flattenValues(m)
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0].Type != "any" {
		t.Errorf("expected type 'any', got %q", values[0].Type)
	}
	if values[0].Value != "(any)" {
		t.Errorf("expected value '(any)', got %q", values[0].Value)
	}
}

func TestFlattenValues_ObjectValue(t *testing.T) {
	m := map[string]interface{}{"nested": map[string]interface{}{"key": "val"}}
	values := flattenValues(m)
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0].Type != "object" {
		t.Errorf("expected type 'object', got %q", values[0].Type)
	}
}

func TestFlattenValues_Sorted(t *testing.T) {
	m := map[string]interface{}{
		"z_key": "last",
		"a_key": "first",
		"m_key": "middle",
	}
	values := flattenValues(m)
	if len(values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(values))
	}
	if values[0].Key != "a_key" {
		t.Errorf("expected first key 'a_key', got %q", values[0].Key)
	}
	if values[1].Key != "m_key" {
		t.Errorf("expected second key 'm_key', got %q", values[1].Key)
	}
	if values[2].Key != "z_key" {
		t.Errorf("expected third key 'z_key', got %q", values[2].Key)
	}
}

func TestFlattenValues_Empty(t *testing.T) {
	values := flattenValues(map[string]interface{}{})
	if len(values) != 0 {
		t.Errorf("expected 0 values, got %d", len(values))
	}
}

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
		Configuration: &contract.Configuration{
			Schema: "config.schema.json",
			Ref:    "shared-config",
			Values: map[string]interface{}{
				"port":    float64(8080),
				"enabled": true,
			},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Configuration == nil {
		t.Fatal("expected configuration to be set")
	}
	if !details.Configuration.HasSchema {
		t.Error("expected HasSchema=true")
	}
	if details.Configuration.Schema != "config.schema.json" {
		t.Errorf("expected schema path, got %q", details.Configuration.Schema)
	}
	if details.Configuration.Ref != "shared-config" {
		t.Errorf("expected ref 'shared-config', got %q", details.Configuration.Ref)
	}
	if len(details.Configuration.Values) != 2 {
		t.Fatalf("expected 2 config values, got %d", len(details.Configuration.Values))
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
		Policy: &contract.Policy{
			Schema: "policy.schema.json",
			Ref:    "shared-policy",
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Policy == nil {
		t.Fatal("expected policy to be set")
	}
	if !details.Policy.HasSchema {
		t.Error("expected HasSchema=true")
	}
	if details.Policy.Schema != "policy.schema.json" {
		t.Errorf("expected schema, got %q", details.Policy.Schema)
	}
	if details.Policy.Ref != "shared-policy" {
		t.Errorf("expected ref, got %q", details.Policy.Ref)
	}
}

func TestServiceDetailsFromBundle_Metadata(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Metadata: map[string]interface{}{
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
		Metadata: map[string]interface{}{
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

func TestValidateBundle_NilRawYAML(t *testing.T) {
	b := &contract.Bundle{
		Contract: &contract.Contract{
			Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
	}
	result := validateBundle(b)
	if result != nil {
		t.Error("expected nil for bundle with no RawYAML")
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
		Configuration: &contract.Configuration{
			Schema: "config.schema.json",
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
	if details.Configuration == nil {
		t.Fatal("expected configuration")
	}
	if len(details.Configuration.Values) != 1 {
		t.Fatalf("expected 1 config value from schema, got %d", len(details.Configuration.Values))
	}
	if details.Configuration.Values[0].Key != "port" {
		t.Errorf("expected key 'port', got %q", details.Configuration.Values[0].Key)
	}
}

func TestServiceDetailsFromBundle_PolicyContent(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Policy: &contract.Policy{
			Ref: "policy.yaml",
		},
	}
	fsys := fstest.MapFS{
		"policy.yaml": &fstest.MapFile{
			Data: []byte("enforce: true\nlevel: strict\n"),
		},
	}
	bundle := &contract.Bundle{Contract: c, FS: fsys}
	details := ServiceDetailsFromBundle(bundle, "local")
	if details.Policy == nil {
		t.Fatal("expected policy")
	}
	if details.Policy.Content == "" {
		t.Error("expected policy content to be populated")
	}
	if len(details.Policy.Values) == 0 {
		t.Error("expected policy values parsed from content")
	}
}

func TestServiceDetailsFromBundle_PolicySchemaFallback(t *testing.T) {
	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Policy: &contract.Policy{
			Schema: "policy.schema.json",
		},
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
	if details.Policy == nil {
		t.Fatal("expected policy")
	}
	if len(details.Policy.Values) != 1 {
		t.Fatalf("expected 1 value from schema, got %d", len(details.Policy.Values))
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

func TestValidateBundle_WithRawYAML(t *testing.T) {
	yamlContent := `pactoVersion: "1.0"
service:
  name: svc
  version: 1.0.0
`
	b := &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		},
		RawYAML: []byte(yamlContent),
	}
	result := validateBundle(b)
	if result == nil {
		t.Fatal("expected non-nil validation result")
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

func TestFlattenSchemaProps_Basic(t *testing.T) {
	props := map[string]any{
		"port": map[string]any{
			"type":    "integer",
			"default": 8080,
		},
		"host": map[string]any{
			"type": "string",
		},
	}
	var values []ConfigValue
	flattenSchemaProps("", props, &values)

	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
	if values[0].Key != "host" {
		t.Errorf("expected first key 'host', got %q", values[0].Key)
	}
	if values[0].Type != "string" {
		t.Errorf("expected type 'string', got %q", values[0].Type)
	}
	if values[0].Value != "(any)" {
		t.Errorf("expected value '(any)' (no default), got %q", values[0].Value)
	}
	if values[1].Key != "port" {
		t.Errorf("expected second key 'port', got %q", values[1].Key)
	}
	if values[1].Value != "8080" {
		t.Errorf("expected default '8080', got %q", values[1].Value)
	}
}

func TestFlattenSchemaProps_Nested(t *testing.T) {
	props := map[string]any{
		"cors": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"enabled": map[string]any{
					"type":    "boolean",
					"default": true,
				},
			},
		},
	}
	var values []ConfigValue
	flattenSchemaProps("", props, &values)

	if len(values) != 1 {
		t.Fatalf("expected 1 value (nested), got %d", len(values))
	}
	if values[0].Key != "cors.enabled" {
		t.Errorf("expected key 'cors.enabled', got %q", values[0].Key)
	}
}

func TestFlattenSchemaProps_WithPrefix(t *testing.T) {
	props := map[string]any{
		"port": map[string]any{
			"type": "integer",
		},
	}
	var values []ConfigValue
	flattenSchemaProps("server", props, &values)

	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0].Key != "server.port" {
		t.Errorf("expected key 'server.port', got %q", values[0].Key)
	}
}

func TestFlattenSchemaProps_NonMapPropSkipped(t *testing.T) {
	props := map[string]any{
		"bad": "not a map",
	}
	var values []ConfigValue
	flattenSchemaProps("", props, &values)
	if len(values) != 0 {
		t.Errorf("expected 0 values for non-map prop, got %d", len(values))
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
		Configuration: &contract.Configuration{
			Values: map[string]interface{}{
				"port": float64(8080),
			},
		},
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if details.Configuration == nil {
		t.Fatal("expected configuration")
	}
	if len(details.Configuration.ValueKeys) != 1 {
		t.Fatalf("expected 1 value key, got %d", len(details.Configuration.ValueKeys))
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
		Policy: &contract.Policy{
			Ref: "policy.yaml",
		},
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
	if details.Policy == nil {
		t.Fatal("expected policy")
	}
	if len(details.Policy.Content) != 10240+len("\n... (truncated)") {
		t.Errorf("expected truncated content, got length %d", len(details.Policy.Content))
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

func TestPhaseFromBundle(t *testing.T) {
	t.Run("nil RawYAML returns unknown", func(t *testing.T) {
		b := &contract.Bundle{
			Contract: &contract.Contract{
				Service: contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
			},
		}
		if got := phaseFromBundle(b); got != PhaseUnknown {
			t.Errorf("expected PhaseUnknown, got %v", got)
		}
	})

	t.Run("valid contract returns healthy", func(t *testing.T) {
		raw := []byte(`pactoVersion: "1.0"
service:
  name: svc
  version: 1.0.0
`)
		c, _ := contract.Parse(bytes.NewReader(raw))
		b := &contract.Bundle{Contract: c, RawYAML: raw}
		if got := phaseFromBundle(b); got != PhaseHealthy {
			t.Errorf("expected PhaseHealthy, got %v", got)
		}
	})

	t.Run("invalid contract returns invalid", func(t *testing.T) {
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
		if got := phaseFromBundle(b); got != PhaseInvalid {
			t.Errorf("expected PhaseInvalid, got %v", got)
		}
	})
}
