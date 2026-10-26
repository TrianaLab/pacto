package app

import (
	"slices"
	"sync"
)

// StreamCache keeps verified record streams by what fixes their content: the
// anchor, the stream, the head the Stage names and the trust they were checked
// with. A record never changes once written, so while the head has not moved
// the stream reads the same, and history, blame, undo and a change's undoneBy
// on every dashboard refresh share one read instead of each fetching and
// verifying every record again. The zero value is ready to use.
type StreamCache struct {
	mu sync.Mutex
	m  map[string]*streamRead
}

// maxStreams bounds the cache.
// ponytail: a bounded map reset when full; an LRU if a dashboard ever watches
// more than maxStreams streams at once.
const maxStreams = 256

func (c *StreamCache) get(key string) *streamRead {
	if c == nil || key == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[key].clone()
}

// put keeps a clean read only: one with an unreadable record or a problem
// may be a registry hiccup, and is read again next time.
func (c *StreamCache) put(key string, r *streamRead) {
	if c == nil || key == "" || len(r.Unreadable) > 0 || len(r.Fold.Problems) > 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= maxStreams {
		c.m = map[string]*streamRead{}
	}
	c.m[key] = r.clone()
}

// clone copies what callers append to, so no answer leaks into another; the
// records themselves are only read.
func (r *streamRead) clone() *streamRead {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Fold.Problems = slices.Clone(r.Fold.Problems)
	cp.Unreadable = slices.Clone(r.Unreadable)
	return &cp
}
