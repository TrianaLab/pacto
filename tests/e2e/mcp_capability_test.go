//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/v2/internal/app"
	pactomcp "github.com/trianalab/pacto/v2/internal/mcp"
)

// TestMCPCapabilityInvokesLiveEndpoint exercises the full agent-capability path:
// parse a bundle's OpenAPI spec, register one MCP tool per operation, and invoke
// a tool that reaches a live HTTP endpoint.
func TestMCPCapabilityInvokesLiveEndpoint(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"pong"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	pactoYAML := `pactoVersion: "1.0"
service:
  name: ping-service
  version: 1.0.0
interfaces:
  - name: http
    type: http
    contract: interfaces/openapi.json
`
	writeFile(t, filepath.Join(dir, "pacto.yaml"), pactoYAML)
	writeFile(t, filepath.Join(dir, "interfaces", "openapi.json"),
		`{"paths":{"/ping":{"get":{"operationId":"ping","summary":"Ping the service"}}}}`)
	writeFile(t, filepath.Join(dir, "skills", "usage.md"), "# How to use ping")

	svc := app.NewService(nil, nil)
	bundle, err := svc.ResolveBundle(context.Background(), dir)
	if err != nil {
		t.Fatalf("ResolveBundle: %v", err)
	}

	server := pactomcp.NewServer(svc, "test-e2e")
	if err := pactomcp.RegisterCapabilities(server, bundle,
		pactomcp.CapabilityOptions{BaseURL: srv.URL}, os.Stderr); err != nil {
		t.Fatalf("RegisterCapabilities: %v", err)
	}

	ctx := context.Background()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e", Version: "1.0"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("CallTool ping: %v", err)
	}
	text := mcpResultText(t, res)
	if !strings.Contains(text, "pong") || !strings.Contains(text, "200") {
		t.Fatalf("ping result = %q", text)
	}
	if gotPath != "/ping" {
		t.Fatalf("server saw path %q", gotPath)
	}

	// the skill tool lists the bundled skill
	skillRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "pacto_skill"})
	if err != nil {
		t.Fatalf("CallTool pacto_skill: %v", err)
	}
	if !strings.Contains(mcpResultText(t, skillRes), "usage.md") {
		t.Fatalf("skill list = %q", mcpResultText(t, skillRes))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
