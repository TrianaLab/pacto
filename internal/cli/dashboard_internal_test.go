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
)

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
	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v)
	cmd.SetArgs([]string{"/nonexistent/empty/dir"})
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)

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

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v)
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

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v)
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
	// Use an empty temp dir with no pacto.yaml, no kubectl, no OCI, no cache.
	emptyDir := t.TempDir()

	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v)
	cmd.SetArgs([]string{emptyDir})

	// Override PATH to ensure kubectl is not found.
	t.Setenv("PATH", emptyDir)
	t.Setenv("HOME", emptyDir)
	t.Setenv("XDG_CACHE_HOME", emptyDir)

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetOut(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "No data sources detected") {
		t.Errorf("expected 'No data sources detected' message, got:\n%s", stderr)
	}
}

func TestNewDashboardCommand_DefaultFlags(t *testing.T) {
	svc := app.NewService(nil, nil)
	v := viper.New()
	cmd := newDashboardCommand(svc, v)

	// Verify default flag values
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
