package oci_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/trianalab/pacto/internal/oci"
)

func TestHasExplicitTag(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"ghcr.io/foo/bar:v1", true},
		{"ghcr.io/foo/bar:1.0.0", true},
		{"ghcr.io/foo/bar", false},
		{"ghcr.io/foo/bar@sha256:abc123", true},
		{"localhost:5000/repo", false},
		{"localhost:5000/repo:v1", true},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			if got := oci.HasExplicitTag(tt.ref); got != tt.want {
				t.Errorf("HasExplicitTag(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestBestTag(t *testing.T) {
	tests := []struct {
		name       string
		tags       []string
		constraint string
		want       string
		wantErr    bool
	}{
		{
			name: "highest semver",
			tags: []string{"1.0.0", "2.0.0", "3.0.0"},
			want: "3.0.0",
		},
		{
			name:       "with constraint",
			tags:       []string{"1.0.0", "2.0.0", "3.0.0"},
			constraint: "^2.0.0",
			want:       "2.0.0",
		},
		{
			name:       "constraint with multiple matches",
			tags:       []string{"1.0.0", "1.1.0", "1.2.0", "2.0.0"},
			constraint: "^1.0.0",
			want:       "1.2.0",
		},
		{
			name: "v prefix",
			tags: []string{"v1.0.0", "v2.0.0"},
			want: "v2.0.0",
		},
		{
			name: "mixed valid and invalid",
			tags: []string{"latest", "main", "1.0.0", "2.0.0", "bad"},
			want: "2.0.0",
		},
		{
			name:    "no semver tags",
			tags:    []string{"latest", "main"},
			wantErr: true,
		},
		{
			name:    "empty tags",
			tags:    []string{},
			wantErr: true,
		},
		{
			name:       "no tags match constraint",
			tags:       []string{"1.0.0", "2.0.0"},
			constraint: "^3.0.0",
			wantErr:    true,
		},
		{
			name:       "invalid constraint",
			tags:       []string{"1.0.0"},
			constraint: "not-valid-%%",
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := oci.BestTag(tt.tags, tt.constraint)
			if tt.wantErr {
				if err == nil {
					t.Errorf("BestTag() expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BestTag() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("BestTag() = %q, want %q", got, tt.want)
			}
		})
	}
}

type mockTagLister struct {
	tags []string
	err  error
}

func (m *mockTagLister) ListTags(_ context.Context, _ string) ([]string, error) {
	return m.tags, m.err
}

func TestResolveRef(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		constraint string
		tags       []string
		listErr    error
		want       string
		wantErr    bool
	}{
		{
			name: "explicit tag unchanged",
			ref:  "ghcr.io/foo/bar:1.0.0",
			want: "ghcr.io/foo/bar:1.0.0",
		},
		{
			name: "digest unchanged",
			ref:  "ghcr.io/foo/bar@sha256:abc",
			want: "ghcr.io/foo/bar@sha256:abc",
		},
		{
			name: "resolve latest",
			ref:  "ghcr.io/foo/bar",
			tags: []string{"1.0.0", "2.0.0"},
			want: "ghcr.io/foo/bar:2.0.0",
		},
		{
			name:       "resolve with constraint",
			ref:        "ghcr.io/foo/bar",
			constraint: "^1.0.0",
			tags:       []string{"1.0.0", "1.5.0", "2.0.0"},
			want:       "ghcr.io/foo/bar:1.5.0",
		},
		{
			name:    "list tags error",
			ref:     "ghcr.io/foo/bar",
			listErr: fmt.Errorf("network error"),
			wantErr: true,
		},
		{
			name:    "no semver tags",
			ref:     "ghcr.io/foo/bar",
			tags:    []string{"latest"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lister := &mockTagLister{tags: tt.tags, err: tt.listErr}
			got, err := oci.ResolveRef(context.Background(), lister, tt.ref, tt.constraint)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveRef() expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRef() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClient_ListTags(t *testing.T) {
	client, host := newTestClient(t)
	ctx := context.Background()
	b := newTestBundle()

	repo := host + "/test/listtags"

	// Push multiple versions.
	for _, tag := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		if _, err := client.Push(ctx, repo+":"+tag, b); err != nil {
			t.Fatalf("Push(%s) error: %v", tag, err)
		}
	}

	tags, err := client.ListTags(ctx, repo)
	if err != nil {
		t.Fatalf("ListTags() error: %v", err)
	}

	want := map[string]bool{"1.0.0": true, "2.0.0": true, "3.0.0": true}
	for _, tag := range tags {
		delete(want, tag)
	}
	if len(want) != 0 {
		t.Errorf("missing tags: %v", want)
	}
}

func TestClient_ListTags_InvalidRepo(t *testing.T) {
	client := oci.NewClient(authn.DefaultKeychain, oci.WithNameOptions(name.Insecure))
	_, err := client.ListTags(context.Background(), "!!!")
	if err == nil {
		t.Fatal("expected error for invalid repository")
	}
	if !strings.Contains(err.Error(), "invalid repository") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid repository")
	}
}

func TestClient_ListTags_NotFound(t *testing.T) {
	client, host := newTestClient(t)
	_, err := client.ListTags(context.Background(), host+"/nonexistent/repo")
	if err == nil {
		t.Fatal("expected error for nonexistent repository")
	}
}

func TestCachedStore_ListTags_DelegatesToInner(t *testing.T) {
	inner := &countingStore{bundle: newTestBundle()}
	store := oci.NewCachedStore(inner)
	tags, err := store.ListTags(context.Background(), "ghcr.io/test/repo")
	if err != nil {
		t.Fatalf("ListTags() error: %v", err)
	}
	if tags != nil {
		t.Errorf("expected nil tags from mock, got %v", tags)
	}
}
