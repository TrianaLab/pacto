package app

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestPack_Success(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	bundleDir := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Pack(context.Background(), PackOptions{Path: bundleDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test-svc" {
		t.Errorf("expected Name=test-svc, got %s", result.Name)
	}
	if result.Version != "1.0.0" {
		t.Errorf("expected Version=1.0.0, got %s", result.Version)
	}
	// Verify archive was created
	if _, err := os.Stat(result.Output); err != nil {
		t.Fatalf("expected archive at %s: %v", result.Output, err)
	}
}

func TestPack_CustomOutput(t *testing.T) {
	dir := t.TempDir()
	bundleDir := writeTestBundle(t)
	output := filepath.Join(dir, "custom.tar.gz")

	svc := NewService(nil, nil)
	result, err := svc.Pack(context.Background(), PackOptions{Path: bundleDir, Output: output})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != output {
		t.Errorf("expected Output=%s, got %s", output, result.Output)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("expected archive at %s: %v", output, err)
	}
}

func TestPack_InvalidContract(t *testing.T) {
	dir := writeInvalidBundle(t)
	svc := NewService(nil, nil)
	_, err := svc.Pack(context.Background(), PackOptions{Path: dir})
	if err == nil {
		t.Error("expected error for invalid contract")
	}
}

func TestPack_FileNotFound(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Pack(context.Background(), PackOptions{Path: "/nonexistent/dir"})
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestPack_BundleToTarGzError(t *testing.T) {
	// Create a valid bundle with an unreadable file to cause BundleToTarGz to fail
	bundleDir := writeTestBundle(t)
	unreadable := filepath.Join(bundleDir, "unreadable.txt")
	if err := os.WriteFile(unreadable, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0644) })

	svc := NewService(nil, nil)
	_, err := svc.Pack(context.Background(), PackOptions{
		Path:   bundleDir,
		Output: filepath.Join(t.TempDir(), "out.tar.gz"),
	})
	if err == nil {
		t.Error("expected error when BundleToTarGz fails on unreadable file")
	}
}

func TestPack_WriteError(t *testing.T) {
	bundleDir := writeTestBundle(t)
	svc := NewService(nil, nil)
	// Try to write to a path that doesn't exist and can't be created
	_, err := svc.Pack(context.Background(), PackOptions{
		Path:   bundleDir,
		Output: "/dev/null/impossible/output.tar.gz",
	})
	if err == nil {
		t.Error("expected error when writing archive to invalid path")
	}
}

func TestPackImplicitVersionPinned(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("service:\n  name: s\n  version: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "s.tar.gz")
	if _, err := NewService(nil, nil).Pack(context.Background(), PackOptions{Path: dir, Output: output}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("no pacto.yaml in the archive: %v", err)
		}
		if h.Name != "pacto.yaml" {
			continue
		}
		data, _ := io.ReadAll(tr)
		if want := `pactoVersion: "` + contract.Current() + `"`; !strings.HasPrefix(string(data), want) {
			t.Fatalf("bundled pacto.yaml %q does not start with %s", data, want)
		}
		return
	}
}

func TestPackRefusesUndeclaredVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewService(nil, nil).Pack(context.Background(), PackOptions{Path: dir})
	if !errors.Is(err, ErrPublishNeedsServiceVersion) {
		t.Fatalf("got %v", err)
	}
}

func TestRootServiceBundleExcludesPlanFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("service:\n  name: s\n  version: 1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto-plan.md"), []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "s.tar.gz")
	if _, err := NewService(nil, nil).Pack(context.Background(), PackOptions{Path: dir, Output: output}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "pacto-plan.md" {
			t.Fatal("the bundle packs pacto-plan.md")
		}
	}
}
