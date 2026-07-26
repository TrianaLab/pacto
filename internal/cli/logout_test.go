package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/oci"
)

func TestRemovePactoConfig_RemovesExistingEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Write initial config with two registries
	initial := oci.PactoConfig{
		Auths: map[string]oci.PactoAuth{
			"ghcr.io":   {Auth: "ghcr-creds"},
			"docker.io": {Auth: "docker-creds"},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Remove ghcr.io
	removed, err := removePactoConfig("ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	// Read and verify
	result, err := os.ReadFile(filepath.Join(pactoDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	var cfg oci.PactoConfig
	if err := json.Unmarshal(result, &cfg); err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.Auths["ghcr.io"]; ok {
		t.Error("expected ghcr.io to be removed")
	}
	if _, ok := cfg.Auths["docker.io"]; !ok {
		t.Error("expected docker.io to still exist")
	}
}

func TestRemovePactoConfig_NoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	removed, err := removePactoConfig("ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("expected removed=false when no file exists")
	}
}

func TestRemovePactoConfig_NoEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Write config with different registry
	initial := oci.PactoConfig{
		Auths: map[string]oci.PactoAuth{
			"docker.io": {Auth: "docker-creds"},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	removed, err := removePactoConfig("ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("expected removed=false when registry not found")
	}
}

func TestRemovePactoConfig_EmptyAuths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Write config with empty auths
	initial := oci.PactoConfig{
		Auths: make(map[string]oci.PactoAuth),
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	removed, err := removePactoConfig("ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("expected removed=false when auths is empty")
	}
}

func TestRemovePactoConfig_NilAuths(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Write config with nil auths (JSON: {})
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}

	removed, err := removePactoConfig("ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("expected removed=false when auths is nil")
	}
}

func TestRemovePactoConfig_InvalidJSON(t *testing.T) {
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

	_, err := removePactoConfig("ghcr.io")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestRemovePactoConfig_PactoConfigPathError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	old := oci.ExportedUserHomeDirFn()
	oci.SetUserHomeDirFn(func() (string, error) { return "", fmt.Errorf("no home") })
	defer oci.SetUserHomeDirFn(old)

	_, err := removePactoConfig("ghcr.io")
	if err == nil {
		t.Error("expected error when PactoConfigPath fails")
	}
}

func TestRemovePactoConfig_ReadFileError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	// Create a directory instead of a file to trigger read error
	configPath := filepath.Join(pactoDir, "config.json")
	if err := os.Mkdir(configPath, 0700); err != nil {
		t.Fatal(err)
	}

	_, err := removePactoConfig("ghcr.io")
	if err == nil {
		t.Error("expected error when ReadFile fails")
	}
}

func TestRemovePactoConfig_MarshalError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	initial := oci.PactoConfig{
		Auths: map[string]oci.PactoAuth{
			"ghcr.io": {Auth: "ghcr-creds"},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	old := jsonMarshalIndentFn
	jsonMarshalIndentFn = func(any, string, string) ([]byte, error) {
		return nil, fmt.Errorf("marshal failed")
	}
	defer func() { jsonMarshalIndentFn = old }()

	_, err := removePactoConfig("ghcr.io")
	if err == nil {
		t.Error("expected error when MarshalIndent fails")
	}
}

func TestRemovePactoConfig_WriteFileError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	initial := oci.PactoConfig{
		Auths: map[string]oci.PactoAuth{
			"ghcr.io": {Auth: "ghcr-creds"},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	configPath := filepath.Join(pactoDir, "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Make the config file itself read-only
	if err := os.Chmod(configPath, 0444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(configPath, 0644) })

	_, err := removePactoConfig("ghcr.io")
	if err == nil {
		t.Error("expected error when WriteFile fails")
	}
}

func TestRemovePactoConfig_XDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	pactoDir := filepath.Join(dir, "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	initial := oci.PactoConfig{
		Auths: map[string]oci.PactoAuth{
			"ghcr.io": {Auth: "ghcr-creds"},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	removed, err := removePactoConfig("ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	// Verify the entry was removed
	result, err := os.ReadFile(filepath.Join(pactoDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	var cfg oci.PactoConfig
	if err := json.Unmarshal(result, &cfg); err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.Auths["ghcr.io"]; ok {
		t.Error("expected ghcr.io to be removed")
	}
}

func TestLogoutCommand_RemovesEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}

	initial := oci.PactoConfig{
		Auths: map[string]oci.PactoAuth{
			"ghcr.io": {Auth: "ghcr-creds"},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	cmd := newLogoutCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ghcr.io"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(out.Bytes(), []byte("Logout succeeded for ghcr.io")) {
		t.Errorf("expected success message, got: %s", out.String())
	}
}

func TestLogoutCommand_NoEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	cmd := newLogoutCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ghcr.io"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Contains(out.Bytes(), []byte("No stored credentials for ghcr.io")) {
		t.Errorf("expected no-credentials message, got: %s", out.String())
	}
}

func TestLogoutCommand_Error(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	old := oci.ExportedUserHomeDirFn()
	oci.SetUserHomeDirFn(func() (string, error) { return "", fmt.Errorf("no home") })
	defer oci.SetUserHomeDirFn(old)

	cmd := newLogoutCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ghcr.io"})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error when removePactoConfig fails")
	}
}

func TestLogoutCommand_ExactArgs(t *testing.T) {
	cmd := newLogoutCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	// No args
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with no args")
	}

	// Too many args
	cmd.SetArgs([]string{"ghcr.io", "extra"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error with too many args")
	}
}
