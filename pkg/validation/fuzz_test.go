package validation

import (
	"net/url"
	"strings"
	"testing"
)

// FuzzValidCapabilityPath proves INV-6 path normalization is total and SSRF-safe.
// For ANY path, constructing the probe URL the way the collector does (authority
// from trusted fields, contract path in the Path component) never lets the path
// hijack the host. Additionally, every path validCapabilityPath ACCEPTS is a clean
// absolute path with no scheme/authority/userinfo/fragment/opaque component.
func FuzzValidCapabilityPath(f *testing.F) {
	for _, s := range []string{
		"/health", "/api/v1/status", "//evil.example", "/%2Fevil.example",
		"/%2f%2fevil", "http://evil.example", "///a", "/a?b=c", "/a#frag",
		"", "relative/path", "\\/evil", "/a/../b",
	} {
		f.Add(s)
	}
	const trusted = "svc.ns.svc:8080"
	f.Fuzz(func(t *testing.T, path string) {
		ok := validCapabilityPath(path) // must not panic

		// SSRF invariant: authority stays trusted for any path.
		u := url.URL{Scheme: "http", Host: trusted, Path: path}
		if parsed, err := url.Parse(u.String()); err == nil && parsed.Host != trusted {
			t.Fatalf("path %q hijacked host to %q", path, parsed.Host)
		}

		if ok {
			if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
				t.Fatalf("accepted non-absolute / authority-like path %q", path)
			}
			pu, err := url.Parse(path)
			if err != nil {
				t.Fatalf("accepted unparseable path %q", path)
			}
			if pu.Scheme != "" || pu.Host != "" || pu.User != nil || pu.Fragment != "" || pu.Opaque != "" || strings.HasPrefix(pu.Path, "//") {
				t.Fatalf("accepted path %q carries scheme/authority components", path)
			}
		}
	})
}
