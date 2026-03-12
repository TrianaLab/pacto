package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/testutil"
	"github.com/trianalab/pacto/pkg/contract"
)

// callTool connects an MCP client to the server and calls the named tool.
func callTool(t *testing.T, svc *app.Service, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	ctx := context.Background()
	server := NewServer(svc, "test")
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0"}, nil)

	t1, t2 := mcpsdk.NewInMemoryTransports()
	_, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%s) error: %v", toolName, err)
	}
	return result
}

// resultText extracts the text content from a CallToolResult.
func resultText(t *testing.T, result *mcpsdk.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected at least 1 content item")
	}
	tc, ok := result.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatal("expected TextContent")
	}
	return tc.Text
}

func TestValidateTool(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)

	t.Run("valid contract", func(t *testing.T) {
		result := callTool(t, svc, "pacto_validate", map[string]any{"path": dir})
		text := resultText(t, result)
		if !strings.Contains(text, `"Valid": true`) {
			t.Errorf("expected valid=true, got: %s", text)
		}
	})

	t.Run("invalid path", func(t *testing.T) {
		result := callTool(t, svc, "pacto_validate", map[string]any{"path": "/nonexistent"})
		if !result.IsError {
			text := resultText(t, result)
			if strings.Contains(text, `"Valid": true`) {
				t.Error("expected invalid result for nonexistent path")
			}
		}
	})
}

func TestInspectTool(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)

	result := callTool(t, svc, "pacto_inspect", map[string]any{"ref": dir})
	text := resultText(t, result)

	if !strings.Contains(text, "test-svc") {
		t.Errorf("expected service name in output, got: %s", text)
	}
	if !strings.Contains(text, "1.0.0") {
		t.Errorf("expected version in output, got: %s", text)
	}
}

func TestResolveDependenciesTool(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)

	result := callTool(t, svc, "pacto_resolve_dependencies", map[string]any{"ref": dir})
	text := resultText(t, result)

	if !strings.Contains(text, "test-svc") {
		t.Errorf("expected service name in graph result, got: %s", text)
	}
}

func TestListInterfacesTool(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)

	result := callTool(t, svc, "pacto_list_interfaces", map[string]any{"ref": dir})
	text := resultText(t, result)

	var parsed interfacesResult
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if len(parsed.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(parsed.Interfaces))
	}
	if parsed.Interfaces[0].Name != "api" {
		t.Errorf("expected interface name 'api', got %q", parsed.Interfaces[0].Name)
	}
}

func TestGenerateDocsTool(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)

	result := callTool(t, svc, "pacto_generate_docs", map[string]any{"ref": dir})
	text := resultText(t, result)

	if !strings.Contains(text, "# test-svc") {
		t.Errorf("expected markdown heading, got: %s", text)
	}
}

func TestExplainTool(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)

	result := callTool(t, svc, "pacto_explain", map[string]any{"ref": dir})
	text := resultText(t, result)

	var parsed explainSummary
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if !strings.Contains(parsed.Summary, "test-svc") {
		t.Errorf("expected service name in summary, got: %s", parsed.Summary)
	}
	if !strings.Contains(parsed.Interfaces, "api") {
		t.Errorf("expected interface in summary, got: %s", parsed.Interfaces)
	}
}

