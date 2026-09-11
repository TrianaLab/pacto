package oci_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/oci"
)

// credHome points the credential file at a directory of this test's own and
// returns the path SetCredential/RemoveCredential will write.
func credHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	return filepath.Join(home, ".config", "pacto", "config.json")
}

// readAuths reports what the credential file currently holds.
func readAuths(t *testing.T, path string) map[string]oci.PactoAuth {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg oci.PactoConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Auths
}

// writeAuths installs a credential file holding exactly auths, at mode.
func writeAuths(t *testing.T, path string, mode os.FileMode, auths map[string]oci.PactoAuth) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(oci.PactoConfig{Auths: auths})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestSetCredential_StoresTheDockerConventionEncoding(t *testing.T) {
	path := credHome(t)

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}

	want := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if got := readAuths(t, path)["ghcr.io"].Auth; got != want {
		t.Errorf("stored auth = %q, want %q", got, want)
	}
}

// TestSetCredential_LeavesEveryOtherRegistryAlone is the counterexample for a
// writer that rebuilds the file from a fresh struct: logging in to one registry
// must never log you out of the others.
func TestSetCredential_LeavesEveryOtherRegistryAlone(t *testing.T) {
	path := credHome(t)
	writeAuths(t, path, 0600, map[string]oci.PactoAuth{"docker.io": {Auth: "keep-me"}})

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}

	auths := readAuths(t, path)
	if auths["docker.io"].Auth != "keep-me" {
		t.Errorf("docker.io = %+v, want the entry that was already there", auths["docker.io"])
	}
	if auths["ghcr.io"].Auth == "" {
		t.Error("ghcr.io was not stored")
	}
}

// TestSetCredential_RewritesAnExistingFileTo0600 covers the reason a plain
// WriteFile is not enough: it applies its mode only when it CREATES the file, so
// a config that was already world-readable would stay that way.
func TestSetCredential_RewritesAnExistingFileTo0600(t *testing.T) {
	path := credHome(t)
	writeAuths(t, path, 0644, map[string]oci.PactoAuth{"docker.io": {Auth: "keep-me"}})

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("mode = %04o, want 0600: stored credentials must not stay readable by other users", got)
	}
}

// TestSetCredential_WritesIntoAReadOnlyConfigDir is the counterexample for
// replacing the file instead of rewriting it: a managed or mounted
// ~/.config/pacto is read-only while the config inside it is writable, so the
// entry can be updated but not unlinked.
func TestSetCredential_WritesIntoAReadOnlyConfigDir(t *testing.T) {
	path := credHome(t)
	writeAuths(t, path, 0600, map[string]oci.PactoAuth{"docker.io": {Auth: "keep-me"}})
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	if got := readAuths(t, path)["ghcr.io"].Auth; got == "" {
		t.Error("ghcr.io was not stored")
	}
}

// TestSetCredential_KeepsAConfigSymlink covers the other cost of unlinking:
// os.Remove does not follow a symlink, so a config linked into a dotfiles repo
// would be replaced by a regular file and the managed target would silently stop
// receiving credentials.
func TestSetCredential_KeepsAConfigSymlink(t *testing.T) {
	path := credHome(t)
	target := filepath.Join(t.TempDir(), "dotfiles-config.json")
	writeAuths(t, target, 0600, map[string]oci.PactoAuth{"docker.io": {Auth: "keep-me"}})
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the config is no longer a symlink: the managed target stopped receiving credentials")
	}
	if got := readAuths(t, target)["ghcr.io"].Auth; got == "" {
		t.Error("the symlink target did not receive the credential")
	}
}

