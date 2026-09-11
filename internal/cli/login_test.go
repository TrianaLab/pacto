package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/oci"
)

func TestLoginCommand_ReadPasswordError(t *testing.T) {
	old := readPasswordFn
	readPasswordFn = func(int) ([]byte, error) { return nil, fmt.Errorf("read failed") }
	t.Cleanup(func() { readPasswordFn = old })

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
	t.Cleanup(func() { readPasswordFn = old })

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

// TestLoginCommand_KeepsOtherRegistriesWhenTheConfigCannotBeRead is the
// counterexample for the data-loss bug: a login that cannot READ the existing
// credential file must not go on to write one from scratch, because that
// rewrite deletes every other registry's stored credential while the command
// reports success.
//
// The config is write-only (0200), which is the mode that separates the two
// behaviours: the read fails the way a transient EACCES does, and the write a
// fail-open login would then make SUCCEEDS.
func TestLoginCommand_KeepsOtherRegistriesWhenTheConfigCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", "")

	pactoDir := filepath.Join(dir, ".config", "pacto")
	if err := os.MkdirAll(pactoDir, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(pactoDir, "config.json")
	data, _ := json.Marshal(oci.PactoConfig{Auths: map[string]oci.PactoAuth{
		"docker.io": {Auth: "keep-me"},
	}})
	if err := os.WriteFile(configPath, data, 0200); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0200); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(configPath, 0600) })

	cmd := newLoginCommand()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"ghcr.io", "--username", "user", "--password", "pass"})

	if err := cmd.Execute(); err == nil {
		t.Error("login reported success over a credential file it could not read")
	}
	if strings.Contains(out.String(), "Login succeeded") {
		t.Errorf("login printed success, got: %s", out.String())
	}

	if err := os.Chmod(configPath, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg oci.PactoConfig
	if err := json.Unmarshal(result, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Auths["docker.io"].Auth; got != "keep-me" {
		t.Errorf("docker.io = %q, want keep-me: a config that could not be read must not be rewritten", got)
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
