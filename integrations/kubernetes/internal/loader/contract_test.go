/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/trianalab/pacto/v3/pkg/contract"
)

func TestTagCacheKey(t *testing.T) {
	tests := []struct {
		ref    string
		expect string
	}{
		{"ghcr.io/org/svc", "tags:ghcr.io/org/svc"},
		{"ghcr.io/org/svc:1.0.0", "tags:ghcr.io/org/svc"},
		{"ghcr.io/org/svc@sha256:abc123", "tags:ghcr.io/org/svc"},
		{"ghcr.io/org/svc:1.0.0@sha256:abc123", "tags:ghcr.io/org/svc"},
		{"registry:5000/org/svc", "tags:registry:5000/org/svc"},
		{"registry:5000/org/svc:2.0.0", "tags:registry:5000/org/svc"},
	}

	for _, tt := range tests {
		got := tagCacheKey(tt.ref)
		if got != tt.expect {
			t.Errorf("tagCacheKey(%q) = %q, want %q", tt.ref, got, tt.expect)
		}
	}
}

func TestTagCacheKey_SameRepoSharesKey(t *testing.T) {
	// All forms of the same repo must produce the same cache key
	unversioned := tagCacheKey("ghcr.io/org/svc")
	tagged := tagCacheKey("ghcr.io/org/svc:1.0.0")
	digest := tagCacheKey("ghcr.io/org/svc@sha256:abcdef")

	if unversioned != tagged {
		t.Errorf("unversioned %q != tagged %q", unversioned, tagged)
	}
	if unversioned != digest {
		t.Errorf("unversioned %q != digest %q", unversioned, digest)
	}
}

func TestListTags_CachesResults(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Pre-populate the cache to simulate a previous call
	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0", "2.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// Should return cached result without hitting the (nil) OCI puller
	tags, err := l.ListTags(t.Context(), "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 2 || tags[0] != "1.0.0" {
		t.Fatalf("expected cached tags [1.0.0 2.0.0], got %v", tags)
	}
}

func TestListTags_CacheExpiry(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Pre-populate with an expired entry
	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0"},
		expiresAt: time.Now().Add(-1 * time.Second), // expired
	}

	// Should NOT return expired cache — will fail calling the real OCI puller
	// (which would error since there's no real registry), proving the cache was skipped
	_, err := l.ListTags(t.Context(), "ghcr.io/org/svc", nil)
	if err == nil {
		t.Fatal("expected error from real OCI call after cache expiry")
	}
}

func TestListTags_EmptyRef(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
	}

	tags, err := l.ListTags(t.Context(), "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tags != nil {
		t.Fatalf("expected nil tags for empty ref, got %v", tags)
	}
}

func TestListTags_TaggedRefSharesCacheWithUnversioned(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Cache under unversioned ref
	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0", "2.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// Query with tagged ref — should hit the same cache entry
	tags, err := l.ListTags(t.Context(), "ghcr.io/org/svc:1.0.0", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("expected cached tags from unversioned entry, got %v", tags)
	}
}

func TestListTags_OCIPrefixNormalized(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"3.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// oci:// prefix should be stripped before cache lookup
	tags, err := l.ListTags(t.Context(), "oci://ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 1 || tags[0] != "3.0.0" {
		t.Fatalf("expected cached tags [3.0.0], got %v", tags)
	}
}

// ---------------------------------------------------------------------------
// BUG-2: authOverride bypasses cache
// ---------------------------------------------------------------------------

func TestListTags_AuthOverrideBypassesCache(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Pre-populate cache
	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// With authOverride, cache should be bypassed — the real OCI puller
	// will fail (no registry), proving the cache was not used
	auth := &authn.AuthConfig{Username: "user", Password: "pass"}
	_, err := l.ListTags(t.Context(), "ghcr.io/org/svc", auth)
	if err == nil {
		t.Fatal("expected error from real OCI call — cache should be bypassed when authOverride is set")
	}
}

func TestListTags_NilAuthUsesCache(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	key := tagCacheKey("ghcr.io/org/svc")
	l.tagCache[key] = tagCacheEntry{
		tags:      []string{"1.0.0"},
		expiresAt: time.Now().Add(5 * time.Minute),
	}

	// Without authOverride, cache should be used
	tags, err := l.ListTags(t.Context(), "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("expected cached tags, got %v", tags)
	}
}

