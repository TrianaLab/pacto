//go:build unix

package app

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestMigrateKeepsNonRegularFile(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/README": "x"})
	if err := syscall.Mkfifo(filepath.Join(dir, "s", "pacto.yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A FIFO would block a read; the row says so and the walk goes on.
	f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}}))
	if f.Status != MigrateKept || f.Message != "kept: pacto.yaml is not a regular file; edit it by hand" {
		t.Fatalf("%+v", f)
	}
}
