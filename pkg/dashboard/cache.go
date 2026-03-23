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

// memoryCache is a simple in-memory cache with TTL-based expiration.
type memoryCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	value     any
	expiresAt time.Time
}

// NewMemoryCache creates a new in-memory cache.
func NewMemoryCache() Cache {
	return &memoryCache{entries: make(map[string]cacheEntry)}
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
	c.entries[key] = cacheEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
	c.mu.Unlock()
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
