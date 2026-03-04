package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/internal/oci"
)

func TestEncodeAuth(t *testing.T) {
	encoded := encodeAuth("user", "pass")
	// base64("user:pass") = "dXNlcjpwYXNz"
	if encoded != "dXNlcjpwYXNz" {
		t.Errorf("expected dXNlcjpwYXNz, got %s", encoded)
	}
}

func TestWritePactoConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := writePactoConfig("ghcr.io", "user", "pass"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configPath := filepath.Join(dir, ".config", "pacto", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected config file: %v", err)
	}

	var cfg pactoLoginConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	auth, ok := cfg.Auths["ghcr.io"]
	if !ok {
		t.Fatal("expected ghcr.io auth entry")
	}
	if auth.Auth != encodeAuth("user", "pass") {
		t.Errorf("expected encoded auth, got %s", auth.Auth)
	}
}

func TestWritePactoConfig_MergeExisting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Write initial config with one registry
	initial := pactoLoginConfig{
		Auths: map[string]pactoLoginAuth{
			"docker.io": {Auth: "existing"},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Add a second registry
	if err := writePactoConfig("ghcr.io", "user", "pass"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read and verify both exist
	result, err := os.ReadFile(filepath.Join(pactoDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	var cfg pactoLoginConfig
	if err := json.Unmarshal(result, &cfg); err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.Auths["docker.io"]; !ok {
		t.Error("expected docker.io to still exist")
	}
	if _, ok := cfg.Auths["ghcr.io"]; !ok {
		t.Error("expected ghcr.io to be added")
	}
}

func TestWritePactoConfig_ReadOnlyHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")
	// Make home read-only so config dir cannot be created
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	err := writePactoConfig("ghcr.io", "user", "pass")
	if err == nil {
		t.Error("expected error when home directory is read-only")
	}
}

func TestWritePactoConfig_WriteFileError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	// Pre-create config dir, then make it read-only.
	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pactoDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pactoDir, 0755) })

	err := writePactoConfig("ghcr.io", "user", "pass")
	if err == nil {
		t.Error("expected error when WriteFile fails on read-only config dir")
	}
}

func TestWritePactoConfig_InvalidExistingJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Write invalid JSON
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}

	err := writePactoConfig("ghcr.io", "user", "pass")
	if err == nil {
		t.Error("expected error for invalid existing JSON")
	}
}

func TestLoginCommand_ReadPasswordError(t *testing.T) {
	old := readPasswordFn
	readPasswordFn = func(int) ([]byte, error) { return nil, fmt.Errorf("read failed") }
	defer func() { readPasswordFn = old }()

	cmd := newLoginCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ghcr.io", "--username", "user"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when ReadPassword fails")
	}
}

func TestLoginCommand_ReadPasswordSuccess(t *testing.T) {
	old := readPasswordFn
	readPasswordFn = func(int) ([]byte, error) { return []byte("secret"), nil }
	defer func() { readPasswordFn = old }()

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	cmd := newLoginCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ghcr.io", "--username", "user"})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWritePactoConfig_PactoConfigPathError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	old := oci.ExportedUserHomeDirFn()
	oci.SetUserHomeDirFn(func() (string, error) { return "", fmt.Errorf("no home") })
	defer oci.SetUserHomeDirFn(old)

	err := writePactoConfig("ghcr.io", "user", "pass")
	if err == nil {
		t.Error("expected error when PactoConfigPath fails")
	}
}

func TestReadPasswordFn_Default(t *testing.T) {
	// Exercise the default readPasswordFn (which wraps term.ReadPassword).
	// Using an invalid fd ensures it returns an error without needing a real terminal.
	_, err := readPasswordFn(-1)
	if err == nil {
		t.Error("expected error from readPasswordFn with invalid fd")
	}
}

func TestWritePactoConfig_MarshalError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	old := jsonMarshalIndentFn
	jsonMarshalIndentFn = func(any, string, string) ([]byte, error) {
		return nil, fmt.Errorf("marshal failed")
	}
	defer func() { jsonMarshalIndentFn = old }()

	err := writePactoConfig("ghcr.io", "user", "pass")
	if err == nil {
		t.Error("expected error when MarshalIndent fails")
	}
}

func TestWritePactoConfig_XDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := writePactoConfig("ghcr.io", "user", "pass"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configPath := filepath.Join(dir, "pacto", "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config file at %s: %v", configPath, err)
	}
}
