//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Base contract for override tests — a minimal valid contract with a config schema.
const overrideBaseContract = `pactoVersion: "1.0"
service:
  name: override-svc
  version: "1.0.0"
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
`

// A config schema that expects DB_HOST (string) and DB_PORT (integer).
const overrideConfigSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "DB_HOST": {"type": "string"},
    "DB_PORT": {"type": "integer"},
    "DEBUG":   {"type": "boolean"}
  },
  "additionalProperties": false
}`

// writeOverrideBundle creates a bundle for override testing with a typed config schema.
func writeOverrideBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "override-svc")
	path := writeBundleDir(t, dir, overrideBaseContract, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "override-svc", "1.0.0"),
	})
	// Overwrite the default empty config schema with one that has typed properties.
	schemaPath := filepath.Join(path, "configuration", "schema.json")
	if err := os.WriteFile(schemaPath, []byte(overrideConfigSchema), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeValuesFile creates a temporary YAML values file and returns its path.
func writeValuesFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOverrideValidate(t *testing.T) {
	t.Parallel()

	t.Run("set overrides service version", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		output, err := runCommand(t, nil, "validate", bundlePath, "--set", "service.version=2.0.0")
		if err != nil {
			t.Fatalf("validate with --set failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("set invalid semver version fails", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		output, err := runCommand(t, nil, "validate", bundlePath, "--set", "service.version=not-semver")
		if err == nil {
			t.Fatalf("expected validate to fail with invalid semver override\noutput: %s", output)
		}
		assertContains(t, output, "INVALID_SEMVER")
	})

	t.Run("values file overrides contract", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		valuesFile := writeValuesFile(t, t.TempDir(), "values.yaml", `service:
  version: "3.0.0"
`)

		output, err := runCommand(t, nil, "validate", bundlePath, "-f", valuesFile)
		if err != nil {
			t.Fatalf("validate with -f failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("set takes precedence over values file", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		// Values file sets a valid version, --set overrides with invalid version.
		valuesFile := writeValuesFile(t, t.TempDir(), "values.yaml", `service:
  version: "2.0.0"
`)

		output, err := runCommand(t, nil, "validate", bundlePath, "-f", valuesFile, "--set", "service.version=not-semver")
		if err == nil {
			t.Fatalf("expected --set to take precedence and fail validation\noutput: %s", output)
		}
		assertContains(t, output, "INVALID_SEMVER")
	})

	t.Run("last values file wins", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)
		tmpDir := t.TempDir()

		// First file sets a valid version.
		f1 := writeValuesFile(t, tmpDir, "v1.yaml", `service:
  version: "2.0.0"
`)
		// Second file overrides with an invalid version.
		f2 := writeValuesFile(t, tmpDir, "v2.yaml", `service:
  version: "bad-version"
`)

		output, err := runCommand(t, nil, "validate", bundlePath, "-f", f1, "-f", f2)
		if err == nil {
			t.Fatalf("expected last values file to win and fail validation\noutput: %s", output)
		}
		assertContains(t, output, "INVALID_SEMVER")
	})

	t.Run("values file then set then values file precedence", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)
		tmpDir := t.TempDir()

		// Values file sets valid version. --set overrides to valid 4.0.0.
		// Since --set always beats -f regardless of order, this should pass.
		f1 := writeValuesFile(t, tmpDir, "vals.yaml", `service:
  version: "bad-version"
`)

		output, err := runCommand(t, nil, "validate", bundlePath, "-f", f1, "--set", "service.version=4.0.0")
		if err != nil {
			t.Fatalf("expected --set to override bad values file: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})
}

func TestOverrideConfigValues(t *testing.T) {
	t.Parallel()

	t.Run("valid config values via set", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		output, err := runCommand(t, nil, "validate", bundlePath,
			"--set", "configurations[0].values.DB_HOST=localhost",
			"--set", "configurations[0].values.DB_PORT=5432",
			"--set", "configurations[0].values.DEBUG=true",
		)
		if err != nil {
			t.Fatalf("validate with valid config values failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("wrong type config value fails", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		// DB_PORT expects integer, "not-a-number" is a string.
		output, err := runCommand(t, nil, "validate", bundlePath,
			"--set", "configurations[0].values.DB_PORT=not-a-number",
		)
		if err == nil {
			t.Fatalf("expected validation to fail for wrong type config value\noutput: %s", output)
		}
		assertContains(t, output, "CONFIG_VALUES_VALIDATION_FAILED")
	})

	t.Run("undefined config property fails", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		// UNKNOWN_KEY is not in the schema (additionalProperties: false).
		output, err := runCommand(t, nil, "validate", bundlePath,
			"--set", "configurations[0].values.UNKNOWN_KEY=hello",
		)
		if err == nil {
			t.Fatalf("expected validation to fail for undefined config property\noutput: %s", output)
		}
		assertContains(t, output, "CONFIG_VALUES_VALIDATION_FAILED")
	})

	t.Run("config values via values file", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		valuesFile := writeValuesFile(t, t.TempDir(), "config-values.yaml", `configurations:
  - name: default
    schema: configuration/schema.json
    values:
      DB_HOST: db.example.com
      DB_PORT: 3306
