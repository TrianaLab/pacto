package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/cli"
	"github.com/trianalab/pacto/internal/plugin"
	"github.com/trianalab/pacto/internal/testutil"
	"github.com/trianalab/pacto/pkg/contract"
)

func TestPackCommand(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"pack", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	if !strings.Contains(out.String(), "Packed test-svc@1.0.0") {
		t.Errorf("expected pack output, got %q", out.String())
	}
}

func TestPackCommand_JSON(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"pack", "--output-format", "json", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("pack json failed: %v", err)
	}

	if !strings.Contains(out.String(), `"Name"`) {
		t.Errorf("expected JSON output, got %q", out.String())
	}
}

func TestPackCommand_Error(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"pack", "/nonexistent/dir"})

	err := root.Execute()
	if err == nil {
		t.Error("expected pack to fail for nonexistent directory")
	}
}

func TestDiffCommand(t *testing.T) {
	dir1 := testutil.WriteTestBundle(t)
	dir2 := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"diff", dir1, dir2})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("diff failed: %v", err)
	}

	if !strings.Contains(out.String(), "Classification:") {
		t.Errorf("expected classification in output, got %q", out.String())
	}
}

func TestDiffCommand_Error(t *testing.T) {
	dir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"diff", "/nonexistent/dir", dir})

	err := root.Execute()
	if err == nil {
		t.Error("expected diff to fail for nonexistent directory")
	}
}

func TestPushCommand_Error(t *testing.T) {
	store := &testutil.MockBundleStore{
		PushFn: func(_ context.Context, _ string, _ *contract.Bundle) (string, error) {
			return "", fmt.Errorf("push failed")
		},
	}
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(store, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"push", "oci://ghcr.io/acme/svc:1.0.0", "--path", bundleDir})

	err := root.Execute()
	if err == nil {
		t.Error("expected push to fail")
	}
}

func TestPushCommand_Success(t *testing.T) {
	store := &testutil.MockBundleStore{}
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(store, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"push", "oci://ghcr.io/acme/svc:1.0.0", "--path", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	if !strings.Contains(out.String(), "Pushed test-svc@1.0.0") {
		t.Errorf("expected push output, got %q", out.String())
	}
}

func TestPullCommand_Success(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "pulled")
	store := &testutil.MockBundleStore{}
	svc := app.NewService(store, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"pull", "oci://ghcr.io/acme/svc:1.0.0", "--output", output})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	if !strings.Contains(out.String(), "Pulled test-svc@1.0.0") {
		t.Errorf("expected pull output, got %q", out.String())
	}
}

func TestPullCommand_Error(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"pull", "oci://ghcr.io/acme/svc:1.0.0"})

	err := root.Execute()
	if err == nil {
		t.Error("expected pull to fail without store")
	}
}

func TestGraphCommand(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"graph", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("graph failed: %v", err)
	}

	if !strings.Contains(out.String(), "test-svc@1.0.0") {
		t.Errorf("expected graph output, got %q", out.String())
	}
}

func TestGraphCommand_Error(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"graph", "/nonexistent/dir"})

	err := root.Execute()
	if err == nil {
		t.Error("expected graph to fail for nonexistent directory")
	}
}

func TestExplainCommand(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"explain", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("explain failed: %v", err)
	}

	if !strings.Contains(out.String(), "Service: test-svc@1.0.0") {
		t.Errorf("expected explain output, got %q", out.String())
	}
}

func TestExplainCommand_JSON(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"explain", "--output-format", "json", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("explain json failed: %v", err)
	}

	if !strings.Contains(out.String(), `"name"`) {
		t.Errorf("expected JSON output, got %q", out.String())
	}
}

func TestExplainCommand_Error(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"explain", "/nonexistent/dir"})

	err := root.Execute()
	if err == nil {
		t.Error("expected explain to fail for nonexistent directory")
	}
}

func TestGenerateCommand_Error(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"generate", "test-plugin", "/nonexistent/dir"})

	err := root.Execute()
	if err == nil {
		t.Error("expected generate to fail without runner")
	}
}

func TestGenerateCommand_Success(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	bundleDir := testutil.WriteTestBundle(t)
	outputDir := filepath.Join(dir, "gen-out")
	runner := &testutil.MockPluginRunner{}
	svc := app.NewService(nil, runner)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"generate", "test-plugin", bundleDir, "--output", outputDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	if !strings.Contains(out.String(), "Generated 1 file(s) using test-plugin") {
		t.Errorf("expected generate output, got %q", out.String())
	}
}

func TestGenerateCommand_WithOptions(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	bundleDir := testutil.WriteTestBundle(t)
	outputDir := filepath.Join(dir, "gen-out")

	var capturedOpts map[string]any
	runner := &testutil.MockPluginRunner{
		RunFn: func(_ context.Context, _ string, req plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
			capturedOpts = req.Options
			return &plugin.GenerateResponse{
				Files:   []plugin.GeneratedFile{{Path: "out.txt", Content: "hello"}},
				Message: "done",
			}, nil
		},
	}
	svc := app.NewService(nil, runner)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"generate", "test-plugin", bundleDir, "--output", outputDir, "--option", "file=config.yaml", "--option", "format=json"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("generate with options failed: %v", err)
	}

	if capturedOpts["file"] != "config.yaml" {
		t.Errorf("expected file=config.yaml, got %v", capturedOpts["file"])
	}
	if capturedOpts["format"] != "json" {
		t.Errorf("expected format=json, got %v", capturedOpts["format"])
	}
}

