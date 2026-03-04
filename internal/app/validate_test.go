package app

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestValidate_LocalValid(t *testing.T) {
	dir := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidate_LocalInvalid(t *testing.T) {
	dir := writeInvalidBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result")
	}
}

func TestValidate_DefaultPath(t *testing.T) {
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: ""})
	if err != nil {
		// This may fail because pacto.yaml doesn't exist in cwd, which is fine
		t.Skip("no pacto.yaml in cwd")
	}
	_ = result
}

func TestValidate_FileNotFound(t *testing.T) {
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: "/nonexistent/dir"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return a result with PARSE_ERROR
	if result.Valid {
		t.Error("expected invalid result for nonexistent directory")
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error")
	}
}

func TestValidate_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid, got errors: %v", result.Errors)
	}
}

func TestValidate_OCIRef_MissingPactoYAML(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			b := testBundle()
			b.FS = fstest.MapFS{} // empty FS, no pacto.yaml
			return b, nil
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Validate(context.Background(), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err == nil {
		t.Error("expected error when pacto.yaml missing from OCI bundle FS")
	}
}

func TestValidate_OCIRef_NilStore(t *testing.T) {
	svc := NewService(nil, nil)
	result, err := svc.Validate(context.Background(), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Valid {
		t.Error("expected invalid result for nil store")
	}
}
