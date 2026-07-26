//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

// pushRawBundle pushes a bundle directly via the OCI client, bypassing the CLI's
// push-time validation. This is needed to stage policy bundles that reference
// each other (a cycle) — neither could be pushed first through `pacto push`,
// which validates and would fail with POLICY_REF_UNRESOLVED.
func pushRawBundle(t *testing.T, reg *testRegistry, ref, dir, contractYAML string) {
	t.Helper()
	c, err := contract.Parse(bytes.NewReader([]byte(contractYAML)))
	if err != nil {
		t.Fatalf("parse bundle %q: %v", ref, err)
	}
	bundle := &contract.Bundle{Contract: c, RawYAML: []byte(contractYAML), FS: os.DirFS(dir)}
	if _, err := reg.client.Push(context.Background(), ref, bundle); err != nil {
		t.Fatalf("raw push %q: %v", ref, err)
	}
}

// TestPolicyRefCycleViaOCI exercises recursive policy-ref cycle detection through
// the real OCI resolver chain (bundleResolverAdapter -> Service -> registry),
// which the pkg/validation unit tests (stub resolver) cannot reach. A regression
// in how the resolution chain is threaded could hang or mis-detect a cycle and
// every unit test would still pass.
func TestPolicyRefCycleViaOCI(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	aYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: policy-a
  version: 1.0.0
policies:
  - name: b
    ref: oci://%s/policy-b:1.0.0
`, reg.host)
	bYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: policy-b
  version: 1.0.0
policies:
  - name: a
    ref: oci://%s/policy-a:1.0.0
`, reg.host)
	aDir := writeBundleDir(t, filepath.Join(t.TempDir(), "policy-a"), aYAML, nil)
	bDir := writeBundleDir(t, filepath.Join(t.TempDir(), "policy-b"), bYAML, nil)
	pushRawBundle(t, reg, reg.host+"/policy-a:1.0.0", aDir, aYAML)
	pushRawBundle(t, reg, reg.host+"/policy-b:1.0.0", bDir, bYAML)

	// A consumer referencing policy-a triggers consumer -> A -> B -> A.
	consumerYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: cycle-consumer
  version: 1.0.0
policies:
  - name: a
    ref: oci://%s/policy-a:1.0.0
`, reg.host)
	consumerDir := writeBundleDir(t, filepath.Join(t.TempDir(), "cycle-consumer"), consumerYAML, nil)

	out, err := runCommand(t, reg, "validate", consumerDir)
	if err == nil {
		t.Fatalf("expected POLICY_REF_CYCLE, got success:\n%s", out)
	}
	assertContains(t, out, "POLICY_REF_CYCLE")
}

// TestPolicyRefSharedAcrossSiblings asserts that two sibling policy refs pointing
// at the SAME bundle resolve independently and are NOT a false cycle — the
// end-to-end counterpart to the chain-membership cycle-detection fix.
func TestPolicyRefSharedAcrossSiblings(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// A shared policy bundle with a trivially-satisfied schema (no refs of its own).
	sharedYAML := `pactoVersion: "2.0"
service:
  name: shared-policy
  version: 1.0.0
`
	sharedDir := writeBundleDirWithPolicy(t, filepath.Join(t.TempDir(), "shared-policy"), sharedYAML, `{"type":"object"}`)
	if _, err := runCommand(t, reg, "push", "oci://"+reg.host+"/shared-policy:1.0.0", "-p", sharedDir); err != nil {
		t.Fatalf("push shared policy: %v", err)
	}

	// Consumer references the same shared policy twice (two siblings).
	consumerYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: diamond-consumer
  version: 1.0.0
policies:
  - name: p1
    ref: oci://%s/shared-policy:1.0.0
  - name: p2
    ref: oci://%s/shared-policy:1.0.0
`, reg.host, reg.host)
	consumerDir := writeBundleDir(t, filepath.Join(t.TempDir(), "diamond-consumer"), consumerYAML, nil)

	out, err := runCommand(t, reg, "validate", consumerDir)
	if err != nil {
		t.Fatalf("shared ref across siblings must not be a cycle, got: %v\n%s", err, out)
	}
	assertContains(t, out, "is valid")
	assertNotContains(t, out, "POLICY_REF_CYCLE")
}

