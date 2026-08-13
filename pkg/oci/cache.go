package oci

import (
	"compress/gzip"
	"container/list"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/logging"
)

// pullCacheMaxEntries bounds the in-memory pulled-bundle cache. Long-running
// callers (the dashboard prefetches every version of every repo on a loop)
// would otherwise retain every bundle — with its decompressed FS — forever,
// scaling memory with repo×version count and eventually OOMing. Evicted entries
// still serve from the disk cache. Package var so tests can shrink it.
//
// ponytail: count-based LRU. Entries vary in size (SBOMs dwarf tiny contracts),
// so this bounds count, not bytes. Switch to a byte-budget LRU if a few huge
// bundles ever blow the ceiling on their own.
var pullCacheMaxEntries = 512

// CachedStore wraps a BundleStore with in-memory and disk caching. Pulled
// bundles are kept in memory (fastest) and persisted to disk under
// ~/.cache/pacto/oci/ so they survive across process invocations. ListTags
// results are cached in memory for the lifetime of the process.
type CachedStore struct {
	inner    BundleStore
	cacheDir string

	// skipDiskReads disables loading from disk cache (cold-start mode).
	// Disk writes remain enabled so same-session pulls are persisted.
	skipDiskReads bool

	// materialized records that THIS process has committed an entry into the disk
	// cache. It is a different fact from what the cache held at startup and from
	// what happens to be discoverable now, and the only one that says the
	// directory has content this run put there.
	materialized atomic.Bool

	// pullCache is a bounded LRU: map ref -> list element, with pullLRU ordering
	// entries most-recently-used at the front. Guarded by pullMu.
	pullMu    sync.Mutex
	pullCache map[string]*list.Element
	pullLRU   *list.List

	tagsMu    sync.Mutex
	tagsCache map[string][]string
}

// pullEntry is the value stored in each pullLRU list element. digest is the
// manifest digest of the artifact this bundle was fetched from, empty when the
// registry would not say; it travels with the bundle so a cache hit reports the
// SAME identity the pull recorded instead of re-asking a mutable tag.
type pullEntry struct {
	ref    string
	digest string
	bundle *contract.Bundle
}

// NewCachedStore creates a BundleStore that caches pulled bundles on disk.
// If the cache directory cannot be determined, caching is silently disabled.
func NewCachedStore(inner BundleStore) *CachedStore {
	dir, err := pactoCacheDir()
	if err != nil {
		dir = ""
	}
	return &CachedStore{
		inner:     inner,
		cacheDir:  dir,
		pullCache: map[string]*list.Element{},
		pullLRU:   list.New(),
		tagsCache: map[string][]string{},
	}
}

// DisableCache skips reading from the disk cache (cold-start mode) and clears
// the in-memory caches (both pulled bundles and listed tags). Disk writes
// remain enabled so that same-session pulls (e.g. fetch-all-versions) are still
// persisted and available for enrichment.
func (c *CachedStore) DisableCache() {
	c.skipDiskReads = true
	c.pullMu.Lock()
	c.pullCache = map[string]*list.Element{}
	c.pullLRU = list.New()
	c.pullMu.Unlock()
	c.tagsMu.Lock()
	c.tagsCache = map[string][]string{}
	c.tagsMu.Unlock()
}

// CacheDir returns the resolved on-disk cache directory (e.g. ~/.cache/pacto/oci).
// Returns empty string if no cache directory could be determined at creation time.
func (c *CachedStore) CacheDir() string {
	return c.cacheDir
}

func pactoCacheDir() (string, error) {
	if os.Getenv("XDG_CACHE_HOME") != "" {
		return CacheDirFor(""), nil
	}
	home, err := userHomeDirFn()
	if err != nil {
		return "", err
	}
	return CacheDirFor(home), nil
}

