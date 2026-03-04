package app

import (
	"context"
	"testing"
)

func TestDiff_LocalFiles(t *testing.T) {
	oldDir := writeTestBundle(t)
	newDir := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OldPath != oldDir {
		t.Errorf("expected OldPath=%s, got %s", oldDir, result.OldPath)
	}
	if result.NewPath != newDir {
		t.Errorf("expected NewPath=%s, got %s", newDir, result.NewPath)
	}
	if result.Classification == "" {
		t.Error("expected non-empty classification")
	}
}

func TestDiff_OldPathError(t *testing.T) {
	newDir := writeTestBundle(t)
	svc := NewService(nil, nil)
	_, err := svc.Diff(context.Background(), DiffOptions{OldPath: "/nonexistent/dir", NewPath: newDir})
	if err == nil {
		t.Error("expected error for nonexistent old path")
	}
}

func TestDiff_NewPathError(t *testing.T) {
	oldDir := writeTestBundle(t)
	svc := NewService(nil, nil)
	_, err := svc.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: "/nonexistent/dir"})
	if err == nil {
		t.Error("expected error for nonexistent new path")
	}
}

func TestDiff_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Diff(context.Background(), DiffOptions{
		OldPath: "oci://ghcr.io/acme/svc:1.0.0",
		NewPath: "oci://ghcr.io/acme/svc:2.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Classification == "" {
		t.Error("expected non-empty classification")
	}
}