func TestLoad_AuthOverrideBypassesCache(t *testing.T) {
	l := &Loader{
		cache:    make(map[string]cacheEntry),
		cacheTTL: 30 * time.Second,
		oci:      &OCIPuller{},
	}

	// Pre-populate cache with an error
	key := "oci:ghcr.io/org/svc"
	l.cache[key] = cacheEntry{
		result:    nil,
		err:       fmt.Errorf("cached auth error"),
		expiresAt: time.Now().Add(30 * time.Second),
	}

	// Without auth, should return cached error
	_, err := l.Load(t.Context(), "ghcr.io/org/svc", "", nil)
	if err == nil || err.Error() != "cached auth error" {
		t.Fatalf("expected cached error, got: %v", err)
	}

	// With authOverride, should bypass cache and hit real OCI (which will fail differently)
	auth := &authn.AuthConfig{Username: "user", Password: "pass"}
	_, err = l.Load(t.Context(), "ghcr.io/org/svc", "", auth)
	if err == nil {
		t.Fatal("expected error from real OCI call")
	}
	if err.Error() == "cached auth error" {
		t.Fatal("got cached error — authOverride should bypass cache")
	}
}

// ---------------------------------------------------------------------------
// Coverage: constructors + inline loading
// ---------------------------------------------------------------------------

func TestNew(t *testing.T) {
	l := New()
	if l == nil {
		t.Fatal("expected non-nil Loader")
	}
	if l.oci == nil {
		t.Fatal("expected OCI puller to be initialized")
	}
	if l.cache == nil || l.tagCache == nil {
		t.Fatal("expected caches to be initialized")
	}
}

func TestNewOCIPuller(t *testing.T) {
	p := NewOCIPuller()
	if p == nil {
		t.Fatal("expected non-nil OCIPuller")
	}
}

