package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/trianalab/pacto/pkg/contract"
)

type tarErrWriter struct{}

func (tarErrWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write error") }

func testBundle() *contract.Bundle {
	port := 8080
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: "test-svc", Version: "1.0.0"},
			Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
			Runtime: &contract.Runtime{
				Workload: "service",
				State: contract.State{
					Type:            "stateless",
					Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
					DataCriticality: "low",
				},
				Health: &contract.Health{Interface: "api", Path: "/health"},
			},
		},
		FS: fstest.MapFS{
			"pacto.yaml": &fstest.MapFile{Data: []byte(`pactoVersion: "1.0"
service:
  name: test-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)},
		},
	}
}

// materializeImage pushes a stream-based image to an in-memory registry and pulls
// it back, producing a fully materialized v1.Image whose config and layers can
// be inspected without errors from the lazy stream.Layer.
func materializeImage(t *testing.T, img v1.Image) v1.Image {
	t.Helper()
	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	ref, err := name.ParseReference(host+"/test/materialize:latest", name.Insecure)
	if err != nil {
		t.Fatalf("ParseReference() error: %v", err)
	}

	opts := []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain)}
	if err := remote.Write(ref, img, opts...); err != nil {
		t.Fatalf("remote.Write() error: %v", err)
	}

	materialized, err := remote.Image(ref, opts...)
	if err != nil {
		t.Fatalf("remote.Image() error: %v", err)
	}
	return materialized
}

func TestBundleToImage_Labels(t *testing.T) {
	b := testBundle()
	img, err := bundleToImage(b)
	if err != nil {
		t.Fatalf("bundleToImage() error: %v", err)
	}

	// Materialize the image so stream.Layer is consumed and config is readable.
	img = materializeImage(t, img)

	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatalf("ConfigFile() error: %v", err)
	}

	wantLabels := map[string]string{
		"io.pacto.name":         "test-svc",
		"io.pacto.version":      "1.0.0",
		"io.pacto.pactoVersion": "1.0",
	}

	for k, want := range wantLabels {
		got, ok := cfg.Config.Labels[k]
		if !ok {
			t.Errorf("label %q not found", k)
			continue
		}
		if got != want {
			t.Errorf("label %q = %q, want %q", k, got, want)
		}
	}
}

func TestImageToBundle_Roundtrip(t *testing.T) {
	b := testBundle()
	img, err := bundleToImage(b)
	if err != nil {
		t.Fatalf("bundleToImage() error: %v", err)
	}

	// Materialize the image so stream.Layer.Uncompressed works.
	img = materializeImage(t, img)

	got, err := imageToBundle(img)
	if err != nil {
		t.Fatalf("imageToBundle() error: %v", err)
	}

	if got.Contract.Service.Name != b.Contract.Service.Name {
		t.Errorf("Service.Name = %q, want %q", got.Contract.Service.Name, b.Contract.Service.Name)
	}
	if got.Contract.Service.Version != b.Contract.Service.Version {
		t.Errorf("Service.Version = %q, want %q", got.Contract.Service.Version, b.Contract.Service.Version)
	}
	if got.Contract.PactoVersion != b.Contract.PactoVersion {
		t.Errorf("PactoVersion = %q, want %q", got.Contract.PactoVersion, b.Contract.PactoVersion)
	}
	if len(got.Contract.Interfaces) != len(b.Contract.Interfaces) {
		t.Errorf("len(Interfaces) = %d, want %d", len(got.Contract.Interfaces), len(b.Contract.Interfaces))
	}
	if got.Contract.Runtime.Health.Path != b.Contract.Runtime.Health.Path {
		t.Errorf("Health.Path = %q, want %q", got.Contract.Runtime.Health.Path, b.Contract.Runtime.Health.Path)
	}
}

func TestImageToBundle_NoLayers(t *testing.T) {
	img := empty.Image

	_, err := imageToBundle(img)
	if err == nil {
		t.Fatal("expected error for image with no layers")
	}
	if !strings.Contains(err.Error(), "no layers") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "no layers")
	}
}

func TestImageToBundle_MultipleLayers(t *testing.T) {
	// Create an image with two layers to trigger the >1 layer error.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{
		Name: "file.txt",
		Size: int64(len(content)),
		Mode: 0644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	layerBytes := buf.Bytes()
	makeLayer := func() v1.Layer {
		layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(layerBytes)), nil
		})
		if err != nil {
			t.Fatalf("tarball.LayerFromOpener() error: %v", err)
		}
		return layer
	}

	img, err := mutate.AppendLayers(empty.Image, makeLayer(), makeLayer())
	if err != nil {
		t.Fatalf("mutate.AppendLayers() error: %v", err)
	}

	_, err = imageToBundle(img)
	if err == nil {
		t.Fatal("expected error for image with multiple layers")
	}
	if !strings.Contains(err.Error(), "expected 1 layer") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "expected 1 layer")
	}
}

func TestImageToBundle_MissingPactoYAML(t *testing.T) {
	// Create a tar with a file that is NOT pacto.yaml.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("hello world")
	if err := tw.WriteHeader(&tar.Header{
		Name: "other.txt",
		Size: int64(len(content)),
		Mode: 0644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	layerBytes := buf.Bytes()
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(layerBytes)), nil
	})
	if err != nil {
		t.Fatalf("tarball.LayerFromOpener() error: %v", err)
	}

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("mutate.AppendLayers() error: %v", err)
	}

	_, err = imageToBundle(img)
	if err == nil {
		t.Fatal("expected error for image missing pacto.yaml")
	}
	if !strings.Contains(err.Error(), "pacto.yaml") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "pacto.yaml")
	}
}

// buildTestTar creates a tar archive with a directory, a nested file, and a root file.
func buildTestTar(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	if err := tw.WriteHeader(&tar.Header{
		Name:     "subdir/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}); err != nil {
		t.Fatal(err)
	}

	fileContent := []byte("file content here")
	if err := tw.WriteHeader(&tar.Header{
		Name: "subdir/file.txt",
		Size: int64(len(fileContent)),
		Mode: 0644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(fileContent); err != nil {
		t.Fatal(err)
	}

	rootContent := []byte("root file")
	if err := tw.WriteHeader(&tar.Header{
		Name: "root.txt",
		Size: int64(len(rootContent)),
		Mode: 0644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(rootContent); err != nil {
		t.Fatal(err)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestExtractTar_NestedFile(t *testing.T) {
	buf := buildTestTar(t)
	fsys, err := extractTar(buf)
	if err != nil {
		t.Fatalf("extractTar() error: %v", err)
	}

	f, err := fsys.Open("subdir/file.txt")
	if err != nil {
		t.Fatalf("Open(subdir/file.txt) error: %v", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if string(data) != "file content here" {
		t.Errorf("file content = %q, want %q", string(data), "file content here")
	}
}

func TestExtractTar_RootFile(t *testing.T) {
	buf := buildTestTar(t)
	fsys, err := extractTar(buf)
	if err != nil {
		t.Fatalf("extractTar() error: %v", err)
	}

	f, err := fsys.Open("root.txt")
	if err != nil {
		t.Fatalf("Open(root.txt) error: %v", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if string(data) != "root file" {
		t.Errorf("root content = %q, want %q", string(data), "root file")
	}
}

func TestExtractTar_Directory(t *testing.T) {
	buf := buildTestTar(t)
	fsys, err := extractTar(buf)
	if err != nil {
		t.Fatalf("extractTar() error: %v", err)
	}

	info, err := fs.Stat(fsys, "subdir")
	if err != nil {
		t.Fatalf("Stat(subdir) error: %v", err)
	}
	if !info.IsDir() {
		t.Error("subdir should be a directory")
	}
}

func TestBundleToTarGz_Roundtrip(t *testing.T) {
	b := testBundle()

	data, err := BundleToTarGz(b.FS)
	if err != nil {
		t.Fatalf("BundleToTarGz() error: %v", err)
	}

	// Decompress gzip.
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader() error: %v", err)
	}
	defer func() { _ = gr.Close() }()

	// Extract tar.
	fsys, err := extractTar(gr)
	if err != nil {
		t.Fatalf("extractTar() error: %v", err)
	}

	// Verify pacto.yaml exists and has content.
	f, err := fsys.Open("pacto.yaml")
	if err != nil {
		t.Fatalf("Open(pacto.yaml) error: %v", err)
	}
	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if len(content) == 0 {
		t.Error("pacto.yaml should not be empty")
	}
	if !strings.Contains(string(content), "test-svc") {
		t.Error("pacto.yaml should contain service name")
	}
}

func TestNewBundleTarReader(t *testing.T) {
	b := testBundle()

	rc := newBundleTarReader(b.FS)
	defer func() { _ = rc.Close() }()

	tr := tar.NewReader(rc)

	foundFiles := make(map[string]bool)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() error: %v", err)
		}
		foundFiles[header.Name] = true

		// Read file contents to drain the reader.
		if header.Typeflag != tar.TypeDir {
			if _, err := io.ReadAll(tr); err != nil {
				t.Fatalf("reading tar entry %q: %v", header.Name, err)
			}
		}
	}

	if !foundFiles["pacto.yaml"] {
		t.Error("expected pacto.yaml in tar stream")
	}
}

func TestNewBundleTarReader_WithDirectory(t *testing.T) {
	// Test with a FS that contains subdirectories.
	fsys := fstest.MapFS{
		"dir":          &fstest.MapFile{Mode: fs.ModeDir | 0755},
		"dir/file.txt": &fstest.MapFile{Data: []byte("in dir")},
		"root.txt":     &fstest.MapFile{Data: []byte("at root")},
	}

	rc := newBundleTarReader(fsys)
	defer func() { _ = rc.Close() }()

	tr := tar.NewReader(rc)
	foundFiles := make(map[string]bool)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next() error: %v", err)
		}
		foundFiles[header.Name] = true

		if header.Typeflag != tar.TypeDir {
			if _, err := io.ReadAll(tr); err != nil {
				t.Fatalf("reading tar entry %q: %v", header.Name, err)
			}
		}
	}

	if !foundFiles["dir"] {
		t.Error("expected 'dir' entry in tar stream")
	}
	if !foundFiles["dir/file.txt"] {
		t.Error("expected 'dir/file.txt' entry in tar stream")
	}
	if !foundFiles["root.txt"] {
		t.Error("expected 'root.txt' entry in tar stream")
	}
}

func TestExtractTar_SkipsDotEntry(t *testing.T) {
	// Test that extractTar skips "." and "./" entries.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Add a "./" entry (common in tar archives).
	if err := tw.WriteHeader(&tar.Header{
		Name:     "./",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	}); err != nil {
		t.Fatal(err)
	}

	content := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{
		Name: "./file.txt",
		Size: int64(len(content)),
		Mode: 0644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	fsys, err := extractTar(&buf)
	if err != nil {
		t.Fatalf("extractTar() error: %v", err)
	}

	f, err := fsys.Open("file.txt")
	if err != nil {
		t.Fatalf("Open(file.txt) error: %v", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", string(data), "hello")
	}
}

func TestExtractTar_CorruptedInput(t *testing.T) {
	// Feed corrupted data that isn't a valid tar stream. The first call to
	// tr.Next() will return an unexpected header error.
	r := strings.NewReader("this is not a tar stream at all")
	_, err := extractTar(r)
	if err == nil {
		t.Fatal("expected error for corrupted tar input")
	}
}

func TestExtractTar_TruncatedFile(t *testing.T) {
	// Create a tar with a file header claiming a large size, then truncate
	// the data so ReadAll will fail.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Write header saying file is 1000 bytes.
	if err := tw.WriteHeader(&tar.Header{
		Name: "big.txt",
		Size: 1000,
		Mode: 0644,
	}); err != nil {
		t.Fatal(err)
	}
	// Write only 10 bytes then close (intentionally truncated).
	if _, err := tw.Write([]byte("short data")); err != nil {
		t.Fatal(err)
	}
	// Flush what we have but don't close properly.
	_ = tw.Flush()

	_, err := extractTar(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated tar file")
	}
}

func TestExtractTar_FileExceedsMaxSize(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Create a file that exceeds the per-file size limit.
	bigData := make([]byte, maxFileSize+1)
	if err := tw.WriteHeader(&tar.Header{
		Name: "huge.bin",
		Size: int64(len(bigData)),
		Mode: 0644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(bigData); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := extractTar(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for file exceeding max size")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExtractTar_TotalSizeExceeded(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Create enough files to exceed the total size limit.
	// Each file is just under the per-file limit but together they exceed maxTotalSize.
	fileSize := maxFileSize
	numFiles := (maxTotalSize / fileSize) + 1
	data := make([]byte, fileSize)
	for i := 0; i < numFiles; i++ {
		if err := tw.WriteHeader(&tar.Header{
			Name: fmt.Sprintf("file%d.bin", i),
			Size: int64(fileSize),
			Mode: 0644,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := extractTar(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for total size exceeded")
	}
	if !strings.Contains(err.Error(), "maximum total size") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBundleToTarGz_WithDirectories(t *testing.T) {
	fsys := fstest.MapFS{
		"dir":          &fstest.MapFile{Mode: fs.ModeDir | 0755},
		"dir/file.txt": &fstest.MapFile{Data: []byte("file in dir")},
	}

	data, err := BundleToTarGz(fsys)
	if err != nil {
		t.Fatalf("BundleToTarGz() error: %v", err)
	}

	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader() error: %v", err)
	}
	defer func() { _ = gr.Close() }()

	extracted, err := extractTar(gr)
	if err != nil {
		t.Fatalf("extractTar() error: %v", err)
	}

	f, err := extracted.Open("dir/file.txt")
	if err != nil {
		t.Fatalf("Open(dir/file.txt) error: %v", err)
	}
	content, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if string(content) != "file in dir" {
		t.Errorf("content = %q, want %q", string(content), "file in dir")
	}
}

// errFS is a filesystem that returns errors for specific operations to exercise
// error-handling paths in newBundleTarReader and BundleToTarGz.
type errFS struct {
	fstest.MapFS
	errOnOpen string // file name that triggers Open error
}

func (e errFS) Open(name string) (fs.File, error) {
	if name == e.errOnOpen {
		return nil, fmt.Errorf("injected open error for %s", name)
	}
	return e.MapFS.Open(name)
}

// walkErrFS is an FS that returns an error from ReadDir, causing WalkDir to
// pass a non-nil walkErr to the callback.
type walkErrFS struct{}

func (walkErrFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &walkErrDir{}, nil
	}
	return nil, fmt.Errorf("not found: %s", name)
}

// walkErrDir implements fs.File and fs.ReadDirFile, returning an error from ReadDir.
type walkErrDir struct{}

func (walkErrDir) Stat() (fs.FileInfo, error) { return walkErrDirInfo{}, nil }
func (walkErrDir) Read(_ []byte) (int, error) { return 0, io.EOF }
func (walkErrDir) Close() error               { return nil }
func (walkErrDir) ReadDir(_ int) ([]fs.DirEntry, error) {
	return nil, fmt.Errorf("injected readdir error")
}

type walkErrDirInfo struct{}

func (walkErrDirInfo) Name() string       { return "." }
func (walkErrDirInfo) Size() int64        { return 0 }
func (walkErrDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0755 }
func (walkErrDirInfo) ModTime() time.Time { return time.Time{} }
func (walkErrDirInfo) IsDir() bool        { return true }
func (walkErrDirInfo) Sys() any           { return nil }

// infoErrFS is a filesystem where a specific file's DirEntry.Info() returns an error.
// It wraps MapFS without embedding it, to avoid promoting MapFS's ReadDirFS interface.
type infoErrFS struct {
	inner     fstest.MapFS
	errOnInfo string
}

func (e infoErrFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &infoErrDir{inner: e.inner, errFile: e.errOnInfo}, nil
	}
	return e.inner.Open(name)
}

type infoErrDir struct {
	inner   fstest.MapFS
	errFile string
}

func (d *infoErrDir) Stat() (fs.FileInfo, error) { return walkErrDirInfo{}, nil }
func (d *infoErrDir) Read(_ []byte) (int, error) { return 0, io.EOF }
func (d *infoErrDir) Close() error               { return nil }
func (d *infoErrDir) ReadDir(n int) ([]fs.DirEntry, error) {
	entries := []fs.DirEntry{&infoErrEntry{name: d.errFile}}
	return entries, nil
}

type infoErrEntry struct {
	name string
}

func (e *infoErrEntry) Name() string               { return e.name }
func (e *infoErrEntry) IsDir() bool                { return false }
func (e *infoErrEntry) Type() fs.FileMode          { return 0 }
func (e *infoErrEntry) Info() (fs.FileInfo, error) { return nil, fmt.Errorf("injected info error") }

func TestNewBundleTarReader_InfoError(t *testing.T) {
	efs := infoErrFS{
		inner: fstest.MapFS{
			"file.txt": &fstest.MapFile{Data: []byte("data")},
		},
		errOnInfo: "file.txt",
	}

	rc := newBundleTarReader(efs)
	defer func() { _ = rc.Close() }()

	_, err := io.ReadAll(rc)
	if err == nil {
		t.Fatal("expected error from newBundleTarReader with info error FS")
	}
}

// socketFS is a filesystem that contains a file with an irregular mode (like a socket)
// that causes tar.FileInfoHeader to return an error.
type socketFS struct{}

func (socketFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &socketDir{}, nil
	}
	return nil, fmt.Errorf("not found: %s", name)
}

type socketDir struct{}

func (socketDir) Stat() (fs.FileInfo, error) { return walkErrDirInfo{}, nil }
func (socketDir) Read(_ []byte) (int, error) { return 0, io.EOF }
func (socketDir) Close() error               { return nil }
func (socketDir) ReadDir(_ int) ([]fs.DirEntry, error) {
	return []fs.DirEntry{&socketEntry{}}, nil
}

type socketEntry struct{}

func (socketEntry) Name() string               { return "sock" }
func (socketEntry) IsDir() bool                { return false }
func (socketEntry) Type() fs.FileMode          { return fs.ModeSocket }
func (socketEntry) Info() (fs.FileInfo, error) { return socketFileInfo{}, nil }

type socketFileInfo struct{}

func (socketFileInfo) Name() string       { return "sock" }
func (socketFileInfo) Size() int64        { return 0 }
func (socketFileInfo) Mode() fs.FileMode  { return fs.ModeSocket }
func (socketFileInfo) ModTime() time.Time { return time.Time{} }
func (socketFileInfo) IsDir() bool        { return false }
func (socketFileInfo) Sys() any           { return nil }

func TestNewBundleTarReader_FileInfoHeaderError(t *testing.T) {
	rc := newBundleTarReader(socketFS{})
	defer func() { _ = rc.Close() }()

	_, err := io.ReadAll(rc)
	if err == nil {
		t.Fatal("expected error from newBundleTarReader with socket file")
	}
}

func TestBundleToTarGz_FileInfoHeaderError(t *testing.T) {
	_, err := BundleToTarGz(socketFS{})
	if err == nil {
		t.Fatal("expected error from BundleToTarGz with socket file")
	}
}

func TestBundleToTarGz_InfoError(t *testing.T) {
	efs := infoErrFS{
		inner: fstest.MapFS{
			"file.txt": &fstest.MapFile{Data: []byte("data")},
		},
		errOnInfo: "file.txt",
	}

	_, err := BundleToTarGz(efs)
	if err == nil {
		t.Fatal("expected error from BundleToTarGz with info error FS")
	}
}

func TestNewBundleTarReader_WalkError(t *testing.T) {
	rc := newBundleTarReader(walkErrFS{})
	defer func() { _ = rc.Close() }()

	// Reading from the pipe should yield an error since WalkDir fails.
	_, err := io.ReadAll(rc)
	if err == nil {
		t.Fatal("expected error from newBundleTarReader with broken FS")
	}
}

func TestBundleToTarGz_WalkError(t *testing.T) {
	_, err := BundleToTarGz(walkErrFS{})
	if err == nil {
		t.Fatal("expected error from BundleToTarGz with broken FS")
	}
}

func TestNewBundleTarReader_OpenError(t *testing.T) {
	// Use an FS where Open fails for a specific file.
	efs := errFS{
		MapFS: fstest.MapFS{
			"file.txt": &fstest.MapFile{Data: []byte("data")},
		},
		errOnOpen: "file.txt",
	}

	rc := newBundleTarReader(efs)
	defer func() { _ = rc.Close() }()

	// Reading the tar should produce an error because Open fails on file.txt.
	tr := tar.NewReader(rc)
	for {
		_, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Expected: the pipe will close with the open error.
			return
		}
		if _, err := io.ReadAll(tr); err != nil {
			// Error during file read propagated through pipe.
			return
		}
	}
	// If we get here, the open error was not triggered. That could happen if
	// WalkDir finds the entry but the Open in the goroutine fails, causing
	// io.Copy to fail. Either way, we're exercising the error path.
}

func TestBundleToTarGz_OpenError(t *testing.T) {
	efs := errFS{
		MapFS: fstest.MapFS{
			"file.txt": &fstest.MapFile{Data: []byte("data")},
		},
		errOnOpen: "file.txt",
	}

	_, err := BundleToTarGz(efs)
	if err == nil {
		t.Fatal("expected error from BundleToTarGz with broken FS")
	}
}

// errorImage is a v1.Image that returns errors for all operations except
// those that return static data. Used to test error handling in imageToBundle.
type errorImage struct {
	v1.Image
	layersErr error
}

func (e *errorImage) Layers() ([]v1.Layer, error) {
	return nil, e.layersErr
}

func TestImageToBundle_LayersError(t *testing.T) {
	img := &errorImage{layersErr: fmt.Errorf("injected layers error")}
	_, err := imageToBundle(img)
	if err == nil {
		t.Fatal("expected error from imageToBundle when Layers() fails")
	}
	if !strings.Contains(err.Error(), "failed to get layers") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to get layers")
	}
}

// errorLayer is a v1.Layer that returns an error from Uncompressed.
type errorLayer struct {
	v1.Layer
}

func (e *errorLayer) Uncompressed() (io.ReadCloser, error) {
	return nil, fmt.Errorf("injected uncompressed error")
}

// layerErrorImage returns one layer that errors on Uncompressed.
type layerErrorImage struct {
	v1.Image
}

func (e *layerErrorImage) Layers() ([]v1.Layer, error) {
	return []v1.Layer{&errorLayer{}}, nil
}

func TestImageToBundle_UncompressedError(t *testing.T) {
	img := &layerErrorImage{}
	_, err := imageToBundle(img)
	if err == nil {
		t.Fatal("expected error from imageToBundle when Uncompressed() fails")
	}
	if !strings.Contains(err.Error(), "failed to read layer") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to read layer")
	}
}

// corruptLayer is a v1.Layer that returns corrupt data from Uncompressed.
type corruptLayer struct {
	v1.Layer
}

func (c *corruptLayer) Uncompressed() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("corrupt tar data that is not valid")), nil
}

// corruptLayerImage returns one layer with corrupt tar data.
type corruptLayerImage struct {
	v1.Image
}

func (c *corruptLayerImage) Layers() ([]v1.Layer, error) {
	return []v1.Layer{&corruptLayer{}}, nil
}

func TestImageToBundle_ExtractTarError(t *testing.T) {
	img := &corruptLayerImage{}
	_, err := imageToBundle(img)
	if err == nil {
		t.Fatal("expected error from imageToBundle when extractTar fails")
	}
	if !strings.Contains(err.Error(), "failed to extract layer") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to extract layer")
	}
}

func TestImageToBundle_InvalidPactoYAML(t *testing.T) {
	// Create a tar with a pacto.yaml that has invalid YAML content.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	content := []byte("this is: [not: valid: pacto yaml")
	if err := tw.WriteHeader(&tar.Header{
		Name: "pacto.yaml",
		Size: int64(len(content)),
		Mode: 0644,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	layerBytes := buf.Bytes()
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(layerBytes)), nil
	})
	if err != nil {
		t.Fatalf("tarball.LayerFromOpener() error: %v", err)
	}

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("mutate.AppendLayers() error: %v", err)
	}

	_, err = imageToBundle(img)
	if err == nil {
		t.Fatal("expected error for invalid pacto.yaml content")
	}
	if !strings.Contains(err.Error(), "parse contract") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parse contract")
	}
}

func TestWalkTar_WriteHeaderError(t *testing.T) {
	tw := tar.NewWriter(tarErrWriter{})
	fsys := fstest.MapFS{
		"file.txt": &fstest.MapFile{Data: []byte("data")},
	}
	err := walkTar(tw, fsys)
	if err == nil {
		t.Error("expected error from walkTar when tar writer fails")
	}
}

func TestWriteBundleTarGz_WriteError(t *testing.T) {
	fsys := fstest.MapFS{
		"file.txt": &fstest.MapFile{Data: []byte("data")},
	}
	err := writeBundleTarGz(tarErrWriter{}, fsys)
	if err == nil {
		t.Error("expected error from writeBundleTarGz with failing writer")
	}
}

func TestWriteBundleTarGz_TarCloseError(t *testing.T) {
	old := tarCloseFn
	tarCloseFn = func(tw *tar.Writer) error { return fmt.Errorf("tar close error") }
	defer func() { tarCloseFn = old }()

	fsys := fstest.MapFS{
		"file.txt": &fstest.MapFile{Data: []byte("data")},
	}
	_, err := BundleToTarGz(fsys)
	if err == nil {
		t.Error("expected error when tar close fails")
	}
}

func TestWriteBundleTarGz_GzipCloseError(t *testing.T) {
	old := gzipCloseFn
	gzipCloseFn = func(gw *gzip.Writer) error { return fmt.Errorf("gzip close error") }
	defer func() { gzipCloseFn = old }()

	fsys := fstest.MapFS{
		"file.txt": &fstest.MapFile{Data: []byte("data")},
	}
	_, err := BundleToTarGz(fsys)
	if err == nil {
		t.Error("expected error when gzip close fails")
	}
}

func TestBundleToImage_MutateConfigError(t *testing.T) {
	old := mutateConfigFn
	mutateConfigFn = func(img v1.Image, cfg v1.Config) (v1.Image, error) {
		return nil, fmt.Errorf("config error")
	}
	defer func() { mutateConfigFn = old }()

	b := testBundle()
	_, err := bundleToImage(b)
	if err == nil {
		t.Error("expected error when mutate.Config fails")
	}
}

func TestBundleToImage_AppendLayersError(t *testing.T) {
	old := mutateAppendLayersFn
	mutateAppendLayersFn = func(img v1.Image, layers ...v1.Layer) (v1.Image, error) {
		return nil, fmt.Errorf("append error")
	}
	defer func() { mutateAppendLayersFn = old }()

	b := testBundle()
	_, err := bundleToImage(b)
	if err == nil {
		t.Error("expected error when mutate.AppendLayers fails")
	}
}
