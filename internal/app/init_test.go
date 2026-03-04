package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_ReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnlyDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0755) })

	orig, _ := os.Getwd()
	if err := os.Chdir(readOnlyDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc := NewService(nil, nil)
	_, err := svc.Init(context.Background(), InitOptions{Name: "new-svc"})
	if err == nil {
		t.Error("expected error when creating dirs in read-only directory")
	}
}

func TestInit_Success(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	svc := NewService(nil, nil)
	result, err := svc.Init(context.Background(), InitOptions{Name: "my-svc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Dir != "my-svc" {
		t.Errorf("expected Dir=my-svc, got %s", result.Dir)
	}
	if result.Path != filepath.Join("my-svc", "pacto.yaml") {
		t.Errorf("expected Path=%s, got %s", filepath.Join("my-svc", "pacto.yaml"), result.Path)
	}

	// Verify directory structure
	for _, sub := range []string{"", "interfaces", "configuration"} {
		p := filepath.Join(dir, "my-svc", sub)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", p)
		}
	}

	// Verify pacto.yaml exists
	if _, err := os.Stat(filepath.Join(dir, "my-svc", "pacto.yaml")); err != nil {
		t.Fatalf("expected pacto.yaml: %v", err)
	}

	// Verify openapi.yaml exists
	if _, err := os.Stat(filepath.Join(dir, "my-svc", "interfaces", "openapi.yaml")); err != nil {
		t.Fatalf("expected openapi.yaml: %v", err)
	}

	// Verify schema.json exists
	if _, err := os.Stat(filepath.Join(dir, "my-svc", "configuration", "schema.json")); err != nil {
		t.Fatalf("expected schema.json: %v", err)
	}

}

func TestInit_EmptyName(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Init(context.Background(), InitOptions{Name: ""})
	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestInit_DirectoryExists(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	if err := os.Mkdir("existing", 0755); err != nil {
		t.Fatal(err)
	}

	svc := NewService(nil, nil)
	_, err := svc.Init(context.Background(), InitOptions{Name: "existing"})
	if err == nil {
		t.Error("expected error for existing directory")
	}
}

func TestInit_WriteFileErrors(t *testing.T) {
	for _, failFile := range []string{"pacto.yaml", "openapi.yaml", "schema.json"} {
		t.Run(failFile, func(t *testing.T) {
			orig, _ := os.Getwd()
			dir := t.TempDir()
			if err := os.Chdir(dir); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chdir(orig) }()

			old := writeFileFn
			writeFileFn = func(name string, data []byte, perm os.FileMode) error {
				if strings.Contains(name, failFile) {
					return fmt.Errorf("write failed")
				}
				return os.WriteFile(name, data, perm)
			}
			defer func() { writeFileFn = old }()

			svc := NewService(nil, nil)
			_, err := svc.Init(context.Background(), InitOptions{Name: "test-svc"})
			if err == nil {
				t.Errorf("expected error when writing %s fails", failFile)
			}
		})
	}
}
