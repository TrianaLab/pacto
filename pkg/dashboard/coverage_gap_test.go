package dashboard

import (
	"archive/tar"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTar_ReadError(t *testing.T) {
	// Create a tar with a valid header but the data read will fail.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Write a header for a file with size 100 but don't write the data.
	_ = tw.WriteHeader(&tar.Header{
		Name: "bad.txt",
		Size: 100,
		Mode: 0644,
	})
	_ = tw.Flush()

	// Concatenate the buffer with an error reader to simulate read failure.
	combinedReader := io.MultiReader(bytes.NewReader(buf.Bytes()), &errorReader{})

	_, err := extractTar(combinedReader)
	if err == nil {
		t.Fatal("expected error from read failure in tar extraction")
	}
}

// errorReader always returns an error on Read.
type errorReader struct{}

func (e *errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestCacheSource_ScanWalkError(t *testing.T) {
	// Create a cache dir with an unreadable subdirectory to trigger walk error callback.
	root := t.TempDir()

	// Create a valid bundle first so there's something to scan.
	writeBundleTarGzFile(t,
		filepath.Join(root, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)

	// Create an unreadable directory that will cause Walk to pass an error to the callback.
	badDir := filepath.Join(root, "ghcr.io/bad")
	if err := os.MkdirAll(badDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(badDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(badDir, 0o755) })

	src := NewCacheSource(root)
	// Should still find the valid bundle despite the walk error.
	if src.ServiceCount() != 1 {
		t.Fatalf("expected 1 service (skipping walk error), got %d", src.ServiceCount())
	}
}

func TestDetectCache_WithValidBundles(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "oci")

	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/api/1.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: api
  version: 1.0.0
`)
	writeBundleTarGzFile(t,
		filepath.Join(cacheDir, "ghcr.io/org/worker/2.0.0/bundle.tar.gz"),
		`pactoVersion: "1.0"
service:
  name: worker
  version: 2.0.0
`)

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache(cacheDir)

	if result.Cache == nil {
		t.Fatal("expected cache source to be detected")
	}
	if result.Diagnostics.Cache.ServiceCount != 2 {
		t.Errorf("expected 2 services, got %d", result.Diagnostics.Cache.ServiceCount)
	}
	if result.Diagnostics.Cache.VersionCount != 2 {
		t.Errorf("expected 2 versions, got %d", result.Diagnostics.Cache.VersionCount)
	}
	if !result.Sources[0].Enabled {
		t.Error("expected source to be enabled")
	}
}

func TestDetectCache_HomeError(t *testing.T) {
	// When cacheDir is empty and HOME is not set, detectCache should handle the error.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")

	result := &DetectResult{Diagnostics: &SourceDiagnostics{}}
	result.detectCache("")

	// On macOS, UserHomeDir may still succeed via /etc/passwd.
	// If it fails, we expect the error path.
	if result.Diagnostics.Cache.Error != "" {
		if len(result.Sources) != 1 {
			t.Fatalf("expected 1 source info, got %d", len(result.Sources))
		}
		if result.Sources[0].Enabled {
			t.Error("expected source to be disabled on home dir error")
		}
	}
}

func TestExtractTar_PrefixDotSlash(t *testing.T) {
	// Entries prefixed with "./" should have the prefix stripped.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	data := []byte("content")
	_ = tw.WriteHeader(&tar.Header{
		Name: "./file.txt",
		Size: int64(len(data)),
		Mode: 0644,
	})
	_, _ = tw.Write(data)
	_ = tw.Close()

	fsys, err := extractTar(&buf)
	if err != nil {
		t.Fatal(err)
	}
	content, err := fs.ReadFile(fsys, "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "content" {
		t.Errorf("expected 'content', got %q", string(content))
	}
}

func TestExtractTar_DotDotInMiddle(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	data := []byte("sneaky")
	_ = tw.WriteHeader(&tar.Header{
		Name: "subdir/../secret.txt",
		Size: int64(len(data)),
		Mode: 0644,
	})
	_, _ = tw.Write(data)
	_ = tw.Close()

	_, err := extractTar(&buf)
	if err == nil {
		t.Fatal("expected error for path containing '..'")
	}
}

func TestLocalSource_FindBundle_SubdirInvalidYAMLThenValid(t *testing.T) {
	// This tests the `continue` path in findBundle when loadLocalBundle fails
	// for a subdirectory (line 115-116 in source_local.go).
	// The root has NO pacto.yaml. One subdir has invalid YAML, another has valid.
	root := t.TempDir()

	// Create a subdir with invalid pacto.yaml that will fail contract.Parse.
	badDir := filepath.Join(root, "aaa-bad")
	if err := os.MkdirAll(badDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "pacto.yaml"), []byte("not valid yaml: [[["), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a subdir with valid pacto.yaml.
	writeLocalPactoYAML(t, filepath.Join(root, "zzz-good"), "target-svc", "1.0.0")

	src := NewLocalSource(root)
	// findBundle iterates sorted entries: "aaa-bad" first (fails), then "zzz-good" (succeeds).
	details, err := src.GetService(t.Context(), "target-svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "target-svc" {
		t.Errorf("expected 'target-svc', got %q", details.Name)
	}
}
