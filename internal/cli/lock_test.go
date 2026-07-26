package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/cli"
	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

func TestLockCommandWritesFile(t *testing.T) {
	dir := t.TempDir()
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: root\n  version: \"2.1.0\"\ndependencies:\n  - name: auth\n    ref: oci://ghcr.io/acme/auth\n    compatibility: ^1.0.0\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	auth := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "auth", Version: "1.2.0"},
		Workload:     contract.WorkloadService,
		State: &contract.State{
			Type:            contract.StateStateless,
			Persistence:     contract.Persistence{Scope: contract.ScopeLocal, Durability: contract.DurabilityEphemeral},
			DataCriticality: contract.DataCriticalityLow,
		},
	}
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.0"}, nil },
		ResolveFn:  func(_ context.Context, _ string) (string, error) { return "sha256:v1", nil },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: auth}, nil
		},
	}
	svc := app.NewService(store, nil)
	root := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root.SetArgs([]string{"lock", dir})
	var out bytes.Buffer
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pacto.lock")); err != nil {
		t.Errorf("pacto.lock not written: %v", err)
	}
	if !strings.Contains(out.String(), "wrote") {
		t.Errorf("expected 'wrote' in output, got %q", out.String())
	}
}

func TestLockCommandCheckFailsOnDrift(t *testing.T) {
	dir := t.TempDir()
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: root\n  version: \"2.1.0\"\ndependencies:\n  - name: auth\n    ref: oci://ghcr.io/acme/auth\n    compatibility: ^1.0.0\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	auth := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "auth", Version: "1.2.0"},
		Workload:     contract.WorkloadService,
		State: &contract.State{
			Type:            contract.StateStateless,
			Persistence:     contract.Persistence{Scope: contract.ScopeLocal, Durability: contract.DurabilityEphemeral},
			DataCriticality: contract.DataCriticalityLow,
		},
	}
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.0"}, nil },
		ResolveFn:  func(_ context.Context, _ string) (string, error) { return "sha256:v1", nil },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: auth}, nil
		},
	}
	svc := app.NewService(store, nil)

	// First create the lock
	root1 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root1.SetArgs([]string{"lock", dir})
	root1.SetOut(&bytes.Buffer{})
	if err := root1.Execute(); err != nil {
		t.Fatalf("initial lock: %v", err)
	}

	// Change the digest returned
	store.ResolveFn = func(_ context.Context, _ string) (string, error) { return "sha256:v2", nil }

	// Check should fail
	root2 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root2.SetArgs([]string{"lock", "--check", dir})
	err := root2.Execute()
	if err == nil {
		t.Fatal("expected check to fail on drift")
	}
	if !strings.HasPrefix(err.Error(), "LOCK_DIGEST_MISMATCH") {
		t.Errorf("expected error starting with LOCK_DIGEST_MISMATCH, got %q", err.Error())
	}
}

func TestLockCommandUpdateRepins(t *testing.T) {
	dir := t.TempDir()
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: root\n  version: \"2.1.0\"\ndependencies:\n  - name: auth\n    ref: oci://ghcr.io/acme/auth\n    compatibility: ^1.0.0\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	auth := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "auth", Version: "1.2.0"},
		Workload:     contract.WorkloadService,
		State: &contract.State{
			Type:            contract.StateStateless,
			Persistence:     contract.Persistence{Scope: contract.ScopeLocal, Durability: contract.DurabilityEphemeral},
			DataCriticality: contract.DataCriticalityLow,
		},
	}
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.0", "1.3.0"}, nil },
		ResolveFn:  func(_ context.Context, _ string) (string, error) { return "sha256:v1", nil },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: auth}, nil
		},
	}
	svc := app.NewService(store, nil)

	// Create lock with v1.2.0
	root1 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root1.SetArgs([]string{"lock", dir})
	root1.SetOut(&bytes.Buffer{})
	if err := root1.Execute(); err != nil {
		t.Fatalf("initial lock: %v", err)
	}

	// Read initial lock
	lockPath := filepath.Join(dir, "pacto.lock")
	lock1, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	// Update store to return 1.3.0
	auth.Service.Version = "1.3.0"
	store.ResolveFn = func(_ context.Context, _ string) (string, error) { return "sha256:v2", nil }

	// Run lock --update
	root2 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root2.SetArgs([]string{"lock", "--update", dir})
	var out bytes.Buffer
	root2.SetOut(&out)
	if err := root2.Execute(); err != nil {
		t.Fatalf("lock --update: %v", err)
	}

	// Read updated lock
	lock2, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(lock1) == string(lock2) {
		t.Error("expected lock to change after update")
	}
	if !strings.Contains(out.String(), "wrote") {
		t.Errorf("expected 'wrote' in output, got %q", out.String())
	}
}

