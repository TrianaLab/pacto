//go:build integration

package integration

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

		var result map[string]any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["dir"] != "json-svc" {
			t.Errorf("expected dir=json-svc, got %v", result["dir"])
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

	t.Run("existing empty dir gets the blank contract", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		os.Mkdir(filepath.Join(dir, "existing-svc"), 0755)

		if _, err := runCommand(t, nil, "init", "existing-svc"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "existing-svc", "pacto.yaml")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("pactify a plain service, plan first", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "fraud")
		os.MkdirAll(filepath.Join(dir, "k8s"), 0755)
		os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0644)
		os.WriteFile(filepath.Join(dir, "k8s", "deployment.yaml"), []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: fraud}\nspec: {template: {spec: {containers: [{name: fraud, image: 'registry.example/story/images/fraud:1.0.0'}]}}}\n"), 0644)

		output, err := runCommand(t, nil, "init", "--plan", "--delivery", "--owner", "risk", dir)
		if err != nil {
			t.Fatalf("init --plan: %v\n%s", err, output)
		}
		assertContains(t, output, "Plan for fraud")
		assertContains(t, output, "bundle: oci://registry.example/story/fraud")
		assertContains(t, output, "kind: Stage")
		if _, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
			t.Fatal("--plan wrote pacto.yaml")
		}

		if output, err = runCommand(t, nil, "init", "--delivery", "--owner", "risk", dir); err != nil {
			t.Fatalf("init: %v\n%s", err, output)
		}
		if output, err = runCommand(t, nil, "validate", dir); err != nil {
			t.Fatalf("validate: %v\n%s", err, output)
		}
		output, err = runCommand(t, nil, "init", "--delivery", dir)
		if err != nil {
			t.Fatal(err)
		}
		assertContains(t, output, "Nothing to add")
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
