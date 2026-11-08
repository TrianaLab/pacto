//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
)

// mcpCallTool sets up an in-memory MCP server+client and calls the named tool.
func mcpCallTool(t *testing.T, svc *app.Service, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	ctx := context.Background()
	server := pactomcp.NewServer(pactomcp.Authoring{Validate: svc.ValidationReport}, "test-e2e")
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
	assertContains(t, text, "tells you which services a change breaks before it ships")
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

// TestMCPCreateFromGoldenPath: the shipped `pacto mcp` over stdio creates a
// service from a golden path directory, records its origin and reports steps.
func TestMCPCreateFromGoldenPath(t *testing.T) {
	t.Setenv("PACTO_DELIVERY_TRUST", "")
	bin := buildPactoBinary(t)
	root := t.TempDir()
	path := filepath.Join(root, "paths", "http-service")
	if err := os.MkdirAll(filepath.Join(path, "interfaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(path, "pacto.yaml"), "pactoVersion: \"2.4\"\nservice:\n  name: http-service\n  version: 1.0.0\n"+
		"  owner: {team: platform}\n  summary: An HTTP API\n  template:\n    plugins:\n      - {name: dockerfile, description: A Dockerfile}\n"+
		"interfaces:\n  - {name: api, type: openapi, ref: interfaces/openapi.yaml}\n")
	writeFile(t, filepath.Join(path, "interfaces", "openapi.yaml"), "openapi: 3.0.0\ninfo: {title: api, version: 1.0.0}\npaths: {}\n")
	session, _, stderr := catalogStdioSession(t, bin, t.TempDir())
	defer session.Close()

	out := filepath.Join(root, "gift-cards")
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pacto_create", Arguments: map[string]any{
		"from": path, "name": "gift-cards", "owner": "checkout", "domain": "commerce", "path": out,
		"set": `["service.summary=Gift card balances"]`,
	}})
	if err != nil {
		t.Fatalf("CallTool: %v\nstderr:\n%s", err, stderr)
	}
	text := mcpResultText(t, res)
	if res.IsError {
		t.Fatalf("pacto_create from: %s", text)
	}
	assertContains(t, text, `"mode": "template"`)
	assertContains(t, text, "a directory carries no signature")
	assertContains(t, text, `"name": "dockerfile"`)
	data, err := os.ReadFile(filepath.Join(out, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: gift-cards", "team: checkout", "domain: commerce", "summary: Gift card balances", "origin:", "ref: ../paths/http-service", "digest: sha256:"} {
		assertContains(t, string(data), want)
	}
	if strings.Contains(string(data), "template:") {
		t.Errorf("the created contract keeps the template marker:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(out, "interfaces", "openapi.yaml")); err != nil {
		t.Errorf("the path's files were not copied: %v", err)
	}
}
