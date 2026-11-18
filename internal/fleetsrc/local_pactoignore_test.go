package fleetsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalSourceHonoursRootPactoignore(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "services", "payments"), "payments")
	writeBundle(t, filepath.Join(root, "services", "legacy"), "legacy")
	if err := os.WriteFile(filepath.Join(root, ".pactoignore"), []byte("services/legacy/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	col, err := NewLocalSource("", root).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(col.Revisions) != 1 || col.Revisions[0].Bundle.Contract.Service.Name != "payments" {
		t.Fatalf("revisions = %+v, want payments only", col.Revisions)
	}
}

func TestLocalSourceRootPactoignoreError(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "a"), "a")
	if err := os.MkdirAll(filepath.Join(root, ".pactoignore"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLocalSource("", root).Collect(context.Background()); err == nil {
		t.Fatal("an unreadable root .pactoignore must be the source error")
	}
}
