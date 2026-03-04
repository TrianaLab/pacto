package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestPush_Success(t *testing.T) {
	dir := writeTestBundle(t)
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Push(context.Background(), PushOptions{Ref: "ghcr.io/acme/svc:1.0.0", Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ref != "ghcr.io/acme/svc:1.0.0" {
		t.Errorf("expected Ref=ghcr.io/acme/svc:1.0.0, got %s", result.Ref)
	}
	if result.Digest != "sha256:abc123" {
		t.Errorf("expected Digest=sha256:abc123, got %s", result.Digest)
	}
	if result.Name != "test-svc" {
		t.Errorf("expected Name=test-svc, got %s", result.Name)
	}
	if result.Version != "1.0.0" {
		t.Errorf("expected Version=1.0.0, got %s", result.Version)
	}
}

func TestPush_NilStore(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "ghcr.io/acme/svc:1.0.0", Path: "."})
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestPush_InvalidContract(t *testing.T) {
	dir := writeInvalidBundle(t)
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "ghcr.io/acme/svc:1.0.0", Path: dir})
	if err == nil {
		t.Error("expected error for invalid contract")
	}
}

func TestPush_FileNotFound(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "ghcr.io/acme/svc:1.0.0", Path: "/nonexistent/dir"})
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestPush_StoreError(t *testing.T) {
	dir := writeTestBundle(t)
	store := &mockBundleStore{
		PushFn: func(_ context.Context, _ string, _ *contract.Bundle) (string, error) {
			return "", fmt.Errorf("push failed")
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "ghcr.io/acme/svc:1.0.0", Path: dir})
	if err == nil {
		t.Error("expected error from store")
	}
}

func TestHasTagOrDigest(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"ghcr.io/acme/svc", false},
		{"ghcr.io/acme/svc:1.0", true},
		{"ghcr.io/acme/svc@sha256:abc", true},
		{"localhost:5000/repo", false},
		{"localhost:5000/repo:v1", true},
		{"myrepo", false},
		{"myrepo:latest", true},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			if got := hasTagOrDigest(tt.ref); got != tt.want {
				t.Errorf("hasTagOrDigest(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestPush_AutoTagFromVersion(t *testing.T) {
	dir := writeTestBundle(t)
	var pushedRef string
	store := &mockBundleStore{
		PushFn: func(_ context.Context, ref string, _ *contract.Bundle) (string, error) {
			pushedRef = ref
			return "sha256:abc123", nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Push(context.Background(), PushOptions{Ref: "ghcr.io/acme/svc", Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ref != "ghcr.io/acme/svc:1.0.0" {
		t.Errorf("expected Ref=ghcr.io/acme/svc:1.0.0, got %s", result.Ref)
	}
	if pushedRef != "ghcr.io/acme/svc:1.0.0" {
		t.Errorf("expected store to receive ref ghcr.io/acme/svc:1.0.0, got %s", pushedRef)
	}
}

func TestPush_ExplicitTagKept(t *testing.T) {
	dir := writeTestBundle(t)
	var pushedRef string
	store := &mockBundleStore{
		PushFn: func(_ context.Context, ref string, _ *contract.Bundle) (string, error) {
			pushedRef = ref
			return "sha256:abc123", nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Push(context.Background(), PushOptions{Ref: "ghcr.io/acme/svc:custom", Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ref != "ghcr.io/acme/svc:custom" {
		t.Errorf("expected Ref=ghcr.io/acme/svc:custom, got %s", result.Ref)
	}
	if pushedRef != "ghcr.io/acme/svc:custom" {
		t.Errorf("expected store to receive ref ghcr.io/acme/svc:custom, got %s", pushedRef)
	}
}