// CacheDirFor returns the on-disk OCI cache directory for the given home
// directory, honoring XDG_CACHE_HOME. Exported so other components (e.g. the
// dashboard diagnostics) resolve the exact same path.
func CacheDirFor(home string) string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "pacto", "oci")
	}
	return filepath.Join(home, ".cache", "pacto", "oci")
}

// Push uploads the bundle to the registry via the wrapped store. It does not
// populate the cache; pushed bundles are only cached when later pulled.
func (c *CachedStore) Push(ctx context.Context, ref string, bundle *contract.Bundle) (string, error) {
	return c.inner.Push(ctx, ref, bundle)
}

// Resolve delegates to the wrapped store to resolve a ref to a digest; it is
// not cached and always contacts the underlying store.
func (c *CachedStore) Resolve(ctx context.Context, ref string) (string, error) {
	return c.inner.Resolve(ctx, ref)
}

// ListTags returns the tags for repo, serving from the in-memory tags cache on
// a hit and otherwise querying the wrapped store and caching the result for the
// process lifetime.
func (c *CachedStore) ListTags(ctx context.Context, repo string) ([]string, error) {
	c.tagsMu.Lock()
	if cached, ok := c.tagsCache[repo]; ok {
		c.tagsMu.Unlock()
		logging.LoggerFromContext(ctx).Debug("tags cache hit", "repo", repo)
		return cached, nil
	}
	c.tagsMu.Unlock()

	tags, err := c.inner.ListTags(ctx, repo)
	if err != nil {
		return nil, err
	}

	c.tagsMu.Lock()
	c.tagsCache[repo] = tags
	c.tagsMu.Unlock()

	return tags, nil
}

// PullCached returns the bundle for ref from the in-memory cache, then the disk
// cache (skipped when DisableCache is active), and reports whether it was found.
// It NEVER contacts the registry, so it is the offline read path: [Resolver] in
// [LocalOnly] mode uses it, which is what keeps "local only" from silently
// becoming a per-ref network pull on a cache miss.
func (c *CachedStore) PullCached(ctx context.Context, ref string) (*contract.Bundle, bool) {
	bundle, _, hit := c.pullCached(ctx, ref)
	return bundle, hit
}

// PullCachedPinned is [CachedStore.PullCached] plus the manifest digest RECORDED
// with the bundle it serves — read from the same cache generation, so the
// offline reader reports the identity of the bytes it got and never has to ask a
// registry the mode forbids. Empty means the entry never recorded one.
func (c *CachedStore) PullCachedPinned(ctx context.Context, ref string) (*contract.Bundle, string, bool) {
	return c.pullCached(ctx, ref)
}

// Materialized reports whether this store has written a bundle into the disk
// cache during this process. A dashboard uses it to tell "the cache is empty
// because nothing has been pulled yet" from "the cache holds what this session
// pulled" — the pod's cache is an emptyDir, so the first is where every
// operator-managed run starts.
func (c *CachedStore) Materialized() bool { return c.materialized.Load() }

// pullCached is [CachedStore.PullCached] plus the manifest digest recorded for
// the served bundle, so a hit reports the identity the pull observed.
func (c *CachedStore) pullCached(ctx context.Context, ref string) (*contract.Bundle, string, bool) {
	// 1. In-memory cache (fastest).
	c.pullMu.Lock()
	if el, ok := c.pullCache[ref]; ok {
		c.pullLRU.MoveToFront(el)
		e := el.Value.(*pullEntry)
		c.pullMu.Unlock()
		logging.LoggerFromContext(ctx).Debug("cache hit (memory)", "ref", ref)
		return e.bundle, e.digest, true
	}
	c.pullMu.Unlock()

	// 2. Disk cache (skipped when --no-cache / DisableCache is active).
	if c.cacheDir != "" && !c.skipDiskReads {
		if bundle, rec, ok := readCacheEntry(filepath.Dir(c.cachePath(ref))); ok {
			logging.LoggerFromContext(ctx).Debug("cache hit (disk)", "ref", ref)
			// The sidecar was committed with these exact bytes, so it is the
			// identity of what was just loaded, not a fresh guess about a tag.
			c.storePull(ref, bundle, rec.Digest)
			return bundle, rec.Digest, true
		}
	}
	return nil, "", false
}

