//go:build e2e

package e2e

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

	os.Exit(m.Run())
}
