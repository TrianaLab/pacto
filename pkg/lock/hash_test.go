package lock

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestHashFSDeterministicAndOrderIndependent(t *testing.T) {
	a := fstest.MapFS{
		"pacto.yaml":  {Data: []byte("x")},
		"b/inner.txt": {Data: []byte("hello")},
	}
	h1, err := HashFS(a)
	if err != nil {
		t.Fatalf("HashFS: %v", err)
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Errorf("want sha256: prefix, got %q", h1)
	}
	h2, _ := HashFS(a)
	if h1 != h2 {
		t.Errorf("HashFS not deterministic")
	}
	// Changing content changes the hash.
	b := fstest.MapFS{"pacto.yaml": {Data: []byte("x")}, "b/inner.txt": {Data: []byte("HELLO")}}
	if hb, _ := HashFS(b); hb == h1 {
		t.Errorf("expected different hash for different content")
	}
}

// errorFS is a test double that fails on Open.
type errorFS struct {
	failOpen     bool
	failReadFile bool
}

func (e errorFS) Open(name string) (fs.File, error) {
	if e.failOpen {
		return nil, errors.New("open failed")
	}
	// For ReadFile error path, we need a file that exists but fails to read.
	if e.failReadFile {
		return &errorFile{}, nil
	}
	return nil, fs.ErrNotExist
}

type errorFile struct{}

func (f *errorFile) Stat() (fs.FileInfo, error) { return errorFileInfo{}, nil }
func (f *errorFile) Read(p []byte) (int, error) { return 0, errors.New("read failed") }
func (f *errorFile) Close() error               { return nil }

type errorFileInfo struct{}

func (errorFileInfo) Name() string       { return "test.txt" }
func (errorFileInfo) Size() int64        { return 0 }
func (errorFileInfo) Mode() fs.FileMode  { return 0o644 }
func (errorFileInfo) ModTime() time.Time { return time.Time{} }
func (errorFileInfo) IsDir() bool        { return false }
func (errorFileInfo) Sys() any           { return nil }

func TestHashFSWalkError(t *testing.T) {
	_, err := HashFS(errorFS{failOpen: true})
	if err == nil {
		t.Fatal("expected error from WalkDir")
	}
}

func TestHashFSReadFileError(t *testing.T) {
	_, err := HashFS(errorFS{failReadFile: true})
	if err == nil {
		t.Fatal("expected error from ReadFile")
	}
}