func TestPolicyLocalSchema(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "policy-svc")
	contractYAML := `pactoVersion: "2.0"
service:
  name: policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: default
    schema: policy/schema.json
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
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
}

func TestPolicyRefUnresolvable(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "policy-ref-svc")
	contractYAML := `pactoVersion: "2.0"
service:
  name: policy-ref-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: platform
    ref: oci://ghcr.io/acme/platform-policy:1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "policy-ref-svc", "1.0.0"),
	})

	output, err := runCommand(t, nil, "validate", bundlePath)
	if err == nil {
		t.Fatalf("expected validation to fail for unresolvable policy ref, output: %s", output)
	}
	assertContains(t, output, "POLICY_REF_UNRESOLVED")
}

func TestConfigRef(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "config-ref-svc")
	contractYAML := `pactoVersion: "2.0"
service:
  name: config-ref-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
configurations:
  - name: default
    ref: oci://ghcr.io/acme/platform-config:1.0.0
    required: true
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "config-ref-svc", "1.0.0"),
	})

	output, err := runCommand(t, nil, "validate", bundlePath)
	if err != nil {
		t.Fatalf("validate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")
}

func TestPolicyEmptyRejected(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "empty-policy-svc")
	contractYAML := `pactoVersion: "2.0"
service:
  name: empty-policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies: []
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "empty-policy-svc", "1.0.0"),
	})

	output, err := runCommand(t, nil, "validate", bundlePath)
	if err == nil {
		t.Fatalf("expected validation to fail for empty policy, output: %s", output)
	}
	assertContains(t, output, "SCHEMA_VIOLATION")
}

func TestPolicyMissingSchemaFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "missing-policy-svc")
	contractYAML := `pactoVersion: "2.0"
service:
  name: missing-policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: default
    schema: policy/schema.json
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "missing-policy-svc", "1.0.0"),
	})

	output, err := runCommand(t, nil, "validate", bundlePath)
	if err == nil {
		t.Fatalf("expected validation to fail for missing policy schema, output: %s", output)
	}
	assertContains(t, output, "FILE_NOT_FOUND")
}

func TestPushRejectsLocalPolicyRef(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	dir := filepath.Join(t.TempDir(), "local-policy-svc")
	contractYAML := `pactoVersion: "2.0"
service:
  name: local-policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: platform
    ref: file://../platform-policy
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "local-policy-svc", "1.0.0"),
	})

	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/local-policy-svc:1.0.0", "-p", bundlePath)
	if err == nil {
		t.Fatal("expected push to reject local policy ref")
	}
}

func TestPolicyOCIRefSuccess(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push a policy contract to the registry
	policyDir := filepath.Join(t.TempDir(), "platform-policy")
	policyContractYAML := `pactoVersion: "2.0"
service:
  name: platform-policy
  version: 1.0.0
`
	policyBundlePath := writeBundleDirWithPolicy(t, policyDir, policyContractYAML,
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["service"]}`)
	if err := os.MkdirAll(filepath.Join(policyBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/platform-policy:1.0.0", "-p", policyBundlePath)
	if err != nil {
		t.Fatalf("failed to push policy bundle: %v", err)
	}

	// Create a service that references this policy
	svcDir := filepath.Join(t.TempDir(), "svc-with-ref-policy")
	svcYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: svc-with-ref-policy
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: platform
    ref: oci://%s/platform-policy:1.0.0
`, reg.host)
	svcBundlePath := writeBundleDir(t, svcDir, svcYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "svc-with-ref-policy", "1.0.0"),
	})

	output, err := runCommand(t, reg, "validate", svcBundlePath)
	if err != nil {
		t.Fatalf("validate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")
}

