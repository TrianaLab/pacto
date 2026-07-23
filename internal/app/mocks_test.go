package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/internal/testutil"
)

// Type aliases for shared mocks so existing tests in this package compile
// without changes to type names.
type mockBundleStore = testutil.MockBundleStore
type mockPluginRunner = testutil.MockPluginRunner

// Delegate to shared fixtures.
var (
	testBundle      = testutil.TestBundle
	writeTestBundle = testutil.WriteTestBundle
)

// writeInvalidBundle creates a bundle directory with an invalid contract
// and returns the directory path.
func writeInvalidBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pactoPath := filepath.Join(dir, "pacto.yaml")
	// Valid YAML, valid parse, but fails structural validation (interface missing required Type)
	content := []byte(`pactoVersion: "2.0"
service:
  name: bad-svc
  version: "1.0.0"
interfaces:
  - name: api
    ref: openapi.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
	if err := os.WriteFile(pactoPath, content, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeUnparseableBundle creates a bundle directory with unparseable YAML
// and returns the directory path.
func writeUnparseableBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pactoPath := filepath.Join(dir, "pacto.yaml")
	if err := os.WriteFile(pactoPath, []byte(`{{{invalid`), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// errFS is an fs.FS that returns errors from Open.
type errFS struct{}

func (errFS) Open(name string) (fs.File, error) {
	return nil, fmt.Errorf("errFS: cannot open %s", name)
}

// readFailFS wraps an fs.FS so that WalkDir works but ReadFile always fails.
type readFailFS struct {
	fstest.MapFS
}

func (r readFailFS) ReadFile(string) ([]byte, error) {
	return nil, fmt.Errorf("readFailFS: read denied")
}

// errBundleStore returns a mock store that returns errors.
var errBundleStore = testutil.ErrBundleStore
