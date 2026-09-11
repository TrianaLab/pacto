package oci

import (
	"container/list"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

func TestEntryDir_TraversalBlocked(t *testing.T) {
	cacheDir := t.TempDir()
	store := &CachedStore{cacheDir: cacheDir}

	// Both spellings: the escaped key and the legacy one a read still consults.
	for _, got := range store.entryDirs("ghcr.io/../../../etc/passwd:1.0.0") {
		rel, err := filepath.Rel(cacheDir, got)
		if err != nil {
			t.Fatalf("filepath.Rel error: %v", err)
		}
		if !filepath.IsLocal(rel) {
			t.Errorf("entry directory escaped the cache: %s (rel=%s)", got, rel)
		}
	}
}

// Containment is not only about escaping upwards. An entry directory that
// resolves to the cache directory ITSELF is not an entry either: a read would
// look for one entry's files among every entry's directories, and the write
// path removes the directory it is handed before renaming the staged one into
// place. Degenerate references no registry would ever issue still reach
// [CachedStore.entryDirs] on the read path, so the containment rule — not the
// caller — has to rule them out.
func TestContained_NeverNamesTheCacheDirectoryItself(t *testing.T) {
	cacheDir := t.TempDir()
	store := &CachedStore{cacheDir: cacheDir}

	for _, rel := range []string{
		"",            // an empty reference
		".",           // the cache directory, spelled as an entry
		"..",          // its parent
		"../escape",   // outside
		"a/../..",     // interior traversal that still leaves
		"/etc/passwd", // an absolute path, not a relative entry
	} {
		got := store.contained(rel)
		if got == cacheDir {
			t.Errorf("contained(%q) named the cache directory itself", rel)
		}
		r, err := filepath.Rel(cacheDir, got)
		if err != nil || !filepath.IsLocal(r) {
			t.Errorf("contained(%q) = %q, not contained under %q", rel, got, cacheDir)
		}
	}

	// ...and through the reference-level spellings a read actually consults.
	for _, ref := range []string{"", ".", "..", "../../etc"} {
		for _, dir := range store.entryDirs(ref) {
			if dir == cacheDir {
				t.Errorf("entryDirs(%q) yielded the cache directory itself", ref)
			}
		}
	}
}

// The cache key must be INJECTIVE: two references that spell to one directory
// mean pulling either destroys the other's offline baseline, and a lookup by
// either is answered with whichever was installed last.
func TestEntryDir_DistinctRefsNeverNameOneDirectory(t *testing.T) {
	store := &CachedStore{cacheDir: t.TempDir()}

	refs := []string{
		"localhost:5000/demo/checkout:1.0.0", // a registry port
		"localhost/5000/demo/checkout:1.0.0", // ...and the path segment it used to spell as
		"ghcr.io/org/svc:1.0.0",
		"ghcr.io/org/svc",             // untagged, not the "svc/1.0.0" of the line above
		"ghcr.io/org/svc:1.0.0/extra", // no tag: the ':' is not after the last '/'
		"ghcr.io/org/svc@sha256:abc",  // a digest, not a "sha256" repo with an "abc" tag
		"ghcr.io/org/svc:%3A",         // a literal escape sequence in a tag
		"ghcr.io/org/svc:" + "%253A",  // ...and its escaping, one round further
		"ghcr.io/org/svc:" + untaggedSegment,
	}
	seen := map[string]string{}
	for _, ref := range refs {
		dir := store.entryDir(ref)
		if other, clash := seen[dir]; clash {
			t.Errorf("%q and %q both cache to %s", other, ref, dir)
		}
		seen[dir] = ref
	}

	// Every reference now writes inside the reserved namespace, so no existing
	// entry is at a path this version commits to — and none is stranded either,
	// because a read still consults the legacy path it may live at.
	const ref = "ghcr.io/org/svc:1.0.0"
	dirs := store.entryDirs(ref)
	if len(dirs) != 2 || dirs[0] != store.entryDir(ref) || dirs[1] != store.legacyEntryDir(ref) {
		t.Errorf("entryDirs(%q) = %v, want the new entry and then the legacy one it must still read", ref, dirs)
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
		tagsCache: map[string]tagsEntry{},
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

// Nothing serializes two pulls of the same ref -- in the case that produced
// this test they were parallel `pacto` invocations sharing one cache dir. They
// commit to the same entry directory, one rename wins, and every loser used to
// come back with `rename ...: directory not empty` for a cache write that had
// in fact just been made by somebody else. The caller logs that, so a healthy
// concurrent pull told the user its cache was broken -- and in tests/integration,
// whose runner hands the command one buffer for both streams, the warning landed
// in front of a `pacto diff --output json` payload and the parse failed.
func TestWriteCacheEntry_ConcurrentCommitsOfTheSameEntryAllSucceed(t *testing.T) {
	root := t.TempDir()
	c := &CachedStore{cacheDir: filepath.Join(root, "oci")}
	dir := filepath.Join(c.cacheDir, "_v2", "reg%3A5000", "demo", "svc", "1.0.0")
	rec := CachedRef{Ref: "reg:5000/demo/svc:1.0.0", Digest: "sha256:aaa"}

	const writers = 24
	start := make(chan struct{})
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = c.writeCacheEntry(dir, rec, markedBundle())
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent commit %d reported a failure for an entry that was written: %v", i, err)
		}
	}
	if !entryIsCommitted(dir) {
		t.Error("after 24 concurrent commits the entry is not on disk")
	}
}

// The race above is tolerated by asking what is on disk, not by ignoring the
// rename error -- so a commit that fails for any OTHER reason must still say so.
func TestWriteCacheEntry_ALostRenameIsOnlyForgivenWhenTheEntryIsThere(t *testing.T) {
	rec := CachedRef{Ref: "reg:5000/demo/svc:1.0.0", Digest: "sha256:aaa"}

	// A directory the process cannot empty stands in for the winner's entry: the
	// RemoveAll leaves it, so the rename hits an occupied destination exactly as
	// it does when another committer got there first.
	occupied := func(t *testing.T, contents map[string]string) (*CachedStore, string) {
		t.Helper()
		root := t.TempDir()
		c := &CachedStore{cacheDir: filepath.Join(root, "oci")}
		dir := filepath.Join(c.cacheDir, "svc", "1.0.0")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range contents {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return c, dir
	}

	t.Run("a whole entry is already there", func(t *testing.T) {
		c, dir := occupied(t, map[string]string{CachedBundleFile: "tgz", CachedRefFile: "{}"})
		if err := c.writeCacheEntry(dir, rec, markedBundle()); err != nil {
			t.Errorf("a commit that lost to a finished one reported %v, want success", err)
		}
	})

	t.Run("what is there is not an entry", func(t *testing.T) {
		c, dir := occupied(t, map[string]string{"stray": "x"})
		if err := c.writeCacheEntry(dir, rec, markedBundle()); err == nil {
			t.Error("a commit blocked by something that is not a cache entry reported success")
		}
	})

	t.Run("the bundle is a directory wearing an entry's name", func(t *testing.T) {
		root := t.TempDir()
		c := &CachedStore{cacheDir: filepath.Join(root, "oci")}
		dir := filepath.Join(c.cacheDir, "svc", "1.0.0")
		if err := os.MkdirAll(filepath.Join(dir, CachedBundleFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, CachedRefFile), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		if err := c.writeCacheEntry(dir, rec, markedBundle()); err == nil {
			t.Error("a directory named bundle.tar.gz was accepted as a committed entry")
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
		tagsCache: map[string]tagsEntry{},
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

func TestCachedStoreDisableCacheIsRaceFree(t *testing.T) {
	c := &CachedStore{
		inner:     &stubStore{},
		cacheDir:  t.TempDir(),
		pullCache: map[string]*list.Element{},
		pullLRU:   list.New(),
		tagsCache: map[string]tagsEntry{},
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			c.DisableCache()
		}
	}()
	go func() {
		defer wg.Done()
		// cachedEntry reads skipDiskReads on every call.
		for i := 0; i < 100; i++ {
			_, _, _ = c.cachedEntry(context.Background(), "oci://example.test/svc:1.0.0")
		}
	}()
	wg.Wait()
}

// tagListingStore answers ListTags with whatever the test last published, and
// counts how often it was actually asked.
type tagListingStore struct {
	mu    sync.Mutex
	tags  []string
	calls int
}

func (s *tagListingStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", nil
}
func (s *tagListingStore) Resolve(context.Context, string) (string, error) { return "", nil }
func (s *tagListingStore) Pull(context.Context, string) (*contract.Bundle, error) {
	return nil, errors.New("this store must not be pulled")
}
func (s *tagListingStore) ListTags(context.Context, string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.tags, nil
}

// TestCachedStore_ListTags_AnAgedMemoIsReListed is the counterexample for a memo
// that never expires. A tag set is a MUTABLE registry fact, so a memo kept for
// the process lifetime answered a long-running reader — the dashboard
// rediscovers on a loop — from its FIRST observation forever: a release
// published after startup was invisible for as long as the process ran.
func TestCachedStore_ListTags_AnAgedMemoIsReListed(t *testing.T) {
	inner := &tagListingStore{tags: []string{"1.0.0"}}
	c := NewCachedStore(inner)
	ctx := context.Background()
	const repo = "ghcr.io/test/repo"

	if _, err := c.ListTags(ctx, repo); err != nil {
		t.Fatalf("first ListTags: %v", err)
	}

	// 2.0.0 is released, and the memo ages past its TTL.
	inner.mu.Lock()
	inner.tags = []string{"1.0.0", "2.0.0"}
	inner.mu.Unlock()
	c.tagsMu.Lock()
	aged := c.tagsCache[repo]
	aged.at = aged.at.Add(-tagsTTL - time.Second)
	c.tagsCache[repo] = aged
	c.tagsMu.Unlock()

	tags, err := c.ListTags(ctx, repo)
	if err != nil {
		t.Fatalf("second ListTags: %v", err)
	}
	if len(tags) != 2 {
		t.Errorf("tags = %v, want the released 2.0.0 too: an expired memo must be re-listed", tags)
	}
	inner.mu.Lock()
	defer inner.mu.Unlock()
	if inner.calls != 2 {
		t.Errorf("the registry was asked %d times, want 2: the aged memo answered instead", inner.calls)
	}
}
