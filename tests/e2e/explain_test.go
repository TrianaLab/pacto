//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

func TestExplainCommand(t *testing.T) {
	t.Parallel()

	t.Run("text output", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "explain", postgresPath)
		if err != nil {
			t.Fatalf("explain failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Service: postgres-pacto@1.0.0")
		assertContains(t, output, "Owner: team/data")
		assertContains(t, output, "Pacto Version: 1.0")
		assertContains(t, output, "Workload: service")
		assertContains(t, output, "State: stateful")
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--output-format", "json", "explain", postgresPath)
		if err != nil {
			t.Fatalf("explain json failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["name"] != "postgres-pacto" {
			t.Errorf("expected name=postgres-pacto, got %v", result["name"])
		}
	})

	t.Run("markdown output", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--output-format", "markdown", "explain", postgresPath)
		if err != nil {
			t.Fatalf("explain markdown failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "postgres-pacto")
	})

	t.Run("OCI reference", func(t *testing.T) {
		t.Parallel()
		reg := newTestRegistry(t)

		postgresPath := writePostgresBundle(t)
		_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		output, err := runCommand(t, reg, "explain", "oci://"+reg.host+"/postgres-pacto:1.0.0")
		if err != nil {
			t.Fatalf("explain via OCI failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "Service: postgres-pacto@1.0.0")
	})

	t.Run("verbose flag", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--verbose", "explain", postgresPath)
		if err != nil {
			t.Fatalf("explain --verbose failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "postgres-pacto")
	})

	t.Run("with set override", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "explain", postgresPath, "--set", "service.version=9.9.9")
		if err != nil {
			t.Fatalf("explain with --set failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "9.9.9")
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "explain", "--help")
		if err != nil {
			t.Fatalf("explain --help failed: %v", err)
		}
		assertContains(t, output, "explain")
		assertContains(t, output, "Usage")
	})
}
