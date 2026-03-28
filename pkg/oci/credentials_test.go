package oci_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/trianalab/pacto/pkg/oci"
)

func TestNewKeychain_WithToken(t *testing.T) {
	kc := oci.NewKeychain(oci.CredentialOptions{Token: "my-token"})

	reg, err := name.NewRegistry("example.com", name.Insecure)
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}

	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	cfg, err := auth.Authorization()
	if err != nil {
		t.Fatalf("Authorization() error: %v", err)
	}

	if cfg.RegistryToken != "my-token" {
		t.Errorf("RegistryToken = %q, want %q", cfg.RegistryToken, "my-token")
	}
}

func TestNewKeychain_WithUsernamePassword(t *testing.T) {
	kc := oci.NewKeychain(oci.CredentialOptions{Username: "user", Password: "pass"})

	reg, err := name.NewRegistry("example.com", name.Insecure)
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}

	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	cfg, err := auth.Authorization()
	if err != nil {
		t.Fatalf("Authorization() error: %v", err)
	}

	if cfg.Username != "user" {
		t.Errorf("Username = %q, want %q", cfg.Username, "user")
	}
	if cfg.Password != "pass" {
		t.Errorf("Password = %q, want %q", cfg.Password, "pass")
	}
}

func TestNewKeychain_Default(t *testing.T) {
	kc := oci.NewKeychain(oci.CredentialOptions{})

	// When no credentials are provided, the multi-keychain should resolve
	// to Anonymous for an unknown registry.
	reg, err := name.NewRegistry("unknown.example.com", name.Insecure)
	if err != nil {
		t.Fatalf("NewRegistry() error: %v", err)
	}

	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	cfg, err := auth.Authorization()
	if err != nil {
		t.Fatalf("Authorization() error: %v", err)
	}

	if cfg.Username != "" || cfg.Password != "" || cfg.RegistryToken != "" {
		t.Errorf("expected anonymous auth, got %+v", cfg)
	}
}

func TestPactoConfigPath_Default(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	p, err := oci.PactoConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(dir, ".config", "pacto", "config.json")
	if p != expected {
		t.Errorf("expected %s, got %s", expected, p)
	}
}

func TestPactoConfigPath_XDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	p, err := oci.PactoConfigPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(dir, "pacto", "config.json")
	if p != expected {
		t.Errorf("expected %s, got %s", expected, p)
	}
}

func TestPactoConfigPath_HomeDirError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	old := oci.ExportedUserHomeDirFn()
	defer oci.SetUserHomeDirFn(old)
	oci.SetUserHomeDirFn(func() (string, error) { return "", fmt.Errorf("no home") })

	_, err := oci.PactoConfigPath()
	if err == nil {
		t.Error("expected error when UserHomeDir fails")
	}
}

func TestPactoConfigKeychain_Found(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	// Write pacto config with a registry credential
	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("myuser:mypass"))
	cfg := map[string]any{
		"auths": map[string]any{
			"example.com": map[string]any{"auth": encoded},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Use keychain without explicit creds so pacto config is tried
	kc := oci.NewKeychain(oci.CredentialOptions{})
	reg, _ := name.NewRegistry("example.com", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	authCfg, _ := auth.Authorization()
	if authCfg.Auth != encoded {
		t.Errorf("expected Auth=%s, got %q", encoded, authCfg.Auth)
	}
}

func TestPactoConfigKeychain_NoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	kc := oci.NewKeychain(oci.CredentialOptions{})
	reg, _ := name.NewRegistry("example.com", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	authCfg, _ := auth.Authorization()
	if authCfg.Username != "" || authCfg.Password != "" {
		t.Errorf("expected anonymous auth when no config file, got %+v", authCfg)
	}
}

func TestPactoConfigKeychain_WrongRegistry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	// Write pacto config with creds for a different registry
	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	cfg := map[string]any{
		"auths": map[string]any{
			"other.example.com": map[string]any{"auth": encoded},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	kc := oci.NewKeychain(oci.CredentialOptions{})
	reg, _ := name.NewRegistry("example.com", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	authCfg, _ := auth.Authorization()
	if authCfg.Username != "" || authCfg.Password != "" {
		t.Errorf("expected anonymous auth for wrong registry, got %+v", authCfg)
	}
}

func TestPactoConfigKeychain_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}

	kc := oci.NewKeychain(oci.CredentialOptions{})
	reg, _ := name.NewRegistry("example.com", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	authCfg, _ := auth.Authorization()
	if authCfg.Username != "" || authCfg.Password != "" {
		t.Errorf("expected anonymous auth for invalid JSON, got %+v", authCfg)
	}
}

func TestPactoConfigKeychain_HomeDirError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	old := oci.ExportedUserHomeDirFn()
	defer oci.SetUserHomeDirFn(old)
	oci.SetUserHomeDirFn(func() (string, error) { return "", fmt.Errorf("no home") })

	kc := oci.NewKeychain(oci.CredentialOptions{})
	reg, _ := name.NewRegistry("example.com", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	authCfg, _ := auth.Authorization()
	if authCfg.Username != "" || authCfg.Password != "" {
		t.Errorf("expected anonymous auth when home dir errors, got %+v", authCfg)
	}
}
