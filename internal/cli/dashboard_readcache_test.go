package cli

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// backend answers every request with its path, counting what reached it.
type backend struct {
	hits   atomic.Int64
	status int
	err    error
	body   io.Reader
}

func (b *backend) RoundTrip(r *http.Request) (*http.Response, error) {
	b.hits.Add(1)
	if b.err != nil {
		return nil, b.err
	}
	body := b.body
	if body == nil {
		body = strings.NewReader(r.URL.Path)
	}
	return &http.Response{StatusCode: max(b.status, http.StatusOK), Header: http.Header{"X": {"y"}}, Body: io.NopCloser(body)}, nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("cut off") }

func call(t *testing.T, rt http.RoundTripper, method, url, token string, headers ...string) (*http.Response, string, error) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return nil, "", err
	}
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b), nil
}

func TestReadCacheServesOnlyTheSameCredentials(t *testing.T) {
	now := time.Unix(0, 0)
	c := newReadCache()
	c.now = func() time.Time { return now }
	b := &backend{}
	rt := c.wrap(b)
	const url = "https://api/apis/x/v1/stages"
	if _, body, _ := call(t, rt, http.MethodGet, url, "alice"); body != "/apis/x/v1/stages" {
		t.Fatalf("body %q", body)
	}
	resp, body, _ := call(t, rt, http.MethodGet, url, "alice")
	if b.hits.Load() != 1 || body != "/apis/x/v1/stages" || resp.Header.Get("X") != "y" || resp.StatusCode != http.StatusOK {
		t.Fatalf("a second read within the ttl must be served from the cache: hits %d body %q", b.hits.Load(), body)
	}
	// Someone else, or the same token impersonating someone, is asked afresh.
	call(t, rt, http.MethodGet, url, "bob")
	call(t, rt, http.MethodGet, url, "alice", "Impersonate-User", "carol")
	if b.hits.Load() != 3 {
		t.Fatalf("another person's read must reach the apiserver: hits %d", b.hits.Load())
	}
	now = now.Add(readTTL)
	call(t, rt, http.MethodGet, url, "alice")
	if b.hits.Load() != 4 || len(c.entries) != 3 {
		t.Fatalf("an expired answer must be read again and replaced: hits %d entries %d", b.hits.Load(), len(c.entries))
	}
	if c.size != 3*len("/apis/x/v1/stages") {
		t.Fatalf("size %d", c.size)
	}
}

func TestReadCacheForgetsWhatAWriterRead(t *testing.T) {
	c := newReadCache()
	b := &backend{}
	rt := c.wrap(b)
	call(t, rt, http.MethodGet, "https://api/a", "alice")
	call(t, rt, http.MethodGet, "https://api/a", "bob")
	call(t, rt, http.MethodPost, "https://api/ops", "alice")
	call(t, rt, http.MethodGet, "https://api/a", "bob")
	call(t, rt, http.MethodGet, "https://api/a", "alice")
	if b.hits.Load() != 4 {
		t.Fatalf("a write must forget the writer's reads and only theirs: hits %d", b.hits.Load())
	}
	call(t, rt, http.MethodGet, "https://api/a?watch=true", "alice")
	call(t, rt, http.MethodGet, "https://api/a?watch=true", "alice")
	if b.hits.Load() != 6 {
		t.Fatalf("a watch is never cached: hits %d", b.hits.Load())
	}
}

// writing is a backend that, while answering a read, lets a write through.
type writing struct {
	backend
	during func()
}

func (w *writing) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet && w.during != nil {
		f := w.during
		w.during = nil
		f()
	}
	return w.backend.RoundTrip(r)
}

func TestReadCacheDropsAReadAWriteOvertook(t *testing.T) {
	now := time.Unix(0, 0)
	c := newReadCache()
	c.now = func() time.Time { return now }
	b := &writing{}
	rt := c.wrap(b)
	b.during = func() {
		now = now.Add(time.Millisecond)
		call(t, rt, http.MethodPost, "https://api/ops", "bob")
	}
	call(t, rt, http.MethodGet, "https://api/a", "alice")
	if len(c.entries) != 0 {
		t.Fatal("a read that started before a write must not be kept")
	}
}

func TestReadCacheKeepsOnlyWholeSuccessfulAnswers(t *testing.T) {
	c := newReadCache()
	b := &backend{status: http.StatusForbidden}
	rt := c.wrap(b)
	resp, _, _ := call(t, rt, http.MethodGet, "https://api/a", "alice")
	call(t, rt, http.MethodGet, "https://api/a", "alice")
	if resp.StatusCode != http.StatusForbidden || b.hits.Load() != 2 {
		t.Fatalf("a refusal is passed on and never kept: %d hits %d", resp.StatusCode, b.hits.Load())
	}
	b.status, b.err = 0, errors.New("down")
	if _, _, err := call(t, rt, http.MethodGet, "https://api/a", "alice"); err == nil {
		t.Fatal("a failed round trip must fail")
	}
	b.err, b.body = nil, failingReader{}
	if _, _, err := call(t, rt, http.MethodGet, "https://api/a", "alice"); err == nil {
		t.Fatal("a body cut off must fail")
	}
	b.body = nil
	c.maxRead = 3
	call(t, rt, http.MethodGet, "https://api/long", "alice")
	if len(c.entries) != 0 {
		t.Fatal("an answer over maxRead is not kept")
	}
	c.maxRead, c.maxBytes = 1<<20, 10
	call(t, rt, http.MethodGet, "https://api/abcdef", "alice")
	call(t, rt, http.MethodGet, "https://api/ghijkl", "alice")
	if len(c.entries) != 1 || c.size != len("/ghijkl") {
		t.Fatalf("past maxBytes the cache starts over: %d entries, %d bytes", len(c.entries), c.size)
	}
}
