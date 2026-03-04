//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var testPluginDir string

func TestMain(m *testing.M) {
	// Build the test plugin binary and place it on PATH.
	tmpBin, err := os.MkdirTemp("", "pacto-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp bin dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpBin)

	pluginSrc := filepath.Join("testplugin", "main.go")
	pluginBin := filepath.Join(tmpBin, "pacto-plugin-test")

	cmd := exec.Command("go", "build", "-o", pluginBin, pluginSrc)
	cmd.Dir, _ = os.Getwd()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build test plugin: %v\n", err)
		os.Exit(1)
	}

	testPluginDir = tmpBin
	os.Setenv("PATH", tmpBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	os.Exit(m.Run())
}

func TestInitCommand(t *testing.T) {
	t.Run("scaffold structure", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		output, err := runCommand(t, nil, "init", "test-svc")
		if err != nil {
			t.Fatalf("init failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Created test-svc/")

		for _, sub := range []string{"", "interfaces", "configuration"} {
			p := filepath.Join(dir, "test-svc", sub)
			info, err := os.Stat(p)
			if err != nil {
				t.Fatalf("expected %s to exist: %v", p, err)
			}
			if !info.IsDir() {
				t.Errorf("expected %s to be a directory", p)
			}
		}

		pactoPath := filepath.Join(dir, "test-svc", "pacto.yaml")
		if _, err := os.Stat(pactoPath); err != nil {
			t.Fatalf("pacto.yaml was not created: %v", err)
		}
	})

	t.Run("json output", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		output, err := runCommand(t, nil, "--output-format", "json", "init", "json-svc")
		if err != nil {
			t.Fatalf("init failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["Dir"] != "json-svc" {
			t.Errorf("expected Dir=json-svc, got %v", result["Dir"])
		}
	})

	t.Run("error on existing dir", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		os.Mkdir(filepath.Join(dir, "existing-svc"), 0755)

		_, err := runCommand(t, nil, "init", "existing-svc")
		if err == nil {
			t.Fatal("expected init to fail when directory already exists")
		}
	})

	t.Run("error without name", func(t *testing.T) {
		_, err := runCommand(t, nil, "init")
		if err == nil {
			t.Fatal("expected init to fail without a name argument")
		}
	})
}

