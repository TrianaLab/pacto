//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(*testing.T) (reg *testRegistry, args []string)
		wantErr bool
		wantOut string
	}{
		{
			name: "local valid",
			setup: func(t *testing.T) (*testRegistry, []string) {
				dir := t.TempDir()
				inDir(t, dir)
				if _, err := runCommand(t, nil, "init", "valid-svc"); err != nil {
					t.Fatalf("init failed: %v", err)
				}
				return nil, []string{"validate", filepath.Join(dir, "valid-svc")}
			},
			wantOut: "is valid",
		},
		{
			name: "local invalid",
			setup: func(t *testing.T) (*testRegistry, []string) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(brokenContract), 0644); err != nil {
					t.Fatal(err)
				}
				return nil, []string{"validate", dir}
			},
			wantErr: true,
			wantOut: "HEALTH_INTERFACE_NOT_FOUND",
		},
		{
			name: "json output",
			setup: func(t *testing.T) (*testRegistry, []string) {
				dir := t.TempDir()
				inDir(t, dir)
				if _, err := runCommand(t, nil, "init", "json-validate"); err != nil {
					t.Fatalf("init failed: %v", err)
				}
				return nil, []string{"--output-format", "json", "validate", filepath.Join(dir, "json-validate")}
			},
			wantOut: `"Valid": true`,
		},
		{
			name: "markdown output",
			setup: func(t *testing.T) (*testRegistry, []string) {
				return nil, []string{"--output-format", "markdown", "validate", writePostgresBundle(t)}
			},
			wantOut: "valid",
		},
		{
			name: "OCI reference validation",
			setup: func(t *testing.T) (*testRegistry, []string) {
				reg := newTestRegistry(t)
				postgresPath := writePostgresBundle(t)
				if _, err := runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath); err != nil {
					t.Fatalf("push failed: %v", err)
				}
				return reg, []string{"validate", "oci://" + reg.host + "/postgres-pacto:1.0.0"}
			},
			wantOut: "is valid",
		},
		{
			name: "verbose flag accepted",
			setup: func(t *testing.T) (*testRegistry, []string) {
				return nil, []string{"--verbose", "validate", writePostgresBundle(t)}
			},
		},
		{
			name: "missing directory error",
			setup: func(t *testing.T) (*testRegistry, []string) {
				return nil, []string{"validate", "/nonexistent/path/to/contract"}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg, args := tt.setup(t)
			output, err := runCommand(t, reg, args...)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got none. output: %s", output)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v\noutput: %s", err, output)
			}
			if tt.wantOut != "" {
				assertContains(t, output, tt.wantOut)
			}
		})
	}

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "validate", "--help")
		if err != nil {
			t.Fatalf("validate --help failed: %v", err)
		}
		assertContains(t, output, "validate")
		assertContains(t, output, "Usage")
	})

	t.Run("structured owner valid", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeStructuredOwnerBundle(t)
		output, err := runCommand(t, nil, "validate", bundlePath)
		if err != nil {
			t.Fatalf("validate failed for structured owner: %v\noutput: %s", err, output)
		}
	})

	t.Run("scalar owner rejected", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "scalar-owner-svc")
		yaml := `pactoVersion: "1.0"
service:
  name: scalar-owner-svc
  version: 1.0.0
  owner: team/platform
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
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
		path := writeBundleDir(t, dir, yaml, nil)
		output, err := runCommand(t, nil, "validate", path)
		if err == nil {
			t.Fatalf("expected validation to fail for scalar owner, got:\n%s", output)
		}
	})

	t.Run("no pacto.yaml error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := runCommand(t, nil, "validate", dir)
		if err == nil {
			t.Fatal("expected validate to fail for directory without pacto.yaml")
		}
	})
}

func TestValidateFileContent(t *testing.T) {
	t.Parallel()

	t.Run("rejects invalid YAML in interface contract", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "bad-yaml-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: bad-yaml-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
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
		bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
			"openapi.yaml": ":\n  invalid:\n  - [yaml\n",
		})

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err == nil {
			t.Fatalf("expected validation to fail for invalid YAML interface contract, output: %s", output)
		}
		assertContains(t, output, "INVALID_CONTRACT_FILE")
	})

	t.Run("rejects invalid JSON in config schema", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "bad-config-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: bad-config-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
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
		bundlePath := writeBundleDirRaw(t, dir, contractYAML, nil, "not valid json")

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err == nil {
			t.Fatalf("expected validation to fail for invalid JSON config schema, output: %s", output)
		}
		assertContains(t, output, "INVALID_CONFIG_JSON")
	})

	t.Run("rejects invalid JSON in policy schema", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "bad-policy-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: bad-policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
policies:
  - name: default
    schema: policy/schema.json
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
		bundlePath := writeBundleDirWithPolicy(t, dir, contractYAML, "not valid json")

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err == nil {
			t.Fatalf("expected validation to fail for invalid JSON policy schema, output: %s", output)
		}
		assertContains(t, output, "INVALID_POLICY_JSON")
	})

	t.Run("rejects uncompilable config schema", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "bad-schema-compile-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: bad-schema-compile-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
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
		bundlePath := writeBundleDirRaw(t, dir, contractYAML, nil,
			`{"type":"object","properties":{"k":{"$ref":"nonexistent://bad"}}}`)

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err == nil {
			t.Fatalf("expected validation to fail for uncompilable config schema, output: %s", output)
		}
		assertContains(t, output, "INVALID_CONFIG_SCHEMA")
	})

	t.Run("accepts valid bundle with all referenced files", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "validate", postgresPath)
		if err != nil {
			t.Fatalf("validate failed for valid bundle: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})
}
