package oci

import (
	"compress/gzip"
	"container/list"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

	// pullCache is a bounded LRU: map ref -> list element, with pullLRU ordering
	// entries most-recently-used at the front. Guarded by pullMu.
	pullMu    sync.Mutex
	pullCache map[string]*list.Element
	pullLRU   *list.List

	tagsMu    sync.Mutex
	tagsCache map[string][]string
}

// pullEntry is the value stored in each pullLRU list element.
type pullEntry struct {
	ref    string
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
	// 1. In-memory cache (fastest).
	c.pullMu.Lock()
	if el, ok := c.pullCache[ref]; ok {
		c.pullLRU.MoveToFront(el)
		b := el.Value.(*pullEntry).bundle
		c.pullMu.Unlock()
		logging.LoggerFromContext(ctx).Debug("cache hit (memory)", "ref", ref)
		return b, true
	}
	c.pullMu.Unlock()

	// 2. Disk cache (skipped when --no-cache / DisableCache is active).
	if c.cacheDir != "" && !c.skipDiskReads {
		cachePath := c.cachePath(ref)
		if bundle, err := c.loadFromCache(cachePath); err == nil {
			logging.LoggerFromContext(ctx).Debug("cache hit (disk)", "ref", ref)
			c.storePull(ref, bundle)
			return bundle, true
		}
	}
	return nil, false
}

// Pull returns the bundle for ref, checking the in-memory cache, then the disk
// cache (skipped when DisableCache is active), then the wrapped store. Registry
// pulls are stored in memory and persisted to disk for future lookups.
func (c *CachedStore) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if bundle, ok := c.PullCached(ctx, ref); ok {
		return bundle, nil
	}

	// 3. Registry (slowest).
	logging.LoggerFromContext(ctx).Debug("cache miss, pulling from registry", "ref", ref)
	bundle, err := c.inner.Pull(ctx, ref)
	if err != nil {
		return nil, err
	}

	c.storePull(ref, bundle)
	if c.cacheDir != "" {
		path := c.cachePath(ref)
		// The sidecar goes down FIRST. A cache walker keys on bundle.tar.gz, so
		// recording what the bundle is BEFORE the bundle appears means a concurrent
		// reader can never find an artifact whose identity is still missing — which
		// is the same reader, in the same process, when a fleet refresh runs its
		// registry source and its cache source side by side.
		c.saveRefRecord(ctx, filepath.Dir(path), ref)
		_ = c.saveToCache(path, bundle)
	}

	return bundle, nil
}

// CachedRefFile is the sidecar written beside every cached bundle.
const CachedRefFile = "ref.json"

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
	var rec CachedRef
	if err := json.Unmarshal(b, &rec); err != nil || rec.Ref == "" {
		return CachedRef{}, false
	}
	return rec, true
}

// saveRefRecord records what the bundle just written to dir actually is.
//
// The cache PATH cannot carry that. cachePath maps every ':' to '/', so
// "reg:5000/demo/svc@sha256:abc" and "reg/5000/demo/svc@sha256/abc" name the
// same directory: a reader walking the cache cannot tell a registry port from a
// path segment, nor a digest from a tag. Reconstructing a ref from the path
// therefore invents a different service domain AND has no manifest digest — so
// the same published artifact enters the fleet twice, once under its immutable
// registry identity and once under a derived content identity. The sidecar
// removes the guess, and removes it OFFLINE: the disconnected reader gets the
// exact identity from disk without dialing anything.
func (c *CachedStore) saveRefRecord(ctx context.Context, dir, ref string) {
	rec := CachedRef{Ref: ref, Digest: digestFromRef(ref)}
	if rec.Digest == "" {
		// A tag pull fetched the content but was never told its manifest digest.
		// One HEAD, on the path that has just performed a FULL pull, is what lets
		// the offline reader agree with the registry about identity; a cache hit
		// never reaches here, so it costs once per artifact. Best effort: a
		// registry that will not answer leaves the digest unknown, which is
		// precisely what the reader then reports.
		if d, err := c.inner.Resolve(ctx, ref); err == nil {
			rec.Digest = d
		}
	}
	b, _ := json.Marshal(rec) // two string fields; marshalling cannot fail
	_ = os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, CachedRefFile), b, 0o600)
}

// digestFromRef returns the manifest digest a reference already pins, else "".
func digestFromRef(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

func (c *CachedStore) storePull(ref string, bundle *contract.Bundle) {
	c.pullMu.Lock()
	defer c.pullMu.Unlock()
	if el, ok := c.pullCache[ref]; ok {
		el.Value.(*pullEntry).bundle = bundle
		c.pullLRU.MoveToFront(el)
		return
	}
	c.pullCache[ref] = c.pullLRU.PushFront(&pullEntry{ref: ref, bundle: bundle})
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
	joined := filepath.Join(c.cacheDir, safe, "bundle.tar.gz")
	// Ensure the resolved path stays inside the cache directory.
	if rel, err := filepath.Rel(c.cacheDir, joined); err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Join(c.cacheDir, "_invalid", "bundle.tar.gz")
	}
	return joined
}

func (c *CachedStore) loadFromCache(path string) (*contract.Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
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

func (c *CachedStore) saveToCache(path string, bundle *contract.Bundle) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	return writeBundleTarGz(f, bundle.FS)
}
