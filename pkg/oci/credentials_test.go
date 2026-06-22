package oci_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/trianalab/pacto/v2/pkg/oci"
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

func TestNewKeychain_ReturnsPactoKeychain(t *testing.T) {
	kc := oci.NewKeychain(oci.CredentialOptions{})
	if _, ok := kc.(*oci.PactoKeychain); !ok {
		t.Errorf("NewKeychain() = %T, want *oci.PactoKeychain", kc)
	}
}

func TestPactoKeychain_Candidates_EnvToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	kc := oci.NewKeychain(oci.CredentialOptions{Token: "tok"}).(*oci.PactoKeychain)
	reg, _ := name.NewRegistry("example.com", name.Insecure)

	cands, err := kc.Candidates(reg)
	if err != nil {
		t.Fatalf("Candidates() error: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1, got %v", len(cands), candNames(cands))
	}
	if cands[0].Name != "env token" {
		t.Errorf("Candidates[0].Name = %q, want %q", cands[0].Name, "env token")
	}
}

func TestPactoKeychain_Candidates_EnvUserPass(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	kc := oci.NewKeychain(oci.CredentialOptions{Username: "u", Password: "p"}).(*oci.PactoKeychain)
	reg, _ := name.NewRegistry("example.com", name.Insecure)

	cands, err := kc.Candidates(reg)
	if err != nil {
		t.Fatalf("Candidates() error: %v", err)
	}
	if len(cands) != 1 || cands[0].Name != "env user/pass" {
		t.Errorf("Candidates = %v, want one [env user/pass]", candNames(cands))
	}
}

func TestPactoKeychain_Candidates_OrderingAndNames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	// Write a pacto login entry AND env token so we get two named candidates in order.
	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("u:p"))
	cfg := map[string]any{"auths": map[string]any{"example.com": map[string]any{"auth": encoded}}}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	kc := oci.NewKeychain(oci.CredentialOptions{Token: "tok"}).(*oci.PactoKeychain)
	reg, _ := name.NewRegistry("example.com", name.Insecure)

	cands, err := kc.Candidates(reg)
	if err != nil {
		t.Fatalf("Candidates() error: %v", err)
	}
	got := candNames(cands)
	want := []string{"env token", "pacto login"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("Candidates order = %v, want %v", got, want)
	}
}

func TestPactoKeychain_Candidates_NoneNonAnon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	kc := oci.NewKeychain(oci.CredentialOptions{}).(*oci.PactoKeychain)
	reg, _ := name.NewRegistry("unknown.example.com", name.Insecure)

	cands, err := kc.Candidates(reg)
	if err != nil {
		t.Fatalf("Candidates() error: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("len(Candidates) = %d, want 0, got %v", len(cands), candNames(cands))
	}
}

func TestPactoKeychain_Resolve_FirstNonAnon(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	// pacto login present; token also present → Resolve returns the FIRST (env token).
	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("u:p"))
	cfg := map[string]any{"auths": map[string]any{"example.com": map[string]any{"auth": encoded}}}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(pactoDir, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	kc := oci.NewKeychain(oci.CredentialOptions{Token: "tok"})
	reg, _ := name.NewRegistry("example.com", name.Insecure)
	auth, err := kc.Resolve(reg)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	authCfg, _ := auth.Authorization()
	if authCfg.RegistryToken != "tok" {
		t.Errorf("Resolve() returned %+v, want env token first", authCfg)
	}
}

func candNames(cands []oci.CredSource) []string {
	names := make([]string, len(cands))
	for i, c := range cands {
		names[i] = c.Name
	}
	return names
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