func TestPolicyOCIRefViolated(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push a policy that requires a health capability (v2 home of runtime.health)
	policyDir := filepath.Join(t.TempDir(), "strict-policy")
	policyYAML := `pactoVersion: "2.0"
service:
  name: strict-policy
  version: 1.0.0
`
	policyBundlePath := writeBundleDirWithPolicy(t, policyDir, policyYAML,
		`{"type":"object","required":["capabilities"],"properties":{"capabilities":{"type":"array","contains":{"type":"object","required":["type"],"properties":{"type":{"const":"health"}}}}}}`)
	if err := os.MkdirAll(filepath.Join(policyBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/strict-policy:1.0.0", "-p", policyBundlePath)
	if err != nil {
		t.Fatalf("failed to push policy bundle: %v", err)
	}

	// Create a service without a health capability — policy violated
	svcDir := filepath.Join(t.TempDir(), "no-health-svc")
	svcYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: no-health-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: strict
    ref: oci://%s/strict-policy:1.0.0
`, reg.host)
	svcBundlePath := writeBundleDir(t, svcDir, svcYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "no-health-svc", "1.0.0"),
	})

	output, err := runCommand(t, reg, "validate", svcBundlePath)
	if err == nil {
		t.Fatalf("expected validation to fail for policy violation, output: %s", output)
	}
	assertContains(t, output, "POLICY_VIOLATION")
}

func TestPolicyMixedLocalAndRef(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push a ref policy that requires "service"
	policyDir := filepath.Join(t.TempDir(), "ref-policy")
	policyYAML := `pactoVersion: "2.0"
service:
  name: ref-policy
  version: 1.0.0
`
	policyBundlePath := writeBundleDirWithPolicy(t, policyDir, policyYAML,
		`{"type":"object","required":["service"]}`)
	if err := os.MkdirAll(filepath.Join(policyBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/ref-policy:1.0.0", "-p", policyBundlePath)
	if err != nil {
		t.Fatalf("failed to push policy: %v", err)
	}

	// Create service with both local policy (requires state) and ref policy
	svcDir := filepath.Join(t.TempDir(), "mixed-policy-svc")
	svcYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: mixed-policy-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: local
    schema: policy/schema.json
  - name: ref
    ref: oci://%s/ref-policy:1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, reg.host)
	svcBundlePath := writeBundleDirWithPolicy(t, svcDir, svcYAML,
		`{"type":"object","required":["state"]}`)
	if err := os.MkdirAll(filepath.Join(svcBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcBundlePath, "interfaces", "openapi.yaml"),
		[]byte(fmt.Sprintf(openapiTemplate, "mixed-policy-svc", "1.0.0")), 0644); err != nil {
		t.Fatal(err)
	}

	output, err := runCommand(t, reg, "validate", svcBundlePath)
	if err != nil {
		t.Fatalf("validate failed for mixed policies: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")
}

func TestMultiConfigValidation(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "multi-config-svc")
	configSchema := `{"type":"object","properties":{"PORT":{"type":"integer"}}}`
	contractYAML := `pactoVersion: "2.0"
service:
  name: multi-config-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
configurations:
  - name: app
    schema: configuration/schema.json
    required: true
    values:
      PORT: 8080
`
	bundlePath := writeBundleDirRaw(t, dir, contractYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "multi-config-svc", "1.0.0"),
	}, configSchema)

	output, err := runCommand(t, nil, "validate", bundlePath)
	if err != nil {
		t.Fatalf("validate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")
}

func TestPushRejectsRemotePolicyViolation(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push a policy that requires "readiness" (a v2 field a service may omit).
	policyDir := filepath.Join(t.TempDir(), "readiness-policy")
	policyYAML := `pactoVersion: "2.0"
service:
  name: readiness-policy
  version: 1.0.0
`
	policyBundlePath := writeBundleDirWithPolicy(t, policyDir, policyYAML,
		`{"type":"object","required":["readiness"]}`)
	if err := os.MkdirAll(filepath.Join(policyBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/readiness-policy:1.0.0", "-p", policyBundlePath)
	if err != nil {
		t.Fatalf("failed to push policy bundle: %v", err)
	}

	// Create a service without readiness — push should fail
	svcDir := filepath.Join(t.TempDir(), "no-readiness-svc")
	svcYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: no-readiness-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: readiness
    ref: oci://%s/readiness-policy:1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, reg.host)
	svcBundlePath := writeBundleDir(t, svcDir, svcYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "no-readiness-svc", "1.0.0"),
	})

	_, err = runCommand(t, reg, "push", "oci://"+reg.host+"/no-readiness-svc:1.0.0", "-p", svcBundlePath)
	if err == nil {
		t.Fatal("expected push to reject contract that violates remote policy (missing readiness)")
	}
}

func TestPushSucceedsWithRemotePolicyCompliance(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push a policy that requires "service" (which every valid contract has)
	policyDir := filepath.Join(t.TempDir(), "basic-policy")
	policyYAML := `pactoVersion: "2.0"
service:
  name: basic-policy
  version: 1.0.0
`
	policyBundlePath := writeBundleDirWithPolicy(t, policyDir, policyYAML,
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["service"]}`)
	if err := os.MkdirAll(filepath.Join(policyBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/basic-policy:1.0.0", "-p", policyBundlePath)
	if err != nil {
		t.Fatalf("failed to push policy bundle: %v", err)
	}

	// Create a compliant service — push should succeed
	svcDir := filepath.Join(t.TempDir(), "compliant-svc")
	svcYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: compliant-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: basic
    ref: oci://%s/basic-policy:1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, reg.host)
	svcBundlePath := writeBundleDir(t, svcDir, svcYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "compliant-svc", "1.0.0"),
	})

	output, err := runCommand(t, reg, "push", "oci://"+reg.host+"/compliant-svc:1.0.0", "-p", svcBundlePath)
	if err != nil {
		t.Fatalf("push failed for compliant service: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Pushed compliant-svc@1.0.0")
}

func TestPushPolicyProviderWithExplicitPolicies(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push a policy provider that declares policies: [{schema: policy/schema.json}]
	policyDir := filepath.Join(t.TempDir(), "explicit-policy")
	if err := os.MkdirAll(policyDir, 0755); err != nil {
		t.Fatal(err)
	}
	policyYAML := `pactoVersion: "2.0"
service:
  name: explicit-policy
  version: 1.0.0
policies:
  - name: default
    schema: policy/schema.json
`
	policyBundlePath := writeBundleDirWithPolicies(t, policyDir, policyYAML, map[string]string{
		"policy/schema.json": `{"type":"object","required":["service"]}`,
	})
	if err := os.MkdirAll(filepath.Join(policyBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/explicit-policy:1.0.0", "-p", policyBundlePath)
	if err != nil {
		t.Fatalf("failed to push policy bundle: %v", err)
	}

	// Consumer references this policy — should validate with exactly 1 policy (no double enforcement)
	svcDir := filepath.Join(t.TempDir(), "consumer-explicit")
	svcYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: consumer-explicit
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: explicit
    ref: oci://%s/explicit-policy:1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, reg.host)
	svcBundlePath := writeBundleDir(t, svcDir, svcYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "consumer-explicit", "1.0.0"),
	})

	output, err := runCommand(t, reg, "validate", svcBundlePath)
	if err != nil {
		t.Fatalf("validate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")
}

func TestPushPolicyProviderWithCustomSchemaPath(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push a policy provider with custom path and NO policy/schema.json
	policyDir := filepath.Join(t.TempDir(), "custom-path-policy")
	if err := os.MkdirAll(policyDir, 0755); err != nil {
		t.Fatal(err)
	}
	policyYAML := `pactoVersion: "2.0"
service:
  name: custom-path-policy
  version: 1.0.0
policies:
  - name: custom
    schema: policy/custom.json
`
	policyBundlePath := writeBundleDirWithPolicies(t, policyDir, policyYAML, map[string]string{
		"policy/custom.json": `{"type":"object","required":["service"]}`,
	})
	if err := os.MkdirAll(filepath.Join(policyBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/custom-path-policy:1.0.0", "-p", policyBundlePath)
	if err != nil {
		t.Fatalf("failed to push policy bundle: %v", err)
	}

	// Consumer references this policy — should resolve via recursion (no fixed-path needed)
	svcDir := filepath.Join(t.TempDir(), "consumer-custom")
	svcYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: consumer-custom
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: custom-path
    ref: oci://%s/custom-path-policy:1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, reg.host)
	svcBundlePath := writeBundleDir(t, svcDir, svcYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "consumer-custom", "1.0.0"),
	})

	output, err := runCommand(t, reg, "validate", svcBundlePath)
	if err != nil {
		t.Fatalf("validate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")
}

func TestPushPolicyProviderWithMultiplePolicies(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push a policy provider with 2 schemas
	policyDir := filepath.Join(t.TempDir(), "multi-policy")
	if err := os.MkdirAll(policyDir, 0755); err != nil {
		t.Fatal(err)
	}
	// The provider itself must comply with its own policies, so both schemas
	// must be satisfiable by the provider's contract.
	policyYAML := `pactoVersion: "2.0"
service:
  name: multi-policy
  version: 1.0.0
policies:
  - name: service
    schema: policy/service.json
  - name: state
    schema: policy/state.json
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	policyBundlePath := writeBundleDirWithPolicies(t, policyDir, policyYAML, map[string]string{
		"policy/service.json": `{"type":"object","required":["service"]}`,
		"policy/state.json":   `{"type":"object","required":["state"]}`,
	})
	if err := os.MkdirAll(filepath.Join(policyBundlePath, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/multi-policy:1.0.0", "-p", policyBundlePath)
	if err != nil {
		t.Fatalf("failed to push policy bundle: %v", err)
	}

	// Consumer that complies with both policies
	svcDir := filepath.Join(t.TempDir(), "consumer-multi")
	svcYAML := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: consumer-multi
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: multi
    ref: oci://%s/multi-policy:1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, reg.host)
	svcBundlePath := writeBundleDir(t, svcDir, svcYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "consumer-multi", "1.0.0"),
	})

	output, err := runCommand(t, reg, "validate", svcBundlePath)
	if err != nil {
		t.Fatalf("validate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")

	// Consumer that violates state policy (no state) — should fail
	svcDir2 := filepath.Join(t.TempDir(), "consumer-multi-fail")
	svcYAML2 := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: consumer-multi-fail
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
policies:
  - name: multi
    ref: oci://%s/multi-policy:1.0.0
`, reg.host)
	svcBundlePath2 := writeBundleDir(t, svcDir2, svcYAML2, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "consumer-multi-fail", "1.0.0"),
	})

	output2, err := runCommand(t, reg, "validate", svcBundlePath2)
	if err == nil {
		t.Fatalf("expected validation to fail for multi-policy violation, output: %s", output2)
	}
	assertContains(t, output2, "POLICY_VIOLATION")
}

func TestPushRejectsLocalConfigRef(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	dir := filepath.Join(t.TempDir(), "local-config-svc")
	contractYAML := `pactoVersion: "2.0"
service:
  name: local-config-svc
  version: 1.0.0
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
configurations:
  - name: default
    ref: file://../platform-config
    required: true
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	bundlePath := writeBundleDir(t, dir, contractYAML, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "local-config-svc", "1.0.0"),
	})

	_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/local-config-svc:1.0.0", "-p", bundlePath)
	if err == nil {
		t.Fatal("expected push to reject local config ref")
	}
}
