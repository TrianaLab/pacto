package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/oci"
	"github.com/trianalab/pacto/v2/pkg/override"
)

func TestPush_Success(t *testing.T) {
	dir := writeTestBundle(t)
	store := &mockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", &oci.ArtifactNotFoundError{Ref: "ghcr.io/acme/svc:1.0.0"}
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir})
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
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: "."})
	if err == nil {
		t.Error("expected error for nil store")
	}
}

func TestPush_InvalidContract(t *testing.T) {
	dir := writeInvalidBundle(t)
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir})
	if err == nil {
		t.Error("expected error for invalid contract")
	}
}

func TestPush_FileNotFound(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: "/nonexistent/dir"})
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestPush_StoreError(t *testing.T) {
	dir := writeTestBundle(t)
	store := &mockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", &oci.ArtifactNotFoundError{Ref: "ghcr.io/acme/svc:1.0.0"}
		},
		PushFn: func(_ context.Context, _ string, _ *contract.Bundle) (string, error) {
			return "", fmt.Errorf("push failed")
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir})
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
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", &oci.ArtifactNotFoundError{Ref: "ghcr.io/acme/svc:1.0.0"}
		},
		PushFn: func(_ context.Context, ref string, _ *contract.Bundle) (string, error) {
			pushedRef = ref
			return "sha256:abc123", nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc", Path: dir})
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

func TestRejectLocalDeps_LocalRef(t *testing.T) {
	c := &contract.Contract{
		Dependencies: []contract.Dependency{
			{Ref: "../local-dep", Required: true},
		},
	}
	err := rejectLocalDeps(c)
	if err == nil {
		t.Fatal("expected error for local dependency ref")
	}
	if !strings.Contains(err.Error(), "local dependency detected") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRejectLocalDeps_FileScheme(t *testing.T) {
	c := &contract.Contract{
		Dependencies: []contract.Dependency{
			{Ref: "file:///abs/path/dep", Required: true},
		},
	}
	err := rejectLocalDeps(c)
	if err == nil {
		t.Fatal("expected error for file:// dependency ref")
	}
}

func TestRejectLocalDeps_OCIAllowed(t *testing.T) {
	c := &contract.Contract{
		Dependencies: []contract.Dependency{
			{Ref: "oci://ghcr.io/acme/dep:1.0.0", Required: true},
		},
	}
	err := rejectLocalDeps(c)
	if err != nil {
		t.Fatalf("unexpected error for OCI dependency: %v", err)
	}
}

func TestRejectLocalDeps_NoDeps(t *testing.T) {
	c := &contract.Contract{}
	err := rejectLocalDeps(c)
	if err != nil {
		t.Fatalf("unexpected error for no dependencies: %v", err)
	}
}

func TestPush_RejectsLocalDeps(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
dependencies:
  - name: local-dep
    ref: "../local-dep"
    required: true
    compatibility: "^1.0.0"
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
capabilities:
  - type: health
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openapi.yaml"), []byte("openapi: \"3.0.0\"\ninfo:\n  title: API\n  version: 1.0.0\npaths: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir})
	if err == nil {
		t.Fatal("expected error for local dependency")
	}
	if !strings.Contains(err.Error(), "local dependency detected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRejectLocalRefs_ConfigRef(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.Configuration{
			{Name: "cfg", Ref: "../local-config"},
		},
	}
	err := rejectLocalRefs(c)
	if err == nil {
		t.Fatal("expected error for local config ref")
	}
	if !strings.Contains(err.Error(), "local config ref detected") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRejectLocalRefs_PolicyRef(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{
			{Name: "pol", Ref: "./local-policy"},
		},
	}
	err := rejectLocalRefs(c)
	if err == nil {
		t.Fatal("expected error for local policy ref")
	}
	if !strings.Contains(err.Error(), "local policy ref detected") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPush_RejectsLocalRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "../local-path", Path: "."})
	if err == nil {
		t.Error("expected error for local ref")
	}
}

