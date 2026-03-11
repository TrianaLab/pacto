package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/internal/app"
	pactomcp "github.com/trianalab/pacto/internal/mcp"
)

func TestMCPCommand_Help(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, "test")
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
	root := NewRootCommand(svc, "test")

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

func TestMCPCommand_NoArgs(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, "test")
	root.SetArgs([]string{"mcp", "extra-arg"})

	err := root.Execute()
	if err == nil {
		t.Error("expected error for extra arguments")
	}
}

func TestMCPCommand_Flags(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, "test")

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
	root := NewRootCommand(svc, "test")

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
