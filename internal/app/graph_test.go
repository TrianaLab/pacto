package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/internal/testutil"
	"github.com/trianalab/pacto/pkg/contract"
)

func TestGraph_Local(t *testing.T) {
	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Graph(context.Background(), GraphOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Root == nil {
		t.Fatal("expected non-nil root")
	}
	if result.Root.Name != "test-svc" {
		t.Errorf("expected root Name=test-svc, got %s", result.Root.Name)
	}
}

func TestGraph_WithStore(t *testing.T) {
	path := writeTestBundle(t)
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Graph(context.Background(), GraphOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Root == nil {
		t.Fatal("expected non-nil root")
	}
}

func TestGraph_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Graph(context.Background(), GraphOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Root == nil {
		t.Fatal("expected non-nil root")
	}
}

func TestGraph_ResolveError(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Graph(context.Background(), GraphOptions{Path: "/nonexistent/pacto.yaml"})
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestDepFetcher_OCISuccess(t *testing.T) {
	store := &mockBundleStore{}
	f := &depFetcher{store: store, baseDir: ""}
	c, err := f.Fetch(context.Background(), "oci://ghcr.io/acme/svc:1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %s", c.Service.Name)
	}
}

func TestDepFetcher_OCIError(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return nil, fmt.Errorf("pull failed")
		},
	}
	f := &depFetcher{store: store, baseDir: ""}
	_, err := f.Fetch(context.Background(), "oci://ghcr.io/acme/svc:1.0.0")
	if err == nil {
		t.Error("expected error from store")
	}
}

func TestDepFetcher_OCINilStore(t *testing.T) {
	f := &depFetcher{store: nil, baseDir: ""}
	_, err := f.Fetch(context.Background(), "oci://ghcr.io/acme/svc:1.0.0")
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestDepFetcher_LocalSuccess(t *testing.T) {
	dir := writeTestBundle(t)
	f := &depFetcher{baseDir: filepath.Dir(dir)}
	c, err := f.Fetch(context.Background(), filepath.Base(dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %s", c.Service.Name)
	}
}

func TestDepFetcher_LocalAbsPath(t *testing.T) {
	dir := writeTestBundle(t)
	f := &depFetcher{baseDir: ""}
	c, err := f.Fetch(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %s", c.Service.Name)
	}
}

func TestDepFetcher_FileScheme(t *testing.T) {
	dir := writeTestBundle(t)
	f := &depFetcher{baseDir: ""}
	c, err := f.Fetch(context.Background(), "file://"+dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Service.Name != "test-svc" {
		t.Errorf("expected test-svc, got %s", c.Service.Name)
	}
}

func TestDepFetcher_LocalError(t *testing.T) {
	f := &depFetcher{baseDir: "/nonexistent"}
	_, err := f.Fetch(context.Background(), "missing")
	if err == nil {
		t.Error("expected error for nonexistent local path")
	}
}

func TestGraph_LocalDependency(t *testing.T) {
	// Create a dependency bundle
	depDir := t.TempDir()
	depBundleDir := filepath.Join(depDir, "dep-svc")
	if err := os.MkdirAll(depBundleDir, 0755); err != nil {
		t.Fatal(err)
	}
	depYAML := []byte(`pactoVersion: "1.0"
service:
  name: dep-svc
  version: "2.0.0"
interfaces:
  - name: api
    type: http
    port: 9090
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
	if err := os.WriteFile(filepath.Join(depBundleDir, "pacto.yaml"), depYAML, 0644); err != nil {
		t.Fatal(err)
	}

	// Create main bundle that depends on the local dep
	mainBundleDir := filepath.Join(depDir, "main-svc")
	if err := os.MkdirAll(mainBundleDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainYAML := []byte(`pactoVersion: "1.0"
service:
  name: main-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
dependencies:
  - ref: ../dep-svc
    required: true
    compatibility: "^2.0.0"
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
	if err := os.WriteFile(filepath.Join(mainBundleDir, "pacto.yaml"), mainYAML, 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(nil, nil)
	result, err := svc.Graph(context.Background(), GraphOptions{Path: mainBundleDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Root.Name != "main-svc" {
		t.Errorf("expected root main-svc, got %s", result.Root.Name)
	}
	if len(result.Root.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Root.Dependencies))
	}
	edge := result.Root.Dependencies[0]
	if edge.Node == nil {
		t.Fatal("expected resolved node")
	}
	if edge.Node.Name != "dep-svc" {
		t.Errorf("expected dep-svc, got %s", edge.Node.Name)
	}
	if !edge.Local {
		t.Error("expected edge to be marked as local")
	}
	if !edge.Node.Local {
		t.Error("expected node to be marked as local")
	}
}

func TestGraph_FileSchemeDepependency(t *testing.T) {
	depDir := t.TempDir()
	depBundleDir := filepath.Join(depDir, "dep-svc")
	if err := os.MkdirAll(depBundleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depBundleDir, "pacto.yaml"), testutil.ValidPactoYAML(), 0644); err != nil {
		t.Fatal(err)
	}

	mainBundleDir := filepath.Join(depDir, "main-svc")
	if err := os.MkdirAll(mainBundleDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainYAML := []byte(fmt.Sprintf(`pactoVersion: "1.0"
service:
  name: main-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
dependencies:
  - ref: "file://%s"
    required: true
    compatibility: "^1.0.0"
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
`, depBundleDir))
	if err := os.WriteFile(filepath.Join(mainBundleDir, "pacto.yaml"), mainYAML, 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(nil, nil)
	result, err := svc.Graph(context.Background(), GraphOptions{Path: mainBundleDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Root.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Root.Dependencies))
	}
	if result.Root.Dependencies[0].Node == nil {
		t.Fatal("expected resolved node for file:// ref")
	}
}

func TestNewDepFetcher_OCIBaseRef(t *testing.T) {
	svc := NewService(nil, nil)
	fetcher := svc.newDepFetcher("oci://ghcr.io/acme/svc:1.0.0")
	df := fetcher.(*depFetcher)
	if df.baseDir != "" {
		t.Errorf("expected empty baseDir for OCI ref, got %q", df.baseDir)
	}
}
