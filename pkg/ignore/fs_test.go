package ignore

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func sampleFS() fstest.MapFS {
	return fstest.MapFS{
		"pacto.yaml":     {Data: []byte("x")},
		".pactoignore":   {Data: []byte("*.tmp\nbuild/\n")},
		"main.go":        {Data: []byte("x")},
		"scratch.tmp":    {Data: []byte("x")},
		"build/out.bin":  {Data: []byte("x")},
		"docs/README.md": {Data: []byte("x")},
	}
}

func TestLoadAndFS(t *testing.T) {
	base := sampleFS()
	m, err := Load(base)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ffs := FS(base, m)

	// Walk and collect surviving files.
	got := map[string]bool{}
	err = fs.WalkDir(ffs, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got[p] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := map[string]bool{"pacto.yaml": true, "main.go": true, "docs/README.md": true}
	for p := range want {
		if !got[p] {
			t.Errorf("expected %q kept", p)
		}
	}
	for _, p := range []string{"scratch.tmp", "build/out.bin", ".pactoignore"} {
		if got[p] {
			t.Errorf("expected %q ignored", p)
		}
	}

	// Open of an ignored file fails as not-exist.
	if _, err := ffs.Open("scratch.tmp"); err == nil {
		t.Errorf("expected Open(scratch.tmp) to fail")
	}
	// Open of a kept file works.
	if _, err := ffs.Open("main.go"); err != nil {
		t.Errorf("Open(main.go): %v", err)
	}
}

func TestLoadNoIgnoreFile(t *testing.T) {
	base := fstest.MapFS{"pacto.yaml": {Data: []byte("x")}, "x.tmp": {Data: []byte("x")}}
	m, err := Load(base)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// No .pactoignore -> defaults only; x.tmp survives (not a default).
	if m.Ignored("x.tmp", false) {
		t.Errorf("x.tmp should survive with defaults only")
	}
}

// Additional coverage tests

func TestFSOpenRoot(t *testing.T) {
	base := sampleFS()
	m, _ := Load(base)
	ffs := FS(base, m)

	// Open(".") should work without checking ignored status.
	f, err := ffs.Open(".")
	if err != nil {
		t.Errorf("Open(.): %v", err)
	}
	if f != nil {
		f.Close()
	}
}

func TestReadDirError(t *testing.T) {
	base := sampleFS()
	m, _ := Load(base)
	ffs := FS(base, m)

	// ReadDir on non-existent directory should return error.
	_, err := fs.ReadDir(ffs, "nonexistent")
	if err == nil {
		t.Errorf("expected ReadDir(nonexistent) to fail")
	}
}

// Custom FS that returns a custom error for .pactoignore
type errorFS struct {
	err error
}

func (e errorFS) Open(name string) (fs.File, error) {
	if name == IgnoreFileName {
		return nil, e.err
	}
	return nil, fs.ErrNotExist
}

func TestLoadNonNotExistError(t *testing.T) {
	base := errorFS{err: fs.ErrPermission}
	_, err := Load(base)
	if err == nil {
		t.Errorf("expected Load to fail with permission error")
	}
	// fs.ReadFile wraps errors, so use errors.Is
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("expected ErrPermission, got %v", err)
	}
}