func TestGenerateContractTool(t *testing.T) {
	svc := app.NewService(nil, nil)

	t.Run("minimal", func(t *testing.T) {
		result := callTool(t, svc, "pacto_generate_contract", map[string]any{
			"service_name": "payments",
		})
		text := resultText(t, result)
		if !strings.Contains(text, "name: payments") {
			t.Errorf("expected service name, got: %s", text)
		}
		if !strings.Contains(text, "pactoVersion:") {
			t.Errorf("expected pactoVersion, got: %s", text)
		}
	})

	t.Run("with HTTP and database", func(t *testing.T) {
		result := callTool(t, svc, "pacto_generate_contract", map[string]any{
			"service_name":   "payments",
			"language":       "go",
			"exposes_http":   true,
			"needs_database": true,
		})
		text := resultText(t, result)
		if !strings.Contains(text, "http-api") {
			t.Errorf("expected HTTP interface, got: %s", text)
		}
		if !strings.Contains(text, "postgres") {
			t.Errorf("expected postgres dependency, got: %s", text)
		}
		if !strings.Contains(text, "language: go") {
			t.Errorf("expected language metadata, got: %s", text)
		}
		if !strings.Contains(text, "stateful") {
			t.Errorf("expected stateful for database, got: %s", text)
		}
	})

	t.Run("with gRPC and cache", func(t *testing.T) {
		result := callTool(t, svc, "pacto_generate_contract", map[string]any{
			"service_name": "cache-svc",
			"exposes_grpc": true,
			"needs_cache":  true,
		})
		text := resultText(t, result)
		if !strings.Contains(text, "grpc-api") {
			t.Errorf("expected gRPC interface, got: %s", text)
		}
		if !strings.Contains(text, "redis") {
			t.Errorf("expected redis dependency, got: %s", text)
		}
	})

	t.Run("missing service_name", func(t *testing.T) {
		result := callTool(t, svc, "pacto_generate_contract", map[string]any{})
		if !result.IsError {
			t.Error("expected IsError for missing service_name")
		}
	})
}

func TestSuggestDependenciesTool(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)

	result := callTool(t, svc, "pacto_suggest_dependencies", map[string]any{"contract": dir})
	text := resultText(t, result)

	var parsed suggestResult
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if len(parsed.Suggested) == 0 {
		t.Error("expected at least one suggestion")
	}
}

func TestSchemaTool(t *testing.T) {
	svc := app.NewService(nil, nil)

	result := callTool(t, svc, "pacto_schema", map[string]any{})
	if result.IsError {
		t.Fatal("expected no error from pacto_schema")
	}

	text := resultText(t, result)

	var parsed schemaResult
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if !strings.Contains(parsed.Description, "operational contract format") {
		t.Errorf("expected description to mention Pacto, got: %s", parsed.Description)
	}
	if parsed.Docs == "" {
		t.Error("expected non-empty docs URL")
	}
	if !strings.Contains(parsed.JSONSchema, `"pactoVersion"`) {
		t.Errorf("expected pactoVersion in JSON schema, got: %.100s...", parsed.JSONSchema)
	}
}

