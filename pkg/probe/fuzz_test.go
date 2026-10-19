package probe

import (
	"net/url"
	"testing"
)

// FuzzURL proves INV-6 holds for any contract-derived path: URL either rejects it or pins the authority to
// the trusted address, so a hostile path can never move the request off-host (SSRF). The address is a
// trusted input (from the platform's binding), so only the path is fuzzed.
func FuzzURL(f *testing.F) {
	for _, p := range []string{
		"/health", "/metrics", "//evil.example", "//evil.example/x",
		"/%2f%2fevil", "http://evil.example", "relative", "", "/a?b=c#d", "\\evil",
	} {
		f.Add(p)
	}
	const addr = "10.1.2.3:8080"
	f.Fuzz(func(t *testing.T, path string) {
		got, err := URL(addr, path)
		if err != nil {
			return
		}
		u, err := url.Parse(got)
		if err != nil {
			t.Fatalf("URL(%q) produced unparseable URL %q: %v", path, got, err)
		}
		if u.Host != addr || u.Scheme != "http" {
			t.Fatalf("path %q moved the request: %q", path, got)
		}
	})
}