// TestSetCredential_FailsClosedOnAnUnreadableConfig is the finding: a transient
// read failure must NOT be treated as "nothing is stored". Continuing from a
// zero-valued config would rewrite the file with one entry and silently delete
// every other stored credential while reporting success.
//
// The config here is WRITE-ONLY, which is the shape that separates the two
// behaviours: the read fails the way an EACCES or EIO does, and the write that
// a fail-open writer would go on to make SUCCEEDS. A directory at the path
// fails both, so it cannot tell a writer that stops at the read apart from one
// that ignores it and is merely blocked later — which is why the second
// assertion reads the file back: fail-open leaves docker.io deleted.
func TestSetCredential_FailsClosedOnAnUnreadableConfig(t *testing.T) {
	path := credHome(t)
	writeAuths(t, path, 0200, map[string]oci.PactoAuth{"docker.io": {Auth: "keep-me"}})
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err == nil {
		t.Fatal("SetCredential reported success over a config it could not read")
	}

	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if got := readAuths(t, path)["docker.io"].Auth; got != "keep-me" {
		t.Errorf("docker.io = %q, want keep-me: a config that could not be read must not be rewritten", got)
	}
}

func TestSetCredential_FailsOnUnparseableConfig(t *testing.T) {
	path := credHome(t)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err == nil {
		t.Fatal("SetCredential reported success over a config it could not parse")
	}
}

func TestSetCredential_NoConfigPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err == nil {
		t.Fatal("expected an error when the config path cannot be determined")
	}
}

func TestSetCredential_ConfigDirCannotBeCreated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := os.Chmod(home, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0700) })

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err == nil {
		t.Fatal("expected an error when the config directory cannot be created")
	}
}

func TestSetCredential_ConfigCannotBeWritten(t *testing.T) {
	path := credHome(t)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	if err := oci.SetCredential("ghcr.io", "user", "pass"); err == nil {
		t.Fatal("expected an error when the config file cannot be written")
	}
}

func TestRemoveCredential_RemovesOnlyTheNamedRegistry(t *testing.T) {
	path := credHome(t)
	writeAuths(t, path, 0600, map[string]oci.PactoAuth{
		"ghcr.io":   {Auth: "drop-me"},
		"docker.io": {Auth: "keep-me"},
	})

	removed, err := oci.RemoveCredential("ghcr.io")
	if err != nil {
		t.Fatalf("RemoveCredential: %v", err)
	}
	if !removed {
		t.Error("removed = false, want true: the entry was there")
	}

	auths := readAuths(t, path)
	if _, still := auths["ghcr.io"]; still {
		t.Error("ghcr.io is still stored")
	}
	if auths["docker.io"].Auth != "keep-me" {
		t.Errorf("docker.io = %+v, want the entry that was already there", auths["docker.io"])
	}
}

func TestRemoveCredential_NoConfigFile(t *testing.T) {
	credHome(t)

	removed, err := oci.RemoveCredential("ghcr.io")
	if err != nil {
		t.Fatalf("RemoveCredential: %v", err)
	}
	if removed {
		t.Error("removed = true, want false: nothing is stored")
	}
}

func TestRemoveCredential_RegistryNotStored(t *testing.T) {
	path := credHome(t)
	writeAuths(t, path, 0600, map[string]oci.PactoAuth{"docker.io": {Auth: "keep-me"}})

	removed, err := oci.RemoveCredential("ghcr.io")
	if err != nil {
		t.Fatalf("RemoveCredential: %v", err)
	}
	if removed {
		t.Error("removed = true, want false: that registry has no entry")
	}
}

// TestRemoveCredential_FailsClosedOnAnUnreadableConfig holds the remover to the
// same rule as the writer: "could not look" is not "nothing stored".
func TestRemoveCredential_FailsClosedOnAnUnreadableConfig(t *testing.T) {
	path := credHome(t)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}

	if _, err := oci.RemoveCredential("ghcr.io"); err == nil {
		t.Fatal("RemoveCredential reported success over a config it could not read")
	}
}

func TestRemoveCredential_NoConfigPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	if _, err := oci.RemoveCredential("ghcr.io"); err == nil {
		t.Fatal("expected an error when the config path cannot be determined")
	}
}

func TestRemoveCredential_ConfigCannotBeRewritten(t *testing.T) {
	path := credHome(t)
	writeAuths(t, path, 0400, map[string]oci.PactoAuth{"ghcr.io": {Auth: "drop-me"}})
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })

	removed, err := oci.RemoveCredential("ghcr.io")
	if err == nil {
		t.Fatal("expected an error when the config file cannot be rewritten")
	}
	if removed {
		t.Error("removed = true after a failed write: nothing was removed")
	}
}
