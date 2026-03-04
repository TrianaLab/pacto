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

	path := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"pack", path})
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

	path := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"pack", "--output-format", "json", path})
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
	root.SetArgs([]string{"pack", "/nonexistent/pacto.yaml"})

	err := root.Execute()
	if err == nil {
		t.Error("expected pack to fail for nonexistent file")
	}
}

func TestDiffCommand(t *testing.T) {
	path1 := testutil.WriteTestBundle(t)
	path2 := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"diff", path1, path2})
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
	path := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"diff", "/nonexistent/pacto.yaml", path})

	err := root.Execute()
	if err == nil {
		t.Error("expected diff to fail for nonexistent file")
	}
}

func TestPushCommand_Error(t *testing.T) {
	store := &testutil.MockBundleStore{
		PushFn: func(_ context.Context, _ string, _ *contract.Bundle) (string, error) {
			return "", fmt.Errorf("push failed")
		},
	}
	path := testutil.WriteTestBundle(t)
	svc := app.NewService(store, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"push", "ghcr.io/acme/svc:1.0.0", "--path", path})

	err := root.Execute()
	if err == nil {
		t.Error("expected push to fail")
	}
}

func TestPushCommand_Success(t *testing.T) {
	store := &testutil.MockBundleStore{}
	path := testutil.WriteTestBundle(t)
	svc := app.NewService(store, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"push", "ghcr.io/acme/svc:1.0.0", "--path", path})
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
	root.SetArgs([]string{"pull", "ghcr.io/acme/svc:1.0.0", "--output", output})
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
	root.SetArgs([]string{"pull", "ghcr.io/acme/svc:1.0.0"})

	err := root.Execute()
	if err == nil {
		t.Error("expected pull to fail without store")
	}
}

func TestGraphCommand(t *testing.T) {
	path := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"graph", path})
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
	root.SetArgs([]string{"graph", "/nonexistent/pacto.yaml"})

	err := root.Execute()
	if err == nil {
		t.Error("expected graph to fail for nonexistent file")
	}
}

func TestExplainCommand(t *testing.T) {
	path := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"explain", path})
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
	path := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"explain", "--output-format", "json", path})
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
	root.SetArgs([]string{"explain", "/nonexistent/pacto.yaml"})

	err := root.Execute()
	if err == nil {
		t.Error("expected explain to fail for nonexistent file")
	}
}

func TestGenerateCommand_Error(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"generate", "test-plugin", "/nonexistent/pacto.yaml"})

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

	path := testutil.WriteTestBundle(t)
	outputDir := filepath.Join(dir, "gen-out")
	runner := &testutil.MockPluginRunner{}
	svc := app.NewService(nil, runner)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"generate", "test-plugin", path, "--output", outputDir})
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

	path := testutil.WriteTestBundle(t)
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
	root.SetArgs([]string{"generate", "test-plugin", path, "--output", outputDir, "--option", "file=config.yaml", "--option", "format=json"})
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
	bundleDir1 := filepath.Join(dir1, "bundle")
	if err := os.MkdirAll(bundleDir1, 0755); err != nil {
		t.Fatal(err)
	}
	path1 := filepath.Join(bundleDir1, "pacto.yaml")
	if err := os.WriteFile(path1, testutil.ValidPactoYAML(), 0644); err != nil {
		t.Fatal(err)
	}

	dir2 := t.TempDir()
	bundleDir2 := filepath.Join(dir2, "bundle")
	if err := os.MkdirAll(bundleDir2, 0755); err != nil {
		t.Fatal(err)
	}
	path2 := filepath.Join(bundleDir2, "pacto.yaml")
	modified := strings.Replace(string(testutil.ValidPactoYAML()), "name: test-svc", "name: renamed-svc", 1)
	if err := os.WriteFile(path2, []byte(modified), 0644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"diff", path1, path2})
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
					Runtime: contract.Runtime{
						Workload: "service",
						State:    contract.State{Type: "stateless", Persistence: contract.Persistence{Scope: "local", Durability: "ephemeral"}, DataCriticality: "low"},
						Health:   contract.Health{Interface: "api", Path: "/health"},
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
	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bundleDir, "pacto.yaml")
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
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"validate", path})

	err := root.Execute()
	if err == nil {
		t.Error("expected validate command to fail for invalid contract")
	}
}

func TestLoginCommand_WriteConfigError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Make home read-only so .docker dir cannot be created
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"login", "ghcr.io", "--username", "user", "--password", "pass"})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when writeDockerConfig fails")
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
	path1 := testutil.WriteTestBundle(t)
	path2 := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"diff", "--output-format", "json", path1, path2})
	root.SetOut(cliErrWriter{})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when output writer fails")
	}
}

func TestValidateCommand_OutputError(t *testing.T) {
	path := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"validate", "--output-format", "json", path})
	root.SetOut(cliErrWriter{})

	err := root.Execute()
	if err == nil {
		t.Error("expected error when output writer fails")
	}
}