func TestValidateCommand(t *testing.T) {
	t.Run("local valid", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		// Use init to create a valid contract
		_, err := runCommand(t, nil, "init", "valid-svc")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "valid-svc")
		output, err := runCommand(t, nil, "validate", svcDir)
		if err != nil {
			t.Fatalf("validate failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "is valid")
	})

	t.Run("local invalid", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(brokenContract), 0644); err != nil {
			t.Fatal(err)
		}

		output, err := runCommand(t, nil, "validate", dir)
		if err == nil {
			t.Fatal("expected validate to fail on broken contract")
		}

		assertContains(t, output, "HEALTH_INTERFACE_NOT_FOUND")
	})

	t.Run("json output", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		_, err := runCommand(t, nil, "init", "json-validate")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "json-validate")
		output, err := runCommand(t, nil, "--output-format", "json", "validate", svcDir)
		if err != nil {
			t.Fatalf("validate json failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, `"Valid": true`)
	})

	t.Run("OCI reference validation", func(t *testing.T) {
		reg := newTestRegistry(t)

		// Push a valid contract first
		postgresPath := writePostgresBundle(t)
		_, err := runCommand(t, reg, "push", reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		// Validate via OCI reference
		output, err := runCommand(t, reg, "validate", "oci://"+reg.host+"/postgres-pacto:1.0.0")
		if err != nil {
			t.Fatalf("validate via OCI failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "is valid")
	})
}

func TestPackCommand(t *testing.T) {
	t.Run("archive creation", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		_, err := runCommand(t, nil, "init", "pack-svc")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "pack-svc")
		output, err := runCommand(t, nil, "pack", svcDir)
		if err != nil {
			t.Fatalf("pack failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Packed pack-svc@0.1.0")

		// Check archive exists with default name
		archivePath := filepath.Join(dir, "pack-svc-0.1.0.tar.gz")
		if _, err := os.Stat(archivePath); err != nil {
			t.Fatalf("expected archive at %s: %v", archivePath, err)
		}

		// Verify tar.gz contents
		verifyArchiveContains(t, archivePath, "pacto.yaml")
	})

	t.Run("json output", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		_, err := runCommand(t, nil, "init", "pack-json")
		if err != nil {
			t.Fatalf("init failed: %v", err)
		}

		svcDir := filepath.Join(dir, "pack-json")
		output, err := runCommand(t, nil, "--output-format", "json", "pack", svcDir)
		if err != nil {
			t.Fatalf("pack json failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["Name"] != "pack-json" {
			t.Errorf("expected Name=pack-json, got %v", result["Name"])
		}
	})
}

func TestPushPullLifecycle(t *testing.T) {
	reg := newTestRegistry(t)

	t.Run("roundtrip push and pull", func(t *testing.T) {
		postgresPath := writePostgresBundle(t)
		ref := reg.host + "/postgres-pacto:1.0.0"

		// Push
		pushOutput, err := runCommand(t, reg, "push", ref, "-p", postgresPath)
		if err != nil {
			t.Fatalf("push failed: %v\noutput: %s", err, pushOutput)
		}

		assertContains(t, pushOutput, "Pushed postgres-pacto@1.0.0")
		assertContains(t, pushOutput, "Digest: sha256:")

		// Pull
		pullDir := t.TempDir()
		pullOutput, err := runCommand(t, reg, "pull", ref, "-o", filepath.Join(pullDir, "pulled"))
		if err != nil {
			t.Fatalf("pull failed: %v\noutput: %s", err, pullOutput)
		}

		assertContains(t, pullOutput, "Pulled postgres-pacto@1.0.0")

		// Verify pulled content
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
	})

	t.Run("json output", func(t *testing.T) {
		redisPath := writeRedisV1Bundle(t)
		ref := reg.host + "/redis-pacto:1.0.0"

		pushOutput, err := runCommand(t, reg, "--output-format", "json", "push", ref, "-p", redisPath)
		if err != nil {
			t.Fatalf("push json failed: %v\noutput: %s", err, pushOutput)
		}

		var pushResult map[string]interface{}
		if err := json.Unmarshal([]byte(pushOutput), &pushResult); err != nil {
			t.Fatalf("expected valid JSON push output, got: %s", pushOutput)
		}
		if pushResult["Name"] != "redis-pacto" {
			t.Errorf("expected Name=redis-pacto, got %v", pushResult["Name"])
		}

		pullDir := t.TempDir()
		pullOutput, err := runCommand(t, reg, "--output-format", "json", "pull", ref, "-o", filepath.Join(pullDir, "pulled"))
		if err != nil {
			t.Fatalf("pull json failed: %v\noutput: %s", err, pullOutput)
		}

		var pullResult map[string]interface{}
		if err := json.Unmarshal([]byte(pullOutput), &pullResult); err != nil {
			t.Fatalf("expected valid JSON pull output, got: %s", pullOutput)
		}
		if pullResult["Name"] != "redis-pacto" {
			t.Errorf("expected Name=redis-pacto, got %v", pullResult["Name"])
		}
	})

	t.Run("nonexistent ref error", func(t *testing.T) {
		_, err := runCommand(t, reg, "pull", reg.host+"/nonexistent:latest")
		if err == nil {
			t.Fatal("expected pull to fail for nonexistent reference")
		}
	})

	t.Run("invalid contract push error", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(brokenContract), 0644); err != nil {
			t.Fatal(err)
		}

		_, err := runCommand(t, reg, "push", reg.host+"/broken:1.0.0", "-p", dir)
		if err == nil {
			t.Fatal("expected push to fail for invalid contract")
		}
	})
}

