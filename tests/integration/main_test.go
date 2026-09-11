//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// Disable the per-command auto update check: it spawns a network call in
	// PersistentPreRunE that makes the parallel suite slow and network-dependent.
	// No test asserts on the update notification; the `update` command itself is
	// exercised directly (TestUpdateCommand), unaffected by this.
	os.Setenv("PACTO_NO_UPDATE_CHECK", "1")

	// Give the suite its own OCI cache. The CLI wraps every registry client in a
	// CachedStore, exactly as cmd/pacto does, so without this the tests would
	// read and write the developer's real ~/.cache/pacto/oci. Set before m.Run,
	// so it is already in place for every t.Parallel() test.
	tmpCache, err := os.MkdirTemp("", "pacto-e2e-cache-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp cache dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpCache)
	os.Setenv("XDG_CACHE_HOME", tmpCache)

	os.Exit(m.Run())
}