// Pull returns the bundle for ref, checking the in-memory cache, then the disk
// cache (skipped when DisableCache is active), then the wrapped store. Registry
// pulls are stored in memory and persisted to disk for future lookups.
func (c *CachedStore) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	bundle, _, err := c.PullPinned(ctx, ref)
	return bundle, err
}

// PullPinned is [CachedStore.Pull] plus the manifest digest of the artifact the
// returned bundle actually came from (see [resolveAndPull] for why the two must
// come from one observation). The digest is empty only when nothing ever told us
// — never a different artifact's.
//
// The originally requested ref is preserved as provenance — it is the cache key
// and the sidecar's Ref — so pinning changes what is fetched, never what this
// entry is called. A cache hit reports the digest RECORDED at pull time, so the
// answer never depends on where the tag points now.
func (c *CachedStore) PullPinned(ctx context.Context, ref string) (*contract.Bundle, string, error) {
	if bundle, digest, ok := c.pullCached(ctx, ref); ok {
		return bundle, digest, nil
	}

	// 3. Registry (slowest).
	logging.LoggerFromContext(ctx).Debug("cache miss, pulling from registry", "ref", ref)
	bundle, digest, err := resolveAndPull(ctx, c.inner, ref)
	if err != nil {
		return nil, "", err
	}

	c.storePull(ref, bundle, digest)
	if c.cacheDir != "" {
		entry := filepath.Dir(c.cachePath(ref))
		if err := c.writeCacheEntry(entry, CachedRef{Ref: ref, Digest: digest}, bundle); err != nil {
			// The pull SUCCEEDED; only its persistence did not. Say so instead of
			// dropping it silently — a cache that never fills is a performance
			// mystery, and the failure that motivated this path was invisible.
			logging.LoggerFromContext(ctx).Warn("could not cache the pulled bundle", "ref", ref, "error", err)
		} else {
			c.materialized.Store(true)
		}
	}

	return bundle, digest, nil
}

// CachedRefFile is the sidecar written beside every cached bundle.
const CachedRefFile = "ref.json"

// CachedBundleFile is the bundle archive of a cache entry. Cache walkers key on
// it, so it is also what makes an entry VISIBLE.
const CachedBundleFile = "bundle.tar.gz"

// CachedRef is what a cached bundle IS: the reference it was pulled under,
// spelled exactly as the registry was asked, plus the manifest digest of the
// artifact that answered (empty when the registry would not say).
type CachedRef struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest,omitempty"`
}

// ReadCachedRef reads the sidecar beside a cached bundle. The second result is
// false when the entry predates the sidecar or the file is unusable, and the
// caller must then fall back to reconstructing an approximate ref from the path.
func ReadCachedRef(dir string) (CachedRef, bool) {
	b, err := os.ReadFile(filepath.Join(dir, CachedRefFile))
	if err != nil {
		return CachedRef{}, false
	}
	return parseCachedRef(b)
}

// readCachedRefFrom reads the sidecar through an already-open entry handle, so
// the identity comes from the same generation as the bundle read beside it.
func readCachedRefFrom(root *os.Root) (CachedRef, bool) {
	b, err := root.ReadFile(CachedRefFile)
	if err != nil {
		return CachedRef{}, false
	}
	return parseCachedRef(b)
}

func parseCachedRef(b []byte) (CachedRef, bool) {
	var rec CachedRef
	if err := json.Unmarshal(b, &rec); err != nil || rec.Ref == "" {
		return CachedRef{}, false
	}
	return rec, true
}

