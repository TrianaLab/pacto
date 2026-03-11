//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
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

func TestMCPValidate(t *testing.T) {
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_validate", map[string]any{"path": postgresPath})
	text := mcpResultText(t, result)
	assertContains(t, text, `"Valid": true`)
}

func TestMCPInspect(t *testing.T) {
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_inspect", map[string]any{"ref": postgresPath})
	text := mcpResultText(t, result)
	assertContains(t, text, "postgres-pacto")
	assertContains(t, text, "1.0.0")
}

func TestMCPResolveDependencies(t *testing.T) {
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_resolve_dependencies", map[string]any{"ref": postgresPath})
	text := mcpResultText(t, result)
	assertContains(t, text, "postgres-pacto")
}

func TestMCPListInterfaces(t *testing.T) {
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_list_interfaces", map[string]any{"ref": postgresPath})
	text := mcpResultText(t, result)

	var parsed struct {
		Interfaces []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"interfaces"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if len(parsed.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(parsed.Interfaces))
	}
	if parsed.Interfaces[0].Name != "db" {
		t.Errorf("expected interface name 'db', got %q", parsed.Interfaces[0].Name)
	}
}

func TestMCPGenerateDocs(t *testing.T) {
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_generate_docs", map[string]any{"ref": postgresPath})
	text := mcpResultText(t, result)
	assertContains(t, text, "# postgres-pacto")
	assertContains(t, text, "Interfaces")
}

func TestMCPExplain(t *testing.T) {
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_explain", map[string]any{"ref": postgresPath})
	text := mcpResultText(t, result)

	var parsed struct {
		Summary      string `json:"summary"`
		Interfaces   string `json:"interfaces"`
		Dependencies string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	assertContains(t, parsed.Summary, "postgres-pacto")
	assertContains(t, parsed.Summary, "stateful")
	assertContains(t, parsed.Interfaces, "db")
}

func TestMCPGenerateContract(t *testing.T) {
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_generate_contract", map[string]any{
		"service_name":   "payments",
		"language":       "go",
		"exposes_http":   true,
		"needs_database": true,
	})
	text := mcpResultText(t, result)
	assertContains(t, text, "name: payments")
	assertContains(t, text, "http-api")
	assertContains(t, text, "postgres")
	assertContains(t, text, "language: go")
}

func TestMCPGenerateContractMissingName(t *testing.T) {
	svc := app.NewService(nil, nil)
	result := mcpCallTool(t, svc, "pacto_generate_contract", map[string]any{})
	if !result.IsError {
		t.Error("expected IsError for missing service_name")
	}
}

func TestMCPSuggestDependencies(t *testing.T) {
	postgresPath := writePostgresBundle(t)
	svc := app.NewService(nil, nil)

	result := mcpCallTool(t, svc, "pacto_suggest_dependencies", map[string]any{"contract": postgresPath})
	text := mcpResultText(t, result)

	var parsed struct {
		Suggested []string `json:"suggested"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if len(parsed.Suggested) == 0 {
		t.Error("expected at least one suggestion")
	}
}

func TestMCPWithOCIReferences(t *testing.T) {
	reg := newTestRegistry(t)

	postgresPath := writePostgresBundle(t)
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	svc := app.NewService(reg.client, nil)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"

	t.Run("validate OCI", func(t *testing.T) {
		result := mcpCallTool(t, svc, "pacto_validate", map[string]any{"path": ref})
		text := mcpResultText(t, result)
		assertContains(t, text, `"Valid": true`)
	})

	t.Run("inspect OCI", func(t *testing.T) {
		result := mcpCallTool(t, svc, "pacto_inspect", map[string]any{"ref": ref})
		text := mcpResultText(t, result)
		assertContains(t, text, "postgres-pacto")
	})

	t.Run("explain OCI", func(t *testing.T) {
		result := mcpCallTool(t, svc, "pacto_explain", map[string]any{"ref": ref})
		text := mcpResultText(t, result)
		assertContains(t, text, "postgres-pacto")
	})
}

func TestMCPCommandHelp(t *testing.T) {
	output, err := runCommand(t, nil, "mcp", "--help")
	if err != nil {
		t.Fatalf("mcp --help failed: %v\noutput: %s", err, output)
	}
	if !strings.Contains(output, "Model Context Protocol") {
		t.Errorf("expected MCP description, got: %s", output)
	}
}

func TestMCPCommandExtraArgs(t *testing.T) {
	_, err := runCommand(t, nil, "mcp", "extra-arg")
	if err == nil {
		t.Error("expected error for extra args")
	}
}
