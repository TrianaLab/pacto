//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCommand(t *testing.T) {
	t.Parallel()

	t.Run("local valid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		_, err := runCommand(t, nil, "init", "valid-svc")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "valid-svc")
		output, err := runCommand(t, nil, "validate", svcDir)
		if err != nil {
			t.Fatalf("validate failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("local invalid", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(brokenContract), 0644); err != nil {
			t.Fatal(err)
		}

		output, err := runCommand(t, nil, "validate", dir)
		if err == nil {
			t.Fatal("expected validate to fail on broken contract")
		}
		assertContains(t, output, "HEALTH_INTERFACE_NOT_FOUND")
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		_, err := runCommand(t, nil, "init", "json-validate")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "json-validate")
		output, err := runCommand(t, nil, "--output-format", "json", "validate", svcDir)
		if err != nil {
			t.Fatalf("validate json failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, `"Valid": true`)
	})

	t.Run("markdown output", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--output-format", "markdown", "validate", postgresPath)
		if err != nil {
			t.Fatalf("validate markdown failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "valid")
	})

	t.Run("OCI reference validation", func(t *testing.T) {
		t.Parallel()
		reg := newTestRegistry(t)

		postgresPath := writePostgresBundle(t)
		_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		output, err := runCommand(t, reg, "validate", "oci://"+reg.host+"/postgres-pacto:1.0.0")
		if err != nil {
			t.Fatalf("validate via OCI failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("verbose flag accepted", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--verbose", "validate", postgresPath)
		if err != nil {
			t.Fatalf("validate --verbose failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("missing directory error", func(t *testing.T) {
		t.Parallel()
		_, err := runCommand(t, nil, "validate", "/nonexistent/path/to/contract")
		if err == nil {
			t.Fatal("expected validate to fail for missing directory")
		}
	})

	t.Run("no pacto.yaml error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := runCommand(t, nil, "validate", dir)
		if err == nil {
			t.Fatal("expected validate to fail for directory without pacto.yaml")
		}
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "validate", "--help")
		if err != nil {
			t.Fatalf("validate --help failed: %v", err)
		}
		assertContains(t, output, "validate")
		assertContains(t, output, "Usage")
	})
}
