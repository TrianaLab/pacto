//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestGraphCommand(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push leaf contracts (setup)
	postgresPath := writePostgresBundle(t)
	redisV1Path := writeRedisV1Bundle(t)
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
	if err != nil {
		t.Fatalf("push postgres failed: %v", err)
	}
	_, err = runCommand(t, reg, "push", "oci://"+reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
	if err != nil {
		t.Fatalf("push redis failed: %v", err)
	}

	t.Run("dependency tree resolution", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		output, err := runCommand(t, reg, "graph", myAppPath)
		if err != nil {
			t.Fatalf("graph failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "my-app@1.0.0")
		assertContains(t, output, "postgres-pacto@1.0.0")
		assertContains(t, output, "redis-pacto@1.0.0")
	})

	t.Run("missing dep error in edge", func(t *testing.T) {
		t.Parallel()
		missingReg := newTestRegistry(t)
		myAppPath := writeMyAppV1Bundle(t, missingReg.host)
		output, err := runCommand(t, missingReg, "graph", myAppPath)
		if err != nil {
			t.Fatalf("graph failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "my-app@1.0.0")
		assertContains(t, output, "error:")
	})

	t.Run("OCI ref graph", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/my-app:1.0.0", "-p", myAppPath)
		if err != nil {
			t.Fatalf("push my-app failed: %v", err)
		}

		output, err := runCommand(t, reg, "graph", "oci://"+reg.host+"/my-app:1.0.0")
		if err != nil {
			t.Fatalf("graph via OCI failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "my-app@1.0.0")
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		output, err := runCommand(t, reg, "--output-format", "json", "graph", myAppPath)
		if err != nil {
			t.Fatalf("graph json failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["root"] == nil {
			t.Error("expected root in JSON output")
		}
	})

	t.Run("markdown output", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		output, err := runCommand(t, reg, "--output-format", "markdown", "graph", myAppPath)
		if err != nil {
			t.Fatalf("graph markdown failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "my-app")
	})

	t.Run("verbose flag", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		_, err := runCommand(t, reg, "--verbose", "graph", myAppPath)
		if err != nil {
			t.Fatalf("graph --verbose failed: %v", err)
		}
	})

	t.Run("no-cache flag", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		output, err := runCommand(t, reg, "--no-cache", "graph", myAppPath)
		if err != nil {
			t.Fatalf("graph --no-cache failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "my-app@1.0.0")
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "graph", "--help")
		if err != nil {
			t.Fatalf("graph --help failed: %v", err)
		}
		assertContains(t, output, "graph")
		assertContains(t, output, "Usage")
	})
}

func TestGraphWithDependencies(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push all deps
	postgresPath := writePostgresBundle(t)
	redisV1Path := writeRedisV1Bundle(t)
	redisV2Path := writeRedisV2Bundle(t)

	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
	if err != nil {
		t.Fatalf("push postgres failed: %v", err)
	}
	_, err = runCommand(t, reg, "push", "oci://"+reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
	if err != nil {
		t.Fatalf("push redis v1 failed: %v", err)
	}
	_, err = runCommand(t, reg, "push", "oci://"+reg.host+"/redis-pacto:2.0.0", "-p", redisV2Path)
	if err != nil {
		t.Fatalf("push redis v2 failed: %v", err)
	}

	t.Run("multi-level resolution", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		output, err := runCommand(t, reg, "graph", myAppPath)
		if err != nil {
			t.Fatalf("graph failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "my-app@1.0.0")
		assertContains(t, output, "postgres-pacto@1.0.0")
		assertContains(t, output, "redis-pacto@1.0.0")
	})

	t.Run("version conflict detection", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "conflict-app")
		conflictYAML := fmt.Sprintf(`pactoVersion: "1.0"

service:
  name: conflict-app
  version: 1.0.0
  owner: team/platform

interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml

configurations:
  - name: default
    schema: configuration/schema.json

dependencies:
  - name: redis-v1
    ref: oci://%s/redis-pacto:1.0.0
    required: true
    compatibility: "^1.0.0"
  - name: redis-v2
    ref: oci://%s/redis-pacto:2.0.0
    required: true
    compatibility: "^2.0.0"

runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health

scaling:
  min: 1
  max: 3
`, reg.host, reg.host)

		conflictDir := writeBundleDir(t, dir, conflictYAML, map[string]string{
			"openapi.yaml": fmt.Sprintf(openapiTemplate, "conflict-app", "1.0.0"),
		})

		output, err := runCommand(t, reg, "graph", conflictDir)
		if err != nil {
			t.Fatalf("graph failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "conflict-app@1.0.0")
		assertContains(t, output, "Conflicts")
		assertContains(t, output, "redis-pacto")
	})

	t.Run("json output with full tree", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		output, err := runCommand(t, reg, "--output-format", "json", "graph", myAppPath)
		if err != nil {
			t.Fatalf("graph json failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}

		root, ok := result["root"].(map[string]interface{})
		if !ok {
			t.Fatal("expected root object in JSON output")
		}
		if root["name"] != "my-app" {
			t.Errorf("expected root name=my-app, got %v", root["name"])
		}

		deps, ok := root["dependencies"].([]interface{})
		if !ok || len(deps) == 0 {
			t.Error("expected non-empty dependencies array in root")
		}
	})

	t.Run("with values override", func(t *testing.T) {
		t.Parallel()
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		output, err := runCommand(t, reg, "graph", myAppPath, "--set", "service.version=5.0.0")
		if err != nil {
			t.Fatalf("graph --set failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "5.0.0")
	})
}
