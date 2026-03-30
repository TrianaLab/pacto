package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/testutil"
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

func TestCreateTool(t *testing.T) {
	svc := app.NewService(nil, nil)

	t.Run("minimal dry run", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":    "payments",
			"dry_run": true,
		})
		text := resultText(t, result)
		if !strings.Contains(text, "payments") {
			t.Errorf("expected service name, got: %s", text)
		}
		var parsed CreateResult
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			t.Fatalf("expected valid JSON: %v", err)
		}
		if parsed.FileCount != 0 {
			t.Errorf("expected fileCount=0 for dry run, got %d", parsed.FileCount)
		}
	})

	t.Run("with description inference", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":        "order-api",
			"description": "REST API backed by postgres",
			"dry_run":     true,
		})
		text := resultText(t, result)
		if !strings.Contains(text, "http") {
			t.Errorf("expected HTTP interface inferred, got: %s", text)
		}
		if !strings.Contains(text, "inferred") {
			t.Errorf("expected 'inferred' in derived, got: %s", text)
		}
	})

	t.Run("with explicit interfaces", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":       "grpc-svc",
			"interfaces": `[{"name":"grpc-api","type":"grpc","port":9090}]`,
			"dry_run":    true,
		})
		text := resultText(t, result)
		if !strings.Contains(text, "grpc-api") {
			t.Errorf("expected gRPC interface, got: %s", text)
		}
	})

	t.Run("with stateful runtime", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":                  "db-svc",
			"stores_data":           true,
			"data_survives_restart": true,
			"dry_run":               true,
		})
		text := resultText(t, result)
		if !strings.Contains(text, "stateful") {
			t.Errorf("expected stateful state, got: %s", text)
		}
	})

	t.Run("writes files when not dry run", func(t *testing.T) {
		dir := t.TempDir()
		outPath := filepath.Join(dir, "my-svc")
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":       "my-svc",
			"path":       outPath,
			"interfaces": `[{"name":"http-api","type":"http","port":8080}]`,
		})
		if result.IsError {
			t.Fatalf("expected no error: %s", resultText(t, result))
		}
		text := resultText(t, result)
		var parsed CreateResult
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			t.Fatalf("expected valid JSON: %v", err)
		}
		if parsed.FileCount < 1 {
			t.Errorf("expected at least 1 file, got %d", parsed.FileCount)
		}
		if _, err := os.Stat(filepath.Join(outPath, "pacto.yaml")); err != nil {
			t.Errorf("expected pacto.yaml to exist: %v", err)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{})
		if !result.IsError {
			t.Error("expected IsError for missing name")
		}
	})
}

func TestCreateToolErrors(t *testing.T) {
	svc := app.NewService(nil, nil)

	t.Run("invalid interfaces JSON", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":       "bad",
			"interfaces": "not-json",
		})
		if !result.IsError {
			t.Error("expected IsError for invalid JSON")
		}
	})

	t.Run("invalid dependencies JSON", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":         "bad",
			"dependencies": "not-json",
		})
		if !result.IsError {
			t.Error("expected IsError for invalid dependencies JSON")
		}
	})

	t.Run("invalid config_properties JSON", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":              "bad",
			"config_properties": "not-json",
		})
		if !result.IsError {
			t.Error("expected IsError for invalid config_properties JSON")
		}
	})

	t.Run("invalid metadata JSON", func(t *testing.T) {
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":     "bad",
			"metadata": "not-json",
		})
		if !result.IsError {
			t.Error("expected IsError for invalid metadata JSON")
		}
	})

	t.Run("create internal error", func(t *testing.T) {
		orig := yamlMarshalFn
		defer func() { yamlMarshalFn = orig }()
		yamlMarshalFn = func(v interface{}) ([]byte, error) {
			return nil, fmt.Errorf("injected marshal error")
		}
		result := callTool(t, svc, "pacto_create", map[string]any{
			"name":    "test",
			"dry_run": true,
		})
		if !result.IsError {
			t.Error("expected IsError for create internal error")
		}
	})
}

