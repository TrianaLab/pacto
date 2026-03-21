//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPushCommand(t *testing.T) {
	t.Parallel()

	t.Run("pushes and reports digest", testPushReportsDigest)
	t.Run("json output", testPushJSON)
	t.Run("markdown output", testPushMarkdown)
	t.Run("force overwrites existing", testPushForce)
	t.Run("rejects invalid contract", testPushRejectsInvalid)
	t.Run("with values override", testPushWithOverride)
	t.Run("verbose flag", testPushVerbose)
	t.Run("help flag", testPushHelp)
}

func testPushReportsDigest(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"

	output, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Pushed postgres-pacto@1.0.0")
	assertContains(t, output, "Digest: sha256:")
}

func testPushJSON(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	redisPath := writeRedisV1Bundle(t)
	ref := "oci://" + reg.host + "/redis-pacto:1.0.0"

	output, err := runCommand(t, reg, "--output-format", "json", "push", ref, "-p", redisPath)
	if err != nil {
		t.Fatalf("push json failed: %v\noutput: %s", err, output)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("expected valid JSON output, got: %s", output)
	}
	if result["Name"] != "redis-pacto" {
		t.Errorf("expected Name=redis-pacto, got %v", result["Name"])
	}
}

func testPushMarkdown(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"

	output, err := runCommand(t, reg, "--output-format", "markdown", "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push markdown failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "postgres-pacto")
}

func testPushForce(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/force-test:1.0.0"

	// First push
	_, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("first push failed: %v", err)
	}

	// Second push with --force
	output, err := runCommand(t, reg, "push", ref, "-p", postgresPath, "--force")
	if err != nil {
		t.Fatalf("force push failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Pushed")
}

func testPushRejectsInvalid(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(brokenContract), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/broken:1.0.0", "-p", dir)
	if err == nil {
		t.Fatal("expected push to fail for invalid contract")
	}
}

func testPushWithOverride(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/vals-test:2.0.0"

	output, err := runCommand(t, reg, "push", ref, "-p", postgresPath, "--set", "service.version=2.0.0")
	if err != nil {
		t.Fatalf("push --set failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "2.0.0")
}

func testPushVerbose(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/verbose-test:1.0.0"

	_, err := runCommand(t, reg, "--verbose", "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push --verbose failed: %v", err)
	}
}

func testPushHelp(t *testing.T) {
	t.Parallel()
	output, err := runCommand(t, nil, "push", "--help")
	if err != nil {
		t.Fatalf("push --help failed: %v", err)
	}
	assertContains(t, output, "push")
	assertContains(t, output, "Usage")
}

func TestPullCommand(t *testing.T) {
	t.Parallel()

	t.Run("pulls to specified output", testPullToOutput)
	t.Run("default output directory", testPullDefaultDir)
	t.Run("json output", testPullJSON)
	t.Run("markdown output", testPullMarkdown)
	t.Run("nonexistent ref error", testPullNonexistent)
	t.Run("no-cache flag", testPullNoCache)
	t.Run("help flag", testPullHelp)
}

func testPullToOutput(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"

	_, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	pullDir := t.TempDir()
	output, err := runCommand(t, reg, "pull", ref, "-o", filepath.Join(pullDir, "pulled"))
	if err != nil {
		t.Fatalf("pull failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Pulled postgres-pacto@1.0.0")

	pulledPacto := filepath.Join(pullDir, "pulled", "pacto.yaml")
	if _, err := os.Stat(pulledPacto); err != nil {
		t.Fatalf("expected pacto.yaml in pulled dir: %v", err)
	}

	data, err := os.ReadFile(pulledPacto)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "postgres-pacto") {
		t.Error("pulled contract doesn't contain expected service name")
	}
}

func testPullDefaultDir(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"

	_, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	dir := t.TempDir()
	inDir(t, dir)

	output, err := runCommand(t, reg, "pull", ref)
	if err != nil {
		t.Fatalf("pull default dir failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Pulled postgres-pacto@1.0.0")

	// Default output should use service name
	if _, err := os.Stat(filepath.Join(dir, "postgres-pacto", "pacto.yaml")); err != nil {
		t.Fatalf("expected pacto.yaml in default output dir: %v", err)
	}
}

func testPullJSON(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	redisPath := writeRedisV1Bundle(t)
	ref := "oci://" + reg.host + "/redis-pacto:1.0.0"

	_, err := runCommand(t, reg, "push", ref, "-p", redisPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	pullDir := t.TempDir()
	output, err := runCommand(t, reg, "--output-format", "json", "pull", ref, "-o", filepath.Join(pullDir, "pulled"))
	if err != nil {
		t.Fatalf("pull json failed: %v\noutput: %s", err, output)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("expected valid JSON output, got: %s", output)
	}
	if result["Name"] != "redis-pacto" {
		t.Errorf("expected Name=redis-pacto, got %v", result["Name"])
	}
}

func testPullMarkdown(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"

	_, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	pullDir := t.TempDir()
	output, err := runCommand(t, reg, "--output-format", "markdown", "pull", ref, "-o", filepath.Join(pullDir, "pulled"))
	if err != nil {
		t.Fatalf("pull markdown failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "postgres-pacto")
}

func testPullNonexistent(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	_, err := runCommand(t, reg, "pull", "oci://"+reg.host+"/nonexistent:latest")
	if err == nil {
		t.Fatal("expected pull to fail for nonexistent reference")
	}
}

func testPullNoCache(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"

	_, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	pullDir := t.TempDir()
	output, err := runCommand(t, reg, "--no-cache", "pull", ref, "-o", filepath.Join(pullDir, "pulled"))
	if err != nil {
		t.Fatalf("pull --no-cache failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Pulled")
}

func testPullHelp(t *testing.T) {
	t.Parallel()
	output, err := runCommand(t, nil, "pull", "--help")
	if err != nil {
		t.Fatalf("pull --help failed: %v", err)
	}
	assertContains(t, output, "pull")
	assertContains(t, output, "Usage")
}

func TestPushPullRoundtrip(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/roundtrip-svc:1.0.0"

	// Push
	_, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Pull
	pullDir := t.TempDir()
	_, err = runCommand(t, reg, "pull", ref, "-o", filepath.Join(pullDir, "pulled"))
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	// Validate pulled contract
	output, err := runCommand(t, nil, "validate", filepath.Join(pullDir, "pulled"))
	if err != nil {
		t.Fatalf("validate pulled failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")

	// Diff should show no changes
	output, err = runCommand(t, nil, "diff", postgresPath, filepath.Join(pullDir, "pulled"))
	if err != nil {
		t.Fatalf("diff failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "No changes detected")
}
