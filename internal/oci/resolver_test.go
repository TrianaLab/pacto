package oci_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
)

// mockStore implements oci.BundleStore for resolver tests.
type mockStore struct {
	bundle  *contract.Bundle
	pullErr error
}

func (m *mockStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}

func (m *mockStore) Resolve(context.Context, string) (string, error) { return "", nil }

func (m *mockStore) Pull(_ context.Context, _ string) (*contract.Bundle, error) {
	if m.pullErr != nil {
		return nil, m.pullErr
	}
	return m.bundle, nil
}

func (m *mockStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }

func TestResolver_CacheHit(t *testing.T) {
	bundle := newTestBundle()
	store := &mockStore{bundle: bundle}
	resolver := oci.NewResolver(store)

	b, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.LocalOnly)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
}

func TestResolver_CacheMissSuccessfulPull(t *testing.T) {
	bundle := newTestBundle()
	store := &mockStore{bundle: bundle}
	resolver := oci.NewResolver(store)

	b, err := resolver.Resolve(context.Background(), "oci://ghcr.io/org/svc:1.0.0", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
}

func TestResolver_CacheMissAuthFailure(t *testing.T) {
	store := &mockStore{
		pullErr: &oci.AuthenticationError{Ref: "ghcr.io/org/svc:1.0.0", Err: fmt.Errorf("401")},
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var authErr *oci.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Errorf("expected AuthenticationError, got %T: %v", err, err)
	}
}

func TestResolver_CacheMissArtifactNotFound(t *testing.T) {
	store := &mockStore{
		pullErr: &oci.ArtifactNotFoundError{Ref: "ghcr.io/org/svc:1.0.0", Err: fmt.Errorf("404")},
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var notFoundErr *oci.ArtifactNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Errorf("expected ArtifactNotFoundError, got %T: %v", err, err)
	}
}

func TestResolver_CacheMissRegistryUnreachable(t *testing.T) {
	store := &mockStore{
		pullErr: &oci.RegistryUnreachableError{Ref: "ghcr.io/org/svc:1.0.0", Err: fmt.Errorf("network error")},
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var unreachableErr *oci.RegistryUnreachableError
	if !errors.As(err, &unreachableErr) {
		t.Errorf("expected RegistryUnreachableError, got %T: %v", err, err)
	}
}

func TestResolver_InvalidBundle(t *testing.T) {
	store := &mockStore{
		pullErr: fmt.Errorf("failed to extract bundle: invalid tar"),
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invalidErr *oci.InvalidBundleError
	if !errors.As(err, &invalidErr) {
		t.Errorf("expected InvalidBundleError, got %T: %v", err, err)
	}
}

func TestResolver_InvalidRef(t *testing.T) {
	store := &mockStore{
		pullErr: fmt.Errorf("invalid reference \"bad-ref:1.0.0\": whatever"),
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "bad-ref:1.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invalidRefErr *oci.InvalidRefError
	if !errors.As(err, &invalidRefErr) {
		t.Errorf("expected InvalidRefError, got %T: %v", err, err)
	}
}

func TestResolver_LocalOnly_CacheMiss(t *testing.T) {
	store := &mockStore{
		pullErr: fmt.Errorf("not found"),
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.LocalOnly)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var notFoundErr *oci.ArtifactNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Errorf("expected ArtifactNotFoundError, got %T: %v", err, err)
	}
}

func TestResolver_NilContract(t *testing.T) {
	store := &mockStore{bundle: &contract.Bundle{}}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invalidErr *oci.InvalidBundleError
	if !errors.As(err, &invalidErr) {
		t.Errorf("expected InvalidBundleError, got %T: %v", err, err)
	}
}

func TestResolver_StripsOCIPrefix(t *testing.T) {
	bundle := newTestBundle()
	var pulledRef string
	store := &refCapturingStore{bundle: bundle, capturedRef: &pulledRef}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "oci://ghcr.io/org/svc:1.0.0", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pulledRef != "ghcr.io/org/svc:1.0.0" {
		t.Errorf("expected stripped ref %q, got %q", "ghcr.io/org/svc:1.0.0", pulledRef)
	}
}

type refCapturingStore struct {
	bundle      *contract.Bundle
	capturedRef *string
}

func (s *refCapturingStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *refCapturingStore) Resolve(context.Context, string) (string, error) { return "", nil }
func (s *refCapturingStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	*s.capturedRef = ref
	return s.bundle, nil
}
func (s *refCapturingStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }

func TestInvalidRefError_Error(t *testing.T) {
	innerErr := fmt.Errorf("parse error")
	err := &oci.InvalidRefError{
		Ref: "bad-ref",
		Err: innerErr,
	}
	expected := `invalid OCI reference "bad-ref": parse error`
	if err.Error() != expected {
		t.Errorf("got %q, want %q", err.Error(), expected)
	}
}

func TestInvalidRefError_Unwrap(t *testing.T) {
	innerErr := fmt.Errorf("parse error")
	err := &oci.InvalidRefError{
		Ref: "bad-ref",
		Err: innerErr,
	}
	if errors.Unwrap(err) != innerErr {
		t.Errorf("Unwrap() did not return the wrapped error")
	}
}

func TestInvalidBundleError_Error(t *testing.T) {
	innerErr := fmt.Errorf("extraction failed")
	err := &oci.InvalidBundleError{
		Ref: "ghcr.io/org/svc:1.0.0",
		Err: innerErr,
	}
	expected := "artifact at ghcr.io/org/svc:1.0.0 is not a valid Pacto bundle: extraction failed"
	if err.Error() != expected {
		t.Errorf("got %q, want %q", err.Error(), expected)
	}
}

func TestInvalidBundleError_Unwrap(t *testing.T) {
	innerErr := fmt.Errorf("extraction failed")
	err := &oci.InvalidBundleError{
		Ref: "ghcr.io/org/svc:1.0.0",
		Err: innerErr,
	}
	if errors.Unwrap(err) != innerErr {
		t.Errorf("Unwrap() did not return the wrapped error")
	}
}

// tagMockStore extends mockStore with configurable ListTags and ref-capturing Pull.
type tagMockStore struct {
	bundle      *contract.Bundle
	pullErr     error
	tags        []string
	tagsErr     error
	lastPullRef string
}

func (m *tagMockStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (m *tagMockStore) Resolve(context.Context, string) (string, error) { return "", nil }
func (m *tagMockStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	m.lastPullRef = ref
	if m.pullErr != nil {
		return nil, m.pullErr
	}
	return m.bundle, nil
}
func (m *tagMockStore) ListTags(_ context.Context, _ string) ([]string, error) {
	return m.tags, m.tagsErr
}

func TestResolver_ResolveConstrained_UntaggedWithConstraint(t *testing.T) {
	bundle := newTestBundle()
	store := &tagMockStore{
		bundle: bundle,
		tags:   []string{"1.0.0", "2.0.0", "2.1.0", "3.0.0"},
	}
	resolver := oci.NewResolver(store)

	b, err := resolver.ResolveConstrained(context.Background(), "ghcr.io/org/svc", "^2.0.0", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
	// Should have resolved to 2.1.0 (highest matching ^2.0.0)
	if store.lastPullRef != "ghcr.io/org/svc:2.1.0" {
		t.Errorf("expected pull ref ghcr.io/org/svc:2.1.0, got %q", store.lastPullRef)
	}
}

func TestResolver_ResolveConstrained_NoMatchingVersion(t *testing.T) {
	store := &tagMockStore{
		tags: []string{"1.0.0", "1.1.0"},
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.ResolveConstrained(context.Background(), "ghcr.io/org/svc", "^5.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var noMatchErr *oci.NoMatchingVersionError
	if !errors.As(err, &noMatchErr) {
		t.Errorf("expected NoMatchingVersionError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "^5.0.0") {
		t.Errorf("expected error to contain constraint, got %v", err)
	}
}

func TestResolver_ResolveConstrained_UntaggedNoConstraint(t *testing.T) {
	bundle := newTestBundle()
	store := &tagMockStore{
		bundle: bundle,
		tags:   []string{"1.0.0", "3.0.0", "2.0.0"},
	}
	resolver := oci.NewResolver(store)

	b, err := resolver.ResolveConstrained(context.Background(), "ghcr.io/org/svc", "", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b == nil {
		t.Fatal("expected bundle, got nil")
	}
	// Should have resolved to 3.0.0 (highest)
	if store.lastPullRef != "ghcr.io/org/svc:3.0.0" {
		t.Errorf("expected pull ref ghcr.io/org/svc:3.0.0, got %q", store.lastPullRef)
	}
}

func TestResolver_ResolveConstrained_UntaggedNoTags(t *testing.T) {
	store := &tagMockStore{
		tags: []string{},
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.ResolveConstrained(context.Background(), "ghcr.io/org/svc", "", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var notFoundErr *oci.ArtifactNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Errorf("expected ArtifactNotFoundError, got %T: %v", err, err)
	}
}

func TestResolver_ResolveConstrained_TaggedIgnoresConstraint(t *testing.T) {
	bundle := newTestBundle()
	store := &tagMockStore{
		bundle: bundle,
		tags:   []string{"1.0.0"}, // shouldn't be used
	}
	resolver := oci.NewResolver(store)

	b, err := resolver.ResolveConstrained(context.Background(), "ghcr.io/org/svc:1.0.0", "^5.0.0", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b == nil {
		t.Fatal("expected bundle, got nil")
	}
	// Should have pulled the explicit tag, ignoring the constraint
	if store.lastPullRef != "ghcr.io/org/svc:1.0.0" {
		t.Errorf("expected pull ref ghcr.io/org/svc:1.0.0, got %q", store.lastPullRef)
	}
}

func TestResolver_ListVersions(t *testing.T) {
	store := &tagMockStore{
		tags: []string{"v1.0.0", "latest", "2.0.0", "abc", "1.5.0"},
	}
	resolver := oci.NewResolver(store)

	versions, err := resolver.ListVersions(context.Background(), "oci://ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return only semver tags, sorted descending
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d: %v", len(versions), versions)
	}
	if versions[0] != "2.0.0" {
		t.Errorf("expected first version 2.0.0, got %q", versions[0])
	}
	if versions[1] != "1.5.0" {
		t.Errorf("expected second version 1.5.0, got %q", versions[1])
	}
	if versions[2] != "v1.0.0" {
		t.Errorf("expected third version v1.0.0, got %q", versions[2])
	}
}

func TestResolver_ListVersions_Error(t *testing.T) {
	store := &tagMockStore{
		tagsErr: fmt.Errorf("registry error"),
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.ListVersions(context.Background(), "ghcr.io/org/svc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNoMatchingVersionError_Error(t *testing.T) {
	err := &oci.NoMatchingVersionError{
		Ref:        "ghcr.io/org/svc",
		Constraint: "^5.0.0",
		Err:        fmt.Errorf("no tags satisfy constraint"),
	}
	expected := `no versions of ghcr.io/org/svc match constraint "^5.0.0": no tags satisfy constraint`
	if err.Error() != expected {
		t.Errorf("got %q, want %q", err.Error(), expected)
	}
}

func TestNoMatchingVersionError_Unwrap(t *testing.T) {
	innerErr := fmt.Errorf("inner")
	err := &oci.NoMatchingVersionError{Ref: "r", Constraint: "c", Err: innerErr}
	if errors.Unwrap(err) != innerErr {
		t.Errorf("Unwrap() did not return the wrapped error")
	}
}

func TestResolver_ResolveConstrained_LocalOnly(t *testing.T) {
	bundle := newTestBundle()
	store := &tagMockStore{bundle: bundle}
	resolver := oci.NewResolver(store)

	b, err := resolver.ResolveConstrained(context.Background(), "oci://ghcr.io/org/svc:1.0.0", "^1.0.0", oci.LocalOnly)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Contract.Service.Name != "test-svc" {
		t.Errorf("got name %q, want test-svc", b.Contract.Service.Name)
	}
}

func TestResolver_ResolveLocal_NilContract(t *testing.T) {
	store := &mockStore{bundle: &contract.Bundle{}}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.LocalOnly)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var invalidErr *oci.InvalidBundleError
	if !errors.As(err, &invalidErr) {
		t.Errorf("expected InvalidBundleError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "bundle has no contract") {
		t.Errorf("expected error to mention 'bundle has no contract', got %v", err)
	}
}

// pullTrackingStore tracks which refs were pulled.
type pullTrackingStore struct {
	bundle     *contract.Bundle
	tags       []string
	pulledRefs []string
}

func (s *pullTrackingStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *pullTrackingStore) Resolve(context.Context, string) (string, error) { return "", nil }
func (s *pullTrackingStore) Pull(_ context.Context, ref string) (*contract.Bundle, error) {
	s.pulledRefs = append(s.pulledRefs, ref)
	return s.bundle, nil
}
func (s *pullTrackingStore) ListTags(_ context.Context, _ string) ([]string, error) {
	return s.tags, nil
}

func TestResolver_FetchAllVersions_PullsAllSemverTags(t *testing.T) {
	bundle := newTestBundle()
	store := &pullTrackingStore{
		bundle: bundle,
		tags:   []string{"v1.0.0", "latest", "2.0.0", "abc", "1.5.0"},
	}
	resolver := oci.NewResolver(store)

	versions, err := resolver.FetchAllVersions(context.Background(), "ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return only semver tags, sorted descending.
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d: %v", len(versions), versions)
	}
	if versions[0] != "2.0.0" {
		t.Errorf("expected first version 2.0.0, got %q", versions[0])
	}

	// Should have pulled all 3 semver versions.
	if len(store.pulledRefs) != 3 {
		t.Fatalf("expected 3 pulls, got %d: %v", len(store.pulledRefs), store.pulledRefs)
	}
	// Verify each pull ref includes the tag.
	for _, ref := range store.pulledRefs {
		if !strings.Contains(ref, ":") {
			t.Errorf("expected ref with tag, got %q", ref)
		}
	}
}

func TestResolver_FetchAllVersions_StripsOCIPrefix(t *testing.T) {
	bundle := newTestBundle()
	store := &pullTrackingStore{
		bundle: bundle,
		tags:   []string{"1.0.0"},
	}
	resolver := oci.NewResolver(store)

	versions, err := resolver.FetchAllVersions(context.Background(), "oci://ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if store.pulledRefs[0] != "ghcr.io/org/svc:1.0.0" {
		t.Errorf("expected ref without oci:// prefix, got %q", store.pulledRefs[0])
	}
}

func TestResolver_FetchAllVersions_ContinuesOnPullError(t *testing.T) {
	// Pull always fails, but FetchAllVersions should still return the version list.
	store := &tagMockStore{
		tags:    []string{"1.0.0", "2.0.0"},
		pullErr: fmt.Errorf("pull failed"),
	}
	resolver := oci.NewResolver(store)

	versions, err := resolver.FetchAllVersions(context.Background(), "ghcr.io/org/svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
}

func TestResolver_FetchAllVersions_ListTagsError(t *testing.T) {
	store := &tagMockStore{
		tagsErr: fmt.Errorf("auth error"),
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.FetchAllVersions(context.Background(), "ghcr.io/org/svc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFilterSemverTags(t *testing.T) {
	tests := []struct {
		name     string
		tags     []string
		expected []string
	}{
		{
			name:     "mixed valid and invalid",
			tags:     []string{"v1.0.0", "latest", "2.0.0", "abc", "1.5.0"},
			expected: []string{"2.0.0", "1.5.0", "v1.0.0"},
		},
		{
			name:     "all invalid",
			tags:     []string{"latest", "main", "abc"},
			expected: nil,
		},
		{
			name:     "all valid",
			tags:     []string{"3.0.0", "1.0.0", "2.0.0"},
			expected: []string{"3.0.0", "2.0.0", "1.0.0"},
		},
		{
			name:     "empty",
			tags:     nil,
			expected: nil,
		},
		{
			name:     "prerelease",
			tags:     []string{"1.0.0-alpha", "1.0.0", "1.0.0-beta"},
			expected: []string{"1.0.0", "1.0.0-beta", "1.0.0-alpha"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := oci.FilterSemverTags(tt.tags)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d tags, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("index %d: expected %q, got %q", i, tt.expected[i], v)
				}
			}
		})
	}
}

func TestResolver_UnexpectedError(t *testing.T) {
	store := &mockStore{
		pullErr: fmt.Errorf("something unexpected"),
	}
	resolver := oci.NewResolver(store)

	_, err := resolver.Resolve(context.Background(), "ghcr.io/org/svc:1.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should pass through the generic error unmodified
	if err.Error() != "something unexpected" {
		t.Errorf("expected error %q, got %q", "something unexpected", err.Error())
	}
	// Should not be wrapped in InvalidRefError or InvalidBundleError
	var invalidRefErr *oci.InvalidRefError
	var invalidBundleErr *oci.InvalidBundleError
	if errors.As(err, &invalidRefErr) || errors.As(err, &invalidBundleErr) {
		t.Errorf("unexpected error wrapping for generic error: %T", err)
	}
}
