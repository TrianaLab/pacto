//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPackCommand(t *testing.T) {
	t.Parallel()

	t.Run("archive creation", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		_, err := runCommand(t, nil, "init", "pack-svc")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "pack-svc")
		output, err := runCommand(t, nil, "pack", svcDir)
		if err != nil {
			t.Fatalf("pack failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Packed pack-svc@0.1.0")

		archivePath := filepath.Join(dir, "pack-svc-0.1.0.tar.gz")
		if _, err := os.Stat(archivePath); err != nil {
			t.Fatalf("expected archive at %s: %v", archivePath, err)
		}
		verifyArchiveContains(t, archivePath, "pacto.yaml")
	})

	t.Run("custom output path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		_, err := runCommand(t, nil, "init", "pack-out")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "pack-out")
		outPath := filepath.Join(dir, "custom-output.tar.gz")
		output, err := runCommand(t, nil, "pack", svcDir, "-o", outPath)
		if err != nil {
			t.Fatalf("pack -o failed: %v\noutput: %s", err, output)
		}

		if _, err := os.Stat(outPath); err != nil {
			t.Fatalf("expected archive at %s: %v", outPath, err)
		}
		verifyArchiveContains(t, outPath, "pacto.yaml")
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		_, err := runCommand(t, nil, "init", "pack-json")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "pack-json")
		output, err := runCommand(t, nil, "--output-format", "json", "pack", svcDir)
		if err != nil {
			t.Fatalf("pack json failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["Name"] != "pack-json" {
			t.Errorf("expected Name=pack-json, got %v", result["Name"])
		}
	})

	t.Run("markdown output", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		_, err := runCommand(t, nil, "init", "pack-md")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "pack-md")
		output, err := runCommand(t, nil, "--output-format", "markdown", "pack", svcDir)
		if err != nil {
			t.Fatalf("pack markdown failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "pack-md")
	})

	t.Run("with set override", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		_, err := runCommand(t, nil, "init", "pack-set")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "pack-set")
		output, err := runCommand(t, nil, "pack", svcDir, "--set", "service.version=2.0.0")
		if err != nil {
			t.Fatalf("pack --set failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "2.0.0")
	})

	t.Run("with values file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inDir(t, dir)

		_, err := runCommand(t, nil, "init", "pack-vals")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "pack-vals")
		valuesFile := writeValuesFile(t, t.TempDir(), "vals.yaml", "service:\n  version: \"3.0.0\"\n")
		output, err := runCommand(t, nil, "pack", svcDir, "-f", valuesFile)
		if err != nil {
			t.Fatalf("pack -f failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "3.0.0")
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "pack", "--help")
		if err != nil {
			t.Fatalf("pack --help failed: %v", err)
		}
		assertContains(t, output, "pack")
		assertContains(t, output, "Usage")
	})
}