// writeCacheEntry commits a bundle and its identity as ONE cache entry.
//
// An entry is two files a reader must agree about: a walker keys on
// bundle.tar.gz and asks the ref.json beside it what that bundle IS. The cache
// PATH cannot answer — cachePath maps every ':' to '/', so a registry port is
// spelled like a path segment and a digest like a tag — so a reader that finds
// no usable sidecar GUESSES an identity from the path, and the same published
// artifact enters the fleet a second time under a derived content identity.
//
// Written in place the two files can disagree: a sidecar write that fails
// (ref.json already exists as a directory) still leaves the bundle to be
// published, and an interruption between the two pairs a fresh sidecar with the
// previous pull's bytes. So the pair is built in a staging directory OUTSIDE the
// tree cache walkers scan and swapped in whole. A reader sees the old entry or
// the new entry, never a mixture, and any failure leaves the entry ABSENT — an
// ordinary cache miss the next pull repairs, not a corrupt identity.
func (c *CachedStore) writeCacheEntry(dir string, rec CachedRef, bundle *contract.Bundle) error {
	// The entry's parent first: it is an ancestor of the staging root too, so one
	// MkdirAll makes both usable.
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	// The staging root is the cache's PARENT: a half-written bundle.tar.gz inside
	// the walked tree is exactly the incoherent entry this function exists to
	// prevent. Same filesystem, so the commit below is a rename, not a copy.
	staging, err := os.MkdirTemp(filepath.Dir(c.cacheDir), ".oci-staging-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()

	if err := stageCacheEntry(staging, rec, bundle); err != nil {
		return err
	}
	// A directory rename cannot overwrite a directory, so the old entry goes
	// first. Absence is coherent; a new bundle beside a stale sidecar is not. A
	// RemoveAll that fails needs no report of its own: the rename then fails and
	// says so, and what survives is the coherent entry that was already there.
	_ = os.RemoveAll(dir)
	return os.Rename(staging, dir)
}

// stageCacheEntry writes both files of an entry into an uncommitted directory.
func stageCacheEntry(dir string, rec CachedRef, bundle *contract.Bundle) error {
	if err := writeBundleFile(filepath.Join(dir, CachedBundleFile), bundle); err != nil {
		return err
	}
	b, _ := json.Marshal(rec) // two string fields; marshalling cannot fail
	return os.WriteFile(filepath.Join(dir, CachedRefFile), b, 0o600)
}

// digestFromRef returns the manifest digest a reference already pins, else "".
func digestFromRef(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

// PinRefToDigest rewrites a reference to its immutable digest-pinned form
// "<repository>@<digest>", dropping any scheme, tag or digest it already
// carries. A tag is a ':' after the last '/', so a registry port
// (localhost:5000/...) is never mistaken for a tag separator.
func PinRefToDigest(ref, digest string) string {
	r := strings.TrimPrefix(ref, "oci://")
	if i := strings.Index(r, "@"); i >= 0 {
		r = r[:i]
	}
	slash := strings.LastIndex(r, "/")
	if colon := strings.LastIndex(r, ":"); colon > slash {
		r = r[:colon]
	}
	return r + "@" + digest
}

func (c *CachedStore) storePull(ref string, bundle *contract.Bundle, digest string) {
	c.pullMu.Lock()
	defer c.pullMu.Unlock()
	if el, ok := c.pullCache[ref]; ok {
		e := el.Value.(*pullEntry)
		e.bundle, e.digest = bundle, digest
		c.pullLRU.MoveToFront(el)
		return
	}
	c.pullCache[ref] = c.pullLRU.PushFront(&pullEntry{ref: ref, digest: digest, bundle: bundle})
	// Evict least-recently-used entries beyond the cap. Len > cap ≥ 0 guarantees
	// a non-nil Back() each iteration.
	for c.pullLRU.Len() > pullCacheMaxEntries {
		oldest := c.pullLRU.Back()
		c.pullLRU.Remove(oldest)
		delete(c.pullCache, oldest.Value.(*pullEntry).ref)
	}
}

func (c *CachedStore) cachePath(ref string) string {
	safe := strings.ReplaceAll(ref, ":", "/")
	joined := filepath.Join(c.cacheDir, safe, CachedBundleFile)
	// Ensure the resolved path stays inside the cache directory.
	if rel, err := filepath.Rel(c.cacheDir, joined); err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Join(c.cacheDir, "_invalid", CachedBundleFile)
	}
	return joined
}

// afterCachedBundleRead is a seam for driving the interleaving below in tests:
// it runs where a competing writer installs a new generation, after this reader
// has the bundle and before it asks what that bundle IS.
var afterCachedBundleRead = func() {}

// cacheEntryAttempts bounds the re-reads below. Each retry observes the
// generation that displaced the last one, so a competing writer would have to
// win the race repeatedly to exhaust them — and exhaustion is a cache MISS, not
// an incoherent answer.
const cacheEntryAttempts = 3

// readCacheEntry reads a cache entry — the bundle and the identity beside it —
// from ONE installed generation.
//
// [CachedStore.writeCacheEntry] commits a generation whole, but that only makes
// each generation coherent; it does not make a READER coherent. Two files opened
// by pathname are two observations, and another process (or another CachedStore
// over the same directory — the disk cache is shared, so its mutex proves
// nothing) can commit a new generation between them. The reader then pairs
// bundle A with identity B and publishes content under a digest it does not
// have, which is the same lie the in-place writer used to tell.
//
// So both files come from one directory HANDLE. A generation swapped out from
// under it is unlinked, not rewritten: its files keep answering this handle, and
// once RemoveAll has taken them the reader sees them ABSENT rather than
// replaced. Absent is coherent — retry and read the generation that displaced
// it, or report a miss the next pull repairs.
func readCacheEntry(dir string) (*contract.Bundle, CachedRef, bool) {
	for range cacheEntryAttempts {
		bundle, rec, swapped := readCacheGeneration(dir)
		if !swapped {
			return bundle, rec, bundle != nil
		}
	}
	return nil, CachedRef{}, false
}

// readCacheGeneration reads one generation through a single directory handle.
// The last result reports that the generation went away mid-read, so the caller
// can read its successor instead of returning half of each.
func readCacheGeneration(dir string) (*contract.Bundle, CachedRef, bool) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, CachedRef{}, false // no entry here at all: an ordinary miss
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(CachedBundleFile)
	if err != nil {
		return nil, CachedRef{}, !heldGenerationIsInstalled(root, dir)
	}
	defer func() { _ = f.Close() }()
	bundle, err := loadBundle(f)
	if err != nil {
		return nil, CachedRef{}, false
	}

	afterCachedBundleRead()

	rec, ok := readCachedRefFrom(root)
	// No sidecar has two meanings: an entry written before sidecars existed —
	// compatible, identity simply unknown — or a generation that has since been
	// swapped away, whose successor does have one.
	if !ok && !heldGenerationIsInstalled(root, dir) {
		return nil, CachedRef{}, true
	}
	return bundle, rec, false
}

// heldGenerationIsInstalled reports whether the directory this handle holds is
// still the one installed at dir. A commit replaces the directory, so a handle
// whose identity no longer matches names a generation nobody can reach.
func heldGenerationIsInstalled(root *os.Root, dir string) bool {
	held, herr := root.Stat(".")
	installed, ierr := os.Stat(dir)
	return herr == nil && ierr == nil && os.SameFile(held, installed)
}

func loadBundle(r io.Reader) (*contract.Bundle, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	fsys, err := extractTar(gr)
	if err != nil {
		return nil, err
	}
	return bundleFromFS(fsys)
}

// writeBundleFile writes the bundle archive to path. Close is reported, not
// deferred away: a gzip flush that fails on close would otherwise commit a
// truncated archive under a valid identity.
func writeBundleFile(path string, bundle *contract.Bundle) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := writeBundleTarGz(f, bundle.FS); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
