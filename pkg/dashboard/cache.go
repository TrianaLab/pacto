package dashboard

import (
	"context"
	"sync"
	"time"
)

// Cache defines the interface for a generic key-value cache.
type Cache interface {
	Get(key string) (any, bool)
	Set(key string, value any, ttl time.Duration)
	InvalidateAll()
}

// defaultMaxCacheEntries bounds the in-memory cache so a large fleet (many
// services × versions × diffs) or expired-but-never-read entries cannot grow it
// without limit and OOM the dashboard.
const defaultMaxCacheEntries = 4096

// memoryCache is a simple in-memory cache with TTL-based expiration and a bounded
// number of entries.
type memoryCache struct {
	mu         sync.RWMutex
	entries    map[string]cacheEntry
	maxEntries int
}

type cacheEntry struct {
	value     any
	expiresAt time.Time
}

// NewMemoryCache creates a new bounded in-memory cache.
func NewMemoryCache() Cache {
	return &memoryCache{entries: make(map[string]cacheEntry), maxEntries: defaultMaxCacheEntries}
}

func (c *memoryCache) Get(key string) (any, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false
	}
	return entry.value, true
}

func (c *memoryCache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	// Bound the cache: if a NEW key would push it over the cap, reclaim room first —
	// drop expired entries, and if none were reclaimable, evict the soonest-to-expire
	// live entry. Existing keys are updates and never grow the map.
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		c.evictLocked()
	}
	c.entries[key] = cacheEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
	c.mu.Unlock()
}

// evictLocked frees at least one slot: it deletes every expired entry in a single
// pass and, if that reclaimed nothing (all entries live and still at capacity),
// evicts the soonest-to-expire entry. The caller must hold c.mu.
// ponytail: soonest-to-expire, not true LRU — it would expire imminently anyway;
// swap for an LRU only if hit-rate measurably suffers.
func (c *memoryCache) evictLocked() {
	now := time.Now()
	var oldestKey string
	oldestSet := false
	var oldest time.Time
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			delete(c.entries, k)
			continue
		}
		if !oldestSet || e.expiresAt.Before(oldest) {
			oldestKey, oldest, oldestSet = k, e.expiresAt, true
		}
	}
	if oldestSet && len(c.entries) >= c.maxEntries {
		delete(c.entries, oldestKey)
	}
}

func (c *memoryCache) InvalidateAll() {
	c.mu.Lock()
	c.entries = make(map[string]cacheEntry)
	c.mu.Unlock()
}

// CachedDataSource wraps a DataSource with an in-memory cache layer.
// The prefix scopes cache keys so that multiple CachedDataSource instances
// sharing the same Cache do not collide (e.g. k8s vs cache vs oci).
type CachedDataSource struct {
	source DataSource
	cache  Cache
	ttl    time.Duration
	prefix string // cache key prefix, e.g. "k8s:" or "cache:"
}

// NewCachedDataSource wraps the given source with caching.
// prefix must be unique per source type when sharing a Cache instance.
// ttl controls how long entries are cached before re-fetching.
func NewCachedDataSource(source DataSource, cache Cache, ttl time.Duration, prefix string) *CachedDataSource {
	return &CachedDataSource{source: source, cache: cache, ttl: ttl, prefix: prefix}
}

// ListServices returns the wrapped source's service list, served from cache
// within the TTL and otherwise fetched and cached.
func (c *CachedDataSource) ListServices(ctx context.Context) ([]Service, error) {
	key := c.prefix + "services:list"
	if v, ok := c.cache.Get(key); ok {
		if sv, ok := v.([]Service); ok {
			return sv, nil
		}
	}
	result, err := c.source.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	c.cache.Set(key, result, c.ttl)
	return result, nil
}

// GetService returns the wrapped source's details for name, served from cache
// within the TTL and otherwise fetched and cached.
func (c *CachedDataSource) GetService(ctx context.Context, name string) (*ServiceDetails, error) {
	key := c.prefix + "service:" + name
	if v, ok := c.cache.Get(key); ok {
		if sv, ok := v.(*ServiceDetails); ok {
			return sv, nil
		}
	}
	result, err := c.source.GetService(ctx, name)
	if err != nil {
		return nil, err
	}
	c.cache.Set(key, result, c.ttl)
	return result, nil
}

// GetVersions returns the wrapped source's version history for name, served
// from cache within the TTL and otherwise fetched and cached.
func (c *CachedDataSource) GetVersions(ctx context.Context, name string) ([]Version, error) {
	key := c.prefix + "versions:" + name
	if v, ok := c.cache.Get(key); ok {
		if sv, ok := v.([]Version); ok {
			return sv, nil
		}
	}
	result, err := c.source.GetVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	c.cache.Set(key, result, c.ttl)
	return result, nil
}

// GetDiff returns the wrapped source's diff between a and b, served from cache
// within the TTL and otherwise computed and cached.
func (c *CachedDataSource) GetDiff(ctx context.Context, a, b Ref) (*DiffResult, error) {
	key := c.prefix + "diff:" + a.Name + "@" + a.Version + ".." + b.Name + "@" + b.Version
	if v, ok := c.cache.Get(key); ok {
		if sv, ok := v.(*DiffResult); ok {
			return sv, nil
		}
	}
	result, err := c.source.GetDiff(ctx, a, b)
	if err != nil {
		return nil, err
	}
	c.cache.Set(key, result, c.ttl)
	return result, nil
}

// GetServiceVersion returns the wrapped source's details for a specific ref,
// served from cache within the TTL and otherwise fetched and cached.
func (c *CachedDataSource) GetServiceVersion(ctx context.Context, ref Ref) (*ServiceDetails, error) {
	key := c.prefix + "serviceversion:" + ref.Name + "@" + ref.Version
	if v, ok := c.cache.Get(key); ok {
		if sv, ok := v.(*ServiceDetails); ok {
			return sv, nil
		}
	}
	result, err := c.source.GetServiceVersion(ctx, ref)
	if err != nil {
		return nil, err
	}
	c.cache.Set(key, result, c.ttl)
	return result, nil
}
