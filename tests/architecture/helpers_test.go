package architecture

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoRoot resolves the repository root relative to this file, so no gate
// depends on the working directory the test runs from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// readRepoFile reads a file named by its path parts under the repository root.
func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	raw, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
