package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/internal/app"
	pactomcp "github.com/trianalab/pacto/v3/internal/mcp"
)

func TestMCPCommand_Help(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "test"})
	root.SetArgs([]string{"mcp", "--help"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("mcp --help failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Model Context Protocol") {
		t.Errorf("expected MCP description, got: %s", output)
	}
	if !strings.Contains(output, "stdio") {
		t.Errorf("expected stdio mention, got: %s", output)
	}
}

func TestMCPCommand_Registered(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "test"})

	found := false
	for _, cmd := range root.Commands() {
		if cmd.Name() == "mcp" {
			found = true
			break
		}
	}
	if !found {
		t.Error("mcp command not registered")
	}
}

func TestMCPCommand_TooManyArgs(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "test"})
	root.SetArgs([]string{"mcp", "a", "b"})

	err := root.Execute()
	if err == nil {
		t.Error("expected error for more than one argument")
	}
}

func TestMCPCommand_BundleResolveError(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "test"})
	root.SetArgs([]string{"mcp", "/nonexistent/bundle/xyz"})
	root.SetErr(&bytes.Buffer{})

	if err := root.Execute(); err == nil {
		t.Error("expected error resolving a nonexistent bundle")
	}
}

func TestParseAuthFlags(t *testing.T) {
	if creds, err := parseAuthFlags(nil); err != nil || creds != nil {
		t.Fatalf("empty = %v,%v", creds, err)
	}
	creds, err := parseAuthFlags([]string{"bearerAuth=tok", "apiKey=k=with=eq"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if creds["bearerAuth"] != "tok" || creds["apiKey"] != "k=with=eq" {
		t.Fatalf("creds = %v", creds)
	}
	if _, err := parseAuthFlags([]string{"noequals"}); err == nil {
		t.Error("expected error for missing =")
	}
	if _, err := parseAuthFlags([]string{"=value"}); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestBuildMCPServer_NoArg(t *testing.T) {
	cmd := newMCPCommand(app.NewService(nil, nil), "v")
	cmd.SetContext(context.Background())
	server, err := buildMCPServer(cmd, app.NewService(nil, nil), "v", nil)
	if err != nil || server == nil {
		t.Fatalf("no-arg build = %v,%v", server, err)
	}
}

func TestBuildMCPServer_WithBundle(t *testing.T) {
	dir := writeCapabilityBundle(t)
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v")
	cmd.SetContext(context.Background())
	_ = cmd.Flags().Set("base-url", "http://example.com")

	server, err := buildMCPServer(cmd, svc, "v", []string{dir})
	if err != nil || server == nil {
		t.Fatalf("with-bundle build = %v,%v", server, err)
	}
}

func TestBuildMCPServer_AuthError(t *testing.T) {
	dir := writeCapabilityBundle(t)
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v")
	cmd.SetContext(context.Background())
	_ = cmd.Flags().Set("auth", "bogus")

	if _, err := buildMCPServer(cmd, svc, "v", []string{dir}); err == nil {
		t.Error("expected auth parse error")
	}
}

func TestBuildMCPServer_RegisterError(t *testing.T) {
	dir := writeCapabilityBundle(t)
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v")
	cmd.SetContext(context.Background())
	// no base-url and the spec has no servers → RegisterCapabilities fails
	if _, err := buildMCPServer(cmd, svc, "v", []string{dir}); err == nil {
		t.Error("expected register error when no base URL is available")
	}
}

// writeCapabilityBundle writes a minimal bundle dir with an http interface and
// an OpenAPI contract, returning its path.
func writeCapabilityBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pactoYAML := `pactoVersion: "2.0"
service:
  name: demo
  version: 1.0.0
interfaces:
  - name: http
    type: openapi
    ref: interfaces/openapi.json
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(pactoYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "interfaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := `{"paths":{"/ping":{"get":{"operationId":"ping","summary":"ping"}}}}`
	if err := os.WriteFile(filepath.Join(dir, "interfaces", "openapi.json"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMCPCommand_Flags(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "test"})

	for _, cmd := range root.Commands() {
		if cmd.Name() == "mcp" {
			transportFlag := cmd.Flags().Lookup("transport")
			if transportFlag == nil {
				t.Fatal("expected --transport flag")
			}
			if transportFlag.DefValue != "stdio" {
				t.Errorf("expected default transport=stdio, got %s", transportFlag.DefValue)
			}
			if transportFlag.Shorthand != "t" {
				t.Errorf("expected shorthand -t, got %s", transportFlag.Shorthand)
			}

			portFlag := cmd.Flags().Lookup("port")
			if portFlag == nil {
				t.Fatal("expected --port flag")
			}
			if portFlag.DefValue != "8585" {
				t.Errorf("expected default port=8585, got %s", portFlag.DefValue)
			}
			return
		}
	}
	t.Error("mcp command not found")
}

func TestMCPCommand_RunE_HTTP(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "test"})

	ctx, cancel := context.WithCancel(context.Background())
	root.SetArgs([]string{"mcp", "-t", "http", "--port", "0"})
	var stderr bytes.Buffer
	root.SetErr(&stderr)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_ = root.ExecuteContext(ctx)

	output := stderr.String()
	if !strings.Contains(output, "MCP server listening on http://") {
		t.Errorf("expected listening message, got: %s", output)
	}
}

func TestRunMCPServer_HTTP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := pactomcp.NewServer(app.NewService(nil, nil), "test")
	var stderr bytes.Buffer

	cancel()
	err := runMCPServer(ctx, server, "http", 0, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "MCP server listening on http://") {
		t.Errorf("expected listening message, got: %s", output)
	}
}

func TestRunMCPServer_StdioMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := pactomcp.NewServer(app.NewService(nil, nil), "test")
	var stderr bytes.Buffer

	_ = runMCPServer(ctx, server, "stdio", 0, &stderr)

	output := stderr.String()
	if !strings.Contains(output, "MCP server running on stdio") {
		t.Errorf("expected stdio message, got: %s", output)
	}
}

func TestRunMCPServer_InvalidPort(t *testing.T) {
	server := pactomcp.NewServer(app.NewService(nil, nil), "test")
	var stderr bytes.Buffer

	err := runMCPServer(context.Background(), server, "http", -1, &stderr)
	if err == nil {
		t.Error("expected error for invalid port")
	}
}

func TestServeHTTP_ContextCancel(t *testing.T) {
	server := pactomcp.NewServer(app.NewService(nil, nil), "test")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- serveHTTP(ctx, server, ln) }()

	// Wait for server to be ready
	addr := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForServer(t, addr+"/mcp", 2*time.Second)

	// Send a valid MCP request to exercise the getServer callback and handler
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	req, _ := http.NewRequest("POST", addr+"/mcp", strings.NewReader(initReq))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp failed: %v", err)
	}
	_ = resp.Body.Close()

	// Test 404 path
	resp, err = http.Get(addr + "/other")
	if err != nil {
		t.Fatalf("GET /other failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}

	cancel()
	if serverErr := <-errCh; serverErr != nil {
		t.Errorf("unexpected error: %v", serverErr)
	}
}

func TestServeHTTP_ListenerClosed(t *testing.T) {
	server := pactomcp.NewServer(app.NewService(nil, nil), "test")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Close the listener immediately so srv.Serve returns an error,
	// exercising the errCh path in the select.
	_ = ln.Close()

	err = serveHTTP(context.Background(), server, ln)
	if err == nil {
		t.Error("expected error for closed listener")
	}
}

func waitForServer(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server not ready at %s after %s", url, timeout)
}
