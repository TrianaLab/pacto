package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/trianalab/pacto/internal/app"
	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
)

// dummyStore satisfies oci.BundleStore for CLI tests.
type dummyStore struct{}

func (dummyStore) Push(context.Context, string, *contract.Bundle) (string, error) { return "", nil }
func (dummyStore) Pull(context.Context, string) (*contract.Bundle, error)         { return nil, nil }
func (dummyStore) Resolve(context.Context, string) (string, error)                { return "", nil }
func (dummyStore) ListTags(context.Context, string) ([]string, error)             { return nil, nil }

var _ oci.BundleStore = dummyStore{}

func TestCacheTTL(t *testing.T) {
	tests := []struct {
		sourceType string
		expected   time.Duration
	}{
		{"k8s", 10 * time.Second},
		{"oci", 5 * time.Minute},
		{"cache", 10 * time.Minute},
		{"local", 2 * time.Second},
		{"unknown", 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.sourceType, func(t *testing.T) {
			got := cacheTTL(tt.sourceType)
			if got != tt.expected {
				t.Errorf("cacheTTL(%q) = %v, want %v", tt.sourceType, got, tt.expected)
			}
		})
	}
}

func TestNewDashboardCommand_NoSources(t *testing.T) {
	// Isolate from host kubeconfig / cache so no real sources are found.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)
	t.Setenv("KUBECONFIG", filepath.Join(emptyDir, "nonexistent"))

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{"/nonexistent/empty/dir"})
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

	// Use a cancelled context as safety net to prevent server from blocking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no data sources are detected")
	}
}

func TestNewDashboardCommand_WithLocalSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: test-svc
  version: 1.0.0
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Prevent real K8s client creation.
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{dir, "--port", "0", "--diagnostics"})

	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)

	// Use a pre-cancelled context so the server stops immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	// The server exits immediately due to cancelled context.
	_ = cmd.Execute()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "local") {
		t.Errorf("expected stderr to mention 'local' source, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "enabled") {
		t.Errorf("expected stderr to mention 'enabled', got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_WithOCISource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: oci-wiring-svc
  version: 1.0.0
`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/svc-a")

	// Create a cache dir with a dummy bundle to exercise SetCacheSource.
	cacheDir := filepath.Join(dir, ".cache", "pacto", "oci", "ghcr.io", "org", "cached", "1.0.0")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, ".cache"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{dir, "--port", "0"})

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "oci: enabled") {
		t.Errorf("expected stderr to mention 'oci: enabled', got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_DefaultDir(t *testing.T) {
	// When no dir arg is provided, it defaults to ".".
	// Create a temp dir with pacto.yaml and chdir into it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: default-dir-svc
  version: 1.0.0
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Prevent real K8s client creation.
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{"--port", "0"}) // no dir arg

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "local") {
		t.Errorf("expected stderr to mention 'local', got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_NoSourcesDetails(t *testing.T) {
	// Use an empty temp dir with no pacto.yaml, no kubeconfig, no OCI, no cache.
	emptyDir := t.TempDir()

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{emptyDir})

	// Ensure no K8s client can be created.
	t.Setenv("PATH", emptyDir)
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)
	t.Setenv("KUBECONFIG", filepath.Join(emptyDir, "nonexistent"))

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	// Use a cancelled context as safety net to prevent server from blocking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "No data sources detected") {
		t.Errorf("expected 'No data sources detected' message, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_RepoEnvVar(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/svc-a,ghcr.io/org/svc-b")
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)
	t.Setenv("KUBECONFIG", filepath.Join(emptyDir, "nonexistent"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{emptyDir, "--port", "0"})

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	if !strings.Contains(stderr, "oci") {
		t.Errorf("expected stderr to mention 'oci' source when PACTO_DASHBOARD_REPO is set, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_RepoFlagOverridesEnv(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PACTO_DASHBOARD_REPO", "ghcr.io/org/from-env")
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)
	t.Setenv("KUBECONFIG", filepath.Join(emptyDir, "nonexistent"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{emptyDir, "--port", "0", "--repo", "ghcr.io/org/from-flag"})

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	// The flag value should be used, not the env var — both enable OCI.
	if !strings.Contains(stderr, "oci") {
		t.Errorf("expected stderr to mention 'oci' source, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_HostFlag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(`pactoVersion: "1.0"
service:
  name: host-test
  version: 1.0.0
`), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", filepath.Join(dir, "nonexistent"))

	svc := app.NewService(dummyStore{}, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")
	cmd.SetArgs([]string{dir, "--port", "0", "--host", "0.0.0.0"})

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd.SetContext(ctx)

	_ = cmd.Execute()

	stderr := errBuf.String()
	// When host is 0.0.0.0, display should show 127.0.0.1 for user-friendliness.
	if !strings.Contains(stderr, "127.0.0.1") {
		t.Errorf("expected display address 127.0.0.1 when host is 0.0.0.0, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_DefaultFlags(t *testing.T) {
	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v, "test")

	// Verify default flag values
	host, _ := cmd.Flags().GetString("host")
	if host != "127.0.0.1" {
		t.Errorf("expected default host 127.0.0.1, got %q", host)
	}
	port, _ := cmd.Flags().GetInt("port")
	if port != 3000 {
		t.Errorf("expected default port 3000, got %d", port)
	}
	ns, _ := cmd.Flags().GetString("namespace")
	if ns != "" {
		t.Errorf("expected default namespace empty, got %q", ns)
	}
	diag, _ := cmd.Flags().GetBool("diagnostics")
	if diag {
		t.Error("expected diagnostics default false")
	}
}
