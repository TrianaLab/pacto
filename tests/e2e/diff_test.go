//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffCommand(t *testing.T) {
	t.Parallel()

	t.Run("same contract no changes", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "diff", postgresPath, postgresPath)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "No changes detected")
	})

	t.Run("breaking changes across versions", func(t *testing.T) {
		t.Parallel()
		redisV1Path := writeRedisV1Bundle(t)
		redisV2Path := writeRedisV2Bundle(t)

		output, _ := runCommand(t, nil, "diff", redisV1Path, redisV2Path)

		assertContains(t, output, "Classification:")
		assertNotContains(t, output, "No changes detected")
	})

	t.Run("OCI ref diff", func(t *testing.T) {
		t.Parallel()
		reg := newTestRegistry(t)

		redisV1Path := writeRedisV1Bundle(t)
		redisV2Path := writeRedisV2Bundle(t)

		_, err := runCommand(t, reg, "push", "oci://"+reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
		if err != nil {
			t.Fatalf("push v1 failed: %v", err)
		}
		_, err = runCommand(t, reg, "push", "oci://"+reg.host+"/redis-pacto:2.0.0", "-p", redisV2Path)
		if err != nil {
			t.Fatalf("push v2 failed: %v", err)
		}

		output, _ := runCommand(t, reg, "diff",
			"oci://"+reg.host+"/redis-pacto:1.0.0",
			"oci://"+reg.host+"/redis-pacto:2.0.0")

		assertContains(t, output, "Classification:")
	})

	t.Run("json output", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "--output-format", "json", "diff", postgresPath, postgresPath)
		if err != nil {
			t.Fatalf("diff json failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["classification"] == nil {
			t.Error("expected classification in JSON output")
		}
	})

	t.Run("markdown output", func(t *testing.T) {
		t.Parallel()
		redisV1Path := writeRedisV1Bundle(t)
		redisV2Path := writeRedisV2Bundle(t)

		output, _ := runCommand(t, nil, "--output-format", "markdown", "diff", redisV1Path, redisV2Path)

		assertContains(t, output, "Classification")
	})

	t.Run("verbose flag", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)

		_, err := runCommand(t, nil, "--verbose", "diff", postgresPath, postgresPath)
		if err != nil {
			t.Fatalf("diff --verbose failed: %v", err)
		}
	})

	t.Run("help flag", func(t *testing.T) {
		t.Parallel()
		output, err := runCommand(t, nil, "diff", "--help")
		if err != nil {
			t.Fatalf("diff --help failed: %v", err)
		}
		assertContains(t, output, "diff")
		assertContains(t, output, "Usage")
	})
}