func TestBuildExplainSummary(t *testing.T) {
	port := 8080
	r := &app.ExplainResult{
		Name:         "my-svc",
		Version:      "1.0.0",
		Owner:        "team/platform",
		PactoVersion: "1.0",
		Runtime: app.ExplainRuntime{
			WorkloadType: "service",
			StateType:    "stateless",
		},
		Interfaces: []app.ExplainInterface{
			{Name: "api", Type: "http", Port: &port, Visibility: "public"},
			{Name: "events", Type: "event"},
		},
		Dependencies: []app.ExplainDependency{
			{Ref: "postgres", Required: true, Compatibility: "^1.0.0"},
			{Ref: "redis", Required: false, Compatibility: "^2.0.0"},
		},
	}

	summary := buildExplainSummary(r)

	if !strings.Contains(summary.Summary, "my-svc@1.0.0") {
		t.Errorf("expected service ref in summary: %s", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "team/platform") {
		t.Errorf("expected owner in summary: %s", summary.Summary)
	}
	if !strings.Contains(summary.Interfaces, "api (http, port 8080, public)") {
		t.Errorf("expected interface details: %s", summary.Interfaces)
	}
	if !strings.Contains(summary.Interfaces, "events (event)") {
		t.Errorf("expected event interface: %s", summary.Interfaces)
	}
	if !strings.Contains(summary.Dependencies, "postgres (^1.0.0, required)") {
		t.Errorf("expected postgres dep: %s", summary.Dependencies)
	}
	if !strings.Contains(summary.Dependencies, "redis (^2.0.0, optional)") {
		t.Errorf("expected redis dep: %s", summary.Dependencies)
	}
}

func TestBuildExplainSummaryNoOwner(t *testing.T) {
	r := &app.ExplainResult{
		Name:         "svc",
		Version:      "1.0.0",
		PactoVersion: "1.0",
		Runtime:      app.ExplainRuntime{WorkloadType: "service", StateType: "stateless"},
	}
	summary := buildExplainSummary(r)
	if strings.Contains(summary.Summary, "owned by") {
		t.Errorf("unexpected owner in summary: %s", summary.Summary)
	}
}

func TestBuildContractYAML(t *testing.T) {
	t.Run("minimal", func(t *testing.T) {
		yaml := buildContractYAML(generateInput{ServiceName: "test"})
		if !strings.Contains(yaml, "name: test") {
			t.Errorf("expected service name: %s", yaml)
		}
		if !strings.Contains(yaml, "stateless") {
			t.Errorf("expected stateless: %s", yaml)
		}
		if strings.Contains(yaml, "interfaces:") {
			t.Errorf("unexpected interfaces block: %s", yaml)
		}
	})

	t.Run("full", func(t *testing.T) {
		yaml := buildContractYAML(generateInput{
			ServiceName: "full-svc",
			Language:    "python",
			ExposesHTTP: true,
			ExposesGRPC: true,
			NeedsDB:     true,
			NeedsCache:  true,
		})
		if !strings.Contains(yaml, "language: python") {
			t.Errorf("expected language: %s", yaml)
		}
		if !strings.Contains(yaml, "http-api") {
			t.Errorf("expected http-api: %s", yaml)
		}
		if !strings.Contains(yaml, "grpc-api") {
			t.Errorf("expected grpc-api: %s", yaml)
		}
		if !strings.Contains(yaml, "postgres") {
			t.Errorf("expected postgres: %s", yaml)
		}
		if !strings.Contains(yaml, "redis") {
			t.Errorf("expected redis: %s", yaml)
		}
		if !strings.Contains(yaml, "stateful") {
			t.Errorf("expected stateful: %s", yaml)
		}
		if !strings.Contains(yaml, "/health") {
			t.Errorf("expected health check: %s", yaml)
		}
	})

	t.Run("no health without http", func(t *testing.T) {
		yaml := buildContractYAML(generateInput{ServiceName: "no-http"})
		if strings.Contains(yaml, "health:") {
			t.Errorf("unexpected health for non-HTTP: %s", yaml)
		}
	})
}

func TestSuggestDependencies(t *testing.T) {
	t.Run("stateless service", func(t *testing.T) {
		r := &app.ExplainResult{
			Name:    "svc",
			Version: "1.0.0",
			Runtime: app.ExplainRuntime{WorkloadType: "service", StateType: "stateless"},
		}
		suggestions := suggestDependencies(r)
		contains := func(name string) bool {
			for _, s := range suggestions {
				if s == name {
					return true
				}
			}
			return false
		}
		if !contains("redis") {
			t.Error("expected redis suggestion for stateless service")
		}
		if !contains("prometheus") {
			t.Error("expected prometheus suggestion for service workload")
		}
	})

	t.Run("stateful with public HTTP", func(t *testing.T) {
		port := 8080
		r := &app.ExplainResult{
			Name:    "api-svc",
			Version: "1.0.0",
			Runtime: app.ExplainRuntime{
				WorkloadType: "service",
				StateType:    "stateful",
				Durability:   "persistent",
				Scope:        "shared",
			},
			Interfaces: []app.ExplainInterface{
				{Name: "api", Type: "http", Port: &port, Visibility: "public"},
			},
		}
		suggestions := suggestDependencies(r)
		contains := func(name string) bool {
			for _, s := range suggestions {
				if s == name {
					return true
				}
			}
			return false
		}
		if !contains("api-gateway") {
			t.Error("expected api-gateway for public HTTP")
		}
		if !contains("postgres") {
			t.Error("expected postgres for stateful+persistent")
		}
		if !contains("redis") {
			t.Error("expected redis for shared scope")
		}
	})

	t.Run("excludes existing deps", func(t *testing.T) {
		r := &app.ExplainResult{
			Name:    "svc",
			Version: "1.0.0",
			Runtime: app.ExplainRuntime{WorkloadType: "service", StateType: "stateless"},
			Dependencies: []app.ExplainDependency{
				{Ref: "redis"},
				{Ref: "prometheus"},
			},
		}
		suggestions := suggestDependencies(r)
		for _, s := range suggestions {
			if s == "redis" || s == "prometheus" {
				t.Errorf("should not suggest existing dep: %s", s)
			}
		}
	})
}

func TestInputSchema(t *testing.T) {
	schema := inputSchema(map[string]property{
		"path": {Type: "string", Description: "a path"},
	}, []string{"path"})

	if schema["type"] != "object" {
		t.Errorf("expected type=object, got %v", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	pathProp, ok := props["path"].(map[string]any)
	if !ok {
		t.Fatal("expected path property")
	}
	if pathProp["type"] != "string" {
		t.Errorf("expected type=string, got %v", pathProp["type"])
	}
	if pathProp["description"] != "a path" {
		t.Errorf("expected description, got %v", pathProp["description"])
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("expected required slice")
	}
	if len(required) != 1 || required[0] != "path" {
		t.Errorf("expected required=[path], got %v", required)
	}
}

func TestToolDefinitions(t *testing.T) {
	tools := []struct {
		name string
		fn   func() *mcpsdk.Tool
	}{
		{"pacto_validate", validateTool},
		{"pacto_inspect", inspectTool},
		{"pacto_resolve_dependencies", resolveDependenciesTool},
		{"pacto_list_interfaces", listInterfacesTool},
		{"pacto_generate_docs", generateDocsTool},
		{"pacto_explain", explainTool},
		{"pacto_generate_contract", generateContractTool},
		{"pacto_suggest_dependencies", suggestDependenciesTool},
		{"pacto_schema", schemaTool},
	}

	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			tool := tt.fn()
			if tool.Name != tt.name {
				t.Errorf("expected name=%s, got %s", tt.name, tool.Name)
			}
			if tool.Description == "" {
				t.Error("expected non-empty description")
			}
			if tool.InputSchema == nil {
				t.Error("expected non-nil InputSchema")
			}
		})
	}
}

