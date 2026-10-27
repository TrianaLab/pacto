package cli

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// readCache keeps the apiserver's answers to a caller's reads for a moment,
// so the views a page polls together, and a person's concurrent requests,
// share one read instead of each listing every Stage and Operation again.
//
// An answer is keyed by the exact request: method, URL and the credentials
// it carried (the caller's own bearer token), so it is only ever served to a
// request the apiserver would have answered the same way for the same
// person. A write through the cache forgets everything those credentials
// read, so a person sees their own change at once.
type readCache struct {
	ttl time.Duration
	now func() time.Time
	// maxBytes bounds what the cache holds, maxRead one answer.
	maxBytes, maxRead int

	mu      sync.Mutex
	entries map[string]cachedRead
	size    int
	group   singleflight.Group
	// wrote is when the last write went through: a read that started
	// before it may hold what the write changed, so it is not kept.
	wrote time.Time
}

type cachedRead struct {
	at     time.Time
	who    string
	status int
	header http.Header
	body   []byte
}

// Bounds. ponytail: past maxCacheBytes the cache starts over; an LRU if a
// dashboard serves enough people at once to keep hitting it.
const (
	readTTL       = 2 * time.Second
	maxCacheBytes = 256 << 20
	maxCachedRead = 64 << 20
)

func newReadCache() *readCache {
	return &readCache{ttl: readTTL, now: time.Now, maxBytes: maxCacheBytes, maxRead: maxCachedRead, entries: map[string]cachedRead{}}
}

// who names the credentials a request carries.
func who(r *http.Request) string {
	var b strings.Builder
	b.WriteString(r.Header.Get("Authorization"))
	for k, v := range r.Header {
		if strings.HasPrefix(k, "Impersonate-") {
			b.WriteString("\x00" + k + "=" + strings.Join(v, ","))
		}
	}
	return b.String()
}

// wrap is a rest.Config WrapTransport.
func (c *readCache) wrap(rt http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := who(r)
		if r.Method != http.MethodGet || r.URL.Query().Get("watch") == "true" {
			c.forget(w)
			return rt.RoundTrip(r)
		}
		key := w + "\x00" + r.URL.String()
		if e, ok := c.fresh(key); ok {
			return e.response(r), nil
		}
		v, err, _ := c.group.Do(key, func() (any, error) {
			started := c.now()
			resp, err := rt.RoundTrip(r)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
			e := cachedRead{at: started, who: w, status: resp.StatusCode, header: resp.Header, body: body}
			if resp.StatusCode == http.StatusOK && len(body) <= c.maxRead {
				c.put(key, e)
			}
			return e, nil
		})
		if err != nil {
			return nil, err
		}
		return v.(cachedRead).response(r), nil
	})
}

func (c *readCache) fresh(key string) (cachedRead, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e, ok && c.now().Sub(e.at) < c.ttl
}

func (c *readCache) put(key string, e cachedRead) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.at.Before(c.wrote) {
		return
	}
	if c.size+len(e.body) > c.maxBytes {
		c.entries, c.size = map[string]cachedRead{}, 0
	}
	c.size += len(e.body) - len(c.entries[key].body)
	c.entries[key] = e
}

// forget drops every answer read with credentials w.
func (c *readCache) forget(w string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wrote = c.now()
	for k, e := range c.entries {
		if e.who == w {
			c.size -= len(e.body)
			delete(c.entries, k)
		}
	}
}

func (e cachedRead) response(r *http.Request) *http.Response {
	return &http.Response{StatusCode: e.status, Status: http.StatusText(e.status), Header: e.header.Clone(),
		Body: io.NopCloser(bytes.NewReader(e.body)), ContentLength: int64(len(e.body)), Request: r,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
