package prober

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProbe_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/health")

	if !result.Reachable {
		t.Fatalf("expected reachable, got error: %s", result.Error)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
	if !result.ContentPresent {
		t.Error("expected content present")
	}
	if result.LatencyMs < 0 {
		t.Error("expected non-negative latency")
	}
}

func TestProbe_ConnectionRefused(t *testing.T) {
	p := New(1 * time.Second)
	result := p.Probe(context.Background(), "http://127.0.0.1:1/nonexistent")

	if result.Reachable {
		t.Fatal("expected unreachable")
	}
	if result.Error == "" {
		t.Error("expected error message")
	}
	if result.StatusCode != 0 {
		t.Errorf("expected status 0, got %d", result.StatusCode)
	}
}

func TestProbe_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/metrics")

	if !result.Reachable {
		t.Fatalf("expected reachable, got error: %s", result.Error)
	}
	if result.ContentPresent {
		t.Error("expected empty content")
	}
}

func TestProbe_PrometheusParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP http_requests_total Total requests\n# TYPE http_requests_total counter\nhttp_requests_total 42\n"))
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/metrics")

	if !result.PrometheusParsed {
		t.Error("expected PrometheusParsed=true for a real Prometheus exposition body")
	}
}

func TestProbe_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/health")

	if !result.Reachable {
		t.Fatal("expected reachable (server responded)")
	}
	if result.StatusCode != 500 {
		t.Errorf("expected status 500, got %d", result.StatusCode)
	}
}

func TestProbe_Redirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/health-check", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/health")

	if !result.Reachable {
		t.Fatal("expected reachable")
	}
	if result.StatusCode != 301 {
		t.Errorf("expected status 301, got %d", result.StatusCode)
	}
}

func TestBuildURL(t *testing.T) {
	tests := []struct {
		svc, ns  string
		port     int32
		path     string
		expected string
	}{
		{"api", "default", 8080, "/health", "http://api.default.svc:8080/health"},
		{"api", "prod", 9090, "/metrics", "http://api.prod.svc:9090/metrics"},
		{"api", "default", 8080, "health", "http://api.default.svc:8080/health"},
	}
	for _, tt := range tests {
		got := BuildURL(tt.svc, tt.ns, tt.port, tt.path)
		if got != tt.expected {
			t.Errorf("BuildURL(%q, %q, %d, %q) = %q, want %q", tt.svc, tt.ns, tt.port, tt.path, got, tt.expected)
		}
	}
}

func TestNew_DefaultTimeout(t *testing.T) {
	// timeout <= 0 should fall back to defaultTimeout
	p := New(0)
	if p.client.Timeout != defaultTimeout {
		t.Errorf("expected default timeout %v, got %v", defaultTimeout, p.client.Timeout)
	}

	p2 := New(-1 * time.Second)
	if p2.client.Timeout != defaultTimeout {
		t.Errorf("expected default timeout %v for negative input, got %v", defaultTimeout, p2.client.Timeout)
	}
}

func TestProbe_InvalidURL(t *testing.T) {
	p := New(1 * time.Second)
	// A URL with a control character triggers NewRequestWithContext failure
	result := p.Probe(context.Background(), "http://invalid\x00url")

	if result.Reachable {
		t.Fatal("expected unreachable for invalid URL")
	}
	if !strings.Contains(result.Error, "invalid URL") {
		t.Errorf("expected 'invalid URL' in error, got: %s", result.Error)
	}
}

func TestSanitizeError_NoSeparator(t *testing.T) {
	err := errors.New("simple error without colon separator")
	got := sanitizeError(err)
	if got != "simple error without colon separator" {
		t.Errorf("expected original message, got: %s", got)
	}
}

func TestSanitizeError_EmptySuffix(t *testing.T) {
	// Error message where text after last ": " is empty — should return full message
	err := errors.New("prefix: ")
	got := sanitizeError(err)
	// suffix is empty (len == 0), so it falls through to return msg
	if got != "prefix: " {
		t.Errorf("expected full message for empty suffix, got: %s", got)
	}
}

func TestProbe_PrometheusParsed_SubstringNotParsed(t *testing.T) {
	// Body contains "# HELP" as a SUBSTRING but does not parse as exposition -> false (parse != substring).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"note":"# HELP and # TYPE appear only as text here"}`))
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/metrics")

	if result.PrometheusParsed {
		t.Error("expected PrometheusParsed=false: '# HELP' substring must not count as parsed metrics")
	}
}

func TestProbe_PrometheusParsed_WrongContentType(t *testing.T) {
	// Valid exposition body but a non-metrics Content-Type -> gated out (false).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# TYPE m counter\nm 1\n"))
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/metrics")

	if result.PrometheusParsed {
		t.Error("expected PrometheusParsed=false when Content-Type is not text/plain or openmetrics")
	}
}

func TestProbe_RedirectNotFollowed(t *testing.T) {
	// Verify that the CheckRedirect returning ErrUseLastResponse
	// causes the redirect to be reported as-is without following it.
	redirectCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectCount++
		if redirectCount > 1 {
			t.Error("redirect was followed — expected CheckRedirect to stop it")
		}
		http.Redirect(w, r, "/other", http.StatusFound)
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/start")

	if !result.Reachable {
		t.Fatalf("expected reachable, got error: %s", result.Error)
	}
	if result.StatusCode != 302 {
		t.Errorf("expected status 302, got %d", result.StatusCode)
	}
}

func TestBuildURL_PathWithoutSlash(t *testing.T) {
	got := BuildURL("svc", "ns", 8080, "metrics")
	want := "http://svc.ns.svc:8080/metrics"
	if got != want {
		t.Errorf("BuildURL with path without slash: got %q, want %q", got, want)
	}
}

func TestBuildURL_SSRFSafe(t *testing.T) {
	// A hostile path must never move the authority off the trusted service host (INV-6).
	wantHost := "svc.ns.svc:8080"
	for _, path := range []string{"//evil.example", "//evil.example/x", "/%2f%2fevil", "/normal"} {
		got := BuildURL("svc", "ns", 8080, path)
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("BuildURL produced unparseable URL %q: %v", got, err)
		}
		if u.Host != wantHost {
			t.Errorf("path %q hijacked host: built %q (host %q), want host %q", path, got, u.Host, wantHost)
		}
	}
}
