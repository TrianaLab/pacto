package oci

import (
	"container/list"
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

func TestCachePath_TraversalBlocked(t *testing.T) {
	cacheDir := t.TempDir()
	store := &CachedStore{cacheDir: cacheDir}

	ref := "ghcr.io/../../../etc/passwd"
	got := store.cachePath(ref)

	rel, err := filepath.Rel(cacheDir, got)
	if err != nil {
		t.Fatalf("filepath.Rel error: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("cachePath escaped cache directory: %s (rel=%s)", got, rel)
	}
}

func TestCachePath_NormalRef(t *testing.T) {
	cacheDir := t.TempDir()
	store := &CachedStore{cacheDir: cacheDir}

	ref := "ghcr.io/acme/svc:1.0.0"
	got := store.cachePath(ref)

	rel, err := filepath.Rel(cacheDir, got)
	if err != nil {
		t.Fatalf("filepath.Rel error: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("normal ref should stay inside cache: %s (rel=%s)", got, rel)
	}
	if !strings.HasSuffix(got, "bundle.tar.gz") {
		t.Errorf("expected bundle.tar.gz suffix, got %s", got)
	}
}

// stubStore is a minimal BundleStore that counts pulls and returns a fresh
// bundle each time, used to verify the bounded pull cache evicts under load.
type stubStore struct {
	pulls atomic.Int64
}

func (s *stubStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *stubStore) Resolve(context.Context, string) (string, error)    { return "", nil }
func (s *stubStore) ListTags(context.Context, string) ([]string, error) { return nil, nil }
func (s *stubStore) Pull(context.Context, string) (*contract.Bundle, error) {
	s.pulls.Add(1)
	return &contract.Bundle{Contract: &contract.Contract{}}, nil
}

// TestCachedStore_PullCacheEvictsBeyondCap proves the in-memory pull cache is
// bounded: after pulling more distinct refs than the cap, the cache holds at
// most cap entries and the oldest ref is evicted (re-pulls from inner).
func TestCachedStore_PullCacheEvictsBeyondCap(t *testing.T) {
	old := pullCacheMaxEntries
	pullCacheMaxEntries = 2
	t.Cleanup(func() { pullCacheMaxEntries = old })

	inner := &stubStore{}
	c := &CachedStore{
		inner:     inner,
		pullCache: map[string]*list.Element{},
		pullLRU:   list.New(),
		tagsCache: map[string][]string{},
	}
	ctx := context.Background()

	// Pull 3 distinct refs into a cap-2 cache; "a" should be evicted.
	for _, ref := range []string{"a", "b", "c"} {
		if _, err := c.Pull(ctx, ref); err != nil {
			t.Fatalf("Pull(%s): %v", ref, err)
		}
	}
	if got := c.pullLRU.Len(); got != 2 {
		t.Fatalf("cache size = %d, want 2 (bounded)", got)
	}
	if inner.pulls.Load() != 3 {
		t.Fatalf("inner pulls = %d, want 3", inner.pulls.Load())
	}

	// "c" is still hot (no new inner pull); "a" was evicted (re-pulls).
	if _, err := c.Pull(ctx, "c"); err != nil {
		t.Fatal(err)
	}
	if inner.pulls.Load() != 3 {
		t.Fatalf("expected memory hit for c, inner pulls = %d", inner.pulls.Load())
	}
	if _, err := c.Pull(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if inner.pulls.Load() != 4 {
		t.Fatalf("expected re-pull for evicted a, inner pulls = %d", inner.pulls.Load())
	}

	// Re-storing an already-cached ref updates the bundle in place (MRU refresh),
	// not a duplicate entry.
	before := c.pullLRU.Len()
	b2 := &contract.Bundle{Contract: &contract.Contract{}}
	c.storePull("a", b2)
	if c.pullLRU.Len() != before {
		t.Fatalf("re-store should not grow cache: len %d -> %d", before, c.pullLRU.Len())
	}
	if got := c.pullCache["a"].Value.(*pullEntry).bundle; got != b2 {
		t.Fatal("re-store should replace the bundle in place")
	}
}