func TestDiffOpenAPIDeep(t *testing.T) {
	t.Parallel()

	v1Path := writeOpenAPIDiffBundleV1(t)
	v2Path := writeOpenAPIDiffBundleV2(t)

	t.Run("identical OpenAPI specs", func(t *testing.T) {
		t.Parallel()
		v1 := writeOpenAPIDiffBundleV1(t)

		output, err := runCommand(t, nil, "diff", v1, v1)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "No changes detected")
	})

	t.Run("method removed is breaking", func(t *testing.T) {
		t.Parallel()
		output, _ := runCommand(t, nil, "diff", v1Path, v2Path)

		assertContains(t, output, "methods[DELETE]")
		assertContains(t, output, "removed")
		assertContains(t, output, "BREAKING")
	})

	t.Run("method added is non-breaking", func(t *testing.T) {
		t.Parallel()
		output, _ := runCommand(t, nil, "diff", v1Path, v2Path)

		assertContains(t, output, "methods[POST]")
		assertContains(t, output, "added")
	})

	t.Run("response modified detected", func(t *testing.T) {
		t.Parallel()
		output, _ := runCommand(t, nil, "diff", v1Path, v2Path)

		assertContains(t, output, "responses[200]")
		assertContains(t, output, "modified")
	})

	t.Run("response added detected", func(t *testing.T) {
		t.Parallel()
		output, _ := runCommand(t, nil, "diff", v1Path, v2Path)

		assertContains(t, output, "responses[404]")
	})

	t.Run("parameter removed detected", func(t *testing.T) {
		t.Parallel()
		output, _ := runCommand(t, nil, "diff", v1Path, v2Path)

		assertContains(t, output, "parameters[sort:query]")
		assertContains(t, output, "parameters[filter:query]")
	})

	t.Run("path added detected", func(t *testing.T) {
		t.Parallel()
		output, _ := runCommand(t, nil, "diff", v1Path, v2Path)

		assertContains(t, output, "openapi.paths[/orders]")
	})

	t.Run("json output shows deep changes", func(t *testing.T) {
		t.Parallel()
		output, _ := runCommand(t, nil, "--output-format", "json", "diff", v1Path, v2Path)

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}

		changes, ok := result["changes"].([]interface{})
		if !ok || len(changes) == 0 {
			t.Fatal("expected non-empty changes array")
		}

		hasMethodChange := false
		hasResponseChange := false
		hasParameterChange := false
		for _, c := range changes {
			change, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			path, _ := change["path"].(string)
			if strings.Contains(path, "methods[") {
				hasMethodChange = true
			}
			if strings.Contains(path, "responses[") {
				hasResponseChange = true
			}
			if strings.Contains(path, "parameters[") {
				hasParameterChange = true
			}
		}
		if !hasMethodChange {
			t.Error("expected method-level changes in JSON output")
		}
		if !hasResponseChange {
			t.Error("expected response-level changes in JSON output")
		}
		if !hasParameterChange {
			t.Error("expected parameter-level changes in JSON output")
		}
	})
}

func TestDiffGraphChanges(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push leaf dependencies (setup before parallel subtests)
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

	t.Run("version change in dependency", func(t *testing.T) {
		t.Parallel()
		oldPath := writeMyAppV1Bundle(t, reg.host)
		newPath := writeMyAppV2Bundle(t, reg.host)

		output, _ := runCommand(t, reg, "diff", oldPath, newPath)

		assertContains(t, output, "Dependency graph changes:")
		assertContains(t, output, "redis-pacto")
		assertContains(t, output, "1.0.0 → 2.0.0")
	})

	t.Run("removed dependency", func(t *testing.T) {
		t.Parallel()
		oldPath := writeMyAppV1Bundle(t, reg.host)
		newPath := writeMyAppV2Bundle(t, reg.host)

		output, _ := runCommand(t, reg, "diff", oldPath, newPath)

		assertContains(t, output, "postgres-pacto")
		assertContains(t, output, "-1.0.0")
	})

	t.Run("added dependency", func(t *testing.T) {
		t.Parallel()
		oldPath := writeMyAppV2Bundle(t, reg.host)
		newPath := writeMyAppV1Bundle(t, reg.host)

		output, _ := runCommand(t, reg, "diff", oldPath, newPath)

		assertContains(t, output, "Dependency graph changes:")
		assertContains(t, output, "postgres-pacto")
		assertContains(t, output, "+1.0.0")
	})

	t.Run("no graph changes for identical contracts", func(t *testing.T) {
		t.Parallel()
		path := writeMyAppV1Bundle(t, reg.host)

		output, err := runCommand(t, reg, "diff", path, path)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertNotContains(t, output, "Dependency graph changes:")
	})

	t.Run("tree formatting with connectors", func(t *testing.T) {
		t.Parallel()
		oldPath := writeMyAppV1Bundle(t, reg.host)
		newPath := writeMyAppV2Bundle(t, reg.host)

		output, _ := runCommand(t, reg, "diff", oldPath, newPath)

		hasTree := strings.Contains(output, "├─") || strings.Contains(output, "└─")
		if !hasTree {
			t.Errorf("expected tree connectors in output:\n%s", output)
		}
	})

	t.Run("json output includes graph diff", func(t *testing.T) {
		t.Parallel()
		oldPath := writeMyAppV1Bundle(t, reg.host)
		newPath := writeMyAppV2Bundle(t, reg.host)

		output, _ := runCommand(t, reg, "--output-format", "json", "diff", oldPath, newPath)

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		gd, ok := result["graphDiff"].(map[string]interface{})
		if !ok {
			t.Fatal("expected graphDiff object in JSON output")
		}
		changes, ok := gd["changes"].([]interface{})
		if !ok || len(changes) == 0 {
			t.Error("expected non-empty changes array in graphDiff")
		}
	})
}

