package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// recordingStore counts every registry read, so a refusal is shown to happen
// before anything is resolved or pulled.
func recordingStore(reads *atomic.Int32) *testutil.MockBundleStore {
	return &testutil.MockBundleStore{
		ResolveFn: func(context.Context, string) (string, error) { reads.Add(1); return "", errors.New("no") },
		PullFn:    func(context.Context, string) (*contract.Bundle, error) { reads.Add(1); return nil, errors.New("no") },
	}
}

// callCreateFrom builds the server through the command layer with flags and
// calls pacto_create from an oci:// golden path into dir.
func callCreateFrom(t *testing.T, flags map[string]string, dir string) (*mcpsdk.CallToolResult, int32) {
	t.Helper()
	var reads atomic.Int32
	svc := app.NewService(recordingStore(&reads), nil)
	d := defaultDeps()
	d.clusterConfig = func() (*rest.Config, error) { return nil, errors.New("no kubeconfig") }
	cmd := newMCPCommand(svc, "v", d)
	cmd.SetContext(context.Background())
	for k, v := range flags {
		if err := cmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	server, err := buildMCPServer(cmd, svc, "v", nil, d)
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
	defer func() { _ = session.Close() }()
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pacto_create", Arguments: map[string]any{
		"from": "oci://ghcr.io/acme/paths/http-service:1.0.0", "name": "gift-cards", "owner": "checkout", "path": dir,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return res, reads.Load()
}

func assertFromRefused(t *testing.T, res *mcpsdk.CallToolResult, reads int32, dir string) {
	t.Helper()
	text := res.Content[0].(*mcpsdk.TextContent).Text
	if !res.IsError || !strings.Contains(text, "creating from a golden path serves stdio only") {
		t.Errorf("from over HTTP: %q", text)
	}
	if reads != 0 {
		t.Errorf("the registry was read %d times", reads)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the target holds %d entries", len(entries))
	}
}

// TestCreateFromRefusedOverHTTP: an HTTP server never creates from a golden
// path, so no caller pulls with this process's credentials or writes here.
func TestCreateFromRefusedOverHTTP(t *testing.T) {
	t.Setenv(trustEnv, "")
	dir := t.TempDir()
	res, reads := callCreateFrom(t, map[string]string{"transport": "http"}, dir)
	assertFromRefused(t, res, reads, dir)
	// Over stdio the same call reaches the registry.
	if _, reads := callCreateFrom(t, nil, t.TempDir()); reads == 0 {
		t.Error("stdio never asked the registry")
	}
}

// TestCreateFromNotOfferedUnderView: the --fleet HTTP server whose callers
// sign in and are narrowed by Kubernetes RBAC refuses from the same way.
func TestCreateFromNotOfferedUnderView(t *testing.T) {
	t.Setenv(trustEnv, "")
	dir := t.TempDir()
	res, reads := callCreateFrom(t, map[string]string{"transport": "http", "auth-issuer": "https://idp.example",
		"fleet": "true", "local": t.TempDir()}, dir)
	assertFromRefused(t, res, reads, dir)
}

// TestMCPInitMapsOverrides: values and set reach the created contract, set
// last, and a broken trust config fails the call.
func TestMCPInitMapsOverrides(t *testing.T) {
	t.Setenv(trustEnv, "")
	path := t.TempDir()
	_ = os.WriteFile(filepath.Join(path, "pacto.yaml"), []byte("pactoVersion: \"2.7\"\nservice:\n  name: http-service\n  version: 1.0.0\n  template: {}\n"), 0o644)
	svc := app.NewService(nil, nil)
	opts := app.InitOptions{Dir: filepath.Join(t.TempDir(), "gift-cards"), Owner: "checkout", Plan: true}
	r, err := mcpInit(svc, "")(context.Background(), opts, pactomcp.FromRequest{Ref: path,
		Values: map[string]any{"service": map[string]any{"summary": "from values", "version": "0.3.0"}},
		Set:    []string{"service.summary=from set"}})
	if err != nil {
		t.Fatal(err)
	}
	var yaml string
	for _, f := range r.Files {
		if f.Path == "pacto.yaml" {
			yaml = f.Content
		}
	}
	if !strings.Contains(yaml, "summary: from set") || !strings.Contains(yaml, "version: 0.3.0") {
		t.Errorf("overrides did not reach the contract:\n%s", yaml)
	}
	if _, err := mcpInit(svc, filepath.Join(path, "missing"))(context.Background(), opts, pactomcp.FromRequest{Ref: path}); err == nil ||
		!strings.Contains(err.Error(), "trust config") {
		t.Errorf("broken trust: %v", err)
	}
}