func TestValidateToolError(t *testing.T) {
	// Validate with a path that triggers an error from svc.Validate
	// (vs wrapping a parse error in the result).
	// When the bundle FS is missing pacto.yaml and RawYAML is nil,
	// validate returns an error.
	store := &testutil.MockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			port := 8080
			return &contract.Bundle{
				Contract: &contract.Contract{
					PactoVersion: "1.0",
					Service:      contract.ServiceIdentity{Name: "test", Version: "1.0.0"},
					Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
				},
				FS: fstest.MapFS{}, // empty FS, no pacto.yaml
				// RawYAML deliberately nil to trigger ReadFile error
			}, nil
		},
	}
	svc := app.NewService(store, nil)
	result := callTool(t, svc, "pacto_validate", map[string]any{"path": "oci://test/svc:1.0.0"})
	if !result.IsError {
		t.Error("expected IsError when validate returns an error")
	}
}

func TestErrorToolCalls(t *testing.T) {
	svc := app.NewService(nil, nil)

	errorTools := []struct {
		name string
		args map[string]any
	}{
		{"pacto_inspect", map[string]any{"ref": "/nonexistent-path-for-test"}},
		{"pacto_resolve_dependencies", map[string]any{"ref": "/nonexistent-path-for-test"}},
		{"pacto_list_interfaces", map[string]any{"ref": "/nonexistent-path-for-test"}},
		{"pacto_generate_docs", map[string]any{"ref": "/nonexistent-path-for-test"}},
		{"pacto_explain", map[string]any{"ref": "/nonexistent-path-for-test"}},
		{"pacto_suggest_dependencies", map[string]any{"contract": "/nonexistent-path-for-test"}},
	}

	for _, tt := range errorTools {
		t.Run(tt.name, func(t *testing.T) {
			result := callTool(t, svc, tt.name, tt.args)
			if !result.IsError {
				t.Error("expected IsError for invalid path")
			}
		})
	}
}