func TestSBOMDiff(t *testing.T) {
	t.Parallel()

	t.Run("diff detects SBOM package changes", func(t *testing.T) {
		t.Parallel()
		v1Path := writeBundleWithSBOM(t, "1.0.0", "sbom.spdx.json", sbomSPDXV1)
		v2Path := writeBundleWithSBOM(t, "2.0.0", "sbom.spdx.json", sbomSPDXV2)

		output, _ := runCommand(t, nil, "diff", v1Path, v2Path)

		assertContains(t, output, "SBOM changes")
		assertContains(t, output, "lib-a")
		assertContains(t, output, "lib-b")
		assertContains(t, output, "lib-c")
	})

	t.Run("diff with no SBOMs shows no SBOM section", func(t *testing.T) {
		t.Parallel()
		postgresPath := writePostgresBundle(t)
		output, err := runCommand(t, nil, "diff", postgresPath, postgresPath)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertNotContains(t, output, "SBOM changes")
	})

	t.Run("diff with identical SBOMs shows no SBOM section", func(t *testing.T) {
		t.Parallel()
		v1a := writeBundleWithSBOM(t, "1.0.0", "sbom.spdx.json", sbomSPDXV1)
		v1b := writeBundleWithSBOM(t, "1.0.0", "sbom.spdx.json", sbomSPDXV1)

		output, err := runCommand(t, nil, "diff", v1a, v1b)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertNotContains(t, output, "SBOM changes")
	})

	t.Run("json output includes sbomDiff", func(t *testing.T) {
		t.Parallel()
		v1Path := writeBundleWithSBOM(t, "1.0.0", "sbom.spdx.json", sbomSPDXV1)
		v2Path := writeBundleWithSBOM(t, "2.0.0", "sbom.spdx.json", sbomSPDXV2)

		output, _ := runCommand(t, nil, "--output-format", "json", "diff", v1Path, v2Path)

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		sbomDiff, ok := result["sbomDiff"].(map[string]interface{})
		if !ok {
			t.Fatal("expected sbomDiff object in JSON output")
		}
		changes, ok := sbomDiff["changes"].([]interface{})
		if !ok || len(changes) == 0 {
			t.Error("expected non-empty SBOM changes array")
		}
	})

	t.Run("markdown output includes SBOM section", func(t *testing.T) {
		t.Parallel()
		v1Path := writeBundleWithSBOM(t, "1.0.0", "sbom.spdx.json", sbomSPDXV1)
		v2Path := writeBundleWithSBOM(t, "2.0.0", "sbom.spdx.json", sbomSPDXV2)

		output, _ := runCommand(t, nil, "--output-format", "markdown", "diff", v1Path, v2Path)

		assertContains(t, output, "### SBOM Changes")
		assertContains(t, output, "| Package | Type |")
	})

	t.Run("CycloneDX format support", func(t *testing.T) {
		t.Parallel()
		v1Path := writeBundleWithSBOM(t, "1.0.0", "bom.cdx.json", sbomCDXV1)
		v2Path := writeBundleWithSBOM(t, "2.0.0", "sbom.spdx.json", sbomSPDXV1)

		output, _ := runCommand(t, nil, "diff", v1Path, v2Path)

		assertContains(t, output, "SBOM changes")
		assertContains(t, output, "lib-x")
	})

	t.Run("one bundle with SBOM one without", func(t *testing.T) {
		t.Parallel()
		withSBOM := writeBundleWithSBOM(t, "1.0.0", "sbom.spdx.json", sbomSPDXV1)
		withoutSBOM := writePostgresBundle(t)

		output, _ := runCommand(t, nil, "diff", withSBOM, withoutSBOM)

		assertContains(t, output, "SBOM changes")
		assertContains(t, output, "lib-a")
		assertContains(t, output, "lib-b")
	})

	t.Run("pack and pull roundtrip preserves SBOM", func(t *testing.T) {
		t.Parallel()
		reg := newTestRegistry(t)
		bundlePath := writeBundleWithSBOM(t, "1.0.0", "sbom.spdx.json", sbomSPDXV1)

		ref := "oci://" + reg.host + "/sbom-svc:1.0.0"
		_, err := runCommand(t, reg, "push", ref, "-p", bundlePath)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		pullDir := t.TempDir()
		_, err = runCommand(t, reg, "pull", ref, "-o", filepath.Join(pullDir, "pulled"))
		if err != nil {
			t.Fatalf("pull failed: %v", err)
		}

		output, err := runCommand(t, nil, "diff", bundlePath, filepath.Join(pullDir, "pulled"))
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertNotContains(t, output, "SBOM changes")
	})
}

