package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/oci"
)

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
	t.Setenv("HOME", "") // no XDG dir and no home: the config path cannot be named

	cmd := newLogoutCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ghcr.io"})

	if err := cmd.Execute(); err == nil {
		t.Error("expected error when the credential file cannot be located")
	}
}

// TestLogoutCommand_TightensExistingPerms: logout rewrites the credential file
// too, so it owes the same 0600 login enforces. os.WriteFile applies its mode
// only to a file it creates, so a world-readable config stays world-readable
// while still holding every credential the reader did not log out of.
func TestLogoutCommand_TightensExistingPerms(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}
	initial := oci.PactoConfig{Auths: map[string]oci.PactoAuth{
		"ghcr.io":   {Auth: "ghcr-creds"},
		"docker.io": {Auth: "docker-creds"},
	}}
	data, _ := json.MarshalIndent(initial, "", "  ")
	configPath := filepath.Join(pactoDir, "config.json")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := newLogoutCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ghcr.io"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected config perms 0600 after logout, got %o", perm)
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
