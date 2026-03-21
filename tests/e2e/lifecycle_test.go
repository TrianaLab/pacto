//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFullLifecycle(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	dir := t.TempDir()
	inDir(t, dir)

	// 1. Init
	output, err := runCommand(t, reg, "init", "lifecycle-svc")
	if err != nil {
		t.Fatalf("init failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Created lifecycle-svc/")

	svcDir := filepath.Join(dir, "lifecycle-svc")

	// 2. Validate
	output, err = runCommand(t, reg, "validate", svcDir)
	if err != nil {
		t.Fatalf("validate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")

	// 3. Pack
	output, err = runCommand(t, reg, "pack", svcDir)
	if err != nil {
		t.Fatalf("pack failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Packed lifecycle-svc@0.1.0")

	// 4. Push
	ref := "oci://" + reg.host + "/lifecycle-svc:0.1.0"
	output, err = runCommand(t, reg, "push", ref, "-p", svcDir)
	if err != nil {
		t.Fatalf("push failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Pushed lifecycle-svc@0.1.0")

	// 5. Pull
	pullDir := filepath.Join(dir, "pulled-lifecycle")
	output, err = runCommand(t, reg, "pull", ref, "-o", pullDir)
	if err != nil {
		t.Fatalf("pull failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Pulled lifecycle-svc@0.1.0")

	// 6. Validate pulled contract
	output, err = runCommand(t, reg, "validate", pullDir)
	if err != nil {
		t.Fatalf("validate pulled failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")

	// 7. Explain
	output, err = runCommand(t, reg, "explain", svcDir)
	if err != nil {
		t.Fatalf("explain failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Service: lifecycle-svc@0.1.0")

	// 8. Diff (same contract)
	output, err = runCommand(t, reg, "diff", svcDir, pullDir)
	if err != nil {
		t.Fatalf("diff failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "No changes detected")

	// 9. Graph
	output, err = runCommand(t, reg, "graph", svcDir)
	if err != nil {
		t.Fatalf("graph failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "lifecycle-svc@0.1.0")

	// 10. Generate
	genDir := filepath.Join(dir, "gen-lifecycle")
	output, err = runCommand(t, reg, "generate", "test", svcDir, "-o", genDir)
	if err != nil {
		t.Fatalf("generate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Generated 2 file(s)")
}

// TestUserStoryPublishAndConsume tests a common user story:
// Team A publishes a service contract, Team B consumes it as a dependency.
func TestUserStoryPublishAndConsume(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Team A publishes postgres service
	postgresPath := writePostgresBundle(t)
	ref := "oci://" + reg.host + "/postgres-pacto:1.0.0"
	_, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Team B pulls and validates the dependency
	pullDir := t.TempDir()
	_, err = runCommand(t, reg, "pull", ref, "-o", filepath.Join(pullDir, "postgres"))
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	output, err := runCommand(t, nil, "validate", filepath.Join(pullDir, "postgres"))
	if err != nil {
		t.Fatalf("validate pulled contract failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "is valid")

	// Team B generates code from the dependency
	genDir := filepath.Join(t.TempDir(), "gen")
	inDir(t, t.TempDir())
	output, err = runCommand(t, nil, "generate", "test", filepath.Join(pullDir, "postgres"), "-o", genDir)
	if err != nil {
		t.Fatalf("generate failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "Generated 2 file(s)")

	if _, err := os.Stat(filepath.Join(genDir, "deployment.yaml")); err != nil {
		t.Fatalf("expected generated deployment.yaml: %v", err)
	}
}

// TestUserStoryBreakingChangeDetection tests detecting breaking changes
// when upgrading a dependency version.
func TestUserStoryBreakingChangeDetection(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Publish two versions of a service
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

	// Compare versions via OCI refs
	output, _ := runCommand(t, reg, "diff",
		"oci://"+reg.host+"/redis-pacto:1.0.0",
		"oci://"+reg.host+"/redis-pacto:2.0.0")

	// Should detect changes between versions
	assertContains(t, output, "Classification:")
	assertNotContains(t, output, "No changes detected")
}

// TestUserStoryMultiServiceDependencyGraph tests resolving a multi-service
// dependency graph with transitive dependencies.
func TestUserStoryMultiServiceDependencyGraph(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Publish leaf dependencies
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

	// Build and inspect a service with dependencies
	myAppPath := writeMyAppV1Bundle(t, reg.host)

	// Graph should resolve all dependencies
	output, err := runCommand(t, reg, "graph", myAppPath)
	if err != nil {
		t.Fatalf("graph failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "my-app@1.0.0")
	assertContains(t, output, "postgres-pacto@1.0.0")
	assertContains(t, output, "redis-pacto@1.0.0")

	// Explain should show dependency info
	output, err = runCommand(t, nil, "explain", myAppPath)
	if err != nil {
		t.Fatalf("explain failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "my-app")
	assertContains(t, output, "Dependencies")
}

// TestUserStoryVersionUpgradeImpact tests evaluating the impact of upgrading
// a dependency by diffing the old and new app contracts.
func TestUserStoryVersionUpgradeImpact(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Setup registry with all deps
	postgresPath := writePostgresBundle(t)
	redisV1Path := writeRedisV1Bundle(t)
	redisV2Path := writeRedisV2Bundle(t)

	_, _ = runCommand(t, reg, "push", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
	_, _ = runCommand(t, reg, "push", "oci://"+reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
	_, _ = runCommand(t, reg, "push", "oci://"+reg.host+"/redis-pacto:2.0.0", "-p", redisV2Path)

	// App v1 uses postgres + redis v1
	// App v2 drops postgres, upgrades redis to v2
	oldApp := writeMyAppV1Bundle(t, reg.host)
	newApp := writeMyAppV2Bundle(t, reg.host)

	// Diff should show dependency changes
	output, _ := runCommand(t, reg, "diff", oldApp, newApp)
	assertContains(t, output, "Dependency graph changes:")
	assertContains(t, output, "redis-pacto")
	assertContains(t, output, "postgres-pacto")

	// JSON format should also capture graph diff
	jsonOutput, _ := runCommand(t, reg, "--output-format", "json", "diff", oldApp, newApp)
	assertContains(t, jsonOutput, "graphDiff")
}