func TestLoginCommand_MissingUsername(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"login", "ghcr.io"})

	err := root.Execute()
	if err == nil {
		t.Error("expected login to fail without username")
	}
}

func TestLoginCommand_WithPassword(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"login", "ghcr.io", "--username", "user", "--password", "pass"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	if !strings.Contains(out.String(), "Login succeeded for ghcr.io") {
		t.Errorf("expected login success output, got %q", out.String())
	}
}

func TestInitCommand_JSON(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"init", "--output-format", "json", "json-svc"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("init json failed: %v", err)
	}

	if !strings.Contains(out.String(), `"Dir"`) {
		t.Errorf("expected JSON output, got %q", out.String())
	}
}

func TestDiffCommand_Breaking(t *testing.T) {
	// Create two bundles with different service names to trigger breaking change
	dir1 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir1, "pacto.yaml"), testutil.ValidPactoYAML(), 0644); err != nil {
		t.Fatal(err)
	}

	dir2 := t.TempDir()
	modified := strings.Replace(string(testutil.ValidPactoYAML()), "name: test-svc", "name: renamed-svc", 1)
	if err := os.WriteFile(filepath.Join(dir2, "pacto.yaml"), []byte(modified), 0644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"diff", dir1, dir2})
	var out bytes.Buffer
	root.SetOut(&out)

	err := root.Execute()
	// Should fail because of breaking changes
	if err == nil {
		t.Error("expected diff to return error for breaking changes")
	}
}

func TestValidateCommand_Error(t *testing.T) {
	store := &testutil.MockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			port := 8080
			return &contract.Bundle{
				Contract: &contract.Contract{
					PactoVersion: "1.0",
					Service:      contract.ServiceIdentity{Name: "test-svc", Version: "1.0.0"},
					Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
					Runtime: &contract.Runtime{
						Workload: "service",
						State:    contract.State{Type: "stateless", Persistence: contract.Persistence{Scope: "local", Durability: "ephemeral"}, DataCriticality: "low"},
						Health:   &contract.Health{Interface: "api", Path: "/health"},
					},
				},
				FS: fstest.MapFS{}, // empty FS, missing pacto.yaml
				// RawYAML is nil
			}, nil
		},
	}
	svc := app.NewService(store, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"validate", "oci://ghcr.io/acme/svc:1.0.0"})

	err := root.Execute()
	if err == nil {
		t.Error("expected validate command to fail when rawYAML read fails")
	}
}

func TestValidateCommand_InvalidContract(t *testing.T) {
	dir := t.TempDir()
	// Valid YAML but fails validation (health interface not found)
	content := []byte(`pactoVersion: "1.0"
service:
  name: bad-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: nonexistent
    path: /health
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"validate", dir})

	err := root.Execute()
	if err == nil {
		t.Error("expected validate command to fail for invalid contract")
	}
}

func TestLoginCommand_WriteConfigError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	// Make home read-only so config dir cannot be created
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"login", "ghcr.io", "--username", "user", "--password", "pass"})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when writePactoConfig fails")
	}
}

func TestRootCommand_CustomConfig(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"--config", "/nonexistent/config.yaml", "version"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("version with custom config failed: %v", err)
	}
}

type cliErrWriter struct{}

func (cliErrWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write error") }

func TestDiffCommand_OutputError(t *testing.T) {
	dir1 := testutil.WriteTestBundle(t)
	dir2 := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"diff", "--output-format", "json", dir1, dir2})
	root.SetOut(cliErrWriter{})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when output writer fails")
	}
}

func TestDocCommand(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"doc", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("doc failed: %v", err)
	}

	if !strings.Contains(out.String(), "# test-svc") {
		t.Errorf("expected markdown heading, got %q", out.String())
	}
}

func TestDocCommand_JSON(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"doc", "--output-format", "json", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("doc json failed: %v", err)
	}

	if !strings.Contains(out.String(), `"markdown"`) {
		t.Errorf("expected JSON output, got %q", out.String())
	}
}

func TestDocCommand_WithOutput(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	outDir := filepath.Join(t.TempDir(), "docs")
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"doc", "--output", outDir, bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("doc with output failed: %v", err)
	}

	expectedFile := filepath.Join(outDir, "test-svc.md")
	if _, err := os.Stat(expectedFile); err != nil {
		t.Errorf("expected output file %s to exist: %v", expectedFile, err)
	}
}

func TestDocCommand_ServeFlag(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)

	// Use a pre-cancelled context so the server starts then stops immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"doc", "--serve", "--port", "0", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	// Execute should return nil (clean shutdown from cancelled context).
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("doc --serve failed: %v", err)
	}
}

func TestDocCommand_ServeMutuallyExclusive(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"doc", "--serve", "--output", "/tmp/out", bundleDir})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when --serve and --output are both set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got: %v", err)
	}
}

func TestDocCommand_Error(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"doc", "/nonexistent/dir"})

	err := root.Execute()
	if err == nil {
		t.Error("expected doc to fail for nonexistent directory")
	}
}

func TestValidateCommand_OutputError(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"validate", "--output-format", "json", bundleDir})
	root.SetOut(cliErrWriter{})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when output writer fails")
	}
}
