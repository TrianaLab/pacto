//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateCommand(t *testing.T) {
	t.Parallel()

	t.Run("plugin execution", testGeneratePlugin)
	t.Run("json output", testGenerateJSON)
	t.Run("markdown output", testGenerateMarkdown)
	t.Run("nonexistent plugin error", testGenerateNonexistentPlugin)
	t.Run("OCI reference", testGenerateOCI)
	t.Run("with plugin option", testGenerateWithOption)
	t.Run("with values override", testGenerateWithOverride)
	t.Run("help flag", testGenerateHelp)
}

func testGeneratePlugin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inDir(t, dir)

	postgresPath := writePostgresBundle(t)
	outDir := filepath.Join(dir, "gen-output")

	output, err := runCommand(t, nil, "generate", "test", postgresPath, "-o", outDir)
	if err != nil {
		t.Fatalf("generate failed: %v\noutput: %s", err, output)
	}

	assertContains(t, output, "Generated 2 file(s) using test")

	deployPath := filepath.Join(outDir, "deployment.yaml")
	if _, err := os.Stat(deployPath); err != nil {
		t.Fatalf("expected deployment.yaml: %v", err)
	}
	servicePath := filepath.Join(outDir, "service.yaml")
	if _, err := os.Stat(servicePath); err != nil {
		t.Fatalf("expected service.yaml: %v", err)
	}

	data, err := os.ReadFile(deployPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "postgres-pacto") {
		t.Error("deployment.yaml doesn't reference the service name")
	}
}

func testGenerateJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inDir(t, dir)

	postgresPath := writePostgresBundle(t)
	outDir := filepath.Join(dir, "gen-json-output")

	output, err := runCommand(t, nil, "--output-format", "json", "generate", "test", postgresPath, "-o", outDir)
	if err != nil {
		t.Fatalf("generate json failed: %v\noutput: %s", err, output)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("expected valid JSON output, got: %s", output)
	}
	if result["plugin"] != "test" {
		t.Errorf("expected plugin=test, got %v", result["plugin"])
	}
	if result["filesCount"] != float64(2) {
		t.Errorf("expected filesCount=2, got %v", result["filesCount"])
	}
}

func testGenerateMarkdown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inDir(t, dir)

	postgresPath := writePostgresBundle(t)
	outDir := filepath.Join(dir, "gen-md-output")

	output, err := runCommand(t, nil, "--output-format", "markdown", "generate", "test", postgresPath, "-o", outDir)
	if err != nil {
		t.Fatalf("generate markdown failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "test")
}

func testGenerateNonexistentPlugin(t *testing.T) {
	t.Parallel()
	postgresPath := writePostgresBundle(t)

	_, err := runCommand(t, nil, "generate", "nonexistent-plugin", postgresPath)
	if err == nil {
		t.Fatal("expected generate to fail for nonexistent plugin")
	}
}

func testGenerateOCI(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	postgresPath := writePostgresBundle(t)
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	dir := t.TempDir()
	inDir(t, dir)

	outDir := filepath.Join(dir, "gen-oci-output")
	output, err := runCommand(t, reg, "generate", "test", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-o", outDir)
	if err != nil {
		t.Fatalf("generate via OCI failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Generated 2 file(s)")
}

func testGenerateWithOption(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inDir(t, dir)

	postgresPath := writePostgresBundle(t)
	outDir := filepath.Join(dir, "gen-opt-output")

	output, err := runCommand(t, nil, "generate", "test", postgresPath, "-o", outDir, "--option", "namespace=production")
	if err != nil {
		t.Fatalf("generate --option failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Generated")
}

func testGenerateWithOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inDir(t, dir)

	postgresPath := writePostgresBundle(t)
	outDir := filepath.Join(dir, "gen-vals-output")

	output, err := runCommand(t, nil, "generate", "test", postgresPath, "-o", outDir, "--set", "service.version=5.0.0")
	if err != nil {
		t.Fatalf("generate --set failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Generated")
}

func testGenerateHelp(t *testing.T) {
	t.Parallel()
	output, err := runCommand(t, nil, "generate", "--help")
	if err != nil {
		t.Fatalf("generate --help failed: %v", err)
	}
	assertContains(t, output, "generate")
	assertContains(t, output, "Usage")
}
