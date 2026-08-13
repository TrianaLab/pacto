//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardCommand(t *testing.T) {
	t.Parallel()

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()

		output, err := runCommand(t, nil, "dashboard", "--help")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertContains(t, output, "dashboard")
		assertContains(t, output, "Usage")
		assertContains(t, output, "port")
	})

	t.Run("no sources detected", func(t *testing.T) {
		// Not parallel: modifies process-wide KUBECONFIG environment variable.

		// Use --no-cache to prevent the disk cache source from being detected
		// on machines that have a populated ~/.cache/pacto/oci/ directory.
		// Prevent K8s client creation via an invalid kubeconfig.
		origKC := os.Getenv("KUBECONFIG")
		os.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
		defer func() {
			if origKC == "" {
				os.Unsetenv("KUBECONFIG")
			} else {
				os.Setenv("KUBECONFIG", origKC)
			}
		}()
		_, err := runCommandWithCancelledCtx(t, nil, "dashboard", "--no-cache", "/nonexistent/empty/dir/xyz")
		if err == nil {
			t.Fatal("expected error when no sources are detected")
		}
	})

	t.Run("local source detection", func(t *testing.T) {
		// Uses inDir which acquires chdirMu, so not parallel.
		postgresDir := writePostgresBundle(t)
		inDir(t, postgresDir)

		output, err := runCommandWithCancelledCtx(t, nil, "dashboard")
		// The cancelled context causes an error from the server, but
		// source detection should still have printed to stderr.
		_ = err

		assertContains(t, output, "local")
		assertContains(t, output, "enabled")
	})

	t.Run("hidden subdir does not activate local source", func(t *testing.T) {
		// Regression: a pacto.yaml inside a hidden directory (e.g. ~/.Trash) must
		// NOT activate the local source. Before the fix, running `pacto dashboard`
		// from such a root (notably $HOME) rooted the local source there and
		// LocalSource.ListServices recursively walked the entire tree, blocking
		// /api/services indefinitely so the dashboard sat on "Loading services…".
		//
		// Not parallel: modifies process-wide KUBECONFIG and uses inDir.
		origKC := os.Getenv("KUBECONFIG")
		os.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
		defer func() {
			if origKC == "" {
				os.Unsetenv("KUBECONFIG")
			} else {
				os.Setenv("KUBECONFIG", origKC)
			}
		}()

		// Mirror ~/.Trash/pacto.yaml: a pacto.yaml directly inside a hidden
		// immediate subdir of the working directory.
		root := t.TempDir()
		hidden := filepath.Join(root, ".Trash")
		if err := os.MkdirAll(hidden, 0o755); err != nil {
			t.Fatal(err)
		}
		contractYAML := "pactoVersion: \"2.0\"\nservice:\n  name: trashed\n  version: 1.0.0\n"
		if err := os.WriteFile(filepath.Join(hidden, "pacto.yaml"), []byte(contractYAML), 0o644); err != nil {
			t.Fatal(err)
		}
		inDir(t, root)

		// With local (hidden-only bundle), k8s (invalid kubeconfig), oci (no ref)
		// and cache (--no-cache) all unavailable, detection must report no sources
		// rather than activating local off the hidden bundle.
		output, err := runCommandWithCancelledCtx(t, nil, "dashboard", "--no-cache")
		if err == nil {
			t.Fatal("expected error: a pacto.yaml in a hidden subdir must not activate any source")
		}
		assertContains(t, output, "local")
		assertContains(t, output, "no pacto.yaml found")
	})

	t.Run("custom port flag", func(t *testing.T) {
		t.Parallel()

		output, err := runCommand(t, nil, "dashboard", "--port", "0", "--help")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertContains(t, output, "port")
	})
}

func TestDashboardWithOCI(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	// Push a bundle to the test registry.
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"
	_, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("failed to push bundle: %v", err)
	}

	output, err := runCommandWithCancelledCtx(t, reg, "dashboard", "oci://"+reg.host+"/postgres-pacto")
	// The cancelled context causes the server to return immediately, but
	// source detection output should already be written to stderr.
	_ = err

	assertContains(t, output, "oci")
}
