package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/graph"
)

func TestDoc_Local(t *testing.T) {
	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Doc(context.Background(), DocOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Markdown, "# test-svc") {
		t.Error("expected service heading in markdown")
	}
	if result.Path != "" {
		t.Errorf("expected empty path when no output dir, got %s", result.Path)
	}
}

func TestDoc_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Doc(context.Background(), DocOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Markdown, "# test-svc") {
		t.Error("expected service heading in markdown")
	}
}

func TestDoc_WithOutputDir(t *testing.T) {
	path := writeTestBundle(t)
	outDir := filepath.Join(t.TempDir(), "docs")
	svc := NewService(nil, nil)
	result, err := svc.Doc(context.Background(), DocOptions{
		Path:      path,
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(outDir, "test-svc.md")
	if result.Path != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, result.Path)
	}

	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if !strings.Contains(string(data), "# test-svc") {
		t.Error("expected service heading in written file")
	}
}

func TestDoc_NotFound(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Doc(context.Background(), DocOptions{Path: "/nonexistent/path"})
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestDoc_GenerateError(t *testing.T) {
	original := generateDoc
	generateDoc = func(_ *contract.Contract, _ fs.FS, _ *graph.Result) (string, error) {
		return "", fmt.Errorf("generate failed")
	}
	t.Cleanup(func() { generateDoc = original })

	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	_, err := svc.Doc(context.Background(), DocOptions{Path: path})
	if err == nil {
		t.Error("expected error from generateDoc")
	}
	if !strings.Contains(err.Error(), "generating documentation") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestDoc_OutputDirError(t *testing.T) {
	path := writeTestBundle(t)
	// Use a path nested under a file to trigger MkdirAll error
	outDir := filepath.Join(path, "pacto.yaml", "impossible")
	svc := NewService(nil, nil)
	_, err := svc.Doc(context.Background(), DocOptions{
		Path:      path,
		OutputDir: outDir,
	})
	if err == nil {
		t.Error("expected error for non-writable output directory")
	}
}

func TestDoc_WriteFileError(t *testing.T) {
	path := writeTestBundle(t)
	// Create a read-only directory so MkdirAll succeeds but WriteFile fails
	outDir := t.TempDir()
	if err := os.Chmod(outDir, 0555); err != nil {
		t.Skipf("cannot set read-only permissions: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(outDir, 0755) })

	svc := NewService(nil, nil)
	_, err := svc.Doc(context.Background(), DocOptions{
		Path:      path,
		OutputDir: outDir,
	})
	if err == nil {
		t.Error("expected error when directory is read-only")
	}
}

func TestDoc_WithConfiguration(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			port := 8080
			return &contract.Bundle{
				Contract: &contract.Contract{
					PactoVersion: "1.0",
					Service:      contract.ServiceIdentity{Name: "cfg-svc", Version: "1.0.0"},
					Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
					Configurations: []contract.ConfigurationSource{
						{Name: "default", Schema: "configuration/schema.json"},
					},
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
					"configuration/schema.json": &fstest.MapFile{Data: []byte(`{
						"type": "object",
						"properties": {
							"PORT": {"type": "integer", "description": "Server port", "default": 8080}
						},
						"required": ["PORT"]
					}`)},
				},
			}, nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Doc(context.Background(), DocOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Markdown, ". Configuration") {
		t.Error("expected configuration section in markdown")
	}
	if !strings.Contains(result.Markdown, "`PORT`") {
		t.Error("expected PORT property in configuration")
	}
}