func TestLoadInline_ValidContract(t *testing.T) {
	yaml := `pactoVersion: "2.0"
service:
  name: test-svc
  version: 1.0.0
  owner:
    team: team-a
workload: service
`
	result, err := loadInline(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Contract == nil {
		t.Fatal("expected contract to be parsed")
	}
	if result.Contract.Service.Name != "test-svc" {
		t.Errorf("expected service name test-svc, got %s", result.Contract.Service.Name)
	}
	if string(result.RawYAML) != yaml {
		t.Error("expected RawYAML to match input")
	}
	if result.BundleFS != nil {
		t.Error("expected nil BundleFS for inline contract")
	}
}

func TestLoadInline_InvalidYAML(t *testing.T) {
	_, err := loadInline("invalid: [unclosed")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadUncached_NoContractSource(t *testing.T) {
	l := &Loader{}
	_, err := l.loadUncached(t.Context(), "", "", nil)
	if err == nil || err.Error() != "no contract source specified: set either spec.contractRef.oci or spec.contractRef.inline" {
		t.Fatalf("expected no-contract-source error, got: %v", err)
	}
}

func TestLoadUncached_InlinePrecedence(t *testing.T) {
	l := &Loader{}
	yaml := `pactoVersion: "2.0"
service:
  name: inline-svc
  version: 1.0.0
  owner:
    team: team-a
workload: service
`
	// When both inline and OCI are set, inline takes precedence
	result, err := l.loadUncached(t.Context(), "ghcr.io/org/svc", yaml, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Contract.Service.Name != "inline-svc" {
		t.Errorf("expected inline contract to be used, got %s", result.Contract.Service.Name)
	}
}

func TestCacheKey_InlineBranch(t *testing.T) {
	l := &Loader{}
	yaml1 := "pactoVersion: \"2.0\"\nservice:\n  name: svc1\n  version: 1.0.0\n  owner:\n    team: team-a\n"
	yaml2 := "pactoVersion: \"2.0\"\nservice:\n  name: svc2\n  version: 1.0.0\n  owner:\n    team: team-a\n"
	key1 := l.cacheKey("", yaml1)
	key2 := l.cacheKey("", yaml2)
	if key1 == key2 {
		t.Error("expected different cache keys for different inline contracts")
	}
	if key1 == "" || !contains(key1, "inline:") {
		t.Errorf("expected inline cache key to start with 'inline:', got %q", key1)
	}
}

func TestCacheKey_OCIBranch(t *testing.T) {
	l := &Loader{}
	key := l.cacheKey("ghcr.io/org/svc:1.0.0", "")
	if key != "oci:ghcr.io/org/svc:1.0.0" {
		t.Errorf("expected oci cache key, got %q", key)
	}
}

func TestLoad_CacheHit(t *testing.T) {
	l := &Loader{
		cache:    make(map[string]cacheEntry),
		cacheTTL: 30 * time.Second,
	}
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: cached-svc\n  version: 1.0.0\n  owner:\n    team: team-a\n"
	key := l.cacheKey("", yaml)
	l.cache[key] = cacheEntry{
		result: &LoadResult{
			Contract: &contract.Contract{Service: contract.Service{Name: "cached-svc"}},
			RawYAML:  []byte(yaml),
		},
		expiresAt: time.Now().Add(30 * time.Second),
	}

	result, err := l.Load(t.Context(), "", yaml, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Contract.Service.Name != "cached-svc" {
		t.Errorf("expected cached contract, got %s", result.Contract.Service.Name)
	}
}

func TestLoad_CacheExpiry(t *testing.T) {
	l := &Loader{
		cache:    make(map[string]cacheEntry),
		cacheTTL: 30 * time.Second,
	}
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: expired-svc\n  version: 1.0.0\n  owner:\n    team: team-a\n"
	key := l.cacheKey("", yaml)
	l.cache[key] = cacheEntry{
		result: &LoadResult{
			Contract: &contract.Contract{Service: contract.Service{Name: "expired-svc"}},
			RawYAML:  []byte(yaml),
		},
		expiresAt: time.Now().Add(-1 * time.Second), // expired
	}

	// Should not return expired cache, will re-parse
	result, err := l.Load(t.Context(), "", yaml, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Contract.Service.Name != "expired-svc" {
		t.Errorf("expected re-parsed contract, got %s", result.Contract.Service.Name)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr
}

func TestLoad_CacheEviction(t *testing.T) {
	l := &Loader{
		cache:    make(map[string]cacheEntry),
		cacheTTL: 30 * time.Second,
	}

	// Fill cache with > 100 entries, some expired
	for i := range 110 {
		key := fmt.Sprintf("inline-%d", i)
		expires := time.Now().Add(30 * time.Second)
		if i%2 == 0 {
			expires = time.Now().Add(-1 * time.Second) // expired
		}
		l.cache[key] = cacheEntry{
			result: &LoadResult{
				Contract: &contract.Contract{Service: contract.Service{Name: "svc"}},
			},
			expiresAt: expires,
		}
	}

	// Trigger eviction by loading one more (>100 threshold)
	yaml := "pactoVersion: \"2.0\"\nservice:\n  name: trigger-svc\n  version: 1.0.0\n  owner:\n    team: team-a\n"
	_, _ = l.Load(t.Context(), "", yaml, nil)

	// Check that some expired entries were evicted
	if len(l.cache) >= 110 {
		t.Errorf("expected cache eviction to reduce size, got %d entries", len(l.cache))
	}
}

func TestListTags_CacheEviction(t *testing.T) {
	l := &Loader{
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
		oci:         &OCIPuller{},
	}

	// Fill tag cache with > 100 entries, some expired
	for i := range 110 {
		key := fmt.Sprintf("tags:ghcr.io/org/svc%d", i)
		expires := time.Now().Add(5 * time.Minute)
		if i%2 == 0 {
			expires = time.Now().Add(-1 * time.Second) // expired
		}
		l.tagCache[key] = tagCacheEntry{
			tags:      []string{"1.0.0"},
			expiresAt: expires,
		}
	}

	// Trigger eviction by listing tags (>100 threshold)
	// This will fail because it's a real OCI call, but eviction logic runs first
	_, _ = l.ListTags(t.Context(), "ghcr.io/org/new-svc", nil)

	// Check that some expired entries were evicted
	if len(l.tagCache) >= 110 {
		t.Errorf("expected tag cache eviction to reduce size, got %d entries", len(l.tagCache))
	}
}
