package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/cli"
	"github.com/trianalab/pacto/internal/testutil"
	"github.com/trianalab/pacto/internal/update"
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
	svcDir := filepath.Join(dir, "test-svc")
	root2.SetArgs([]string{"validate", svcDir})
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
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), broken, 0644); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"validate", dir})
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
	svcDir := filepath.Join(dir, "json-test")
	root2.SetArgs([]string{"validate", "--output-format", "json", svcDir})
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

func TestUpdateCommand_DevBuild(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "dev")
	root.SetArgs([]string{"update"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for dev build")
	}
	if !strings.Contains(err.Error(), "cannot update a dev build") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpdateCommand_Success(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	if err := os.MkdirAll(filepath.Join(tmpDir, "pacto"), 0700); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/TrianaLab/pacto/releases/tags/v2.0.0":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
		default:
			_, _ = w.Write([]byte("new-binary"))
		}
	}))
	t.Cleanup(server.Close)

	execDir := t.TempDir()
	execPath := filepath.Join(execDir, "pacto")
	if err := os.WriteFile(execPath, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	cleanup := update.SetTestOverrides(
		server.Client(), server.URL, server.URL,
		func() (string, error) { return execPath, nil },
	)
	defer cleanup()

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "v0.0.1")
	root.SetArgs([]string{"update", "v2.0.0"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if !strings.Contains(out.String(), "Updated pacto v0.0.1 -> v2.0.0") {
		t.Errorf("expected success message, got: %s", out.String())
	}
}

func TestUpdateCommand_Error(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	if err := os.MkdirAll(filepath.Join(tmpDir, "pacto"), 0700); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	cleanup := update.SetTestOverrides(server.Client(), server.URL, server.URL, nil)
	defer cleanup()

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "v0.0.1")
	root.SetArgs([]string{"update", "v99.99.99"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent release")
	}
}

func TestUpdateNotification(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("PACTO_NO_UPDATE_CHECK", "")
	cacheDir := filepath.Join(tmpDir, "pacto")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}

	cacheEntry := struct {
		CheckedAt     time.Time `json:"checked_at"`
		LatestVersion string    `json:"latest_version"`
	}{CheckedAt: time.Now(), LatestVersion: "v99.0.0"}
	data, _ := json.Marshal(cacheEntry)
	if err := os.WriteFile(filepath.Join(cacheDir, "update-check.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "v0.0.1")
	root.SetArgs([]string{"version"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if !strings.Contains(out.String(), "A new version of pacto is available") {
		t.Errorf("expected update notification, got: %s", out.String())
	}
}

func TestUpdateNotification_SuppressedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("PACTO_NO_UPDATE_CHECK", "")
	cacheDir := filepath.Join(tmpDir, "pacto")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}

	cacheEntry := struct {
		CheckedAt     time.Time `json:"checked_at"`
		LatestVersion string    `json:"latest_version"`
	}{CheckedAt: time.Now(), LatestVersion: "v99.0.0"}
	data, _ := json.Marshal(cacheEntry)
	if err := os.WriteFile(filepath.Join(cacheDir, "update-check.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "v0.0.1")
	root.SetArgs([]string{"version", "--output-format", "json"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if strings.Contains(out.String(), "A new version of pacto is available") {
		t.Errorf("notification should be suppressed for JSON format, got: %s", out.String())
	}
}

func TestUpdateNotification_SuppressedMarkdown(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("PACTO_NO_UPDATE_CHECK", "")
	cacheDir := filepath.Join(tmpDir, "pacto")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}

	cacheEntry := struct {
		CheckedAt     time.Time `json:"checked_at"`
		LatestVersion string    `json:"latest_version"`
	}{CheckedAt: time.Now(), LatestVersion: "v99.0.0"}
	data, _ := json.Marshal(cacheEntry)
	if err := os.WriteFile(filepath.Join(cacheDir, "update-check.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "v0.0.1")
	root.SetArgs([]string{"version", "--output-format", "markdown"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if strings.Contains(out.String(), "A new version of pacto is available") {
		t.Errorf("notification should be suppressed for markdown format, got: %s", out.String())
	}
}

func TestVerboseFlag(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"--verbose", "validate", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("validate --verbose failed: %v", err)
	}
	if !strings.Contains(out.String(), "is valid") {
		t.Errorf("expected validation output, got %q", out.String())
	}
}

func TestVerboseFlag_ShortForm(t *testing.T) {
	bundleDir := testutil.WriteTestBundle(t)
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, "test")
	root.SetArgs([]string{"-v", "validate", bundleDir})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("validate -v failed: %v", err)
	}
	if !strings.Contains(out.String(), "is valid") {
		t.Errorf("expected validation output, got %q", out.String())
	}
}
