package oci

import (
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/pkg/contract"
)

// CachedStore wraps a BundleStore with local disk caching for Pull operations.
// Bundles are cached by OCI reference under ~/.cache/pacto/oci/.
type CachedStore struct {
	inner    BundleStore
	cacheDir string
}

// NewCachedStore creates a BundleStore that caches pulled bundles on disk.
// If the cache directory cannot be determined, caching is silently disabled.
func NewCachedStore(inner BundleStore) *CachedStore {
	dir, err := pactoCacheDir()
	if err != nil {
		dir = ""
	}
	return &CachedStore{inner: inner, cacheDir: dir}
}

// DisableCache turns off caching so all operations go directly to the registry.
func (c *CachedStore) DisableCache() {
	c.cacheDir = ""
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
	return c.inner.ListTags(ctx, repo)
}

func (c *CachedStore) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	if c.cacheDir != "" {
		cachePath := c.cachePath(ref)
		if bundle, err := c.loadFromCache(cachePath); err == nil {
			return bundle, nil
		}
	}

	bundle, err := c.inner.Pull(ctx, ref)
	if err != nil {
		return nil, err
	}

	if c.cacheDir != "" {
		_ = c.saveToCache(c.cachePath(ref), bundle)
	}

	return bundle, nil
}

func (c *CachedStore) cachePath(ref string) string {
	safe := strings.ReplaceAll(ref, ":", "/")
	return filepath.Join(c.cacheDir, safe, "bundle.tar.gz")
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

	pf, err := fsys.Open("pacto.yaml")
	if err != nil {
		return nil, err
	}
	defer func() { _ = pf.Close() }()

	ct, err := contract.Parse(pf)
	if err != nil {
		return nil, err
	}

	return &contract.Bundle{Contract: ct, FS: fsys}, nil
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
