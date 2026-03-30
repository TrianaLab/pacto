//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/internal/app"
	pactomcp "github.com/trianalab/pacto/internal/mcp"
)

// mcpCallTool sets up an in-memory MCP server+client and calls the named tool.
func mcpCallTool(t *testing.T, svc *app.Service, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	ctx := context.Background()
	server := pactomcp.NewServer(svc, "test-e2e")
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e-client", Version: "1.0"}, nil)

	t1, t2 := mcpsdk.NewInMemoryTransports()
	_, err := server.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%s) error: %v", toolName, err)
	}
	return result
}

func mcpResultText(t *testing.T, result *mcpsdk.CallToolResult) string {
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

func TestMCPCreate(t *testing.T) {
	t.Parallel()
	svc := app.NewService(nil, nil)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "payments")

	result := mcpCallTool(t, svc, "pacto_create", map[string]any{
		"name":        "payments",
		"path":        outPath,
		"description": "REST API backed by postgres",
	})
	text := mcpResultText(t, result)
	assertContains(t, text, "payments")
	assertContains(t, text, "pacto.yaml")

	// Verify files were created
	if _, err := os.Stat(filepath.Join(outPath, "pacto.yaml")); err != nil {
		t.Errorf("expected pacto.yaml to exist: %v", err)
	}
}

func TestMCPCreateDryRun(t *testing.T) {
	t.Parallel()
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_create", map[string]any{
		"name":    "dry-run-svc",
		"dry_run": true,
	})
	text := mcpResultText(t, result)
	assertContains(t, text, "dry-run-svc")

	var parsed struct {
		FileCount int `json:"fileCount"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if parsed.FileCount != 0 {
		t.Errorf("expected fileCount=0 for dry run, got %d", parsed.FileCount)
	}
}

func TestMCPCreateMissingName(t *testing.T) {
	t.Parallel()
	svc := app.NewService(nil, nil)
	result := mcpCallTool(t, svc, "pacto_create", map[string]any{})
	if !result.IsError {
		t.Error("expected IsError for missing name")
	}
}

func TestMCPCheck(t *testing.T) {
	t.Parallel()
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_check", map[string]any{"path": postgresPath})
	text := mcpResultText(t, result)
	assertContains(t, text, `"valid": true`)
	assertContains(t, text, "postgres-pacto")
}

func TestMCPEdit(t *testing.T) {
	t.Parallel()
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_edit", map[string]any{
		"path":    postgresPath,
		"version": "2.0.0",
	})
	text := mcpResultText(t, result)
	assertContains(t, text, "2.0.0")

	var parsed struct {
		Changes []string `json:"changes"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if len(parsed.Changes) == 0 {
		t.Error("expected at least one change")
	}
}

func TestMCPSchema(t *testing.T) {
	t.Parallel()
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_schema", map[string]any{})
	text := mcpResultText(t, result)
	assertContains(t, text, "pactoVersion")
	assertContains(t, text, "operational contract format")
}

func TestMCPCommandHelp(t *testing.T) {
	t.Parallel()
	output, err := runCommand(t, nil, "mcp", "--help")
	if err != nil {
		t.Fatalf("mcp --help failed: %v\noutput: %s", err, output)
	}
	if !strings.Contains(output, "Model Context Protocol") {
		t.Errorf("expected MCP description, got: %s", output)
	}
}

func TestMCPCommandExtraArgs(t *testing.T) {
	t.Parallel()
	_, err := runCommand(t, nil, "mcp", "extra-arg")
	if err == nil {
		t.Error("expected error for extra args")
	}
}
