package oci

import (
	"compress/gzip"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

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

	pullMu    sync.Mutex
	pullCache map[string]*contract.Bundle

	tagsMu    sync.Mutex
	tagsCache map[string][]string
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
		pullCache: map[string]*contract.Bundle{},
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
	c.pullCache = map[string]*contract.Bundle{}
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
		slog.Debug("tags cache hit", "repo", repo)
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

// Pull returns the bundle for ref, checking the in-memory cache, then the disk
// cache (skipped when DisableCache is active), then the wrapped store. Registry
// pulls are stored in memory and persisted to disk for future lookups.
func (c *CachedStore) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	// 1. In-memory cache (fastest).
	c.pullMu.Lock()
	if b, ok := c.pullCache[ref]; ok {
		c.pullMu.Unlock()
		slog.Debug("cache hit (memory)", "ref", ref)
		return b, nil
	}
	c.pullMu.Unlock()

	// 2. Disk cache (skipped when --no-cache / DisableCache is active).
	if c.cacheDir != "" && !c.skipDiskReads {
		cachePath := c.cachePath(ref)
		if bundle, err := c.loadFromCache(cachePath); err == nil {
			slog.Debug("cache hit (disk)", "ref", ref)
			c.storePull(ref, bundle)
			return bundle, nil
		}
	}

	// 3. Registry (slowest).
	slog.Debug("cache miss, pulling from registry", "ref", ref)
	bundle, err := c.inner.Pull(ctx, ref)
	if err != nil {
		return nil, err
	}

	c.storePull(ref, bundle)
	if c.cacheDir != "" {
		_ = c.saveToCache(c.cachePath(ref), bundle)
	}

	return bundle, nil
}

func (c *CachedStore) storePull(ref string, bundle *contract.Bundle) {
	c.pullMu.Lock()
	c.pullCache[ref] = bundle
	c.pullMu.Unlock()
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
