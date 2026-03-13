package oci

import (
	"bytes"
	"compress/gzip"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/trianalab/pacto/pkg/contract"
)

// CachedStore wraps a BundleStore with in-memory and disk caching. Pulled
// bundles are kept in memory (fastest) and persisted to disk under
// ~/.cache/pacto/oci/ so they survive across process invocations. ListTags
// results are cached in memory for the lifetime of the process.
type CachedStore struct {
	inner    BundleStore
	cacheDir string

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

// DisableCache turns off caching so all operations go directly to the registry.
func (c *CachedStore) DisableCache() {
	c.cacheDir = ""
	c.pullMu.Lock()
	c.pullCache = map[string]*contract.Bundle{}
	c.pullMu.Unlock()
}

func pactoCacheDir() (string, error) {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		home, err := userHomeDirFn()
		if err != nil {
			return "", err
		}
		cacheDir = filepath.Join(home, ".cache")
	}
	return filepath.Join(cacheDir, "pacto", "oci"), nil
}

func (c *CachedStore) Push(ctx context.Context, ref string, bundle *contract.Bundle) (string, error) {
	return c.inner.Push(ctx, ref, bundle)
}

func (c *CachedStore) Resolve(ctx context.Context, ref string) (string, error) {
	return c.inner.Resolve(ctx, ref)
}

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

func (c *CachedStore) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	// 1. In-memory cache (fastest).
	c.pullMu.Lock()
	if b, ok := c.pullCache[ref]; ok {
		c.pullMu.Unlock()
		slog.Debug("cache hit (memory)", "ref", ref)
		return b, nil
	}
	c.pullMu.Unlock()

	// 2. Disk cache.
	if c.cacheDir != "" {
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

	rawYAML, err := fs.ReadFile(fsys, "pacto.yaml")
	if err != nil {
		return nil, err
	}

	ct, err := contract.Parse(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, err
	}

	return &contract.Bundle{Contract: ct, RawYAML: rawYAML, FS: fsys}, nil
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
