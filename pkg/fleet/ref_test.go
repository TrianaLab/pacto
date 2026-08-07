package fleet

import (
	"strings"
	"testing"
)

// hex64 is a syntactically valid lower-case sha256 body (64 hex chars). Built
// programmatically so no digest-body literal drifts.
func hex64(fill string) string { return strings.Repeat(fill, 64/len(fill)+1)[:64] }

func validDigest(fill string) string { return "sha256:" + hex64(fill) }

// TestParseCanonicalOCIRef is the strict identity spec: a canonical, immutable,
// resolver-compatible reference MUST be an oci:// ref that names a repository and
// carries a syntactically valid content digest (algorithm+body validated by the
// OCI go-digest primitive). Everything else — a scheme-less ref (the dashboard
// bug), a mutable tag, a local path, a malformed/short/uppercase/unsupported
// digest, an empty repository, or extra separators — is NOT immutable.
func TestParseCanonicalOCIRef(t *testing.T) {
	good := validDigest("a")
	bad := validDigest("b")
	cases := []struct {
		name     string
		ref      string
		ok       bool
		wantRepo string
		wantDig  string
	}{
		{"canonical", "oci://ghcr.io/acme/payments@" + good, true, "ghcr.io/acme/payments", good},
		{"canonical registry port", "oci://localhost:5000/acme/pay@" + good, true, "localhost:5000/acme/pay", good},
		// The dashboard bug: a resolved digest with the oci:// scheme STRIPPED is a
		// local path to the resolver, so it must NOT count as immutable.
		{"scheme-less digest ref", "ghcr.io/acme/payments@" + good, false, "", ""},
		{"bare-name digest ref", "payments@" + good, false, "", ""},
		{"local-looking path with digest", "./svc@" + good, false, "", ""},
		{"absolute path with digest", "/abs/svc@" + good, false, "", ""},
		{"mutable tag", "oci://ghcr.io/acme/payments:1.0", false, "", ""},
		{"no tag no digest", "oci://ghcr.io/acme/payments", false, "", ""},
		{"empty repository", "oci://@" + good, false, "", ""},
		{"empty digest body", "oci://repo@sha256:", false, "", ""},
		{"short digest body", "oci://repo@sha256:abc", false, "", ""},
		{"63-hex digest body", "oci://repo@sha256:" + hex64("a")[:63], false, "", ""},
		{"uppercase digest body", "oci://repo@sha256:" + strings.ToUpper(hex64("a")), false, "", ""},
		{"unsupported algorithm", "oci://repo@md5:" + strings.Repeat("a", 32), false, "", ""},
		{"no algorithm separator", "oci://repo@nocolon", false, "", ""},
		{"extra @ separators", "oci://repo@" + good + "@" + bad, false, "", ""},
		{"file scheme", "file:///abs/path", false, "", ""},
		{"relative path", "./relative", false, "", ""},
		{"empty", "", false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, dgst, err := ParseCanonicalOCIRef(c.ref)
			if (err == nil) != c.ok {
				t.Fatalf("ParseCanonicalOCIRef(%q) err=%v, want ok=%v", c.ref, err, c.ok)
			}
			if c.ok {
				if repo != c.wantRepo {
					t.Errorf("repository = %q, want %q", repo, c.wantRepo)
				}
				if dgst.String() != c.wantDig {
					t.Errorf("digest = %q, want %q", dgst.String(), c.wantDig)
				}
			}
			if got := IsDigestPinnedRef(c.ref); got != c.ok {
				t.Errorf("IsDigestPinnedRef(%q) = %v, want %v", c.ref, got, c.ok)
			}
		})
	}
}