func TestLockCommandJSON(t *testing.T) {
	dir := t.TempDir()
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: root\n  version: \"2.1.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root.SetArgs([]string{"lock", "--output-format", "json", dir})
	var out bytes.Buffer
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("lock json: %v", err)
	}
	if !strings.Contains(out.String(), `"path"`) {
		t.Errorf("expected JSON output, got %q", out.String())
	}
}

func TestLockCommandError(t *testing.T) {
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root.SetArgs([]string{"lock", "/nonexistent/dir"})

	err := root.Execute()
	if err == nil {
		t.Error("expected lock to fail for nonexistent directory")
	}
}

func TestLockCommandUpToDate(t *testing.T) {
	dir := t.TempDir()
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: root\n  version: \"2.1.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(nil, nil)

	// Create initial lock
	root1 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root1.SetArgs([]string{"lock", dir})
	root1.SetOut(&bytes.Buffer{})
	if err := root1.Execute(); err != nil {
		t.Fatalf("initial lock: %v", err)
	}

	// Run again without changes
	root2 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root2.SetArgs([]string{"lock", dir})
	var out bytes.Buffer
	root2.SetOut(&out)
	if err := root2.Execute(); err != nil {
		t.Fatalf("second lock: %v", err)
	}

	if !strings.Contains(out.String(), "up to date") {
		t.Errorf("expected 'up to date' in output, got %q", out.String())
	}
}

// TestLockCommandUpdateNameSelective proves `--update-name X` re-resolves only the
// named dependency and preserves every other pin — it must NOT re-pin the whole
// closure the way `--update` does.
func TestLockCommandUpdateNameSelective(t *testing.T) {
	dir := t.TempDir()
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: root\n  version: \"2.1.0\"\ndependencies:\n  - name: auth\n    ref: oci://ghcr.io/acme/auth\n    compatibility: ^1.0.0\n  - name: db\n    ref: oci://ghcr.io/acme/db\n    compatibility: ^1.0.0\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	authDigest, dbDigest := "sha256:auth-v1", "sha256:db-v1"
	store := &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.0"}, nil },
		ResolveFn: func(_ context.Context, ref string) (string, error) {
			if strings.Contains(ref, "/auth") {
				return authDigest, nil
			}
			return dbDigest, nil
		},
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			name := "db"
			if strings.Contains(ref, "/auth") {
				name = "auth"
			}
			return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.2.0"}}}, nil
		},
	}
	svc := app.NewService(store, nil)

	// Initial lock: both deps pinned at v1.
	root1 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root1.SetArgs([]string{"lock", dir})
	root1.SetOut(&bytes.Buffer{})
	if err := root1.Execute(); err != nil {
		t.Fatalf("initial lock: %v", err)
	}

	// Registry drifts: both deps now serve v2.
	authDigest, dbDigest = "sha256:auth-v2", "sha256:db-v2"

	// Selectively update only auth.
	root2 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root2.SetArgs([]string{"lock", "--update-name", "auth", dir})
	root2.SetOut(&bytes.Buffer{})
	if err := root2.Execute(); err != nil {
		t.Fatalf("lock --update-name: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "pacto.lock"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := lock.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := l.Dependency("auth"); !ok || e.Digest != "sha256:auth-v2" {
		t.Errorf("auth should be repinned to v2, got %+v", e)
	}
	if e, ok := l.Dependency("db"); !ok || e.Digest != "sha256:db-v1" {
		t.Errorf("db pin should be preserved at v1, got %+v", e)
	}
}
