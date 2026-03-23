package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeLocalPactoYAML(t *testing.T, dir, name, version string) {
	t.Helper()
	yaml := `pactoVersion: "1.0"
service:
  name: ` + name + `
  version: ` + version + `
`
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSource_ListServices(t *testing.T) {
	root := t.TempDir()

	writeLocalPactoYAML(t, filepath.Join(root, "api"), "api", "1.0.0")
	writeLocalPactoYAML(t, filepath.Join(root, "worker"), "worker", "2.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	// Should be sorted alphabetically
	if services[0].Name != "api" {
		t.Errorf("expected first service 'api', got %q", services[0].Name)
	}
	if services[1].Name != "worker" {
		t.Errorf("expected second service 'worker', got %q", services[1].Name)
	}
	if services[0].Source != "local" {
		t.Errorf("expected source 'local', got %q", services[0].Source)
	}
}

func TestLocalSource_ListServices_RootPactoYAML(t *testing.T) {
	root := t.TempDir()

	// Place pacto.yaml at root level (no subdirectory)
	writeLocalPactoYAML(t, root, "root-svc", "1.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service (from root), got %d", len(services))
	}
	if services[0].Name != "root-svc" {
		t.Errorf("expected 'root-svc', got %q", services[0].Name)
	}
}

func TestLocalSource_ListServices_BothRootAndSubdir(t *testing.T) {
	root := t.TempDir()

	writeLocalPactoYAML(t, root, "root-svc", "1.0.0")
	writeLocalPactoYAML(t, filepath.Join(root, "sub"), "sub-svc", "2.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
}

func TestLocalSource_ListServices_EmptyDir(t *testing.T) {
	root := t.TempDir()

	src := NewLocalSource(root)
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(services))
	}
}

func TestLocalSource_ListServices_SkipsInvalidDirs(t *testing.T) {
	root := t.TempDir()

	writeLocalPactoYAML(t, filepath.Join(root, "valid"), "valid-svc", "1.0.0")
	// Create a subdirectory without pacto.yaml
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create a file (not a directory)
	if err := os.WriteFile(filepath.Join(root, "not-a-dir.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewLocalSource(root)
	ctx := context.Background()

	services, err := src.ListServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
}

func TestLocalSource_GetService_Found(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "api"), "api", "1.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	details, err := src.GetService(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "api" {
		t.Errorf("expected name 'api', got %q", details.Name)
	}
	if details.Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", details.Version)
	}
	if details.Source != "local" {
		t.Errorf("expected source 'local', got %q", details.Source)
	}
}

func TestLocalSource_GetService_NotFound(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "api"), "api", "1.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	_, err := src.GetService(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestLocalSource_GetService_RootLevel(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, root, "root-svc", "1.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	details, err := src.GetService(ctx, "root-svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "root-svc" {
		t.Errorf("expected 'root-svc', got %q", details.Name)
	}
}

func TestLocalSource_GetVersions(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "api"), "api", "3.2.1")

	src := NewLocalSource(root)
	ctx := context.Background()

	versions, err := src.GetVersions(ctx, "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].Version != "3.2.1" {
		t.Errorf("expected version '3.2.1', got %q", versions[0].Version)
	}
}

func TestLocalSource_GetVersions_NotFound(t *testing.T) {
	root := t.TempDir()
	src := NewLocalSource(root)
	ctx := context.Background()

	_, err := src.GetVersions(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestLocalSource_GetDiff(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "svc-a"), "svc-a", "1.0.0")
	writeLocalPactoYAML(t, filepath.Join(root, "svc-b"), "svc-b", "2.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	a := Ref{Name: "svc-a", Version: "1.0.0"}
	b := Ref{Name: "svc-b", Version: "2.0.0"}

	result, err := src.GetDiff(ctx, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil diff result")
	}
	if result.From.Name != "svc-a" {
		t.Errorf("expected from name 'svc-a', got %q", result.From.Name)
	}
}

func TestLocalSource_GetDiff_NotFound(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "svc-a"), "svc-a", "1.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	a := Ref{Name: "svc-a", Version: "1.0.0"}
	b := Ref{Name: "missing", Version: "1.0.0"}

	_, err := src.GetDiff(ctx, a, b)
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
}

func TestLocalSource_ImplementsDataSource(t *testing.T) {
	root := t.TempDir()
	src := NewLocalSource(root)
	var _ DataSource = src
}

func TestLocalSource_FindBundle_ReadDirError(t *testing.T) {
	// Use a non-existent root so that ReadDir fails.
	src := NewLocalSource("/nonexistent/path/that/does/not/exist")
	_, err := src.findBundle("any-service")
	if err == nil {
		t.Fatal("expected error when root directory does not exist")
	}
}

func TestLocalSource_LoadLocalBundle_InvalidYAML(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "bad")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write invalid YAML that will fail contract.Parse.
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("not: [valid: yaml: {{"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadLocalBundle(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLocalSource_GetDiff_FromFindBundleError(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "svc-b"), "svc-b", "1.0.0")

	src := NewLocalSource(root)
	ctx := context.Background()

	// "from" service doesn't exist.
	a := Ref{Name: "missing", Version: "1.0.0"}
	b := Ref{Name: "svc-b", Version: "1.0.0"}

	_, err := src.GetDiff(ctx, a, b)
	if err == nil {
		t.Fatal("expected error when 'from' bundle is not found")
	}
}

func TestLocalSource_FindBundle_RootNameMismatch(t *testing.T) {
	// Root has a valid pacto.yaml but with a different name.
	// findBundle should fall through to subdirectory search.
	root := t.TempDir()
	writeLocalPactoYAML(t, root, "root-svc", "1.0.0")
	writeLocalPactoYAML(t, filepath.Join(root, "sub"), "sub-svc", "2.0.0")

	src := NewLocalSource(root)
	details, err := src.GetService(context.Background(), "sub-svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "sub-svc" {
		t.Errorf("expected 'sub-svc', got %q", details.Name)
	}
}

func TestLocalSource_FindBundle_SkipsFiles(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "svc"), "svc", "1.0.0")
	// Create a regular file (not a directory) that should be skipped during findBundle.
	if err := os.WriteFile(filepath.Join(root, "not-a-dir.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewLocalSource(root)
	details, err := src.GetService(context.Background(), "svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "svc" {
		t.Errorf("expected 'svc', got %q", details.Name)
	}
}

func TestLocalSource_FindBundle_SkipsInvalidSubdirs(t *testing.T) {
	root := t.TempDir()
	writeLocalPactoYAML(t, filepath.Join(root, "valid"), "valid-svc", "1.0.0")
	// Create subdirectory without pacto.yaml — findBundle should skip it.
	if err := os.MkdirAll(filepath.Join(root, "invalid"), 0755); err != nil {
		t.Fatal(err)
	}

	src := NewLocalSource(root)
	details, err := src.GetService(context.Background(), "valid-svc")
	if err != nil {
		t.Fatal(err)
	}
	if details.Name != "valid-svc" {
		t.Errorf("expected 'valid-svc', got %q", details.Name)
	}
}

func TestLocalSource_ListServices_ReadDirError(t *testing.T) {
	src := NewLocalSource("/nonexistent/path/that/does/not/exist")
	ctx := context.Background()

	_, err := src.ListServices(ctx)
	if err == nil {
		t.Fatal("expected error when root directory does not exist")
	}
}
