package k8sclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
)

// proxyClient is a client built from a kubeconfig whose server is srv and
// whose user holds token, outside the cluster.
func proxyClient(t *testing.T, srv *httptest.Server, token string) *k8sGoClient {
	t.Helper()
	kubeconfig := filepath.Join(t.TempDir(), "config")
	_ = os.WriteFile(kubeconfig, fmt.Appendf(nil, `apiVersion: v1
kind: Config
clusters:
- cluster: {server: %q, insecure-skip-tls-verify: true}
  name: c
contexts:
- context: {cluster: c, user: u}
  name: c
current-context: c
users:
- name: u
  user: {token: %q}
`, srv.URL, token), 0o600)
	t.Setenv("KUBECONFIG", kubeconfig)
	b := defaultBuilders()
	b.inCluster = func() (*rest.Config, error) { return nil, fmt.Errorf("not in cluster") }
	c, err := b.goClient("")
	if err != nil {
		t.Fatal(err)
	}
	return c.(*k8sGoClient)
}

func TestServiceProxyGetUsesCallerConfig(t *testing.T) {
	var path, query, auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query, auth = r.URL.Path, r.URL.Query().Get("query"), r.Header.Get("Authorization")
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		if r.URL.Query().Get("deny") != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	c := proxyClient(t, srv, "caller-token")
	body, err := c.ServiceProxyGet(context.Background(), "monitoring", "prometheus-k8s", 9090, "/api/v1/query", url.Values{"query": {"up"}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(body)
	_ = body.Close()
	if string(got) != "ok" || path != "/api/v1/namespaces/monitoring/services/prometheus-k8s:9090/proxy/api/v1/query" || query != "up" || auth != "Bearer caller-token" {
		t.Errorf("body %q path %q query %q auth %q", got, path, query, auth)
	}
	if _, err := c.ServiceProxyGet(context.Background(), "monitoring", "prometheus-k8s", 9090, "/api/v1/query", url.Values{"deny": {"1"}}); !apierrors.IsForbidden(err) {
		t.Errorf("a forbidden proxy read = %v", err)
	}
}

// TestServiceProxyGetBodyBound shows the answer streams: the server holds the
// rest of an answer larger than 16 MiB until the caller has read past the
// bound, so a client that buffered the whole answer would never return.
func TestServiceProxyGetBodyBound(t *testing.T) {
	const bound = 16 << 20
	release := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), bound+1024))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), bound))
	}))
	defer srv.Close()
	defer close(release)
	body, err := proxyClient(t, srv, "t").ServiceProxyGet(context.Background(), "monitoring", "prometheus-k8s", 9090, "/api/v1/query", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	n, err := io.Copy(io.Discard, io.LimitReader(body, bound+1))
	if err != nil || n != bound+1 {
		t.Errorf("read %d, %v", n, err)
	}
}
