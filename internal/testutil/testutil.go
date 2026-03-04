// Package testutil provides shared test mocks and fixtures used across
// multiple test packages to avoid duplication.
package testutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/internal/plugin"
	"github.com/trianalab/pacto/pkg/contract"
)

// MockBundleStore implements oci.BundleStore for testing.
type MockBundleStore struct {
	PushFn    func(ctx context.Context, ref string, bundle *contract.Bundle) (string, error)
	PullFn    func(ctx context.Context, ref string) (*contract.Bundle, error)
	ResolveFn func(ctx context.Context, ref string) (string, error)
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

// TestBundle returns a valid in-memory Bundle for testing.
func TestBundle() *contract.Bundle {
	port := 8080
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: "test-svc", Version: "1.0.0"},
			Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
			Runtime: contract.Runtime{
				Workload: "service",
				State: contract.State{
					Type:            "stateless",
					Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
					DataCriticality: "low",
				},
				Health: contract.Health{Interface: "api", Path: "/health"},
			},
		},
		FS: fstest.MapFS{
			"pacto.yaml": &fstest.MapFile{Data: ValidPactoYAML()},
		},
	}
}

// WriteTestBundle creates a valid bundle directory structure in a temp dir
// and returns the bundle directory path.
func WriteTestBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		t.Fatal(err)
	}
	pactoPath := filepath.Join(bundleDir, "pacto.yaml")
	if err := os.WriteFile(pactoPath, ValidPactoYAML(), 0644); err != nil {
		t.Fatal(err)
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
	}
}
