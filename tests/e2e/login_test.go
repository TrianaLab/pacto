//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func setXDGConfigHome(t *testing.T, dir string) {
	t.Helper()
	xdgMu.Lock()
	orig := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", dir)
	t.Cleanup(func() {
		os.Setenv("XDG_CONFIG_HOME", orig)
		xdgMu.Unlock()
	})
}

var xdgMu sync.Mutex

func TestLoginCommand(t *testing.T) {
	t.Parallel()

	t.Run("pacto config write", testLoginConfigWrite)
	t.Run("config merge", testLoginConfigMerge)
	t.Run("missing username error", testLoginMissingUsername)
	t.Run("json output", testLoginJSON)
	t.Run("help flag", testLoginHelp)
}

func testLoginConfigWrite(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	setXDGConfigHome(t, tmpDir)

	output, err := runCommand(t, nil, "login", "registry.example.com", "-u", "testuser", "-p", "testpass")
	if err != nil {
		t.Fatalf("login failed: %v\noutput: %s", err, output)
	}

	assertContains(t, output, "Login succeeded for registry.example.com")

	configPath := filepath.Join(tmpDir, "pacto", "config.json")
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
}

func testLoginConfigMerge(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	setXDGConfigHome(t, tmpDir)

	_, err := runCommand(t, nil, "login", "registry1.example.com", "-u", "user1", "-p", "pass1")
	if err != nil {
		t.Fatalf("login to registry1 failed: %v", err)
	}
	_, err = runCommand(t, nil, "login", "registry2.example.com", "-u", "user2", "-p", "pass2")
	if err != nil {
		t.Fatalf("login to registry2 failed: %v", err)
	}

	configPath := filepath.Join(tmpDir, "pacto", "config.json")
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
}

func testLoginMissingUsername(t *testing.T) {
	t.Parallel()
	_, err := runCommand(t, nil, "login", "registry.example.com")
	if err == nil {
		t.Fatal("expected login to fail without username")
	}
}

func testLoginJSON(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	setXDGConfigHome(t, tmpDir)

	output, err := runCommand(t, nil, "--output-format", "json", "login", "registry.example.com", "-u", "user", "-p", "pass")
	if err != nil {
		t.Fatalf("login json failed: %v\noutput: %s", err, output)
	}
	assertContains(t, output, "registry.example.com")
}

func testLoginHelp(t *testing.T) {
	t.Parallel()
	output, err := runCommand(t, nil, "login", "--help")
	if err != nil {
		t.Fatalf("login --help failed: %v", err)
	}
	assertContains(t, output, "login")
	assertContains(t, output, "Usage")
}
