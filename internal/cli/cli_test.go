package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/cli"
)

func TestInitCreatesProjectStructure(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
	}()

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"init", "test-svc"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if !strings.Contains(out.String(), "Created test-svc/") {
		t.Errorf("expected output to contain 'Created test-svc/', got %q", out.String())
	}

	for _, sub := range []string{"", "interfaces", "configuration"} {
		p := filepath.Join(dir, "test-svc", sub)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", p)
		}
	}

	pactoPath := filepath.Join(dir, "test-svc", "pacto.yaml")
	if _, err := os.Stat(pactoPath); err != nil {
		t.Fatalf("pacto.yaml was not created: %v", err)
	}
}

func TestInitThenValidateSucceeds(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
	}()

	svc := app.NewService(nil, nil)

	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"init", "test-svc"})
	var initOut bytes.Buffer
	root.SetOut(&initOut)
	if err := root.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	root2 := cli.NewRootCommand(svc, "test")
	path := filepath.Join(dir, "test-svc", "pacto.yaml")
	root2.SetArgs([]string{"validate", path})
	var validateOut bytes.Buffer
	root2.SetOut(&validateOut)
	if err := root2.Execute(); err != nil {
		t.Fatalf("validate failed: %v\noutput: %s", err, validateOut.String())
	}

	if !strings.Contains(validateOut.String(), "is valid") {
		t.Errorf("expected 'is valid' in output, got %q", validateOut.String())
	}
}

func TestValidateFailsOnBrokenContract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacto.yaml")

	broken := []byte(`
pactoVersion: "1.0"
service:
  name: my-svc
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
    interface: wrong-name
    path: /health
`)
	if err := os.WriteFile(path, broken, 0644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"validate", path})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected validate to fail on broken contract")
	}

	if !strings.Contains(out.String(), "HEALTH_INTERFACE_NOT_FOUND") {
		t.Errorf("expected HEALTH_INTERFACE_NOT_FOUND in output, got %q", out.String())
	}
}

func TestVersionCommand(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "1.2.3")
	root.SetArgs([]string{"version"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("version failed: %v", err)
	}

	if !strings.Contains(out.String(), "1.2.3") {
		t.Errorf("expected version output to contain '1.2.3', got %q", out.String())
	}
}

func TestValidateJSONOutput(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
	}()

	svc := app.NewService(nil, nil)

	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"init", "json-test"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	root2 := cli.NewRootCommand(svc, "test")
	path := filepath.Join(dir, "json-test", "pacto.yaml")
	root2.SetArgs([]string{"validate", "--output-format", "json", path})
	var out bytes.Buffer
	root2.SetOut(&out)

	if err := root2.Execute(); err != nil {
		t.Fatalf("validate json failed: %v", err)
	}

	if !strings.Contains(out.String(), `"Valid": true`) {
		t.Errorf("expected JSON output with Valid: true, got %q", out.String())
	}
}

func TestInitFailsIfDirectoryExists(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
	}()

	if err := os.Mkdir(filepath.Join(dir, "existing-svc"), 0755); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"init", "existing-svc"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected init to fail when directory already exists")
	}
}

func TestInitRequiresName(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"init"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected init to fail without a name argument")
	}
}