func TestEditTool(t *testing.T) {
	svc := app.NewService(nil, nil)

	t.Run("change version", func(t *testing.T) {
		dir := testutil.WriteTestBundle(t)
		result := callTool(t, svc, "pacto_edit", map[string]any{
			"path":    dir,
			"version": "2.0.0",
		})
		text := resultText(t, result)
		if !strings.Contains(text, "2.0.0") {
			t.Errorf("expected version 2.0.0, got: %s", text)
		}
		var parsed EditResult
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			t.Fatalf("expected valid JSON: %v", err)
		}
		if len(parsed.Changes) == 0 {
			t.Error("expected at least one change")
		}
	})

	t.Run("dry run", func(t *testing.T) {
		dir := testutil.WriteTestBundle(t)
		// Read original content
		original, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))

		result := callTool(t, svc, "pacto_edit", map[string]any{
			"path":    dir,
			"version": "9.9.9",
			"dry_run": true,
		})
		if result.IsError {
			t.Fatalf("unexpected error: %s", resultText(t, result))
		}

		// Verify file wasn't changed
		current, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
		if string(current) != string(original) {
			t.Error("dry run should not modify the file")
		}
	})

	t.Run("add interface", func(t *testing.T) {
		dir := testutil.WriteTestBundle(t)
		result := callTool(t, svc, "pacto_edit", map[string]any{
			"path":           dir,
			"add_interfaces": `[{"name":"events","type":"event"}]`,
		})
		text := resultText(t, result)
		if !strings.Contains(text, "events") {
			t.Errorf("expected events interface, got: %s", text)
		}
	})

	t.Run("invalid path", func(t *testing.T) {
		result := callTool(t, svc, "pacto_edit", map[string]any{
			"path":    "/nonexistent-test-path",
			"version": "1.0.0",
		})
		if !result.IsError {
			t.Error("expected IsError for invalid path")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		result := callTool(t, svc, "pacto_edit", map[string]any{
			"add_interfaces": "not-json",
		})
		if !result.IsError {
			t.Error("expected IsError for invalid JSON")
		}
	})
}

func TestCheckTool(t *testing.T) {
	svc := app.NewService(nil, nil)

	t.Run("valid contract", func(t *testing.T) {
		dir := testutil.WriteTestBundle(t)
		result := callTool(t, svc, "pacto_check", map[string]any{"path": dir})
		text := resultText(t, result)
		var parsed CheckResult
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			t.Fatalf("expected valid JSON: %v", err)
		}
		if !parsed.Valid {
			t.Errorf("expected valid=true, got errors: %v", parsed.Errors)
		}
		if parsed.Summary.Name != "test-svc" {
			t.Errorf("expected name=test-svc, got %q", parsed.Summary.Name)
		}
	})

	t.Run("invalid path", func(t *testing.T) {
		result := callTool(t, svc, "pacto_check", map[string]any{"path": "/nonexistent"})
		if !result.IsError {
			t.Error("expected IsError for invalid path")
		}
	})
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

func TestToolDefinitions(t *testing.T) {
	tools := []struct {
		name string
		fn   func() *mcpsdk.Tool
	}{
		{"pacto_create", createTool},
		{"pacto_edit", editTool},
		{"pacto_check", checkTool},
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

func TestInputSchemaNoRequired(t *testing.T) {
	schema := inputSchema(map[string]property{
		"path": {Type: "string", Description: "a path"},
	}, nil)
	if _, ok := schema["required"]; ok {
		t.Error("expected no required field when nil")
	}
}

func TestParseInputIntPtr(t *testing.T) {
	t.Run("valid int", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"replicas": 3})
		got := parseInputIntPtr(req, "replicas")
		if got == nil || *got != 3 {
			t.Errorf("expected 3, got %v", got)
		}
	})

	t.Run("missing field", func(t *testing.T) {
		req := makeRequest(t, map[string]any{})
		got := parseInputIntPtr(req, "replicas")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-number", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"replicas": "abc"})
		got := parseInputIntPtr(req, "replicas")
		if got != nil {
			t.Errorf("expected nil for non-number, got %v", got)
		}
	})

	t.Run("nil args", func(t *testing.T) {
		req := &mcpsdk.CallToolRequest{}
		got := parseInputIntPtr(req, "replicas")
		if got != nil {
			t.Errorf("expected nil for nil args")
		}
	})
}

func TestParseInputHasField(t *testing.T) {
	t.Run("has field", func(t *testing.T) {
		req := makeRequest(t, map[string]any{"stores_data": true})
		if !parseInputHasField(req, "stores_data") {
			t.Error("expected true")
		}
	})

	t.Run("missing field", func(t *testing.T) {
		req := makeRequest(t, map[string]any{})
		if parseInputHasField(req, "stores_data") {
			t.Error("expected false")
		}
	})

	t.Run("nil args", func(t *testing.T) {
		req := &mcpsdk.CallToolRequest{}
		if parseInputHasField(req, "stores_data") {
			t.Error("expected false for nil args")
		}
	})
}
