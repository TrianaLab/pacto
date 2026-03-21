//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitCommand(t *testing.T) {
	t.Parallel()

	t.Run("scaffold structure", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		output, err := runCommand(t, nil, "init", "test-svc")
		if err != nil {
			t.Fatalf("init failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Created test-svc/")

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
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		output, err := runCommand(t, nil, "--output-format", "json", "init", "json-svc")
		if err != nil {
			t.Fatalf("init failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["Dir"] != "json-svc" {
			t.Errorf("expected Dir=json-svc, got %v", result["Dir"])
		}
	})

	t.Run("markdown output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		output, err := runCommand(t, nil, "--output-format", "markdown", "init", "md-svc")
		if err != nil {
			t.Fatalf("init markdown failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "md-svc")
	})

	t.Run("error on existing dir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		os.Mkdir(filepath.Join(dir, "existing-svc"), 0755)

		_, err := runCommand(t, nil, "init", "existing-svc")
		if err == nil {
			t.Fatal("expected init to fail when directory already exists")
		}
	})

	t.Run("error without name", func(t *testing.T) {
		t.Parallel()
		_, err := runCommand(t, nil, "init")
		if err == nil {
			t.Fatal("expected init to fail without a name argument")
		}
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "init", "--help")
		if err != nil {
			t.Fatalf("init --help failed: %v", err)
		}
		assertContains(t, output, "init")
		assertContains(t, output, "Usage")
	})
}
