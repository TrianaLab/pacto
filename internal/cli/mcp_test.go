package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
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

func TestMCPCommand_UnknownTransport(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "test"})
	root.SetArgs([]string{"mcp", "-t", "bogus"})
	var stderr bytes.Buffer
	root.SetErr(&stderr)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for an unknown transport")
	}
	for _, want := range []string{`"bogus"`, "stdio", "http"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error naming %s, got: %v", want, err)
		}
	}
	if stderr.String() != "" {
		t.Errorf("expected no server to start, got stderr: %s", stderr.String())
	}
}

// The transport is checked before the bundle is resolved, so a typo reports the
// typo instead of whatever the server build would have failed on afterwards.
func TestMCPCommand_UnknownTransportBeforeBuild(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "test"})
	root.SetArgs([]string{"mcp", "/nonexistent/bundle/xyz", "-t", "bogus"})
	root.SetErr(&bytes.Buffer{})

	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "unsupported transport") {
		t.Fatalf("expected transport error, got: %v", err)
	}
}

func TestRunMCPServer_UnknownTransport(t *testing.T) {
	server := pactomcp.NewServer(pactomcp.Authoring{Validate: app.NewService(nil, nil).ValidationReport}, "test")
	var stderr bytes.Buffer

	err := runMCPServer(context.Background(), mcpServe{server: server, transport: "bogus"}, &stderr)
	if err == nil {
		t.Fatal("expected error for an unknown transport")
	}
	if stderr.String() != "" {
		t.Errorf("expected no server to start, got stderr: %s", stderr.String())
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
	cmd := newMCPCommand(app.NewService(nil, nil), "v", defaultDeps())
	cmd.SetContext(context.Background())
	server, err := buildMCPServer(cmd, app.NewService(nil, nil), "v", nil, defaultDeps())
	if err != nil || server == nil {
		t.Fatalf("no-arg build = %v,%v", server, err)
	}
}

func TestBuildMCPServer_WithBundle(t *testing.T) {
	dir := writeCapabilityBundle(t)
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(context.Background())
	_ = cmd.Flags().Set("base-url", "http://example.com")

	server, err := buildMCPServer(cmd, svc, "v", []string{dir}, defaultDeps())
	if err != nil || server == nil {
		t.Fatalf("with-bundle build = %v,%v", server, err)
	}
}

func TestBuildMCPServer_AuthError(t *testing.T) {
	dir := writeCapabilityBundle(t)
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(context.Background())
	_ = cmd.Flags().Set("auth", "bogus")

	if _, err := buildMCPServer(cmd, svc, "v", []string{dir}, defaultDeps()); err == nil {
		t.Error("expected auth parse error")
	}
}

func TestBuildMCPServer_RegisterError(t *testing.T) {
	dir := writeCapabilityBundle(t)
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(context.Background())
	// no base-url and the spec has no servers → RegisterCapabilities fails
	if _, err := buildMCPServer(cmd, svc, "v", []string{dir}, defaultDeps()); err == nil {
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
	server := pactomcp.NewServer(pactomcp.Authoring{Validate: app.NewService(nil, nil).ValidationReport}, "test")
	var stderr bytes.Buffer

	cancel()
	err := runMCPServer(ctx, mcpServe{server: server, transport: "http"}, &stderr)
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
	server := pactomcp.NewServer(pactomcp.Authoring{Validate: app.NewService(nil, nil).ValidationReport}, "test")
	var stderr bytes.Buffer

	_ = runMCPServer(ctx, mcpServe{server: server, transport: "stdio"}, &stderr)

	output := stderr.String()
	if !strings.Contains(output, "MCP server running on stdio") {
		t.Errorf("expected stdio message, got: %s", output)
	}
}

func TestRunMCPServer_InvalidPort(t *testing.T) {
	server := pactomcp.NewServer(pactomcp.Authoring{Validate: app.NewService(nil, nil).ValidationReport}, "test")
	var stderr bytes.Buffer

	err := runMCPServer(context.Background(), mcpServe{server: server, transport: "http", port: -1}, &stderr)
	if err == nil {
		t.Error("expected error for invalid port")
	}
}

func TestServeHTTP_ContextCancel(t *testing.T) {
	server := pactomcp.NewServer(pactomcp.Authoring{Validate: app.NewService(nil, nil).ValidationReport}, "test")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- serveHTTP(ctx, mcpHandler(mcpServe{server: server}), ln) }()

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
	server := pactomcp.NewServer(pactomcp.Authoring{Validate: app.NewService(nil, nil).ValidationReport}, "test")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Close the listener immediately so srv.Serve returns an error,
	// exercising the errCh path in the select.
	_ = ln.Close()

	err = serveHTTP(context.Background(), mcpHandler(mcpServe{server: server}), ln)
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

// mcpImpactSession builds a fleet MCP server over a local fleet holding
// orders with flags set, and connects a client to it.
func mcpImpactSession(t *testing.T, flags map[string]string) *mcpsdk.ClientSession {
	t.Helper()
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", defaultDeps())
	cmd.SetContext(context.Background())
	local := t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "orders"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "orders", "pacto.yaml"), []byte("pactoVersion: \"2.0\"\nservice:\n  name: orders\n  version: \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	flags["fleet"], flags["local"] = "true", local
	for k, v := range flags {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	server, err := buildMCPServer(cmd, svc, "v", nil, defaultDeps())
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callImpact calls pacto_impact and returns its text and whether it failed.
func callImpact(t *testing.T, s *mcpsdk.ClientSession, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pacto_impact", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[0].(*mcpsdk.TextContent).Text, res.IsError
}

// writeTraces writes an empty OTLP/JSON trace document.
func writeTraces(t *testing.T) string {
	t.Helper()
	tf := filepath.Join(t.TempDir(), "traces.json")
	if err := os.WriteFile(tf, []byte(`{"resourceSpans":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return tf
}

// TestMCPImpactHTTPNoAuthNoDelivery: an HTTP server without --auth-issuer
// answers a regressed event without reading delivery as itself.
func TestMCPImpactHTTPNoAuthNoDelivery(t *testing.T) {
	s := mcpImpactSession(t, map[string]string{"transport": "http"})
	text, failed := callImpact(t, s, map[string]any{"event": "regressed", "service": "orders", "revision": "sha256:a"})
	if failed || !strings.Contains(text, "DELIVERY_EVIDENCE_UNAVAILABLE") || !strings.Contains(text, "reads no delivery for its callers") {
		t.Fatalf("regressed over HTTP:\n%s", text)
	}
}

// TestMCPImpactHTTPNoAuthRefusesTraces: an HTTP server without --auth-issuer
// reads no traces path and no local ref a caller names.
func TestMCPImpactHTTPNoAuthRefusesTraces(t *testing.T) {
	s := mcpImpactSession(t, map[string]string{"transport": "http"})
	if text, failed := callImpact(t, s, map[string]any{"event": "unavailable", "service": "orders", "traces": writeTraces(t)}); !failed || !strings.Contains(text, "reads no file path") {
		t.Fatalf("traces over HTTP:\n%s", text)
	}
	if text, failed := callImpact(t, s, map[string]any{"old_ref": "./a", "new_ref": "oci://x/a:1"}); !failed || !strings.Contains(text, "pass oci:// references") {
		t.Fatalf("local ref over HTTP:\n%s", text)
	}
}

// TestMCPImpactStdioReadsTraces: a stdio server is its own user's, so it
// reads the traces file and answers the event.
func TestMCPImpactStdioReadsTraces(t *testing.T) {
	s := mcpImpactSession(t, map[string]string{})
	text, failed := callImpact(t, s, map[string]any{"event": "unavailable", "service": "orders", "traces": writeTraces(t)})
	if failed || !strings.Contains(text, `"gate": "clear"`) {
		t.Fatalf("stdio event with traces:\n%s", text)
	}
	// The file is read: a document that is not OTLP/JSON fails the call.
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if text, failed := callImpact(t, s, map[string]any{"event": "unavailable", "service": "orders", "traces": bad}); !failed || !strings.Contains(text, "parse traces") {
		t.Fatalf("a bad traces file is read and refused:\n%s", text)
	}
	if text, failed := callImpact(t, s, map[string]any{"event": "unavailable", "service": "orders", "traces": "/nonexistent/traces.json"}); !failed {
		t.Fatalf("a missing traces file is an error:\n%s", text)
	}
}

// signedInImpact is the provider a server whose callers sign in gets: it adds
// no revision and is not trusted.
func signedInImpact(t *testing.T, d deps) func(context.Context, *fleet.FleetSnapshot, pactomcp.ImpactRequest) (*impact.Result, error) {
	t.Helper()
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", d)
	cmd.SetContext(context.Background())
	return mcpImpactProvider(cmd, svc, d, false, false)
}

func TestMCPImpactSignedInRefusesTraces(t *testing.T) {
	_, err := signedInImpact(t, defaultDeps())(context.Background(), &fleet.FleetSnapshot{}, pactomcp.ImpactRequest{OldRef: "oci://x/a:1", NewRef: "oci://x/a:2", TracesPath: writeTraces(t)})
	if err == nil || !strings.Contains(err.Error(), "reads no file path") {
		t.Fatalf("traces for a signed-in caller: %v", err)
	}
}

func TestMCPImpactSignedInRefusesLocalRefs(t *testing.T) {
	provide := signedInImpact(t, defaultDeps())
	for _, req := range []pactomcp.ImpactRequest{{OldRef: "./old", NewRef: "oci://x/a:2"}, {OldRef: "oci://x/a:1", NewRef: "/tmp/new"}} {
		if _, err := provide(context.Background(), &fleet.FleetSnapshot{}, req); err == nil || !strings.Contains(err.Error(), "pass oci:// references") {
			t.Errorf("%+v: %v", req, err)
		}
	}
}

func TestMCPImpactSignedInNoDelivery(t *testing.T) {
	d := defaultDeps()
	d.newDynamic = func() (dynamic.Interface, error) {
		t.Fatal("a signed-in caller's request never reads delivery as this process")
		return nil, nil
	}
	res, err := signedInImpact(t, d)(context.Background(), &fleet.FleetSnapshot{},
		pactomcp.ImpactRequest{Event: &impact.Event{Kind: impact.EventRegressed, Service: "orders", Revision: "sha256:a"}})
	if err != nil || !strings.Contains(res.GateReason, "not read in full") {
		t.Fatalf("signed-in regressed = %+v %v", res, err)
	}
}

// TestMCPImpactStdioReadsDelivery: a stdio server reads delivery for a
// regressed event with its own kubeconfig.
func TestMCPImpactStdioReadsDelivery(t *testing.T) {
	d := defaultDeps()
	d.newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no kubeconfig") }
	svc := app.NewService(nil, nil)
	cmd := newMCPCommand(svc, "v", d)
	cmd.SetContext(context.Background())
	res, err := mcpImpactProvider(cmd, svc, d, true, true)(context.Background(), &fleet.FleetSnapshot{},
		pactomcp.ImpactRequest{Event: &impact.Event{Kind: impact.EventRegressed, Service: "orders", Revision: "sha256:a"}})
	if err != nil || len(res.Limitations) == 0 || !strings.Contains(res.Limitations[len(res.Limitations)-1].Message+res.Limitations[0].Message, "no cluster to read delivery from") {
		t.Fatalf("stdio regressed = %+v %v", res, err)
	}
}
