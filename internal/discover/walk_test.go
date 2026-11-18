package discover

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// walkFixture writes files (slash paths) under a fresh root.
func walkFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func relDirs(t *testing.T, root string, dirs []string) []string {
	t.Helper()
	out := []string{}
	for _, d := range dirs {
		rel, err := filepath.Rel(root, d)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func failOnErr(t *testing.T) func(string, error) error {
	return func(p string, err error) error {
		t.Errorf("unexpected walk error at %s: %v", p, err)
		return err
	}
}

func TestWalkRootPactoignoreSkipsFolder(t *testing.T) {
	root := walkFixture(t, map[string]string{
		".pactoignore":                 "services/legacy/\n",
		"services/payments/pacto.yaml": "",
		"services/legacy/pacto.yaml":   "",
	})
	res, err := Walk(context.Background(), root, failOnErr(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := relDirs(t, root, res.Dirs); !reflect.DeepEqual(got, []string{"services/payments"}) {
		t.Errorf("Dirs = %v", got)
	}
	if want := []Skip{{Rel: "services/legacy", Rule: ".pactoignore:1"}}; !reflect.DeepEqual(res.Skipped, want) {
		t.Errorf("Skipped = %v, want %v", res.Skipped, want)
	}
}

func TestWalkNeverAsksAboutPactoYAML(t *testing.T) {
	root := walkFixture(t, map[string]string{
		".pactoignore": "pacto.yaml\n*.yaml\n",
		"pacto.yaml":   "",
		"a/pacto.yaml": "",
		"b/pacto.yaml": "",
	})
	res, err := Walk(context.Background(), root, failOnErr(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := relDirs(t, root, res.Dirs); !reflect.DeepEqual(got, []string{"a", "b", "."}) {
		t.Errorf("Dirs = %v", got)
	}
	if len(res.Skipped) != 0 {
		t.Errorf("Skipped = %v, want none", res.Skipped)
	}
}

func TestWalkKeepsSkipDirRules(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".x")
	files := map[string]string{
		"ok/pacto.yaml":                         "",
		".hidden/pacto.yaml":                    "",
		"node_modules/m/pacto.yaml":             "",
		"vendor/v/pacto.yaml":                   "",
		"l1/l2/l3/l4/l5/l6/l7/l8/l9/pacto.yaml": "",
	}
	for name := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Walk(context.Background(), root, failOnErr(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := relDirs(t, root, res.Dirs); !reflect.DeepEqual(got, []string{"ok"}) {
		t.Errorf("Dirs = %v", got)
	}
	// Hidden, node_modules and vendor stay silent; only the depth cut is listed.
	want := []Skip{{Rel: "l1/l2/l3/l4/l5/l6/l7/l8/l9", Rule: "depth limit 8"}}
	if !reflect.DeepEqual(res.Skipped, want) {
		t.Errorf("Skipped = %v, want %v", res.Skipped, want)
	}
}

func TestWalkListsDepthCut(t *testing.T) {
	root := walkFixture(t, map[string]string{
		"a/pacto.yaml":                 "",
		"d/1/2/3/4/5/6/7/pacto.yaml":   "",
		"d/1/2/3/4/5/6/7/8/pacto.yaml": "",
		"d/1/2/3/4/5/6/7/8/9/x":        "",
	})
	res, err := Walk(context.Background(), root, failOnErr(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := relDirs(t, root, res.Dirs); !reflect.DeepEqual(got, []string{"a", "d/1/2/3/4/5/6/7"}) {
		t.Errorf("Dirs = %v", got)
	}
	want := []Skip{{Rel: "d/1/2/3/4/5/6/7/8", Rule: "depth limit 8"}}
	if !reflect.DeepEqual(res.Skipped, want) {
		t.Errorf("Skipped = %v, want %v (the cut folder only, not its descendants)", res.Skipped, want)
	}
}

func TestWalkCancelled(t *testing.T) {
	root := walkFixture(t, map[string]string{"a/pacto.yaml": ""})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Walk(ctx, root, failOnErr(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWalkRootPactoignoreUnreadable(t *testing.T) {
	root := walkFixture(t, map[string]string{".pactoignore/x": "", "a/pacto.yaml": ""})
	if _, err := Walk(context.Background(), root, failOnErr(t)); err == nil {
		t.Fatal("a root .pactoignore that cannot be read must fail the walk")
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
		{"deep is left to Walk", "/r", "/r/" + deep, "i", false},
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
