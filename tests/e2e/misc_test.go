//go:build e2e

package e2e

import (
	"testing"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	t.Run("prints version", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "version")
		if err != nil {
			t.Fatalf("version failed: %v", err)
		}
		assertContains(t, output, "test-e2e")
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "version", "--help")
		if err != nil {
			t.Fatalf("version --help failed: %v", err)
		}
		assertContains(t, output, "version")
	})
}

func TestUpdateCommand(t *testing.T) {
	t.Parallel()

	t.Run("dev build error", func(t *testing.T) {
		t.Parallel()
		_, err := runCommandWithVersion(t, nil, "dev", "update")
		if err == nil {
			t.Fatal("expected update to fail on dev build")
		}
		assertContains(t, err.Error(), "cannot update a dev build")
	})

	t.Run("specific version dev build error", func(t *testing.T) {
		t.Parallel()
		_, err := runCommandWithVersion(t, nil, "dev", "update", "v1.0.0")
		if err == nil {
			t.Fatal("expected update to fail on dev build")
		}
		assertContains(t, err.Error(), "cannot update a dev build")
	})

	t.Run("command registered", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "update", "--help")
		if err != nil {
			t.Fatalf("update --help failed: %v", err)
		}
		assertContains(t, output, "Downloads and installs the specified version")
		assertContains(t, output, "update [version]")
	})

	t.Run("too many args", func(t *testing.T) {
		t.Parallel()
		_, err := runCommand(t, nil, "update", "v1.0.0", "extra")
		if err == nil {
			t.Fatal("expected error for too many arguments")
		}
	})
}

func TestRootCommand(t *testing.T) {
	t.Parallel()

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "--help")
		if err != nil {
			t.Fatalf("root --help failed: %v", err)
		}
		assertContains(t, output, "pacto")
		assertContains(t, output, "Available Commands")
	})

	t.Run("unknown command error", func(t *testing.T) {
		t.Parallel()
		_, err := runCommand(t, nil, "nonexistent-command")
		if err == nil {
			t.Fatal("expected error for unknown command")
		}
	})

	t.Run("verbose flag accepted", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "--verbose", "version")
		if err != nil {
			t.Fatalf("--verbose version failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "test-e2e")
	})

	t.Run("no-cache flag accepted", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)
		output, err := runCommand(t, nil, "--no-cache", "validate", postgresPath)
		if err != nil {
			t.Fatalf("--no-cache validate failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})
}
