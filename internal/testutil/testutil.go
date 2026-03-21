// Package testutil provides shared test mocks and fixtures used across
// multiple test packages to avoid duplication.
package testutil

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/plugin"
)

// MockBundleStore implements oci.BundleStore for testing.
type MockBundleStore struct {
	PushFn     func(ctx context.Context, ref string, bundle *contract.Bundle) (string, error)
	PullFn     func(ctx context.Context, ref string) (*contract.Bundle, error)
	ResolveFn  func(ctx context.Context, ref string) (string, error)
	ListTagsFn func(ctx context.Context, repo string) ([]string, error)
}

func (m *MockBundleStore) Push(ctx context.Context, ref string, bundle *contract.Bundle) (string, error) {
	if m.PushFn != nil {
		return m.PushFn(ctx, ref, bundle)
	}
	return "sha256:abc123", nil
}

func (m *MockBundleStore) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if m.PullFn != nil {
		return m.PullFn(ctx, ref)
	}
	return TestBundle(), nil
}

func (m *MockBundleStore) Resolve(ctx context.Context, ref string) (string, error) {
	if m.ResolveFn != nil {
		return m.ResolveFn(ctx, ref)
	}
	return "sha256:abc123", nil
}

func (m *MockBundleStore) ListTags(ctx context.Context, repo string) ([]string, error) {
	if m.ListTagsFn != nil {
		return m.ListTagsFn(ctx, repo)
	}
	return []string{"1.0.0"}, nil
}

// MockPluginRunner implements app.PluginRunner for testing.
type MockPluginRunner struct {
	RunFn func(ctx context.Context, name string, req plugin.GenerateRequest) (*plugin.GenerateResponse, error)
}

func (m *MockPluginRunner) Run(ctx context.Context, name string, req plugin.GenerateRequest) (*plugin.GenerateResponse, error) {
	if m.RunFn != nil {
		return m.RunFn(ctx, name, req)
	}
	return &plugin.GenerateResponse{
		Files:   []plugin.GeneratedFile{{Path: "out.txt", Content: "hello"}},
		Message: "done",
	}, nil
}

// ValidPactoYAML returns a minimal valid pacto.yaml content for testing.
func ValidPactoYAML() []byte {
	return []byte(`pactoVersion: "1.0"
service:
  name: test-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
    contract: openapi.yaml
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
`)
}

// TestOpenAPI returns a minimal OpenAPI spec for testing.
func TestOpenAPI() []byte {
	return []byte(`openapi: "3.0.0"
info:
  title: Test API
  version: "1.0.0"
paths:
  /health:
    get:
      summary: Health check
      responses:
        "200":
          description: OK
`)
}

// TestBundle returns a valid in-memory Bundle for testing.
// The bundle includes pacto.yaml plus additional files (openapi.yaml,
// docs/) to verify that the full directory tree survives round-trips.
func TestBundle() *contract.Bundle {
	port := 8080
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: "test-svc", Version: "1.0.0"},
			Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port, Contract: "openapi.yaml"}},
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
		RawYAML: ValidPactoYAML(),
		FS: fstest.MapFS{
			"pacto.yaml":      &fstest.MapFile{Data: ValidPactoYAML()},
			"openapi.yaml":    &fstest.MapFile{Data: TestOpenAPI()},
			"docs":            &fstest.MapFile{Mode: fs.ModeDir | 0755},
			"docs/README.md":  &fstest.MapFile{Data: []byte("# Test Service\n")},
			"docs/runbook.md": &fstest.MapFile{Data: []byte("# Runbook\n")},
		},
	}
}

// WriteTestBundle creates a valid bundle directory structure in a temp dir
// and returns the bundle directory path. The directory includes pacto.yaml
// plus additional files to verify full directory tree handling.
func WriteTestBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(filepath.Join(bundleDir, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"pacto.yaml":      ValidPactoYAML(),
		"openapi.yaml":    TestOpenAPI(),
		"docs/README.md":  []byte("# Test Service\n"),
		"docs/runbook.md": []byte("# Runbook\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(bundleDir, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return bundleDir
}

// ErrBundleStore returns a MockBundleStore where all methods return the given error.
func ErrBundleStore(msg string) *MockBundleStore {
	return &MockBundleStore{
		PushFn: func(_ context.Context, _ string, _ *contract.Bundle) (string, error) {
			return "", fmt.Errorf("%s", msg)
		},
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return nil, fmt.Errorf("%s", msg)
		},
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", fmt.Errorf("%s", msg)
		},
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return nil, fmt.Errorf("%s", msg)
		},
	}
}
