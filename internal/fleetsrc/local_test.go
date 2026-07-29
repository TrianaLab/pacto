package fleetsrc

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// writeBundle writes a minimal parseable pacto.yaml for service name at dir.
func writeBundle(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: " + name + "\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSource_IDAndKind(t *testing.T) {
	if got := NewLocalSource("", "/x").ID(); got != "local" {
		t.Errorf("default id = %q, want local", got)
	}
	if got := NewLocalSource("custom", "/x").ID(); got != "custom" {
		t.Errorf("custom id = %q, want custom", got)
	}
	if got := NewLocalSource("", "/x").Kind(); got != "local" {
		t.Errorf("kind = %q, want local", got)
	}
}

func TestLocalSource_Collect(t *testing.T) {
	root := t.TempDir()

	// Two valid bundles: one at a top-level subdir, one nested deeper.
	writeBundle(t, filepath.Join(root, "svc-a"), "svc-a")
	writeBundle(t, filepath.Join(root, "nested", "svc-b"), "svc-b")

	// Skipped: hidden dir, node_modules, vendor.
	writeBundle(t, filepath.Join(root, ".git", "svc-x"), "svc-x")
	writeBundle(t, filepath.Join(root, "node_modules", "svc-y"), "svc-y")
	writeBundle(t, filepath.Join(root, "vendor", "svc-v"), "svc-v")

	// Skipped: deeper than maxScanDepth (rel depth >= 8).
	deep := filepath.Join(root, "l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9")
	writeBundle(t, deep, "svc-deep")

	// Skipped: pacto.yaml that fails contract.Parse (unsupported version).
	brokenDir := filepath.Join(root, "broken")
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "pacto.yaml"),
		[]byte("pactoVersion: \"9.9\"\nservice:\n  name: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A non-pacto file at the root exercises the name filter.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	col, err := NewLocalSource("", root).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	names := map[string]bool{}
	for _, rev := range col.Revisions {
		if rev.Bundle == nil || rev.Bundle.Contract == nil {
			t.Fatal("revision missing bundle/contract")
		}
		names[rev.Bundle.Contract.Service.Name] = true
		if rev.Digest == "" {
			t.Errorf("revision %s: empty Digest", rev.Bundle.Contract.Service.Name)
		}
		if rev.FetchedAt == nil {
			t.Errorf("revision %s: nil FetchedAt", rev.Bundle.Contract.Service.Name)
		}
	}
	if len(col.Revisions) != 2 || !names["svc-a"] || !names["svc-b"] {
		t.Errorf("got revisions %v, want exactly svc-a and svc-b", names)
	}
}

func TestLocalSource_MissingRoot(t *testing.T) {
	_, err := NewLocalSource("", filepath.Join(t.TempDir(), "does-not-exist")).Collect(context.Background())
	if err == nil {
		t.Fatal("expected error for missing root")
	}
}

func TestLocalSource_ContextCancelled(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "svc-a"), "svc-a")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewLocalSource("", root).Collect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// fakeDirEntry is a synthetic fs.DirEntry for exercising skipDir directly.
type fakeDirEntry struct{ name string }

func (f fakeDirEntry) Name() string             { return f.name }
func (fakeDirEntry) IsDir() bool                { return true }
func (fakeDirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

func TestSkipDir(t *testing.T) {
	sep := string(filepath.Separator)
	deep := "a" + sep + "b" + sep + "c" + sep + "d" + sep + "e" + sep + "f" + sep + "g" + sep + "h" + sep + "i"
	cases := []struct {
		name string
		root string
		p    string
		dir  string
		want bool
	}{
		{"root itself", "/r", "/r", "r", false},
		{"hidden", "/r", "/r/.git", ".git", true},
		{"node_modules", "/r", "/r/node_modules", "node_modules", true},
		{"vendor", "/r", "/r/vendor", "vendor", true},
		{"rel error (abs root, rel path)", "/r", "relative/child", "child", true},
		{"too deep", "/r", "/r/" + deep, "i", true},
		{"shallow ok", "/r", "/r/sub", "sub", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipDir(tc.root, tc.p, fakeDirEntry{name: tc.dir}); got != tc.want {
				t.Errorf("skipDir(%q,%q,%q) = %v, want %v", tc.root, tc.p, tc.dir, got, tc.want)
			}
		})
	}
}

func TestLoadRevision_ReadError(t *testing.T) {
	// A directory with no pacto.yaml → ReadFile fails → not ok.
	if _, ok := loadRevision(t.TempDir()); ok {
		t.Error("expected ok=false when pacto.yaml is absent")
	}
}

func TestLoadRevision_ParseError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("::: not yaml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadRevision(dir); ok {
		t.Error("expected ok=false for unparseable contract")
	}
}