func TestPush_ExplicitTagKept(t *testing.T) {
	dir := writeTestBundle(t)
	var pushedRef string
	store := &mockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", &oci.ArtifactNotFoundError{Ref: "ghcr.io/acme/svc:custom"}
		},
		PushFn: func(_ context.Context, ref string, _ *contract.Bundle) (string, error) {
			pushedRef = ref
			return "sha256:abc123", nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:custom", Path: dir})
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

func TestPush_OverridesPersistInBundle(t *testing.T) {
	dir := writeTestBundle(t)
	var pushedBundle *contract.Bundle
	store := &mockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", &oci.ArtifactNotFoundError{Ref: "ghcr.io/acme/svc:2.0.0"}
		},
		PushFn: func(_ context.Context, _ string, b *contract.Bundle) (string, error) {
			pushedBundle = b
			return "sha256:abc123", nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Push(context.Background(), PushOptions{
		Ref:  "oci://ghcr.io/acme/svc",
		Path: dir,
		Overrides: override.Overrides{
			SetValues: []string{"service.version=2.0.0"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Version != "2.0.0" {
		t.Errorf("expected Version=2.0.0, got %s", result.Version)
	}

	// Verify the bundle FS contains the overridden pacto.yaml
	data, err := fs.ReadFile(pushedBundle.FS, "pacto.yaml")
	if err != nil {
		t.Fatalf("failed to read pacto.yaml from pushed bundle: %v", err)
	}
	if !strings.Contains(string(data), "version: 2.0.0") && !strings.Contains(string(data), `version: "2.0.0"`) {
		t.Errorf("pushed bundle pacto.yaml should contain overridden version, got:\n%s", string(data))
	}

	// Verify non-overridden files are still accessible
	if _, err := fs.ReadFile(pushedBundle.FS, "openapi.yaml"); err != nil {
		t.Errorf("non-overridden file should still be accessible: %v", err)
	}
}

func TestPush_AlreadyExists(t *testing.T) {
	dir := writeTestBundle(t)
	store := &mockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "sha256:existingdigest", nil
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir})
	if err == nil {
		t.Fatal("expected error when artifact already exists")
	}
	if !errors.Is(err, ErrArtifactAlreadyExists) {
		t.Errorf("expected ErrArtifactAlreadyExists, got: %v", err)
	}
	if !strings.Contains(err.Error(), "use --force") {
		t.Errorf("expected hint about --force, got: %v", err)
	}
}

func TestPush_AlreadyExistsWithForce(t *testing.T) {
	dir := writeTestBundle(t)
	store := &mockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "sha256:existingdigest", nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir, Force: true})
	if err != nil {
		t.Fatalf("unexpected error with --force: %v", err)
	}
	if result.Ref != "ghcr.io/acme/svc:1.0.0" {
		t.Errorf("expected Ref=ghcr.io/acme/svc:1.0.0, got %s", result.Ref)
	}
}

func TestPush_ResolveNonNotFoundError(t *testing.T) {
	dir := writeTestBundle(t)
	store := &mockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", &oci.AuthenticationError{Ref: "ghcr.io/acme/svc:1.0.0", Err: fmt.Errorf("401")}
		},
	}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir})
	if err == nil {
		t.Fatal("expected error for authentication failure")
	}
	if errors.Is(err, ErrArtifactAlreadyExists) {
		t.Error("should not be ErrArtifactAlreadyExists for auth errors")
	}
}

func TestPush_RejectsLocalConfigRef(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
configurations:
  - name: default
    ref: "../local-config"
    required: false
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
capabilities:
  - type: health
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openapi.yaml"), []byte("openapi: \"3.0.0\"\ninfo:\n  title: API\n  version: 1.0.0\npaths: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir})
	if err == nil {
		t.Fatal("expected error for local config ref")
	}
	if !strings.Contains(err.Error(), "local config ref detected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPush_RejectsRemotePolicyViolation(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
policies:
  - name: acme
    ref: oci://ghcr.io/acme/policy:1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
capabilities:
  - type: health
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}

	// Policy requires "scaling" which the contract does not have
	policySchema := []byte(`{"type":"object","required":["scaling"]}`)
	store := &mockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) {
			return "", &oci.ArtifactNotFoundError{Ref: "ghcr.io/acme/svc:1.0.0"}
		},
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{
				Contract: &contract.Contract{},
				FS: fstest.MapFS{
					"pacto.yaml":         &fstest.MapFile{Data: []byte(`pactoVersion: "2.0"`)},
					"policy/schema.json": &fstest.MapFile{Data: policySchema},
				},
			}, nil
		},
	}

	svc := NewService(store, nil)
	_, err := svc.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Path: dir})
	if err == nil {
		t.Fatal("expected push to reject contract that violates remote policy")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRejectLocalRefs_NilPolicyAndConfig(t *testing.T) {
	c := &contract.Contract{}
	if err := rejectLocalRefs(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRejectLocalRefs_LocalConfigRef(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.Configuration{{Name: "default", Ref: "file://../config"}},
	}
	err := rejectLocalRefs(c)
	if err == nil {
		t.Fatal("expected error for local config ref")
	}
	if !strings.Contains(err.Error(), "local config ref detected") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRejectLocalRefs_LocalPolicyRef(t *testing.T) {
	c := &contract.Contract{
		Policies: []contract.Policy{{Name: "local", Ref: "../policy"}},
	}
	err := rejectLocalRefs(c)
	if err == nil {
		t.Fatal("expected error for local policy ref")
	}
	if !strings.Contains(err.Error(), "local policy ref detected") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRejectLocalRefs_OCIRefsAllowed(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.Configuration{{Name: "default", Ref: "oci://ghcr.io/acme/config:1.0.0"}},
		Policies:       []contract.Policy{{Name: "remote", Ref: "oci://ghcr.io/acme/policy:1.0.0"}},
	}
	if err := rejectLocalRefs(c); err != nil {
		t.Fatalf("unexpected error for OCI refs: %v", err)
	}
}

func TestRejectLocalRefs_EmptyRefs(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.Configuration{{Name: "default", Schema: "configuration/schema.json"}},
		Policies:       []contract.Policy{{Name: "local", Schema: "policy/schema.json"}},
	}
	if err := rejectLocalRefs(c); err != nil {
		t.Fatalf("unexpected error for empty refs: %v", err)
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"not found error", &oci.ArtifactNotFoundError{Ref: "foo"}, true},
		{"wrapped not found", fmt.Errorf("wrap: %w", &oci.ArtifactNotFoundError{Ref: "foo"}), true},
		{"auth error", &oci.AuthenticationError{Ref: "foo"}, false},
		{"generic error", fmt.Errorf("something"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNotFound(tt.err); got != tt.want {
				t.Errorf("isNotFound() = %v, want %v", got, tt.want)
			}
		})
	}
}
