/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package loader handles resolving and parsing Pacto contracts from various sources.
package loader

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/trianalab/pacto/v2/pkg/contract"
)

// LoadResult contains the parsed contract, raw bytes, and bundle filesystem.
type LoadResult struct {
	Contract       *contract.Contract
	RawYAML        []byte
	BundleFS       fs.FS
	ResolvedRef    string // The resolved OCI reference (with tag), empty for inline
	ResolvedDigest string // The OCI manifest digest (sha256:...), empty for inline
}

type cacheEntry struct {
	result    *LoadResult
	err       error
	expiresAt time.Time
}

type tagCacheEntry struct {
	tags      []string
	err       error
	expiresAt time.Time
}

// Loader resolves and parses Pacto contracts from OCI registries or inline YAML.
type Loader struct {
	oci         *OCIPuller
	cache       map[string]cacheEntry
	cacheMu     sync.RWMutex
	cacheTTL    time.Duration
	tagCache    map[string]tagCacheEntry
	tagCacheMu  sync.RWMutex
	tagCacheTTL time.Duration
}

// New creates a Loader with OCI pulling configured.
func New() *Loader {
	return &Loader{
		oci:         NewOCIPuller(),
		cache:       make(map[string]cacheEntry),
		cacheTTL:    30 * time.Second,
		tagCache:    make(map[string]tagCacheEntry),
		tagCacheTTL: 5 * time.Minute,
	}
}

// Load resolves a Pacto contract from the given spec.
// Results are cached for 30s to avoid redundant parsing during rapid reconciliation.
// authOverride provides per-call credentials from a K8s Secret (nil uses default keychains).
func (l *Loader) Load(ctx context.Context, ociRef, inline string, authOverride *authn.AuthConfig) (*LoadResult, error) {
	// Skip cache when per-CR credentials are provided — different CRs may
	// have different pull secrets for the same OCI ref.
	if authOverride != nil {
		return l.loadUncached(ctx, ociRef, inline, authOverride)
	}

	key := l.cacheKey(ociRef, inline)

	// Check cache
	l.cacheMu.RLock()
	if entry, ok := l.cache[key]; ok && time.Now().Before(entry.expiresAt) {
		l.cacheMu.RUnlock()
		return entry.result, entry.err
	}
	l.cacheMu.RUnlock()

	result, err := l.loadUncached(ctx, ociRef, inline, nil)

	// Cache the result (even errors, to avoid repeated failing loads)
	l.cacheMu.Lock()
	l.cache[key] = cacheEntry{
		result:    result,
		err:       err,
		expiresAt: time.Now().Add(l.cacheTTL),
	}
	// Evict expired entries periodically (simple inline cleanup)
	if len(l.cache) > 100 {
		now := time.Now()
		for k, v := range l.cache {
			if now.After(v.expiresAt) {
				delete(l.cache, k)
			}
		}
	}
	l.cacheMu.Unlock()

	return result, err
}

func (l *Loader) loadUncached(ctx context.Context, ociRef, inline string, authOverride *authn.AuthConfig) (*LoadResult, error) {
	if inline != "" {
		return loadInline(inline)
	}
	if ociRef != "" {
		return l.oci.Pull(ctx, normalizeOCIRef(ociRef), authOverride)
	}
	return nil, fmt.Errorf("no contract source specified: set either spec.contractRef.oci or spec.contractRef.inline")
}

// ListTags returns all semver tags for the given OCI repository.
// Results are cached for 5 minutes to avoid redundant registry API calls
// during cascade reconciles triggered by PactoRevision creation.
func (l *Loader) ListTags(ctx context.Context, ociRef string, authOverride *authn.AuthConfig) ([]string, error) {
	if ociRef == "" {
		return nil, nil
	}

	// Skip cache when per-CR credentials are provided — different CRs may
	// have different pull secrets for the same OCI ref.
	if authOverride != nil {
		return l.oci.ListTags(ctx, normalizeOCIRef(ociRef), authOverride)
	}

	ref := normalizeOCIRef(ociRef)
	key := tagCacheKey(ref)

	l.tagCacheMu.RLock()
	if entry, ok := l.tagCache[key]; ok && time.Now().Before(entry.expiresAt) {
		l.tagCacheMu.RUnlock()
		return entry.tags, entry.err
	}
	l.tagCacheMu.RUnlock()

	tags, err := l.oci.ListTags(ctx, ref, nil)

	l.tagCacheMu.Lock()
	l.tagCache[key] = tagCacheEntry{
		tags:      tags,
		err:       err,
		expiresAt: time.Now().Add(l.tagCacheTTL),
	}
	if len(l.tagCache) > 100 {
		now := time.Now()
		for k, v := range l.tagCache {
			if now.After(v.expiresAt) {
				delete(l.tagCache, k)
			}
		}
	}
	l.tagCacheMu.Unlock()

	return tags, err
}

// tagCacheKey returns a normalized cache key for tag list lookups.
// Strips any tag or digest from the ref so all variants of the same repo share one entry.
func tagCacheKey(ref string) string {
	// Strip digest
	if idx := strings.Index(ref, "@"); idx >= 0 {
		ref = ref[:idx]
	}
	// Strip tag
	if idx := strings.LastIndex(ref, ":"); idx > strings.LastIndex(ref, "/") {
		ref = ref[:idx]
	}
	return "tags:" + ref
}

func (l *Loader) cacheKey(ociRef, inline string) string {
	if inline != "" {
		h := sha256.Sum256([]byte(inline))
		return fmt.Sprintf("inline:%x", h[:8])
	}
	return "oci:" + normalizeOCIRef(ociRef)
}

// normalizeOCIRef cleans an OCI reference by URL-decoding and stripping the oci:// scheme.
func normalizeOCIRef(ref string) string {
	// URL-decode if it contains percent-encoded characters
	if strings.Contains(ref, "%") {
		if decoded, err := url.QueryUnescape(ref); err == nil {
			ref = decoded
		}
	}
	return strings.TrimPrefix(ref, "oci://")
}

func loadInline(yaml string) (*LoadResult, error) {
	raw := []byte(yaml)
	c, err := contract.Parse(strings.NewReader(yaml))
	if err != nil {
		return nil, fmt.Errorf("failed to parse inline contract: %w", err)
	}
	return &LoadResult{Contract: c, RawYAML: raw, BundleFS: nil}, nil
}
