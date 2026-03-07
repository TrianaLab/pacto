package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestPull_Success(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "pulled")
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Pull(context.Background(), PullOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Output: output})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ref != "ghcr.io/acme/svc:1.0.0" {
		t.Errorf("expected Ref=ghcr.io/acme/svc:1.0.0, got %s", result.Ref)
	}
	if result.Output != output {
		t.Errorf("expected Output=%s, got %s", output, result.Output)
	}
	if result.Name != "test-svc" {
		t.Errorf("expected Name=test-svc, got %s", result.Name)
	}
	if result.Version != "1.0.0" {
		t.Errorf("expected Version=1.0.0, got %s", result.Version)
	}
	// Verify extracted file
	if _, err := os.Stat(filepath.Join(output, "pacto.yaml")); err != nil {
		t.Fatalf("expected pacto.yaml in output: %v", err)
	}
}

func TestPull_DefaultOutput(t *testing.T) {
	orig, _ := os.Getwd()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Pull(context.Background(), PullOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "test-svc" {
		t.Errorf("expected Output=test-svc (service name), got %s", result.Output)
	}
}

func TestPull_NilStore(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Pull(context.Background(), PullOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0"})
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestPull_StoreError(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return nil, fmt.Errorf("pull failed")
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Pull(context.Background(), PullOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0"})
	if err == nil {
		t.Error("expected error from store")
	}
}

func TestPull_RejectsLocalRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	_, err := svc.Pull(context.Background(), PullOptions{Ref: "../local-path"})
	if err == nil {
		t.Error("expected error for local ref")
	}
}

func TestPull_ExtractError(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			b := testBundle()
			b.FS = &errFS{} // FS that errors on WalkDir
			return b, nil
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Pull(context.Background(), PullOptions{
		Ref:    "oci://ghcr.io/acme/svc:1.0.0",
		Output: "/dev/null/impossible",
	})
	if err == nil {
		t.Error("expected error when extractBundleFS fails")
	}
}
