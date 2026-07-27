package prober

import (
	"fmt"
	"net/url"
	"testing"
)

// FuzzBuildURL proves INV-6 holds for any contract-derived path: BuildURL always
// pins the authority to the trusted service/namespace/port, so a hostile path can
// never move the request off-host (SSRF). The service, namespace and port are
// trusted inputs (from the Kubernetes API), so only the path is fuzzed.
func FuzzBuildURL(f *testing.F) {
	for _, p := range []string{
		"/health", "/metrics", "//evil.example", "//evil.example/x",
		"/%2f%2fevil", "http://evil.example", "relative", "", "/a?b=c#d", "\\evil",
	} {
		f.Add(p)
	}
	const svc, ns = "mysvc", "myns"
	const port int32 = 8080
	wantHost := fmt.Sprintf("%s.%s.svc:%d", svc, ns, port)

	f.Fuzz(func(t *testing.T, path string) {
		got := BuildURL(svc, ns, port, path)
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("BuildURL(%q) produced unparseable URL %q: %v", path, got, err)
		}
		if u.Host != wantHost {
			t.Fatalf("path %q hijacked authority: host=%q want %q (url=%q)", path, u.Host, wantHost, got)
		}
		if u.Scheme != "http" {
			t.Fatalf("path %q changed scheme to %q", path, u.Scheme)
		}
	})
}
