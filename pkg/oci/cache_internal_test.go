package oci

import (
	"container/list"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
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
	c.storePull("a", b2, CachedRef{Ref: "a", Digest: "sha256:a2"})
	if c.pullLRU.Len() != before {
		t.Fatalf("re-store should not grow cache: len %d -> %d", before, c.pullLRU.Len())
	}
	got := c.pullCache["a"].Value.(*pullEntry)
	if got.bundle != b2 {
		t.Fatal("re-store should replace the bundle in place")
	}
	// The recorded identity travels with the bundle: a stale digest left behind
	// would make a memory hit report a digest for content it no longer holds.
	if (got.rec != CachedRef{Ref: "a", Digest: "sha256:a2"}) {
		t.Fatalf("re-store left record %+v, want the record of the bundle now held", got.rec)
	}
}

// unreadableFS is a bundle filesystem that refuses to be read, standing in for
// the archive write failing partway. Its point is that a bundle that cannot be
// written is a commit that must not happen at all.
type unreadableFS struct{}

func (unreadableFS) Open(string) (fs.File, error) { return nil, errors.New("unreadable") }

func TestWriteBundleFile_ReportsWhatWentWrong(t *testing.T) {
	dir := t.TempDir()

	// The archive cannot be created at all.
	if err := writeBundleFile(filepath.Join(dir, "missing", CachedBundleFile), markedBundle()); err == nil {
		t.Error("writing into a directory that does not exist reported success")
	}
	// The archive is created but its content cannot be read.
	path := filepath.Join(dir, CachedBundleFile)
	if err := writeBundleFile(path, &contract.Bundle{FS: unreadableFS{}}); err == nil {
		t.Error("an unreadable bundle filesystem reported a successful write")
	}
}

// markedBundle is a minimal writable bundle for the write-path tests.
func markedBundle() *contract.Bundle {
	y := []byte("pactoVersion: \"2.0\"\nservice:\n  name: svc\n  version: \"1.0.0\"\n")
	return &contract.Bundle{
		Contract: &contract.Contract{},
		FS:       fstest.MapFS{"pacto.yaml": &fstest.MapFile{Data: y}},
	}
}

// A cache entry is committed or it is not. Every way the commit can fail must
// leave what was already on disk alone, and say so — a silently skipped write is
// the performance mystery this path used to be.
func TestWriteCacheEntry_FailedCommitsAreReported(t *testing.T) {
	rec := CachedRef{Ref: "reg:5000/demo/svc:1.0.0", Digest: "sha256:aaa"}

	t.Run("the entry's parent cannot be created", func(t *testing.T) {
		root := t.TempDir()
		blocker := filepath.Join(root, "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		c := &CachedStore{cacheDir: filepath.Join(blocker, "oci")}
		if err := c.writeCacheEntry(filepath.Join(c.cacheDir, "svc", "1.0.0"), rec, markedBundle()); err == nil {
			t.Error("a cache path running through a regular file reported success")
		}
	})

	t.Run("the staging area cannot be created", func(t *testing.T) {
		root := t.TempDir()
		c := &CachedStore{cacheDir: filepath.Join(root, "oci")}
		dir := filepath.Join(c.cacheDir, "svc", "1.0.0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// The staging root exists but refuses new entries, so MkdirAll is content
		// and MkdirTemp is not.
		if err := os.Chmod(root, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
		if err := c.writeCacheEntry(dir, rec, markedBundle()); err == nil {
			t.Error("an unwritable staging root reported a successful commit")
		}
	})

	t.Run("the bundle cannot be staged", func(t *testing.T) {
		root := t.TempDir()
		c := &CachedStore{cacheDir: filepath.Join(root, "oci")}
		dir := filepath.Join(c.cacheDir, "svc", "1.0.0")
		if err := c.writeCacheEntry(dir, rec, &contract.Bundle{FS: unreadableFS{}}); err == nil {
			t.Error("an unreadable bundle reported a successful commit")
		}
		if _, err := os.Stat(dir); err == nil {
			t.Error("a failed commit published an entry anyway")
		}
	})
}

func TestPinRefToDigest_DropsWhateverTheRefAlreadyPins(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"localhost:5000/demo/svc:1.0.0", "localhost:5000/demo/svc@sha256:new"},
		{"localhost:5000/demo/svc@sha256:old", "localhost:5000/demo/svc@sha256:new"},
		{"oci://ghcr.io/org/svc", "ghcr.io/org/svc@sha256:new"},
	} {
		if got := PinRefToDigest(tc.ref, "sha256:new"); got != tc.want {
			t.Errorf("PinRefToDigest(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

// TestCachedStore_PullCacheEvictsLRUNotFIFO proves eviction respects recency:
// a read hit promotes the entry so a stale entry is evicted first. A pure FIFO
// cache (no MoveToFront on the read path) would evict the accessed entry instead
// and fail this test.
func TestCachedStore_PullCacheEvictsLRUNotFIFO(t *testing.T) {
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

	// Insert x, y (cap 2). Then ACCESS x (read hit → MoveToFront), then insert z.
	// LRU evicts y (least recently used); FIFO would evict x (oldest inserted).
	for _, ref := range []string{"x", "y"} {
		if _, err := c.Pull(ctx, ref); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Pull(ctx, "x"); err != nil { // hit — promotes x
		t.Fatal(err)
	}
	if _, err := c.Pull(ctx, "z"); err != nil { // insert — evicts LRU (y)
		t.Fatal(err)
	}

	if _, ok := c.pullCache["x"]; !ok {
		t.Fatal("x was accessed most recently and must survive eviction (LRU); a FIFO cache would have dropped it")
	}
	if _, ok := c.pullCache["y"]; ok {
		t.Fatal("y was least recently used and must be evicted")
	}
}
