package oci

import (
	"context"
	"os"
	"path/filepath"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Test-only views of the cache, never the registry. They exist so the tests can
// ask what an entry holds without a pull falling through to the registry.

// PullCached serves ref from the cache alone and reports whether it was found.
func (c *CachedStore) PullCached(ctx context.Context, ref string) (*contract.Bundle, bool) {
	bundle, _, _, ok := c.cachedEntryFrom(ctx, ref)
	return bundle, ok
}

// PullCachedPinned is PullCached plus the identity recorded for the served bundle.
func (c *CachedStore) PullCachedPinned(ctx context.Context, ref string) (*contract.Bundle, CachedRef, bool) {
	bundle, rec, _, ok := c.cachedEntryFrom(ctx, ref)
	return bundle, rec, ok
}

// ReadCachedRef reads the sidecar beside a cached bundle, for tests checking
// what a writer wrote.
func ReadCachedRef(dir string) (CachedRef, bool) {
	b, err := os.ReadFile(filepath.Join(dir, CachedRefFile))
	if err != nil {
		return CachedRef{}, false
	}
	return parseCachedRef(b)
}
