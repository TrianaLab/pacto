//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyAndConfigRef(t *testing.T) {
	t.Parallel()

	t.Run("validate contract with local policy schema", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "policy-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
policy:
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
		bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
			"openapi.yaml": fmt.Sprintf(openapiTemplate, "policy-svc", "1.0.0"),
		})
		policyDir := filepath.Join(bundlePath, "policy")
		if err := os.MkdirAll(policyDir, 0755); err != nil {
			t.Fatal(err)
		}
		policySchema := `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`
		if err := os.WriteFile(filepath.Join(policyDir, "schema.json"), []byte(policySchema), 0644); err != nil {
			t.Fatal(err)
		}

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err != nil {
			t.Fatalf("validate failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("validate contract with policy ref", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "policy-ref-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: policy-ref-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
policy:
  ref: oci://ghcr.io/acme/platform-policy:1.0.0
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
			"openapi.yaml": fmt.Sprintf(openapiTemplate, "policy-ref-svc", "1.0.0"),
		})

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err != nil {
			t.Fatalf("validate failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("validate contract with config ref", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "config-ref-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: config-ref-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
configuration:
  ref: oci://ghcr.io/acme/platform-config:1.0.0
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
			"openapi.yaml": fmt.Sprintf(openapiTemplate, "config-ref-svc", "1.0.0"),
		})

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err != nil {
			t.Fatalf("validate failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "is valid")
	})

	t.Run("validate rejects empty policy", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "empty-policy-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: empty-policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
policy: {}
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
			"openapi.yaml": fmt.Sprintf(openapiTemplate, "empty-policy-svc", "1.0.0"),
		})

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err == nil {
			t.Fatalf("expected validation to fail for empty policy, output: %s", output)
		}
		assertContains(t, output, "SCHEMA_VIOLATION")
	})

	t.Run("validate rejects missing policy schema file", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "missing-policy-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: missing-policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
policy:
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
		bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
			"openapi.yaml": fmt.Sprintf(openapiTemplate, "missing-policy-svc", "1.0.0"),
		})

		output, err := runCommand(t, nil, "validate", bundlePath)
		if err == nil {
			t.Fatalf("expected validation to fail for missing policy schema, output: %s", output)
		}
		assertContains(t, output, "FILE_NOT_FOUND")
	})

	t.Run("push rejects local policy ref", func(t *testing.T) {
		t.Parallel()
		reg := newTestRegistry(t)
		dir := filepath.Join(t.TempDir(), "local-policy-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: local-policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
policy:
  ref: file://../platform-policy
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
			"openapi.yaml": fmt.Sprintf(openapiTemplate, "local-policy-svc", "1.0.0"),
		})

		_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/local-policy-svc:1.0.0", "-p", bundlePath)
		if err == nil {
			t.Fatal("expected push to reject local policy ref")
		}
	})

	t.Run("push rejects local config ref", func(t *testing.T) {
		t.Parallel()
		reg := newTestRegistry(t)
		dir := filepath.Join(t.TempDir(), "local-config-svc")
		contractYAML := `pactoVersion: "1.0"
service:
  name: local-config-svc
  version: 1.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
configuration:
  ref: file://../platform-config
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
			"openapi.yaml": fmt.Sprintf(openapiTemplate, "local-config-svc", "1.0.0"),
		})

		_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/local-config-svc:1.0.0", "-p", bundlePath)
		if err == nil {
			t.Fatal("expected push to reject local config ref")
		}
	})
}
