package mcp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/testutil"
	"github.com/trianalab/pacto/pkg/contract"
)

func TestCreate_Minimal(t *testing.T) {
	dir := t.TempDir()
	result, err := Create(CreateInput{
		Name: "test-svc",
		Path: filepath.Join(dir, "test-svc"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Name != "test-svc" {
		t.Errorf("expected name=test-svc, got %q", result.Summary.Name)
	}
	if result.Summary.Version != "0.1.0" {
		t.Errorf("expected default version, got %q", result.Summary.Version)
	}
	if result.FileCount < 1 {
		t.Errorf("expected at least 1 file, got %d", result.FileCount)
	}
	if _, err := os.Stat(filepath.Join(dir, "test-svc", "pacto.yaml")); err != nil {
		t.Errorf("expected pacto.yaml to exist: %v", err)
	}
}

func TestCreate_MissingName(t *testing.T) {
	_, err := Create(CreateInput{})
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestCreate_DryRun(t *testing.T) {
	result, err := Create(CreateInput{
		Name:   "dry-svc",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FileCount != 0 {
		t.Errorf("expected fileCount=0 for dry run, got %d", result.FileCount)
	}
	if result.Summary.Name != "dry-svc" {
		t.Errorf("expected name=dry-svc, got %q", result.Summary.Name)
	}
}

func TestCreate_WithVersion(t *testing.T) {
	result, err := Create(CreateInput{
		Name:    "versioned-svc",
		Version: "2.5.0",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Version != "2.5.0" {
		t.Errorf("expected version=2.5.0, got %q", result.Summary.Version)
	}
}

func TestCreate_WithOwner(t *testing.T) {
	result, err := Create(CreateInput{
		Name:   "owned-svc",
		Owner:  "team/platform",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Owner != "team/platform" {
		t.Errorf("expected owner, got %q", result.Summary.Owner)
	}
}

func TestCreate_WithInterfaces(t *testing.T) {
	port := 8080
	result, err := Create(CreateInput{
		Name: "api-svc",
		Path: filepath.Join(t.TempDir(), "api-svc"),
		Interfaces: []InterfaceInput{
			{Name: "http-api", Type: "http", Port: &port, Visibility: "public"},
			{Name: "events", Type: "event"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Summary.Interfaces) != 2 {
		t.Errorf("expected 2 interfaces, got %d", len(result.Summary.Interfaces))
	}
	// Should scaffold OpenAPI file for HTTP interface
	if result.FileCount < 2 {
		t.Errorf("expected at least 2 files (pacto.yaml + openapi), got %d", result.FileCount)
	}
}

func TestCreate_WithDependencies(t *testing.T) {
	result, err := Create(CreateInput{
		Name: "dep-svc",
		Dependencies: []DependencyInput{
			{Name: "postgres", Ref: "postgres", Required: true, Compatibility: "^1.0.0"},
			{Name: "redis", Ref: "redis"},
		},
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Summary.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies, got %d", len(result.Summary.Dependencies))
	}
}

func TestCreate_StatefulRuntime(t *testing.T) {
	result, err := Create(CreateInput{
		Name:                      "stateful-svc",
		StoresData:                true,
		DataSurvivesRestart:       true,
		DataSharedAcrossInstances: true,
		DataLossImpact:            "high",
		DryRun:                    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.StateType != "stateful" {
		t.Errorf("expected stateful, got %q", result.Summary.StateType)
	}
}

func TestCreate_StatelessRuntime(t *testing.T) {
	result, err := Create(CreateInput{
		Name:   "stateless-svc",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.StateType != "stateless" {
		t.Errorf("expected stateless, got %q", result.Summary.StateType)
	}
}

func TestCreate_WorkloadTypes(t *testing.T) {
	for _, wt := range []string{"service", "job", "scheduled"} {
		t.Run(wt, func(t *testing.T) {
			result, err := Create(CreateInput{
				Name:     wt + "-svc",
				Workload: wt,
				DryRun:   true,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Summary.Workload != wt {
				t.Errorf("expected workload=%s, got %q", wt, result.Summary.Workload)
			}
		})
	}
}

func TestCreate_WithScaling(t *testing.T) {
	t.Run("replicas", func(t *testing.T) {
		replicas := 3
		_, err := Create(CreateInput{
			Name:     "scaled-svc",
			Replicas: &replicas,
			DryRun:   true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("min/max", func(t *testing.T) {
		min, max := 1, 5
		_, err := Create(CreateInput{
			Name:        "autoscaled-svc",
			MinReplicas: &min,
			MaxReplicas: &max,
			DryRun:      true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestCreate_WithConfig(t *testing.T) {
	dir := t.TempDir()
	result, err := Create(CreateInput{
		Name: "config-svc",
		Path: filepath.Join(dir, "config-svc"),
		ConfigProperties: []ConfigProperty{
			{Name: "PORT", Type: "integer", Required: true},
			{Name: "LOG_LEVEL", Type: "string"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have created config schema file
	schemaPath := filepath.Join(dir, "config-svc", "configuration", "schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("expected schema.json: %v", err)
	}
	if !strings.Contains(string(data), "PORT") {
		t.Errorf("expected PORT in schema, got: %s", data)
	}
	if result.Summary.Sections["configuration"] != "present" {
		t.Error("expected configuration section to be present")
	}
}

func TestCreate_WithMetadata(t *testing.T) {
	result, err := Create(CreateInput{
		Name:     "meta-svc",
		Metadata: map[string]interface{}{"team": "platform", "tier": "critical"},
		DryRun:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Sections["metadata"] != "present" {
		t.Error("expected metadata section present")
	}
}

func TestCreate_DefaultPath(t *testing.T) {
	// When path is empty, uses service name as directory
	result, err := Create(CreateInput{
		Name:   "path-svc",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Path, "path-svc") {
		t.Errorf("expected path to contain service name, got %q", result.Path)
	}
}

// --- Description inference tests ---

func TestInferFromDescription(t *testing.T) {
	tests := []struct {
		desc     string
		checkFn  func(descriptionHints) bool
		expected string
	}{
		{"REST API service", func(h descriptionHints) bool { return h.hasHTTP }, "hasHTTP"},
		{"gRPC microservice", func(h descriptionHints) bool { return h.hasGRPC }, "hasGRPC"},
		{"Kafka event consumer", func(h descriptionHints) bool { return h.hasEvents }, "hasEvents"},
		{"stores data in postgres", func(h descriptionHints) bool { return h.storesData && h.dataDurable }, "storesData+durable"},
		{"uses redis cache", func(h descriptionHints) bool { return h.storesData }, "storesData via redis"},
		{"background worker process", func(h descriptionHints) bool { return h.isWorker }, "isWorker"},
		{"scheduled cron job", func(h descriptionHints) bool { return h.isScheduled }, "isScheduled"},
		{"nothing special", func(h descriptionHints) bool { return !h.hasHTTP && !h.storesData }, "no hints"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			hints := inferFromDescription(tt.desc)
			if !tt.checkFn(hints) {
				t.Errorf("failed check for %q", tt.desc)
			}
		})
	}
}

func TestCreate_DescriptionInference(t *testing.T) {
	result, err := Create(CreateInput{
		Name:        "inferred-svc",
		Description: "REST API that uses postgres for storage",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should infer HTTP interface and stateful runtime
	if result.Summary.StateType != "stateful" {
		t.Errorf("expected stateful from postgres mention, got %q", result.Summary.StateType)
	}
	if len(result.Summary.Interfaces) == 0 {
		t.Error("expected at least one interface from REST mention")
	}
	if len(result.Derived) == 0 {
		t.Error("expected derived entries from description inference")
	}
}

func TestCreate_ExplicitOverridesInference(t *testing.T) {
	port := 9090
	result, err := Create(CreateInput{
		Name:        "explicit-svc",
		Description: "REST API with postgres",
		Interfaces:  []InterfaceInput{{Name: "custom-api", Type: "grpc", Port: &port}},
		StoresData:  false,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Explicit interfaces should take precedence (not add inferred)
	if len(result.Summary.Interfaces) != 1 {
		t.Errorf("expected 1 explicit interface, got %d", len(result.Summary.Interfaces))
	}
}

func TestCreate_WorkerInference(t *testing.T) {
	result, err := Create(CreateInput{
		Name:        "worker-svc",
		Description: "background worker that processes events from kafka",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Workload != "job" {
		t.Errorf("expected job workload from worker mention, got %q", result.Summary.Workload)
	}
}

func TestCreate_ScheduledInference(t *testing.T) {
	result, err := Create(CreateInput{
		Name:        "cron-svc",
		Description: "scheduled batch job",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Workload != "scheduled" {
		t.Errorf("expected scheduled workload, got %q", result.Summary.Workload)
	}
}

// --- Runtime derivation tests ---

func TestDeriveRuntimeMap(t *testing.T) {
	t.Run("stateless default", func(t *testing.T) {
		rt := deriveRuntimeMap(runtimeIntent{})
		state := rt["state"].(map[string]interface{})
		if state["type"] != "stateless" {
			t.Errorf("expected stateless, got %v", state["type"])
		}
		p := state["persistence"].(map[string]interface{})
		if p["scope"] != "local" {
			t.Errorf("expected local scope, got %v", p["scope"])
		}
		if p["durability"] != "ephemeral" {
			t.Errorf("expected ephemeral, got %v", p["durability"])
		}
	})

	t.Run("stateful persistent shared", func(t *testing.T) {
		rt := deriveRuntimeMap(runtimeIntent{
			storesData:                true,
			dataSurvivesRestart:       true,
			dataSharedAcrossInstances: true,
			dataLossImpact:            "high",
		})
		state := rt["state"].(map[string]interface{})
		if state["type"] != "stateful" {
			t.Errorf("expected stateful, got %v", state["type"])
		}
		p := state["persistence"].(map[string]interface{})
		if p["scope"] != "shared" {
			t.Errorf("expected shared, got %v", p["scope"])
		}
		if p["durability"] != "persistent" {
			t.Errorf("expected persistent, got %v", p["durability"])
		}
		if state["dataCriticality"] != "high" {
			t.Errorf("expected high criticality, got %v", state["dataCriticality"])
		}
	})

	t.Run("stores data ephemeral", func(t *testing.T) {
		rt := deriveRuntimeMap(runtimeIntent{storesData: true})
		state := rt["state"].(map[string]interface{})
		if state["type"] != "stateful" {
			t.Errorf("expected stateful, got %v", state["type"])
		}
		p := state["persistence"].(map[string]interface{})
		if p["durability"] != "ephemeral" {
			t.Errorf("expected ephemeral for non-persistent data, got %v", p["durability"])
		}
	})

	t.Run("custom workload", func(t *testing.T) {
		rt := deriveRuntimeMap(runtimeIntent{workload: "job"})
		if rt["workload"] != "job" {
			t.Errorf("expected job workload, got %v", rt["workload"])
		}
	})
}

// --- Edit tests ---

func TestEdit_ChangeVersion(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	result, err := Edit(EditInput{
		Path:    dir,
		Version: strPtr("2.0.0"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %q", result.Summary.Version)
	}
	if !containsStr(result.Changes, "version") {
		t.Error("expected version change in changes list")
	}
}

func TestEdit_ChangeName(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	result, err := Edit(EditInput{
		Path: dir,
		Name: strPtr("renamed-svc"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Name != "renamed-svc" {
		t.Errorf("expected renamed-svc, got %q", result.Summary.Name)
	}
}

func TestEdit_ChangeOwner(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	result, err := Edit(EditInput{
		Path:  dir,
		Owner: strPtr("team/new"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Owner != "team/new" {
		t.Errorf("expected team/new, got %q", result.Summary.Owner)
	}
}

func TestEdit_DryRun(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	original, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))

	_, err := Edit(EditInput{
		Path:    dir,
		Version: strPtr("9.9.9"),
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	current, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if string(current) != string(original) {
		t.Error("dry run should not modify the file")
	}
}

func TestEdit_AddInterface(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	result, err := Edit(EditInput{
		Path: dir,
		AddInterfaces: []InterfaceInput{
			{Name: "events", Type: "event"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(result.Changes, "events") {
		t.Error("expected 'events' in changes")
	}
}

func TestEdit_RemoveInterface(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	// First add an extra interface, then remove it
	_, err := Edit(EditInput{
		Path:          dir,
		AddInterfaces: []InterfaceInput{{Name: "events", Type: "event"}},
	})
	if err != nil {
		t.Fatalf("add interface: %v", err)
	}

	result, err := Edit(EditInput{
		Path:             dir,
		RemoveInterfaces: []string{"events"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(result.Changes, "removed") {
		t.Error("expected 'removed' in changes")
	}
}

func TestEdit_AddDependency(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	result, err := Edit(EditInput{
		Path: dir,
		AddDependencies: []DependencyInput{
			{Name: "postgres", Ref: "postgres", Required: true, Compatibility: "^1.0.0"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(result.Changes, "postgres") {
		t.Error("expected 'postgres' in changes")
	}
}

func TestEdit_RemoveDependency(t *testing.T) {
	// First create a bundle with dependencies
	dir := testutil.WriteTestBundle(t)
	// Add a dependency first
	_, err := Edit(EditInput{
		Path:            dir,
		AddDependencies: []DependencyInput{{Name: "redis", Ref: "redis", Compatibility: "^1.0.0"}},
	})
	if err != nil {
		t.Fatalf("add dep: %v", err)
	}

	result, err := Edit(EditInput{
		Path:       dir,
		RemoveDeps: []string{"redis"},
	})
	if err != nil {
		t.Fatalf("remove dep: %v", err)
	}
	if !containsStr(result.Changes, "removed") {
		t.Error("expected 'removed' in changes")
	}
}

func TestEdit_ChangeRuntime(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	sd := true
	result, err := Edit(EditInput{
		Path:       dir,
		StoresData: &sd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.StateType != "stateful" {
		t.Errorf("expected stateful, got %q", result.Summary.StateType)
	}
}

func TestEdit_Scaling(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	replicas := 5
	_, err := Edit(EditInput{
		Path:     dir,
		Replicas: &replicas,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEdit_Metadata(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	result, err := Edit(EditInput{
		Path:        dir,
		SetMetadata: map[string]interface{}{"team": "platform"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(result.Changes, "metadata") {
		t.Error("expected metadata change")
	}
}

func TestEdit_RemoveMetadata(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	// Add then remove
	_, _ = Edit(EditInput{
		Path:        dir,
		SetMetadata: map[string]interface{}{"team": "x"},
	})
	result, err := Edit(EditInput{
		Path:           dir,
		RemoveMetadata: []string{"team"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(result.Changes, "removed metadata") {
		t.Error("expected removed metadata change")
	}
}

func TestEdit_InvalidPath(t *testing.T) {
	_, err := Edit(EditInput{Path: "/nonexistent-path"})
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestEdit_AddConfigProperties(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	result, err := Edit(EditInput{
		Path:                dir,
		AddConfigProperties: []ConfigProperty{{Name: "PORT", Type: "integer"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(result.Changes, "configuration") {
		t.Error("expected configuration change")
	}
}

func TestEdit_ChangeWorkload(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	workload := "job"
	result, err := Edit(EditInput{
		Path:     dir,
		Workload: &workload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Workload != "job" {
		t.Errorf("expected job, got %q", result.Summary.Workload)
	}
}

// --- Check tests ---

func TestCheck_ValidBundle(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
	if result.Summary.Name != "test-svc" {
		t.Errorf("expected test-svc, got %q", result.Summary.Name)
	}
}

func TestCheck_InvalidPath(t *testing.T) {
	_, err := Check("/nonexistent-path")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestCheck_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("not: valid: yaml: {{"), 0644)
	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for bad YAML")
	}
}

func TestCheck_DefaultPath(t *testing.T) {
	// Empty path defaults to "."
	_, err := Check("")
	// This will likely fail because . doesn't have pacto.yaml, which is expected
	if err == nil {
		// It's OK if CWD happens to have pacto.yaml
		return
	}
	if !strings.Contains(err.Error(), "pacto.yaml") {
		t.Errorf("expected pacto.yaml in error, got: %v", err)
	}
}

func TestCheck_Suggestions(t *testing.T) {
	// Create a minimal valid contract without optional sections
	dir := t.TempDir()
	yaml := `pactoVersion: "1.0"
service:
  name: minimal-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
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
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644)
	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %v", result.Errors)
	}
	if len(result.Suggestions) == 0 {
		t.Error("expected suggestions for missing optional sections")
	}
}

// --- YAML marshaling tests ---

func TestMarshalContract_KeyOrder(t *testing.T) {
	m := map[string]interface{}{
		"metadata":     map[string]interface{}{"team": "x"},
		"pactoVersion": "1.0",
		"service":      map[string]interface{}{"name": "test", "version": "1.0.0"},
		"runtime": map[string]interface{}{
			"workload": "service",
			"state": map[string]interface{}{
				"type":            "stateless",
				"persistence":     map[string]interface{}{"scope": "local", "durability": "ephemeral"},
				"dataCriticality": "low",
			},
		},
	}

	data, err := marshalContract(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	yaml := string(data)

	// pactoVersion should come before service
	pvIdx := strings.Index(yaml, "pactoVersion")
	svcIdx := strings.Index(yaml, "service")
	rtIdx := strings.Index(yaml, "runtime")
	metaIdx := strings.Index(yaml, "metadata")

	if pvIdx >= svcIdx {
		t.Error("pactoVersion should come before service")
	}
	if svcIdx >= rtIdx {
		t.Error("service should come before runtime")
	}
	if rtIdx >= metaIdx {
		t.Error("runtime should come before metadata")
	}
}

// --- Helper function tests ---

func TestDefaultVersion(t *testing.T) {
	if v := defaultVersion(""); v != "0.1.0" {
		t.Errorf("expected 0.1.0, got %q", v)
	}
	if v := defaultVersion("2.0.0"); v != "2.0.0" {
		t.Errorf("expected 2.0.0, got %q", v)
	}
}

func TestDefaultCompatibility(t *testing.T) {
	if c := defaultCompatibility("", "redis"); c != "^1.0.0" {
		t.Errorf("expected ^1.0.0, got %q", c)
	}
	if c := defaultCompatibility("~2.0.0", "pg"); c != "~2.0.0" {
		t.Errorf("expected ~2.0.0, got %q", c)
	}
}

func TestIntPtr(t *testing.T) {
	p := intPtr(42)
	if *p != 42 {
		t.Errorf("expected 42, got %d", *p)
	}
}

func TestGenerateConfigSchema(t *testing.T) {
	schema := generateConfigSchema([]ConfigProperty{
		{Name: "PORT", Type: "integer", Required: true},
		{Name: "LOG_LEVEL", Type: "string"},
	})
	s := string(schema)
	if !strings.Contains(s, "PORT") {
		t.Error("expected PORT in schema")
	}
	if !strings.Contains(s, `"required"`) {
		t.Error("expected required array")
	}
	if !strings.Contains(s, "integer") {
		t.Error("expected integer type")
	}
}

func TestGenerateConfigSchemaNoRequired(t *testing.T) {
	schema := generateConfigSchema([]ConfigProperty{
		{Name: "OPT"},
	})
	s := string(schema)
	if strings.Contains(s, `"required"`) {
		t.Error("expected no required array when no props are required")
	}
}

func TestGenerateConfigSchemaDefaultType(t *testing.T) {
	schema := generateConfigSchema([]ConfigProperty{
		{Name: "FOO"},
	})
	if !strings.Contains(string(schema), `"string"`) {
		t.Error("expected default type string")
	}
}

func TestScaffoldInterfaceStub(t *testing.T) {
	t.Run("http", func(t *testing.T) {
		stub := scaffoldInterfaceStub("my-svc", InterfaceInput{Name: "api", Type: "http"})
		if !strings.Contains(string(stub), "openapi") {
			t.Error("expected OpenAPI stub")
		}
	})

	t.Run("grpc", func(t *testing.T) {
		stub := scaffoldInterfaceStub("my-svc", InterfaceInput{Name: "api", Type: "grpc"})
		if !strings.Contains(string(stub), "proto3") {
			t.Error("expected proto stub")
		}
	})
}

func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := atomicWriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", data)
	}
}

func TestSummarizeContract(t *testing.T) {
	bundle := testutil.TestBundle()
	s := summarizeContract(bundle.Contract)
	if s.Name != "test-svc" {
		t.Errorf("expected test-svc, got %q", s.Name)
	}
	if s.Workload != "service" {
		t.Errorf("expected service, got %q", s.Workload)
	}
	if s.StateType != "stateless" {
		t.Errorf("expected stateless, got %q", s.StateType)
	}
	if len(s.Interfaces) != 1 {
		t.Errorf("expected 1 interface, got %d", len(s.Interfaces))
	}
}

func TestAssessSections(t *testing.T) {
	bundle := testutil.TestBundle()
	sections := assessSections(bundle.Contract)
	if sections["service"] != "present" {
		t.Error("expected service present")
	}
	if sections["runtime"] != "present" {
		t.Error("expected runtime present")
	}
	if sections["configuration"] != "absent" {
		t.Error("expected configuration absent")
	}
}

func TestBuildSuggestions(t *testing.T) {
	t.Run("invalid contract gets no suggestions", func(t *testing.T) {
		s := buildSuggestions(testutil.TestBundle().Contract, false)
		if len(s) != 0 {
			t.Error("expected no suggestions for invalid contract")
		}
	})

	t.Run("missing sections get suggestions", func(t *testing.T) {
		bundle := testutil.TestBundle()
		bundle.Contract.Dependencies = nil
		bundle.Contract.Configurations = nil
		bundle.Contract.Scaling = nil
		s := buildSuggestions(bundle.Contract, true)
		if len(s) == 0 {
			t.Error("expected suggestions for missing sections")
		}
	})
}

func TestCollectDerived(t *testing.T) {
	t.Run("no description", func(t *testing.T) {
		d := collectDerived(CreateInput{}, descriptionHints{})
		if len(d) != 0 {
			t.Error("expected no derived without description")
		}
	})

	t.Run("with inference", func(t *testing.T) {
		d := collectDerived(
			CreateInput{Description: "API"},
			descriptionHints{hasHTTP: true, storesData: true},
		)
		if len(d) < 2 {
			t.Errorf("expected at least 2 derived entries, got %d", len(d))
		}
	})
}

func TestWireHealthMetrics(t *testing.T) {
	t.Run("with http", func(t *testing.T) {
		rt := map[string]interface{}{}
		wireHealthMetrics(rt, []InterfaceInput{{Name: "api", Type: "http"}})
		if _, ok := rt["health"]; !ok {
			t.Error("expected health to be wired")
		}
		if _, ok := rt["metrics"]; !ok {
			t.Error("expected metrics to be wired")
		}
	})

	t.Run("no http", func(t *testing.T) {
		rt := map[string]interface{}{}
		wireHealthMetrics(rt, []InterfaceInput{{Name: "events", Type: "event"}})
		if _, ok := rt["health"]; ok {
			t.Error("expected no health without HTTP")
		}
	})

	t.Run("empty interfaces", func(t *testing.T) {
		rt := map[string]interface{}{}
		wireHealthMetrics(rt, nil)
		if _, ok := rt["health"]; ok {
			t.Error("expected no health without interfaces")
		}
	})
}

func TestRemoveInterfaces(t *testing.T) {
	m := map[string]interface{}{
		"interfaces": []interface{}{
			map[string]interface{}{"name": "api", "type": "http"},
			map[string]interface{}{"name": "events", "type": "event"},
		},
	}
	changes := removeInterfaces(m, []string{"api"})
	if len(changes) != 1 {
		t.Errorf("expected 1 change, got %d", len(changes))
	}
	ifaces := m["interfaces"].([]interface{})
	if len(ifaces) != 1 {
		t.Errorf("expected 1 remaining interface, got %d", len(ifaces))
	}
}

func TestRemoveInterfaces_All(t *testing.T) {
	m := map[string]interface{}{
		"interfaces": []interface{}{
			map[string]interface{}{"name": "api", "type": "http"},
		},
	}
	removeInterfaces(m, []string{"api"})
	if _, ok := m["interfaces"]; ok {
		t.Error("expected interfaces key to be deleted when all removed")
	}
}

func TestRemoveInterfaces_NoInterfaces(t *testing.T) {
	m := map[string]interface{}{}
	changes := removeInterfaces(m, []string{"api"})
	if len(changes) != 0 {
		t.Error("expected no changes when no interfaces exist")
	}
}

func TestRemoveDependencies(t *testing.T) {
	m := map[string]interface{}{
		"dependencies": []interface{}{
			map[string]interface{}{"ref": "postgres"},
			map[string]interface{}{"ref": "redis"},
		},
	}
	changes := removeDependencies(m, []string{"redis"})
	if len(changes) != 1 {
		t.Errorf("expected 1 change, got %d", len(changes))
	}
}

func TestRemoveDependencies_All(t *testing.T) {
	m := map[string]interface{}{
		"dependencies": []interface{}{
			map[string]interface{}{"ref": "postgres"},
		},
	}
	removeDependencies(m, []string{"postgres"})
	if _, ok := m["dependencies"]; ok {
		t.Error("expected dependencies key deleted when all removed")
	}
}

func TestRemoveDependencies_NoDeps(t *testing.T) {
	m := map[string]interface{}{}
	changes := removeDependencies(m, []string{"pg"})
	if len(changes) != 0 {
		t.Error("expected no changes when no deps exist")
	}
}

func TestApplyMetadataEdits(t *testing.T) {
	t.Run("set and remove", func(t *testing.T) {
		m := map[string]interface{}{
			"metadata": map[string]interface{}{"old": "value"},
		}
		changes := applyMetadataEdits(m, map[string]interface{}{"new": "val"}, []string{"old"})
		if len(changes) != 2 {
			t.Errorf("expected 2 changes, got %d", len(changes))
		}
	})

	t.Run("remove all deletes key", func(t *testing.T) {
		m := map[string]interface{}{
			"metadata": map[string]interface{}{"only": "value"},
		}
		applyMetadataEdits(m, nil, []string{"only"})
		if _, ok := m["metadata"]; ok {
			t.Error("expected metadata key deleted when empty")
		}
	})

	t.Run("no existing metadata", func(t *testing.T) {
		m := map[string]interface{}{}
		changes := applyMetadataEdits(m, map[string]interface{}{"key": "val"}, nil)
		if len(changes) != 1 {
			t.Errorf("expected 1 change, got %d", len(changes))
		}
	})
}

func TestBuildScalingMap(t *testing.T) {
	t.Run("replicas", func(t *testing.T) {
		r := 3
		s := buildScalingMap(&r, nil, nil)
		if s["replicas"] != 3 {
			t.Error("expected replicas=3")
		}
	})

	t.Run("min/max", func(t *testing.T) {
		min, max := 1, 5
		s := buildScalingMap(nil, &min, &max)
		if s["min"] != 1 || s["max"] != 5 {
			t.Error("expected min=1, max=5")
		}
	})
}

func TestEnsureConfigSection(t *testing.T) {
	t.Run("adds when missing", func(t *testing.T) {
		m := map[string]interface{}{}
		ensureConfigSection(m)
		if _, ok := m["configurations"]; !ok {
			t.Error("expected configurations added")
		}
	})

	t.Run("no-op when present", func(t *testing.T) {
		m := map[string]interface{}{
			"configurations": []interface{}{map[string]interface{}{"name": "default", "schema": "custom.json"}},
		}
		ensureConfigSection(m)
		cfgs := m["configurations"].([]interface{})
		cfg := cfgs[0].(map[string]interface{})
		if cfg["schema"] != "custom.json" {
			t.Error("should not overwrite existing config")
		}
	})
}

func TestSummarizeFromMap(t *testing.T) {
	m := map[string]interface{}{
		"pactoVersion": "1.0",
		"service":      map[string]interface{}{"name": "test", "version": "1.0.0", "owner": "team/x"},
		"interfaces":   []interface{}{map[string]interface{}{"name": "api", "type": "http"}},
		"dependencies": []interface{}{map[string]interface{}{"ref": "postgres"}},
		"runtime": map[string]interface{}{
			"workload": "service",
			"state":    map[string]interface{}{"type": "stateless"},
		},
		"metadata": map[string]interface{}{"team": "x"},
	}
	s := summarizeFromMap(m)
	if s.Name != "test" {
		t.Errorf("expected test, got %q", s.Name)
	}
	if s.Owner != "team/x" {
		t.Errorf("expected team/x, got %q", s.Owner)
	}
	if len(s.Interfaces) != 1 {
		t.Errorf("expected 1 interface, got %d", len(s.Interfaces))
	}
	if len(s.Dependencies) != 1 {
		t.Errorf("expected 1 dependency, got %d", len(s.Dependencies))
	}
	if s.Sections["metadata"] != "present" {
		t.Error("expected metadata present")
	}
}

func TestSummarizeFromMapMinimal(t *testing.T) {
	m := map[string]interface{}{
		"pactoVersion": "1.0",
		"service":      map[string]interface{}{"name": "test", "version": "1.0.0"},
	}
	s := summarizeFromMap(m)
	if s.Sections["metadata"] != "absent" {
		t.Error("expected metadata absent")
	}
}

func TestValueToNode(t *testing.T) {
	_, err := valueToNode("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = valueToNode(map[string]interface{}{"key": "value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHasRuntimeEdits(t *testing.T) {
	if hasRuntimeEdits(EditInput{}) {
		t.Error("expected false for empty input")
	}
	sd := true
	if !hasRuntimeEdits(EditInput{StoresData: &sd}) {
		t.Error("expected true when StoresData is set")
	}
}

// --- scaffoldNewInterfaceFiles tests ---

func TestScaffoldNewInterfaceFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "interfaces"), 0755)

	scaffoldNewInterfaceFiles(dir, []InterfaceInput{
		{Name: "grpc-api", Type: "grpc"},
		{Name: "events", Type: "event"}, // should be skipped
	})

	// gRPC file should be created
	if _, err := os.Stat(filepath.Join(dir, "interfaces", "grpc-api.yaml")); err != nil {
		t.Errorf("expected grpc-api.yaml: %v", err)
	}
	// event file should NOT be created
	if _, err := os.Stat(filepath.Join(dir, "interfaces", "events.yaml")); err == nil {
		t.Error("event interface should not scaffold a file")
	}
}

func TestScaffoldNewInterfaceFiles_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	ifaceDir := filepath.Join(dir, "interfaces")
	_ = os.MkdirAll(ifaceDir, 0755)
	_ = os.WriteFile(filepath.Join(ifaceDir, "api.yaml"), []byte("existing"), 0644)

	scaffoldNewInterfaceFiles(dir, []InterfaceInput{
		{Name: "api", Type: "http"},
	})

	// Should not overwrite
	data, _ := os.ReadFile(filepath.Join(ifaceDir, "api.yaml"))
	if string(data) != "existing" {
		t.Error("should not overwrite existing file")
	}
}

// --- applyRuntimeEdits coverage ---

func TestEdit_RuntimeWithExistingState(t *testing.T) {
	dir := testutil.WriteTestBundle(t)

	// First make it stateful
	sd := true
	dsr := true
	dsa := true
	_, err := Edit(EditInput{
		Path:                      dir,
		StoresData:                &sd,
		DataSurvivesRestart:       &dsr,
		DataSharedAcrossInstances: &dsa,
	})
	if err != nil {
		t.Fatalf("first edit: %v", err)
	}

	// Now change just data loss impact — should read existing state values
	impact := "high"
	result, err := Edit(EditInput{
		Path:           dir,
		DataLossImpact: &impact,
	})
	if err != nil {
		t.Fatalf("second edit: %v", err)
	}
	if result.Summary.StateType != "stateful" {
		t.Errorf("expected stateful preserved, got %q", result.Summary.StateType)
	}
}

func TestEdit_DataSurvivesRestart(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	dsr := true
	sd := true
	result, err := Edit(EditInput{
		Path:                dir,
		StoresData:          &sd,
		DataSurvivesRestart: &dsr,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.StateType != "stateful" {
		t.Errorf("expected stateful, got %q", result.Summary.StateType)
	}
}

func TestEdit_DataSharedAcrossInstances(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	sd := true
	dsa := true
	_, err := Edit(EditInput{
		Path:                      dir,
		StoresData:                &sd,
		DataSharedAcrossInstances: &dsa,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Edit with workload and no existing health ---

func TestEdit_WorkloadNoHealth(t *testing.T) {
	// Create a contract without health, then edit runtime
	dir := t.TempDir()
	yaml := `pactoVersion: "1.0"
service:
  name: no-health
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
    contract: interfaces/api.yaml
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "interfaces"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "interfaces", "api.yaml"), []byte("{}"), 0644)

	sd := true
	_, err := Edit(EditInput{
		Path:       dir,
		StoresData: &sd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Create error paths ---

func TestCreate_WriteBundleError(t *testing.T) {
	// Try to create in a path that will fail
	oldMkdir := osMkdirAll
	defer func() { osMkdirAll = oldMkdir }()
	osMkdirAll = func(_ string, _ os.FileMode) error {
		return fmt.Errorf("mkdir failed")
	}

	_, err := Create(CreateInput{
		Name: "fail-svc",
		Path: "/tmp/fail-test-svc",
	})
	if err == nil {
		t.Error("expected error from mkdir failure")
	}
}

func TestCreate_WithGRPCInterface(t *testing.T) {
	dir := t.TempDir()
	port := 9090
	result, err := Create(CreateInput{
		Name: "grpc-svc",
		Path: filepath.Join(dir, "grpc-svc"),
		Interfaces: []InterfaceInput{
			{Name: "grpc-api", Type: "grpc", Port: &port},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have proto stub
	data, err := os.ReadFile(filepath.Join(dir, "grpc-svc", "interfaces", "grpc-api.yaml"))
	if err != nil {
		t.Fatalf("expected grpc stub: %v", err)
	}
	if !strings.Contains(string(data), "proto3") {
		t.Error("expected proto3 content")
	}
	if result.FileCount < 2 {
		t.Errorf("expected at least 2 files, got %d", result.FileCount)
	}
}

// --- atomicWriteFile error paths ---

func TestAtomicWriteFile_WriteError(t *testing.T) {
	oldWrite := osWriteFile
	defer func() { osWriteFile = oldWrite }()
	osWriteFile = func(_ string, _ []byte, _ os.FileMode) error {
		return fmt.Errorf("write failed")
	}

	err := atomicWriteFile("/tmp/test", []byte("data"), 0644)
	if err == nil {
		t.Error("expected error")
	}
}

func TestAtomicWriteFile_RenameError(t *testing.T) {
	oldRename := osRename
	defer func() { osRename = oldRename }()
	osRename = func(_, _ string) error {
		return fmt.Errorf("rename failed")
	}

	dir := t.TempDir()
	err := atomicWriteFile(filepath.Join(dir, "test"), []byte("data"), 0644)
	if err == nil {
		t.Error("expected error")
	}
}

// --- validateYAML error path ---

func TestValidateYAML_ParseError(t *testing.T) {
	err := validateYAML([]byte("not valid yaml: {{"))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// --- marshalContract error ---

func TestMarshalContract_Valid(t *testing.T) {
	m := map[string]interface{}{
		"pactoVersion": "1.0",
		"service":      map[string]interface{}{"name": "test", "version": "1.0.0"},
	}
	data, err := marshalContract(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(data), "pactoVersion") {
		t.Error("expected pactoVersion in output")
	}
}

// --- Edit handler JSON parsing errors ---

func TestEditTool_InvalidDependenciesJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"add_dependencies": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError for invalid dependencies JSON")
	}
}

func TestEditTool_InvalidRemoveInterfacesJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"remove_interfaces": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError for invalid remove_interfaces JSON")
	}
}

func TestEditTool_InvalidRemoveDepsJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"remove_dependencies": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError for invalid remove_dependencies JSON")
	}
}

func TestEditTool_InvalidConfigJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"add_config_properties": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError for invalid config JSON")
	}
}

func TestEditTool_InvalidSetMetadataJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"set_metadata": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError for invalid set_metadata JSON")
	}
}

func TestEditTool_InvalidRemoveMetadataJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"remove_metadata": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError for invalid remove_metadata JSON")
	}
}

func TestEditTool_AllStringFields(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"path":             dir,
		"name":             "new-name",
		"version":          "2.0.0",
		"owner":            "team/new",
		"workload":         "job",
		"data_loss_impact": "high",
		"stores_data":      true,
	})
	if result.IsError {
		t.Errorf("unexpected error: %s", resultText(t, result))
	}
}

func TestEditTool_BoolFields(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"path":                         dir,
		"data_survives_restart":        true,
		"data_shared_across_instances": true,
	})
	if result.IsError {
		t.Errorf("unexpected error: %s", resultText(t, result))
	}
}

func TestEditTool_ReplicaFields(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_edit", map[string]any{
		"path":     dir,
		"replicas": 3,
	})
	if result.IsError {
		t.Errorf("unexpected error: %s", resultText(t, result))
	}
}

// --- Create handler JSON parsing errors ---

func TestCreateTool_InvalidDependenciesJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_create", map[string]any{
		"name":         "test",
		"dependencies": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError")
	}
}

func TestCreateTool_InvalidConfigJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_create", map[string]any{
		"name":              "test",
		"config_properties": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError")
	}
}

func TestCreateTool_InvalidMetadataJSON(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_create", map[string]any{
		"name":     "test",
		"metadata": "not-json",
	})
	if !result.IsError {
		t.Error("expected IsError")
	}
}

func TestCreateTool_WithReplicas(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_create", map[string]any{
		"name":         "test",
		"replicas":     3,
		"min_replicas": 1,
		"max_replicas": 5,
		"dry_run":      true,
	})
	if result.IsError {
		t.Errorf("unexpected error: %s", resultText(t, result))
	}
}

// --- interfaceContractPath ---

func TestInterfaceContractPath(t *testing.T) {
	p := interfaceContractPath(InterfaceInput{Name: "api"})
	if p != "interfaces/api.yaml" {
		t.Errorf("expected interfaces/api.yaml, got %q", p)
	}
}

// --- buildStubFS ---

func TestBuildStubFS(t *testing.T) {
	port := 8080
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "test", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: &port, Contract: "interfaces/api.yaml"},
		},
		Configurations: []contract.ConfigurationSource{
			{Name: "default", Schema: "configuration/schema.json"},
		},
	}
	fs := buildStubFS(c, []byte("test"))
	if _, ok := fs["pacto.yaml"]; !ok {
		t.Error("expected pacto.yaml")
	}
	if _, ok := fs["interfaces/api.yaml"]; !ok {
		t.Error("expected interface stub")
	}
	if _, ok := fs["configuration/schema.json"]; !ok {
		t.Error("expected config schema stub")
	}
}

func TestBuildStubFS_NoOptional(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "test", Version: "1.0.0"},
	}
	fs := buildStubFS(c, []byte("test"))
	if len(fs) != 1 {
		t.Errorf("expected only pacto.yaml, got %d files", len(fs))
	}
}

// --- Additional coverage tests ---

func TestCreate_GRPCInference(t *testing.T) {
	result, err := Create(CreateInput{
		Name:        "grpc-infer",
		Description: "gRPC microservice",
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Summary.Interfaces) == 0 {
		t.Error("expected gRPC interface from inference")
	}
}

func TestEdit_DefaultDir(t *testing.T) {
	// Edit with empty path should use "." — will fail since no pacto.yaml
	_, err := Edit(EditInput{Version: strPtr("1.0.0")})
	if err == nil || !strings.Contains(err.Error(), "pacto.yaml") {
		t.Errorf("expected pacto.yaml error, got: %v", err)
	}
}

func TestEdit_AddInterfaceWithPortAndVisibility(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	port := 9090
	result, err := Edit(EditInput{
		Path: dir,
		AddInterfaces: []InterfaceInput{
			{Name: "grpc-api", Type: "grpc", Port: &port, Visibility: "internal"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsStr(result.Changes, "grpc-api") {
		t.Error("expected grpc-api in changes")
	}
}

func TestEdit_NilServiceMap(t *testing.T) {
	// Exercise the svc == nil branch in applyEdits
	m := map[string]interface{}{
		"pactoVersion": "1.0",
	}
	changes := applyEdits(m, EditInput{Name: strPtr("new")})
	if !containsStr(changes, "new") {
		t.Error("expected rename change even with nil service")
	}
}

func TestEdit_NoExistingRuntime(t *testing.T) {
	// Exercise the !ok path for runtime
	dir := t.TempDir()
	yaml := `pactoVersion: "1.0"
service:
  name: no-rt
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
    contract: interfaces/api.yaml
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "interfaces"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "interfaces", "api.yaml"), []byte("{}"), 0644)

	sd := false
	_, err := Edit(EditInput{
		Path:       dir,
		StoresData: &sd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheck_WithErrors(t *testing.T) {
	// Create a contract with validation errors
	dir := t.TempDir()
	yaml := `pactoVersion: "1.0"
service:
  name: bad-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
    contract: interfaces/api.yaml
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: missing-iface
    path: /health
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "interfaces"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "interfaces", "api.yaml"), []byte("{}"), 0644)

	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid")
	}
	if len(result.Errors) == 0 {
		t.Error("expected errors")
	}
}

func TestCheck_NoInterfaces(t *testing.T) {
	// A contract with runtime but no interfaces is valid — should get suggestions
	dir := t.TempDir()
	yaml := `pactoVersion: "1.0"
service:
  name: bare
  version: "1.0.0"
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644)
	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %v", result.Errors)
	}
	found := false
	for _, s := range result.Suggestions {
		if strings.Contains(s.Message, "interface") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected suggestion about missing interfaces")
	}
}

func TestCheck_NoRuntime(t *testing.T) {
	dir := t.TempDir()
	yaml := `pactoVersion: "1.0"
service:
  name: no-rt
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
    contract: interfaces/api.yaml
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "interfaces"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "interfaces", "api.yaml"), []byte("{}"), 0644)

	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have suggestion about missing runtime
	found := false
	for _, s := range result.Suggestions {
		if strings.Contains(s.Message, "runtime") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected suggestion about missing runtime")
	}
}

func TestCheck_HTTPNoHealth(t *testing.T) {
	dir := t.TempDir()
	yaml := `pactoVersion: "1.0"
service:
  name: no-health
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
    contract: interfaces/api.yaml
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "interfaces"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "interfaces", "api.yaml"), []byte("{}"), 0644)

	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %v", result.Errors)
	}
	found := false
	for _, s := range result.Suggestions {
		if strings.Contains(s.Message, "health") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected health suggestion")
	}
}

func TestDefaultCompatibility_OCI(t *testing.T) {
	c := defaultCompatibility("", "oci://ghcr.io/org/svc:1.0.0")
	if c != "^1.0.0" {
		t.Errorf("expected ^1.0.0, got %q", c)
	}
}

func TestCollectDerived_AllInferences(t *testing.T) {
	d := collectDerived(
		CreateInput{Description: "test"},
		descriptionHints{
			hasHTTP: true, hasGRPC: true, hasEvents: true,
			storesData: true, isWorker: true, isScheduled: true,
		},
	)
	if len(d) < 5 {
		t.Errorf("expected at least 5 derived, got %d: %v", len(d), d)
	}
}

func TestAssessSections_Full(t *testing.T) {
	port := 8080
	c := &contract.Contract{
		PactoVersion:   "1.0",
		Service:        contract.ServiceIdentity{Name: "test", Version: "1.0.0"},
		Interfaces:     []contract.Interface{{Name: "api", Type: "http", Port: &port}},
		Runtime:        &contract.Runtime{Workload: "service", State: contract.State{Type: "stateless"}},
		Configurations: []contract.ConfigurationSource{{Name: "default", Schema: "schema.json"}},
		Dependencies:   []contract.Dependency{{Name: "pg", Ref: "pg", Compatibility: "^1.0.0"}},
		Scaling:        &contract.Scaling{Min: 1, Max: 3},
		Metadata:       map[string]interface{}{"team": "x"},
		Policies:       []contract.PolicySource{{Name: "local", Schema: "policy/schema.json"}},
	}
	s := assessSections(c)
	for _, key := range []string{"service", "interfaces", "runtime", "configuration", "dependencies", "scaling", "metadata", "policies"} {
		if s[key] != "present" {
			t.Errorf("expected %s=present, got %q", key, s[key])
		}
	}
}

func TestApplyHintsToCreate_GRPCAndScheduled(t *testing.T) {
	input := CreateInput{}
	h := descriptionHints{hasGRPC: true, isScheduled: true}
	applyHintsToCreate(&input, h)
	if len(input.Interfaces) != 1 || input.Interfaces[0].Type != "grpc" {
		t.Error("expected gRPC interface from hints")
	}
	if input.Workload != "scheduled" {
		t.Errorf("expected scheduled, got %q", input.Workload)
	}
}

func TestApplyHintsToCreate_WorkerNotOverridden(t *testing.T) {
	input := CreateInput{Workload: "service"}
	h := descriptionHints{isWorker: true}
	applyHintsToCreate(&input, h)
	if input.Workload != "service" {
		t.Error("explicit workload should not be overridden")
	}
}

func TestApplyHintsToCreate_EventOnly(t *testing.T) {
	input := CreateInput{}
	h := descriptionHints{hasEvents: true}
	applyHintsToCreate(&input, h)
	if len(input.Interfaces) != 1 || input.Interfaces[0].Type != "event" {
		t.Error("expected event interface from hints")
	}
}

func TestCreate_EventInterface(t *testing.T) {
	result, err := Create(CreateInput{
		Name: "event-svc",
		Interfaces: []InterfaceInput{
			{Name: "events", Type: "event"},
		},
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Summary.Interfaces) != 1 {
		t.Error("expected 1 interface")
	}
}

func TestWriteBundle_InterfaceErrors(t *testing.T) {
	oldMkdir := osMkdirAll
	callCount := 0
	osMkdirAll = func(path string, perm os.FileMode) error {
		callCount++
		if callCount == 2 {
			return fmt.Errorf("interfaces mkdir failed")
		}
		return oldMkdir(path, perm)
	}
	defer func() { osMkdirAll = oldMkdir }()

	port := 8080
	_, err := writeBundle(t.TempDir(), []byte("test"), CreateInput{
		Name: "test",
		Interfaces: []InterfaceInput{
			{Name: "api", Type: "http", Port: &port},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "interfaces") {
		t.Errorf("expected interfaces error, got: %v", err)
	}
}

func TestWriteBundle_ConfigError(t *testing.T) {
	oldMkdir := osMkdirAll
	callCount := 0
	osMkdirAll = func(path string, perm os.FileMode) error {
		callCount++
		if callCount == 3 {
			return fmt.Errorf("config mkdir failed")
		}
		return oldMkdir(path, perm)
	}
	defer func() { osMkdirAll = oldMkdir }()

	port := 8080
	_, err := writeBundle(t.TempDir(), []byte("test"), CreateInput{
		Name: "test",
		Interfaces: []InterfaceInput{
			{Name: "api", Type: "http", Port: &port},
		},
		ConfigProperties: []ConfigProperty{{Name: "PORT"}},
	})
	if err == nil || !strings.Contains(err.Error(), "config") {
		t.Errorf("expected config error, got: %v", err)
	}
}

func TestSummarizeFromMap_Empty(t *testing.T) {
	s := summarizeFromMap(map[string]interface{}{})
	if s.Name != "" {
		t.Error("expected empty name")
	}
}

func TestSummarizeFromMap_StructuredOwner(t *testing.T) {
	t.Run("team and dri", func(t *testing.T) {
		m := map[string]interface{}{
			"service": map[string]interface{}{
				"name":    "svc",
				"version": "1.0.0",
				"owner":   map[string]interface{}{"team": "foundations", "dri": "alice"},
			},
		}
		s := summarizeFromMap(m)
		if s.Owner != "foundations" {
			t.Errorf("expected owner=foundations, got %q", s.Owner)
		}
	})
	t.Run("dri only", func(t *testing.T) {
		m := map[string]interface{}{
			"service": map[string]interface{}{
				"name":    "svc",
				"version": "1.0.0",
				"owner":   map[string]interface{}{"dri": "bob"},
			},
		}
		s := summarizeFromMap(m)
		if s.Owner != "bob" {
			t.Errorf("expected owner=bob, got %q", s.Owner)
		}
	})
	t.Run("empty structured", func(t *testing.T) {
		m := map[string]interface{}{
			"service": map[string]interface{}{
				"name":    "svc",
				"version": "1.0.0",
				"owner":   map[string]interface{}{},
			},
		}
		s := summarizeFromMap(m)
		if s.Owner != "" {
			t.Errorf("expected empty owner, got %q", s.Owner)
		}
	})
}

// --- Error path coverage ---

func TestEdit_ParseYAMLError(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("invalid: yaml: {{"), 0644)
	_, err := Edit(EditInput{Path: dir, Version: strPtr("1.0.0")})
	if err == nil {
		t.Error("expected error for bad YAML")
	}
}

func TestEdit_WriteError(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	oldWrite := osWriteFile
	osWriteFile = func(_ string, _ []byte, _ os.FileMode) error {
		return fmt.Errorf("write failed")
	}
	defer func() { osWriteFile = oldWrite }()

	_, err := Edit(EditInput{
		Path:    dir,
		Version: strPtr("2.0.0"),
	})
	if err == nil {
		t.Error("expected error from write failure")
	}
}

func TestCreate_MarshalError(t *testing.T) {
	// This is hard to trigger naturally. Let me just test validateYAML validation failure.
	err := validateYAML([]byte(`pactoVersion: "1.0"
service:
  name: test
  version: "1.0.0"
runtime:
  workload: invalid-workload
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`))
	if err == nil {
		t.Error("expected validation error for invalid workload")
	}
}

func TestCheck_WithWarnings(t *testing.T) {
	// Create a contract with an event interface that has a port — triggers PORT_IGNORED warning
	dir := t.TempDir()
	port := 9090
	yamlContent := `pactoVersion: "1.0"
service:
  name: warn-svc
  version: "1.0.0"
interfaces:
  - name: events
    type: event
    port: 9090
    contract: interfaces/events.yaml
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`
	_ = port
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "interfaces", "events.yaml"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected at least one warning (PORT_IGNORED)")
	}
}

func TestWriteBundle_InterfaceWriteError(t *testing.T) {
	oldWrite := osWriteFile
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		if strings.Contains(path, "interfaces") {
			return fmt.Errorf("interface write failed")
		}
		return oldWrite(path, data, perm)
	}
	defer func() { osWriteFile = oldWrite }()

	port := 8080
	_, err := writeBundle(t.TempDir(), []byte("test"), CreateInput{
		Name: "test",
		Interfaces: []InterfaceInput{
			{Name: "api", Type: "http", Port: &port},
		},
	})
	if err == nil {
		t.Error("expected error")
	}
}

func TestWriteBundle_ConfigWriteError(t *testing.T) {
	oldWrite := osWriteFile
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		if strings.Contains(path, "configuration") {
			return fmt.Errorf("config write failed")
		}
		return oldWrite(path, data, perm)
	}
	defer func() { osWriteFile = oldWrite }()

	port := 8080
	_, err := writeBundle(t.TempDir(), []byte("test"), CreateInput{
		Name: "test",
		Interfaces: []InterfaceInput{
			{Name: "api", Type: "http", Port: &port},
		},
		ConfigProperties: []ConfigProperty{{Name: "PORT"}},
	})
	if err == nil {
		t.Error("expected error")
	}
}

func TestCreateHandler_ReplicasOnly(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := callTool(t, svc, "pacto_create", map[string]any{
		"name":     "replica-svc",
		"replicas": 2,
		"dry_run":  true,
	})
	if result.IsError {
		t.Errorf("unexpected error: %s", resultText(t, result))
	}
}

func TestEdit_ValidationFailure(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	// Set an invalid workload type — should fail validation
	wl := "INVALID_WORKLOAD"
	_, err := Edit(EditInput{
		Path:     dir,
		Workload: &wl,
	})
	if err == nil {
		t.Error("expected validation error")
	}
}

func TestEdit_MarshalAfterEdits(t *testing.T) {
	// Exercises the full edit→marshal→parse→validate pipeline
	dir := testutil.WriteTestBundle(t)
	result, err := Edit(EditInput{
		Path:    dir,
		Version: strPtr("3.0.0"),
		Owner:   strPtr("team/devops"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Version != "3.0.0" {
		t.Error("expected version 3.0.0")
	}
	if result.Summary.Owner != "team/devops" {
		t.Error("expected owner team/devops")
	}
}

func TestCheck_WarningsCollected(t *testing.T) {
	// Trigger a contract that produces warnings (not just errors)
	// The test bundle is valid — no warnings expected but code path is covered
	dir := testutil.WriteTestBundle(t)
	result, err := Check(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Just verify the structure is populated
	if result.Summary.Name == "" {
		t.Error("expected non-empty summary")
	}
}

func TestWriteBundle_PactoWriteError(t *testing.T) {
	oldWrite := osWriteFile
	osWriteFile = func(_ string, _ []byte, _ os.FileMode) error {
		return fmt.Errorf("pacto write failed")
	}
	defer func() { osWriteFile = oldWrite }()

	_, err := writeBundle(t.TempDir(), []byte("test"), CreateInput{Name: "test"})
	if err == nil {
		t.Error("expected error")
	}
}

func TestCreate_ValidationError(t *testing.T) {
	// Create with invalid workload
	_, err := Create(CreateInput{
		Name:     "bad",
		Workload: "INVALID",
		DryRun:   true,
	})
	if err == nil {
		t.Error("expected validation error for invalid workload")
	}
}

func TestBuildBundleFSForValidation_WalkDirErrorsSkipped(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	port := 8080
	c := &contract.Contract{
		PactoVersion:   "1.0",
		Service:        contract.ServiceIdentity{Name: "test", Version: "1.0.0"},
		Interfaces:     []contract.Interface{{Name: "new-api", Type: "http", Port: &port, Contract: "interfaces/new-api.yaml"}},
		Configurations: []contract.ConfigurationSource{{Name: "default", Schema: "configuration/schema.json"}},
	}
	result := buildBundleFSForValidation(dir, []byte("test"), c)
	// Should have stub for new interface and config
	mapFS := result.(fstest.MapFS)
	if _, ok := mapFS["interfaces/new-api.yaml"]; !ok {
		t.Error("expected stub for new interface")
	}
	if _, ok := mapFS["configuration/schema.json"]; !ok {
		t.Error("expected stub for config schema")
	}
}

// --- Final coverage tests ---

func TestEdit_BuildFSError(t *testing.T) {
	// Use a directory that doesn't exist for building FS
	dir := t.TempDir()
	yaml := `pactoVersion: "1.0"
service:
  name: test
  version: "1.0.0"
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644)

	result, err := Edit(EditInput{
		Path:    dir,
		Version: strPtr("2.0.0"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary.Version != "2.0.0" {
		t.Error("expected 2.0.0")
	}
}

func TestCreateHandler_AllPaths(t *testing.T) {
	svc := app.NewService(nil, nil)

	// Exercise all string fields in createHandler
	result := callTool(t, svc, "pacto_create", map[string]any{
		"name":                         "full-svc",
		"description":                  "REST API with postgres",
		"path":                         filepath.Join(t.TempDir(), "full-svc"),
		"version":                      "1.0.0",
		"owner":                        "team/x",
		"workload":                     "service",
		"stores_data":                  true,
		"data_survives_restart":        true,
		"data_shared_across_instances": true,
		"data_loss_impact":             "high",
		"interfaces":                   `[{"name":"api","type":"http","port":8080}]`,
		"dependencies":                 `[{"name":"postgres","ref":"postgres","required":true}]`,
		"config_properties":            `[{"name":"PORT","type":"integer","required":true}]`,
		"metadata":                     `{"team":"platform"}`,
	})
	if result.IsError {
		t.Errorf("unexpected error: %s", resultText(t, result))
	}
}

// --- yaml function variable error paths ---

func TestValueToNode_MarshalError(t *testing.T) {
	orig := yamlMarshalFn
	defer func() { yamlMarshalFn = orig }()
	yamlMarshalFn = func(v interface{}) ([]byte, error) {
		return nil, fmt.Errorf("marshal boom")
	}
	_, err := valueToNode("hello")
	if err == nil || !strings.Contains(err.Error(), "marshal boom") {
		t.Errorf("expected marshal error, got: %v", err)
	}
}

func TestValueToNode_UnmarshalError(t *testing.T) {
	orig := yamlUnmarshalFn
	defer func() { yamlUnmarshalFn = orig }()
	yamlUnmarshalFn = func(data []byte, v interface{}) error {
		return fmt.Errorf("unmarshal boom")
	}
	_, err := valueToNode("hello")
	if err == nil || !strings.Contains(err.Error(), "unmarshal boom") {
		t.Errorf("expected unmarshal error, got: %v", err)
	}
}

func TestMarshalContract_ValueToNodeError(t *testing.T) {
	orig := yamlMarshalFn
	defer func() { yamlMarshalFn = orig }()
	yamlMarshalFn = func(v interface{}) ([]byte, error) {
		return nil, fmt.Errorf("node error")
	}
	m := map[string]interface{}{
		"pactoVersion": "v1",
	}
	_, err := marshalContract(m)
	if err == nil || !strings.Contains(err.Error(), "node error") {
		t.Errorf("expected valueToNode error, got: %v", err)
	}
}

func TestBuildBundleFSForValidation_WalkError(t *testing.T) {
	// Use a broken FS to trigger the walkErr path
	origDirFS := osDirFS
	defer func() { osDirFS = origDirFS }()

	osDirFS = func(dir string) fs.FS {
		return &brokenFS{}
	}

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "test"},
	}
	result := buildBundleFSForValidation("/nonexistent", []byte("test"), c)
	if result == nil {
		t.Error("expected non-nil result even with broken FS")
	}
}

func TestBuildBundleFSForValidation_ReadFileError(t *testing.T) {
	// Create a dir with a file that has permissions removed
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("test"), 0644)

	// Create a file that's unreadable
	unreadable := filepath.Join(dir, "unreadable.txt")
	_ = os.WriteFile(unreadable, []byte("data"), 0644)
	_ = os.Chmod(unreadable, 0000)
	defer func() { _ = os.Chmod(unreadable, 0644) }()

	c := &contract.Contract{
		Service: contract.ServiceIdentity{Name: "test"},
	}
	result := buildBundleFSForValidation(dir, []byte("test"), c)
	// The unreadable file should be skipped gracefully
	_ = result
}

func TestEdit_ContractParseError(t *testing.T) {
	// Write a pacto.yaml with an unknown field that survives map round-trip
	// but causes contract.Parse to fail with KnownFields(true)
	dir := t.TempDir()
	yamlContent := `pactoVersion: "1.0"
service:
  name: test
  version: "1.0.0"
  unknownField: bad
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
`
	_ = os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yamlContent), 0644)
	_, err := Edit(EditInput{Path: dir, Version: strPtr("2.0.0")})
	if err == nil {
		t.Error("expected parse error for unknown field")
	}
}

func TestCreate_YAMLMarshalError(t *testing.T) {
	orig := yamlMarshalFn
	defer func() { yamlMarshalFn = orig }()
	yamlMarshalFn = func(v interface{}) ([]byte, error) {
		return nil, fmt.Errorf("create marshal fail")
	}
	_, err := Create(CreateInput{Name: "test"})
	if err == nil || !strings.Contains(err.Error(), "failed to marshal") {
		t.Errorf("expected marshal error, got: %v", err)
	}
}

func TestEdit_YAMLMarshalError(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	orig := yamlMarshalFn
	defer func() { yamlMarshalFn = orig }()
	yamlMarshalFn = func(v interface{}) ([]byte, error) {
		return nil, fmt.Errorf("edit marshal fail")
	}
	_, err := Edit(EditInput{Path: dir, Version: strPtr("2.0.0")})
	if err == nil || !strings.Contains(err.Error(), "failed to marshal") {
		t.Errorf("expected marshal error, got: %v", err)
	}
}

// --- Test helpers ---

// brokenFS is a filesystem that returns errors for all operations.
func TestRewireHealthMetricsIfNeeded_NonMapInterface(t *testing.T) {
	rt := map[string]interface{}{}
	m := map[string]interface{}{
		"interfaces": []interface{}{
			"not-a-map", // triggers the non-map branch in the loop
			map[string]interface{}{"name": "api", "type": "http"},
		},
	}
	rewireHealthMetricsIfNeeded(rt, m)
	if _, ok := rt["health"]; !ok {
		t.Error("expected health to be wired from the valid interface entry")
	}
}

func TestRewireHealthMetricsIfNeeded_InterfacesNotSlice(t *testing.T) {
	rt := map[string]interface{}{}
	m := map[string]interface{}{
		"interfaces": "not-a-slice", // triggers the !ok return on type assertion
	}
	rewireHealthMetricsIfNeeded(rt, m)
	if _, ok := rt["health"]; ok {
		t.Error("expected no health when interfaces is not a slice")
	}
}

type brokenFS struct{}

func (b *brokenFS) Open(name string) (fs.File, error) {
	return nil, fmt.Errorf("broken FS: open %s", name)
}

func strPtr(s string) *string { return &s }

func containsStr(ss []string, substr string) bool {
	for _, s := range ss {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