func TestDiffCommand(t *testing.T) {
	t.Run("same contract no changes", func(t *testing.T) {
		postgresPath := writePostgresBundle(t)

		output, err := runCommand(t, nil, "diff", postgresPath, postgresPath)
		if err != nil {
			t.Fatalf("diff failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "No changes detected")
	})

	t.Run("breaking changes across versions", func(t *testing.T) {
		redisV1Path := writeRedisV1Bundle(t)
		redisV2Path := writeRedisV2Bundle(t)

		output, err := runCommand(t, nil, "diff", redisV1Path, redisV2Path)
		// Diff may return err if breaking changes detected
		_ = err

		assertContains(t, output, "Classification:")
		// There should be changes detected (version, scaling, dataCriticality)
		assertNotContains(t, output, "No changes detected")
	})

	t.Run("OCI ref diff", func(t *testing.T) {
		reg := newTestRegistry(t)

		// Push redis v1 and v2
		redisV1Path := writeRedisV1Bundle(t)
		redisV2Path := writeRedisV2Bundle(t)

		_, err := runCommand(t, reg, "push", reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
		if err != nil {
			t.Fatalf("push v1 failed: %v", err)
		}
		_, err = runCommand(t, reg, "push", reg.host+"/redis-pacto:2.0.0", "-p", redisV2Path)
		if err != nil {
			t.Fatalf("push v2 failed: %v", err)
		}

		output, err := runCommand(t, reg, "diff",
			"oci://"+reg.host+"/redis-pacto:1.0.0",
			"oci://"+reg.host+"/redis-pacto:2.0.0")
		_ = err

		assertContains(t, output, "Classification:")
	})

	t.Run("json output", func(t *testing.T) {
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
}

func TestGraphCommand(t *testing.T) {
	t.Run("dependency tree resolution", func(t *testing.T) {
		reg := newTestRegistry(t)

		// Push leaf contracts
		postgresPath := writePostgresBundle(t)
		redisV1Path := writeRedisV1Bundle(t)
		_, err := runCommand(t, reg, "push", reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push postgres failed: %v", err)
		}
		_, err = runCommand(t, reg, "push", reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
		if err != nil {
			t.Fatalf("push redis failed: %v", err)
		}

		// Create and test my-app graph
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
		reg := newTestRegistry(t)

		// Create a contract referencing a nonexistent dep
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		output, err := runCommand(t, reg, "graph", myAppPath)
		// Graph should still succeed, but show error in edges
		if err != nil {
			t.Fatalf("graph failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "my-app@1.0.0")
		// Missing deps should show error
		assertContains(t, output, "error:")
	})

	t.Run("OCI ref graph", func(t *testing.T) {
		reg := newTestRegistry(t)

		// Push all deps first
		postgresPath := writePostgresBundle(t)
		redisV1Path := writeRedisV1Bundle(t)
		_, err := runCommand(t, reg, "push", reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push postgres failed: %v", err)
		}
		_, err = runCommand(t, reg, "push", reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
		if err != nil {
			t.Fatalf("push redis failed: %v", err)
		}

		// Push my-app
		myAppPath := writeMyAppV1Bundle(t, reg.host)
		_, err = runCommand(t, reg, "push", reg.host+"/my-app:1.0.0", "-p", myAppPath)
		if err != nil {
			t.Fatalf("push my-app failed: %v", err)
		}

		// Graph via OCI reference
		output, err := runCommand(t, reg, "graph", "oci://"+reg.host+"/my-app:1.0.0")
		if err != nil {
			t.Fatalf("graph via OCI failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "my-app@1.0.0")
	})

	t.Run("json output", func(t *testing.T) {
		reg := newTestRegistry(t)

		postgresPath := writePostgresBundle(t)
		_, err := runCommand(t, reg, "push", reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		redisV1Path := writeRedisV1Bundle(t)
		_, err = runCommand(t, reg, "push", reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

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
}

func TestExplainCommand(t *testing.T) {
	t.Run("text output", func(t *testing.T) {
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

	t.Run("OCI reference", func(t *testing.T) {
		reg := newTestRegistry(t)

		postgresPath := writePostgresBundle(t)
		_, err := runCommand(t, reg, "push", reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		output, err := runCommand(t, reg, "explain", "oci://"+reg.host+"/postgres-pacto:1.0.0")
		if err != nil {
			t.Fatalf("explain via OCI failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Service: postgres-pacto@1.0.0")
	})
}

func TestGenerateCommand(t *testing.T) {
	t.Run("plugin execution", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		postgresPath := writePostgresBundle(t)
		outDir := filepath.Join(dir, "gen-output")

		output, err := runCommand(t, nil, "generate", "test", postgresPath, "-o", outDir)
		if err != nil {
			t.Fatalf("generate failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Generated 2 file(s) using test")

		// Verify generated files
		deployPath := filepath.Join(outDir, "deployment.yaml")
		if _, err := os.Stat(deployPath); err != nil {
			t.Fatalf("expected deployment.yaml: %v", err)
		}
		servicePath := filepath.Join(outDir, "service.yaml")
		if _, err := os.Stat(servicePath); err != nil {
			t.Fatalf("expected service.yaml: %v", err)
		}

		data, err := os.ReadFile(deployPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "postgres-pacto") {
			t.Error("deployment.yaml doesn't reference the service name")
		}
	})

	t.Run("json output", func(t *testing.T) {
		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		postgresPath := writePostgresBundle(t)
		outDir := filepath.Join(dir, "gen-json-output")

		output, err := runCommand(t, nil, "--output-format", "json", "generate", "test", postgresPath, "-o", outDir)
		if err != nil {
			t.Fatalf("generate json failed: %v\noutput: %s", err, output)
		}

		var result map[string]interface{}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("expected valid JSON output, got: %s", output)
		}
		if result["plugin"] != "test" {
			t.Errorf("expected plugin=test, got %v", result["plugin"])
		}
		if result["filesCount"] != float64(2) {
			t.Errorf("expected filesCount=2, got %v", result["filesCount"])
		}
	})

	t.Run("nonexistent plugin error", func(t *testing.T) {
		postgresPath := writePostgresBundle(t)

		_, err := runCommand(t, nil, "generate", "nonexistent-plugin", postgresPath)
		if err == nil {
			t.Fatal("expected generate to fail for nonexistent plugin")
		}
	})

	t.Run("OCI reference", func(t *testing.T) {
		reg := newTestRegistry(t)

		postgresPath := writePostgresBundle(t)
		_, err := runCommand(t, reg, "push", reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		orig, _ := os.Getwd()
		dir := t.TempDir()
		os.Chdir(dir)
		defer os.Chdir(orig)

		outDir := filepath.Join(dir, "gen-oci-output")
		output, err := runCommand(t, reg, "generate", "test", "oci://"+reg.host+"/postgres-pacto:1.0.0", "-o", outDir)
		if err != nil {
			t.Fatalf("generate via OCI failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Generated 2 file(s)")
	})
}

func TestLoginCommand(t *testing.T) {
	t.Run("pacto config write", func(t *testing.T) {
		// Override HOME to avoid writing to real config
		origHome := os.Getenv("HOME")
		tmpHome := t.TempDir()
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", origHome)
		origXDG := os.Getenv("XDG_CONFIG_HOME")
		os.Setenv("XDG_CONFIG_HOME", "")
		defer os.Setenv("XDG_CONFIG_HOME", origXDG)

		output, err := runCommand(t, nil, "login", "registry.example.com", "-u", "testuser", "-p", "testpass")
		if err != nil {
			t.Fatalf("login failed: %v\noutput: %s", err, output)
		}

		assertContains(t, output, "Login succeeded for registry.example.com")

		// Verify pacto config was written
		configPath := filepath.Join(tmpHome, ".config", "pacto", "config.json")
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("expected pacto config at %s: %v", configPath, err)
		}

		var cfg map[string]interface{}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("invalid pacto config JSON: %v", err)
		}

		auths, ok := cfg["auths"].(map[string]interface{})
		if !ok {
			t.Fatal("expected auths in pacto config")
		}

		regAuth, ok := auths["registry.example.com"].(map[string]interface{})
		if !ok {
			t.Fatal("expected registry.example.com in auths")
		}

		authStr, ok := regAuth["auth"].(string)
		if !ok {
			t.Fatal("expected auth string")
		}

		decoded, err := base64.StdEncoding.DecodeString(authStr)
		if err != nil {
			t.Fatalf("invalid base64 auth: %v", err)
		}
		if string(decoded) != "testuser:testpass" {
			t.Errorf("expected testuser:testpass, got %s", string(decoded))
		}
	})

	t.Run("config merge", func(t *testing.T) {
		origHome := os.Getenv("HOME")
		tmpHome := t.TempDir()
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", origHome)
		origXDG := os.Getenv("XDG_CONFIG_HOME")
		os.Setenv("XDG_CONFIG_HOME", "")
		defer os.Setenv("XDG_CONFIG_HOME", origXDG)

		// Login to first registry
		_, err := runCommand(t, nil, "login", "registry1.example.com", "-u", "user1", "-p", "pass1")
		if err != nil {
			t.Fatalf("login to registry1 failed: %v", err)
		}

		// Login to second registry
		_, err = runCommand(t, nil, "login", "registry2.example.com", "-u", "user2", "-p", "pass2")
		if err != nil {
			t.Fatalf("login to registry2 failed: %v", err)
		}

		// Verify both registries are in config
		configPath := filepath.Join(tmpHome, ".config", "pacto", "config.json")
		data, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}

		var cfg map[string]interface{}
		json.Unmarshal(data, &cfg)
		auths := cfg["auths"].(map[string]interface{})

		if _, ok := auths["registry1.example.com"]; !ok {
			t.Error("expected registry1.example.com in auths after merge")
		}
		if _, ok := auths["registry2.example.com"]; !ok {
			t.Error("expected registry2.example.com in auths after merge")
		}
	})

	t.Run("missing username error", func(t *testing.T) {
		_, err := runCommand(t, nil, "login", "registry.example.com")
		if err == nil {
			t.Fatal("expected login to fail without username")
		}
	})
}

func TestVersionCommand(t *testing.T) {
	output, err := runCommand(t, nil, "version")
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}

	assertContains(t, output, "test-e2e")
}

func TestFullLifecycle(t *testing.T) {
	reg := newTestRegistry(t)

	orig, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(orig)

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
	ref := reg.host + "/lifecycle-svc:0.1.0"
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

	// 8. Diff (same contract - no changes)
	output, err = runCommand(t, reg, "diff", svcDir, pullDir)
	if err != nil {
		t.Fatalf("diff failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "No changes detected")

	// 9. Graph (no deps in default init scaffold)
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

func TestGraphWithDependencies(t *testing.T) {
	reg := newTestRegistry(t)

	// Push leaf contracts
	postgresPath := writePostgresBundle(t)
	redisV1Path := writeRedisV1Bundle(t)
	redisV2Path := writeRedisV2Bundle(t)

	_, err := runCommand(t, reg, "push", reg.host+"/postgres-pacto:1.0.0", "-p", postgresPath)
	if err != nil {
		t.Fatalf("push postgres failed: %v", err)
	}
	_, err = runCommand(t, reg, "push", reg.host+"/redis-pacto:1.0.0", "-p", redisV1Path)
	if err != nil {
		t.Fatalf("push redis v1 failed: %v", err)
	}
	_, err = runCommand(t, reg, "push", reg.host+"/redis-pacto:2.0.0", "-p", redisV2Path)
	if err != nil {
		t.Fatalf("push redis v2 failed: %v", err)
	}

	t.Run("multi-level resolution", func(t *testing.T) {
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
		// Create a contract that depends on both redis v1 and v2 (via different paths)
		// To detect conflicts, we need two different versions of the same service in the graph.
		// We'll create a contract that directly references both redis versions.
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

configuration:
  schema: configuration/schema.json

dependencies:
  - ref: %s/redis-pacto:1.0.0
    required: true
    compatibility: "^1.0.0"
  - ref: %s/redis-pacto:2.0.0
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
		// Should detect version conflict for redis-pacto
		assertContains(t, output, "Conflicts")
		assertContains(t, output, "redis-pacto")
	})

	t.Run("json output with full tree", func(t *testing.T) {
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
}