func TestDiffOverrides(t *testing.T) {
	t.Parallel()

	t.Run("new-set changes version for diff", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		output, err := runCommand(t, nil, "diff", bundlePath, bundlePath)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "No changes detected")

		output, _ = runCommand(t, nil, "diff", bundlePath, bundlePath,
			"--new-set", "service.version=2.0.0")
		assertContains(t, output, "service.version")
		assertNotContains(t, output, "No changes detected")
	})

	t.Run("old-set and new-set independently", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		output, _ := runCommand(t, nil, "diff", bundlePath, bundlePath,
			"--old-set", "service.version=1.0.0",
			"--new-set", "service.version=3.0.0")
		assertContains(t, output, "service.version")
		assertNotContains(t, output, "No changes detected")
	})

	t.Run("old-values and new-values files", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)
		tmpDir := t.TempDir()

		oldVals := writeValuesFile(t, tmpDir, "old.yaml", "service:\n  owner: team/old\n")
		newVals := writeValuesFile(t, tmpDir, "new.yaml", "service:\n  owner: team/new\n")

		output, _ := runCommand(t, nil, "diff", bundlePath, bundlePath,
			"--old-values", oldVals,
			"--new-values", newVals)
		assertContains(t, output, "service.owner")
		assertNotContains(t, output, "No changes detected")
	})

	t.Run("new-set takes precedence over new-values in diff", func(t *testing.T) {
		t.Parallel()
		bundlePath := writeOverrideBundle(t)

		newVals := writeValuesFile(t, t.TempDir(), "new.yaml", "service:\n  version: \"2.0.0\"\n")

		output, err := runCommand(t, nil, "diff", bundlePath, bundlePath,
			"--new-values", newVals,
			"--new-set", "service.version=1.0.0")
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}
		assertContains(t, output, "No changes detected")
	})
}

func TestDiffPolicyChanges(t *testing.T) {
	t.Parallel()

	dir1 := filepath.Join(t.TempDir(), "svc-v1")
	contract1 := `pactoVersion: "1.0"
service:
  name: diff-policy-svc
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
	dir2 := filepath.Join(t.TempDir(), "svc-v2")
	contract2 := `pactoVersion: "1.0"
service:
  name: diff-policy-svc
  version: 2.0.0
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
policies:
  - name: acme
    ref: oci://ghcr.io/acme/policy:1.0.0
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
	path1 := writeBundleDir(t, dir1, contract1, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "diff-policy-svc", "1.0.0"),
	})
	path2 := writeBundleDir(t, dir2, contract2, map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, "diff-policy-svc", "2.0.0"),
	})

	output, _ := runCommand(t, nil, "diff", path1, path2)
	assertContains(t, output, "policies")
}
