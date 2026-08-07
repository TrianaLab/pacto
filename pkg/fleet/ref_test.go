package fleet

import "testing"

func TestIsDigestPinnedRef(t *testing.T) {
	cases := []struct {
		ref        string
		pinned     bool
		wantDigest string
	}{
		{"oci://ghcr.io/acme/payments@sha256:abc123", true, "sha256:abc123"},
		{"localhost:5000/acme/payments@sha256:deadbeef", true, "sha256:deadbeef"},
		{"@sha256:abc", true, "sha256:abc"},
		{"oci://ghcr.io/acme/payments:1.0", false, ""}, // mutable tag
		{"payments", false, ""},                        // bare name
		{"file:///abs/path", false, ""},                // local path (only "/")
		{"./relative", false, ""},                      // local path
		{"repo@sha256:", false, ""},                    // empty digest body
		{"repo@", false, ""},                           // empty after @
		{"repo@nocolon", false, ""},                    // no algorithm separator
		{"repo@a:b:c", false, ""},                      // not a canonical single-colon digest
		{"localhost:5000/repo", false, ""},             // registry port, no digest
	}
	for _, c := range cases {
		got := IsDigestPinnedRef(c.ref)
		if got != c.pinned {
			t.Errorf("IsDigestPinnedRef(%q) = %v, want %v", c.ref, got, c.pinned)
		}
		d, ok := DigestFromRef(c.ref)
		if ok != c.pinned || d != c.wantDigest {
			t.Errorf("DigestFromRef(%q) = (%q,%v), want (%q,%v)", c.ref, d, ok, c.wantDigest, c.pinned)
		}
	}
}