`)

		output, err := runCommand(t, nil, "validate", bundlePath, "-f", valuesFile)
		if err != nil {
			t.Fatalf("validate with config values file failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("set overrides config values from file", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		// Values file sets a valid DB_PORT. --set overrides it with a string (wrong type).
		valuesFile := writeValuesFile(t, t.TempDir(), "config-values.yaml", `configurations:
  - name: default
    schema: configuration/schema.json
    values:
      DB_PORT: 5432
`)

		output, err := runCommand(t, nil, "validate", bundlePath,
			"-f", valuesFile,
			"--set", "configurations[0].values.DB_PORT=not-a-number",
		)
		if err == nil {
			t.Fatalf("expected --set to override config values file and fail\noutput: %s", output)
		}
		assertContains(t, output, "CONFIG_VALUES_VALIDATION_FAILED")
	})
}

func TestOverrideDiff(t *testing.T) {
	t.Parallel()

	t.Run("new-set changes version for diff", func(t *testing.T) {
		bundlePath := writeOverrideBundle(t)

		// Without override: same contract, no changes.
		output, err := runCommand(t, nil, "diff", bundlePath, bundlePath)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "No changes detected")

		// With --new-set: override version on the new contract → version change detected.
		output, err = runCommand(t, nil, "diff", bundlePath, bundlePath,
			"--new-set", "service.version=2.0.0",
		)
		_ = err
		assertContains(t, output, "service.version")
		assertNotContains(t, output, "No changes detected")
	})

	t.Run("old-set and new-set independently", func(t *testing.T) {
		bundlePath := writeOverrideBundle(t)

		// Override both: old gets 1.0.0 (same as base), new gets 3.0.0.
		output, err := runCommand(t, nil, "diff", bundlePath, bundlePath,
			"--old-set", "service.version=1.0.0",
			"--new-set", "service.version=3.0.0",
		)
		_ = err
		assertContains(t, output, "service.version")
		assertNotContains(t, output, "No changes detected")
	})

	t.Run("old-values and new-values files", func(t *testing.T) {
		bundlePath := writeOverrideBundle(t)
		tmpDir := t.TempDir()

		oldVals := writeValuesFile(t, tmpDir, "old.yaml", `service:
  owner: team/old
`)
		newVals := writeValuesFile(t, tmpDir, "new.yaml", `service:
  owner: team/new
`)

		output, err := runCommand(t, nil, "diff", bundlePath, bundlePath,
			"--old-values", oldVals,
			"--new-values", newVals,
		)
		_ = err
		assertContains(t, output, "service.owner")
		assertNotContains(t, output, "No changes detected")
	})

	t.Run("new-set takes precedence over new-values in diff", func(t *testing.T) {
		bundlePath := writeOverrideBundle(t)

		// new-values sets version to 2.0.0, new-set overrides it to 1.0.0 (same as base).
		newVals := writeValuesFile(t, t.TempDir(), "new.yaml", `service:
  version: "2.0.0"
`)

		output, err := runCommand(t, nil, "diff", bundlePath, bundlePath,
			"--new-values", newVals,
			"--new-set", "service.version=1.0.0",
		)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		// Version is overridden back to 1.0.0 (same as base), so no version change.
		// Owner is unchanged too. Should be no changes.
		assertContains(t, output, "No changes detected")
	})
}

func TestOverrideExplain(t *testing.T) {
	t.Parallel()

	t.Run("set overrides reflected in explain", func(t *testing.T) {
		bundlePath := writeOverrideBundle(t)

		output, err := runCommand(t, nil, "explain", bundlePath, "--set", "service.version=9.9.9")
		if err != nil {
			t.Fatalf("explain with --set failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "9.9.9")
	})
}
