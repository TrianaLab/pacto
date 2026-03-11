package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/internal/plugin"
	"github.com/trianalab/pacto/pkg/contract"
)

func TestGenerate_Success(t *testing.T) {
	bundleDir := writeTestBundle(t)
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "gen-output")

	runner := &mockPluginRunner{}
	svc := NewService(nil, runner)
	result, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      bundleDir,
		OutputDir: outputDir,
		Plugin:    "test-plugin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Plugin != "test-plugin" {
		t.Errorf("expected Plugin=test-plugin, got %s", result.Plugin)
	}
	if result.OutputDir != outputDir {
		t.Errorf("expected OutputDir=%s, got %s", outputDir, result.OutputDir)
	}
	if result.FilesCount != 1 {
		t.Errorf("expected FilesCount=1, got %d", result.FilesCount)
	}
	if result.Message != "done" {
		t.Errorf("expected Message=done, got %s", result.Message)
	}
	// Verify file was written
	data, err := os.ReadFile(filepath.Join(outputDir, "out.txt"))
	if err != nil {
		t.Fatalf("expected out.txt: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}
}

func TestGenerate_DefaultOutputDir(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	bundleDir := writeTestBundle(t)
	runner := &mockPluginRunner{}
	svc := NewService(nil, runner)
	result, err := svc.Generate(context.Background(), GenerateOptions{
		Path:   bundleDir,
		Plugin: "my-plugin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OutputDir != "my-plugin-output" {
		t.Errorf("expected OutputDir=my-plugin-output, got %s", result.OutputDir)
	}
}

func TestGenerate_NilRunner(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:   ".",
		Plugin: "test-plugin",
	})
	if err == nil {
		t.Error("expected error for nil runner")
	}
}

func TestGenerate_PluginError(t *testing.T) {
	bundleDir := writeTestBundle(t)
	dir := t.TempDir()
	runner := &mockPluginRunner{
		RunFn: func(_ context.Context, _ string, _ plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
			return nil, fmt.Errorf("plugin crashed")
		},
	}
	svc := NewService(nil, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      bundleDir,
		OutputDir: filepath.Join(dir, "out"),
		Plugin:    "bad-plugin",
	})
	if err == nil {
		t.Error("expected error from plugin")
	}
}

func TestGenerate_ResolveError(t *testing.T) {
	runner := &mockPluginRunner{}
	svc := NewService(nil, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:   "/nonexistent/dir",
		Plugin: "test-plugin",
	})
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestGenerate_OutputDirError(t *testing.T) {
	bundleDir := writeTestBundle(t)
	runner := &mockPluginRunner{}
	svc := NewService(nil, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      bundleDir,
		OutputDir: "/dev/null/impossible/dir",
		Plugin:    "test-plugin",
	})
	if err == nil {
		t.Error("expected error when creating output directory fails")
	}
}

func TestGenerate_PrepareBundleDirError(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "gen-output")
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			b := testBundle()
			b.FS = &errFS{} // FS that errors on Open, causing extractBundleFS to fail
			return b, nil
		},
	}
	runner := &mockPluginRunner{}
	svc := NewService(store, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      "oci://ghcr.io/acme/svc:1.0.0",
		OutputDir: outputDir,
		Plugin:    "test-plugin",
	})
	if err == nil {
		t.Error("expected error when prepareBundleDir fails")
	}
}

func TestGenerate_WriteFileError(t *testing.T) {
	bundleDir := writeTestBundle(t)
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "gen-output")

	runner := &mockPluginRunner{
		RunFn: func(_ context.Context, _ string, _ plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
			// Make the output dir read-only so writing plugin files fails
			if err := os.Chmod(outputDir, 0555); err != nil {
				return nil, err
			}
			return &plugin.GenerateResponse{
				Files: []plugin.GeneratedFile{{Path: "sub/out.txt", Content: "hello"}},
			}, nil
		},
	}
	t.Cleanup(func() { _ = os.Chmod(outputDir, 0755) })

	svc := NewService(nil, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      bundleDir,
		OutputDir: outputDir,
		Plugin:    "test-plugin",
	})
	if err == nil {
		t.Error("expected error when writing plugin output files fails")
	}
}

func TestGenerate_OCIRef(t *testing.T) {
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "gen-output")
	store := &mockBundleStore{}
	runner := &mockPluginRunner{}
	svc := NewService(store, runner)
	result, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      "oci://ghcr.io/acme/svc:1.0.0",
		OutputDir: outputDir,
		Plugin:    "test-plugin",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FilesCount != 1 {
		t.Errorf("expected FilesCount=1, got %d", result.FilesCount)
	}
}

func TestGenerate_WriteFileError_FlatPath(t *testing.T) {
	bundleDir := writeTestBundle(t)
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "gen-output")

	old := writeFileFn
	writeFileFn = func(name string, data []byte, perm os.FileMode) error {
		return fmt.Errorf("write failed")
	}
	defer func() { writeFileFn = old }()

	runner := &mockPluginRunner{
		RunFn: func(_ context.Context, _ string, _ plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
			return &plugin.GenerateResponse{
				Files: []plugin.GeneratedFile{{Path: "out.txt", Content: "hello"}},
			}, nil
		},
	}

	svc := NewService(nil, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      bundleDir,
		OutputDir: outputDir,
		Plugin:    "test-plugin",
	})
	if err == nil {
		t.Error("expected error when WriteFile fails for flat path")
	}
}

func TestGenerate_PathTraversalBlocked(t *testing.T) {
	bundleDir := writeTestBundle(t)
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "gen-output")

	runner := &mockPluginRunner{
		RunFn: func(_ context.Context, _ string, _ plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
			return &plugin.GenerateResponse{
				Files: []plugin.GeneratedFile{{Path: "../../escape.txt", Content: "pwned"}},
			}, nil
		},
	}

	svc := NewService(nil, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      bundleDir,
		OutputDir: outputDir,
		Plugin:    "bad-plugin",
	})
	if err == nil {
		t.Fatal("expected error for path traversal attempt")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "escape.txt")); statErr == nil {
		t.Error("file was written outside output directory")
	}
}

func TestGenerate_AbsPathError(t *testing.T) {
	bundleDir := writeTestBundle(t)
	dir := t.TempDir()
	outputDir := filepath.Join(dir, "gen-output")

	old := absPathFn
	absPathFn = func(string) (string, error) {
		return "", fmt.Errorf("getwd failed")
	}
	defer func() { absPathFn = old }()

	runner := &mockPluginRunner{}
	svc := NewService(nil, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      bundleDir,
		OutputDir: outputDir,
		Plugin:    "test-plugin",
	})
	if err == nil {
		t.Error("expected error when filepath.Abs fails")
	}
}

func TestGenerate_MkdirTempError(t *testing.T) {
	old := mkdirTempFn
	mkdirTempFn = func(string, string) (string, error) {
		return "", fmt.Errorf("temp failed")
	}
	defer func() { mkdirTempFn = old }()

	store := &mockBundleStore{}
	runner := &mockPluginRunner{}
	svc := NewService(store, runner)
	_, err := svc.Generate(context.Background(), GenerateOptions{
		Path:      "oci://ghcr.io/acme/svc:1.0.0",
		OutputDir: filepath.Join(t.TempDir(), "out"),
		Plugin:    "test-plugin",
	})
	if err == nil {
		t.Error("expected error when MkdirTemp fails")
	}
}
