//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocCommand(t *testing.T) {
	t.Parallel()

	t.Run("text output", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "doc", postgresPath)
		if err != nil {
			t.Fatalf("doc failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "# postgres-pacto")
		assertContains(t, output, "Interfaces")
		assertContains(t, output, "Architecture")
		assertContains(t, output, "```mermaid")
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--output-format", "json", "doc", postgresPath)
		if err != nil {
			t.Fatalf("doc json failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["serviceName"] != "postgres-pacto" {
			t.Errorf("expected serviceName=postgres-pacto, got %v", result["serviceName"])
		}
		if result["markdown"] == nil {
			t.Error("expected markdown field in JSON output")
		}
	})

	t.Run("markdown output", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--output-format", "markdown", "doc", postgresPath)
		if err != nil {
			t.Fatalf("doc markdown failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "postgres-pacto")
	})

	t.Run("with output dir", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)
		outDir := filepath.Join(t.TempDir(), "doc-output")

		output, err := runCommand(t, nil, "doc", postgresPath, "-o", outDir)
		if err != nil {
			t.Fatalf("doc with output failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "Wrote")

		docPath := filepath.Join(outDir, "postgres-pacto.md")
		data, err := os.ReadFile(docPath)
		if err != nil {
			t.Fatalf("expected doc file at %s: %v", docPath, err)
		}
		if !strings.Contains(string(data), "# postgres-pacto") {
			t.Error("expected service heading in written file")
		}
	})

	t.Run("serve and output mutually exclusive", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)
		outDir := t.TempDir()

		_, err := runCommand(t, nil, "doc", postgresPath, "--serve", "-o", outDir)
		if err == nil {
			t.Fatal("expected error for --serve with --output")
		}
	})

	t.Run("OCI reference", func(t *testing.T) {
		t.Parallel()
		reg := newTestRegistry(t)

		postgresPath := writePostgresBundle(t)
		_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		output, err := runCommand(t, reg, "doc", "oci://"+reg.host+"/postgres-pacto:1.0.0")
		if err != nil {
			t.Fatalf("doc via OCI failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "# postgres-pacto")
	})

	t.Run("verbose flag", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--verbose", "doc", postgresPath)
		if err != nil {
			t.Fatalf("doc --verbose failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "postgres-pacto")
	})

	t.Run("with set override", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "doc", postgresPath, "--set", "service.version=9.0.0")
		if err != nil {
			t.Fatalf("doc --set failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "9.0.0")
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "doc", "--help")
		if err != nil {
			t.Fatalf("doc --help failed: %v", err)
		}
		assertContains(t, output, "doc")
		assertContains(t, output, "Usage")
	})
}

func TestDocCommandUI(t *testing.T) {
	t.Parallel()

	t.Run("zero interfaces errors", func(t *testing.T) {
		t.Parallel()
		path := writeZeroInterfaceBundle(t)
		_, err := runCommand(t, nil, "doc", "--ui", "swagger", path)
		if err == nil {
			t.Fatal("expected error for --ui swagger with zero HTTP interfaces")
		}
		assertContains(t, err.Error(), "no HTTP interfaces")
	})

	t.Run("one interface", func(t *testing.T) {
		t.Parallel()
		path := writeMyAppV1Bundle(t, "localhost")
		_, err := runCommandWithCancelledCtx(t, nil, "doc", "--ui", "swagger", "--port", "0", path)
		if err != nil {
			t.Fatalf("doc --ui swagger failed: %v", err)
		}
	})

	t.Run("two interfaces", func(t *testing.T) {
		t.Parallel()
		path := writeTwoInterfaceBundle(t)
		_, err := runCommandWithCancelledCtx(t, nil, "doc", "--ui", "swagger", "--port", "0", path)
		if err != nil {
			t.Fatalf("doc --ui swagger with 2 interfaces failed: %v", err)
		}
	})

	t.Run("interface filter", func(t *testing.T) {
		t.Parallel()
		path := writeTwoInterfaceBundle(t)
		_, err := runCommandWithCancelledCtx(t, nil, "doc", "--ui", "swagger", "--interface", "admin-api", "--port", "0", path)
		if err != nil {
			t.Fatalf("doc --ui swagger --interface admin-api failed: %v", err)
		}
	})

	t.Run("unknown interface errors", func(t *testing.T) {
		t.Parallel()
		path := writeTwoInterfaceBundle(t)
		_, err := runCommand(t, nil, "doc", "--ui", "swagger", "--interface", "nonexistent", path)
		if err == nil {
			t.Fatal("expected error for unknown interface")
		}
		assertContains(t, err.Error(), "not found among OpenAPI interfaces")
	})

	t.Run("interface without ui errors", func(t *testing.T) {
		t.Parallel()
		path := writeMyAppV1Bundle(t, "localhost")
		_, err := runCommand(t, nil, "doc", "--interface", "api", path)
		if err == nil {
			t.Fatal("expected error for --interface without --ui")
		}
		assertContains(t, err.Error(), "--interface requires --ui")
	})

	t.Run("ui and serve mutually exclusive", func(t *testing.T) {
		t.Parallel()
		path := writeMyAppV1Bundle(t, "localhost")
		_, err := runCommand(t, nil, "doc", "--ui", "swagger", "--serve", path)
		if err == nil {
			t.Fatal("expected error for --ui with --serve")
		}
		assertContains(t, err.Error(), "mutually exclusive")
	})

	t.Run("ui and output mutually exclusive", func(t *testing.T) {
		t.Parallel()
		path := writeMyAppV1Bundle(t, "localhost")
		_, err := runCommand(t, nil, "doc", "--ui", "swagger", "--output", t.TempDir(), path)
		if err == nil {
			t.Fatal("expected error for --ui with --output")
		}
		assertContains(t, err.Error(), "mutually exclusive")
	})

	t.Run("global target", func(t *testing.T) {
		t.Parallel()
		path := writeMyAppV1Bundle(t, "localhost")
		_, err := runCommandWithCancelledCtx(t, nil, "doc", "--ui", "swagger", "--target", "http://localhost:3000", "--port", "0", path)
		if err != nil {
			t.Fatalf("doc --ui swagger --target failed: %v", err)
		}
	})

	t.Run("per-interface targets", func(t *testing.T) {
		t.Parallel()
		path := writeTwoInterfaceBundle(t)
		_, err := runCommandWithCancelledCtx(t, nil, "doc", "--ui", "swagger",
			"--target", "public-api=http://localhost:3000",
			"--target", "admin-api=http://localhost:3001",
			"--port", "0", path)
		if err != nil {
			t.Fatalf("doc --ui swagger with per-interface targets failed: %v", err)
		}
	})
}
