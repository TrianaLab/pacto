//go:build e2e

package e2e

import (
	"os"
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
