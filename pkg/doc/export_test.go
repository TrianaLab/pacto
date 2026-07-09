package doc

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v2/pkg/dashboard"
)

func TestBuildStaticExport(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{
			Name:    "svc",
			Version: "1.0.0",
		},
	}
	files, err := BuildStaticExport(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	idx, ok := files["index.html"]
	if !ok {
		t.Fatal("index.html missing from export")
	}
	if !bytes.Contains(idx, []byte("__PACTO_STATIC__")) {
		t.Error("index.html not injected")
	}
	if !bytes.Contains(idx, []byte(`\"svc\"`)) && !bytes.Contains(idx, []byte(`"svc"`)) {
		t.Error("service name not embedded")
	}
	if _, ok := files["assets"]; ok {
		t.Error("assets should be individual files, not a dir key")
	}
}

func TestBuildStaticExport_HappyPath(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{
			Name:    "test-service",
			Version: "2.0.0",
		},
	}
	g := &dashboard.DependencyGraph{
		Root: &dashboard.GraphNode{
			Name:    "test-service",
			Version: "2.0.0",
		},
	}

	mockFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head></head><body><div id="app"></div></body></html>`),
			Mode: 0644,
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte(`console.log("app");`),
			Mode: 0644,
		},
		"assets/style.css": &fstest.MapFile{
			Data: []byte(`body { margin: 0; }`),
			Mode: 0644,
		},
	}

	files, err := buildStaticExport(mockFS, d, g)
	if err != nil {
		t.Fatalf("buildStaticExport failed: %v", err)
	}

	// Check index.html exists and was modified
	idx, ok := files["index.html"]
	if !ok {
		t.Fatal("index.html missing from export")
	}
	if !bytes.Contains(idx, []byte("__PACTO_STATIC__")) {
		t.Error("index.html missing __PACTO_STATIC__ injection")
	}
	if !bytes.Contains(idx, []byte("test-service")) {
		t.Error("service name not embedded in snapshot")
	}
	if !bytes.Contains(idx, []byte("</head>")) {
		t.Error("index.html structure corrupted")
	}

	// Verify script is before </head>
	headIdx := bytes.Index(idx, []byte("</head>"))
	scriptIdx := bytes.Index(idx, []byte("__PACTO_STATIC__"))
	if scriptIdx == -1 || headIdx == -1 || scriptIdx > headIdx {
		t.Error("script not injected before </head>")
	}

	// Check assets are copied
	if _, ok := files["assets/app.js"]; !ok {
		t.Error("assets/app.js not copied")
	}
	if _, ok := files["assets/style.css"]; !ok {
		t.Error("assets/style.css not copied")
	}
	if string(files["assets/app.js"]) != `console.log("app");` {
		t.Error("assets/app.js content wrong")
	}
}

func TestBuildStaticExport_MissingIndexHTML(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
	}

	mockFS := fstest.MapFS{
		"assets/app.js": &fstest.MapFile{
			Data: []byte(`console.log("app");`),
			Mode: 0644,
		},
	}

	_, err := buildStaticExport(mockFS, d, nil)
	if err == nil {
		t.Fatal("expected error for missing index.html, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("index.html")) {
		t.Errorf("error should mention index.html, got: %v", err)
	}
}

func TestBuildStaticExport_FSReadError(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
	}

	errFS := &errorFS{err: errors.New("read failed")}

	_, err := buildStaticExport(errFS, d, nil)
	if err == nil {
		t.Fatal("expected error from broken FS, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("read failed")) {
		t.Errorf("error should wrap fs error, got: %v", err)
	}
}

func TestBuildStaticExport_ReadFileError(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
	}

	// FS with a file that exists but cannot be read
	errFS := &readErrorFS{}

	_, err := buildStaticExport(errFS, d, nil)
	if err == nil {
		t.Fatal("expected error from file read failure, got nil")
	}
	// The error wraps the underlying read error
	if !bytes.Contains([]byte(err.Error()), []byte("reading embedded ui")) {
		t.Errorf("error should wrap reading error, got: %v", err)
	}
}

// errorFS is a minimal fs.FS that fails on Open.
type errorFS struct {
	err error
}

func (e *errorFS) Open(name string) (fs.File, error) {
	return nil, e.err
}

func (e *errorFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return nil, e.err
}

// readErrorFS is an fs.FS that walks successfully but fails on ReadFile.
type readErrorFS struct{}

func (r *readErrorFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &errorDir{}, nil
	}
	return nil, errors.New("file read error")
}

type errorDir struct{}

func (d *errorDir) Stat() (fs.FileInfo, error)           { return &errorFileInfo{}, nil }
func (d *errorDir) Read([]byte) (int, error)             { return 0, errors.New("is a directory") }
func (d *errorDir) Close() error                         { return nil }
func (d *errorDir) ReadDir(n int) ([]fs.DirEntry, error) { return []fs.DirEntry{&errorDirEntry{}}, nil }

type errorDirEntry struct{}

func (e *errorDirEntry) Name() string               { return "test.txt" }
func (e *errorDirEntry) IsDir() bool                { return false }
func (e *errorDirEntry) Type() fs.FileMode          { return 0 }
func (e *errorDirEntry) Info() (fs.FileInfo, error) { return &errorFileInfo{}, nil }

type errorFileInfo struct{}

func (f *errorFileInfo) Name() string       { return "test.txt" }
func (f *errorFileInfo) Size() int64        { return 0 }
func (f *errorFileInfo) Mode() fs.FileMode  { return 0644 }
func (f *errorFileInfo) ModTime() time.Time { return time.Time{} }
func (f *errorFileInfo) IsDir() bool        { return false }
func (f *errorFileInfo) Sys() any           { return nil }
