//go:build unix

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCIPlanNotesUnreadableFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	root := t.TempDir()
	writeCIFile(t, root, "a/pacto.yaml", "service:\n  name: a\n")
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	p, _, err := NewService(nil, nil).CIPlan(context.Background(), CIOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || len(p.Notes) != 1 || !strings.HasPrefix(p.Notes[0], "unreadable, not planned: ") {
		t.Fatalf("rows %+v, notes %v", p.Rows, p.Notes)
	}
}
