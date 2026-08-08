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
		// Malformed repository syntax under the real OCI grammar (requirement, item
		// 10): these carry a valid digest and a non-empty repository, so the old
		// "some non-empty text before @" check accepted them, but the
		// go-containerregistry name parser the resolver uses rejects them.
		{"uppercase repository", "oci://UPPER/repo@" + good, false, "", ""},
		{"uppercase path component", "oci://ghcr.io/UP/repo@" + good, false, "", ""},
		{"space in repository", "oci://has space/repo@" + good, false, "", ""},
		{"invalid registry port", "oci://reg.io:notaport/repo@" + good, false, "", ""},
		{"trailing slash repository", "oci://end.slash/@" + good, false, "", ""},
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

// TestClassifyExactIdentity is the spec for the SINGLE exact-content-identity
// evaluation shared by RevisionDetail, TargetDetail and Product Impact eligibility
// (requirement, item 9). The `immutable`/Exact boolean must mean the same thing
// everywhere, and the class must distinguish every failure mode:
//   - canonical OCI ref + matching recorded digest => exact (accepted);
//   - canonical OCI ref + mismatched digest => digest-mismatch (rejected);
//   - canonical OCI ref + missing recorded digest => missing-digest (accepted:
//     the pinned digest IS the content address; the recorded digest is only a
//     cross-check when present);
//   - mutable tag => mutable (rejected);
//   - local / scheme-less ref => local (rejected);
//   - malformed digest or repository => malformed (rejected).
func TestClassifyExactIdentity(t *testing.T) {
	good := validDigest("a")
	other := validDigest("b")
	cases := []struct {
		name         string
		resolvedRef  string
		recorded     string
		wantClass    IdentityClass
		wantExact    bool
		wantHasDigit bool // Repository/Digest populated (a valid canonical ref)
	}{
		{"exact match", "oci://ghcr.io/acme/pay@" + good, good, IdentityExact, true, true},
		{"missing recorded digest", "oci://ghcr.io/acme/pay@" + good, "", IdentityMissingDigest, true, true},
		{"digest mismatch", "oci://ghcr.io/acme/pay@" + good, other, IdentityDigestMismatch, false, true},
		{"mutable tag", "oci://ghcr.io/acme/pay:1.0", good, IdentityMutable, false, false},
		{"local scheme-less", "ghcr.io/acme/pay@" + good, good, IdentityLocal, false, false},
		{"local file", "file:///abs/pay", "", IdentityLocal, false, false},
		{"local empty", "", "", IdentityLocal, false, false},
		{"malformed digest", "oci://ghcr.io/acme/pay@sha256:abc", "sha256:abc", IdentityMalformed, false, false},
		{"malformed repository", "oci://UPPER/pay@" + good, good, IdentityMalformed, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ei := ClassifyExactIdentity(c.resolvedRef, c.recorded)
			if ei.Class != c.wantClass {
				t.Errorf("class = %q, want %q", ei.Class, c.wantClass)
			}
			if ei.Exact() != c.wantExact {
				t.Errorf("Exact() = %v, want %v", ei.Exact(), c.wantExact)
			}
			if (ei.Repository != "" && ei.Digest != "") != c.wantHasDigit {
				t.Errorf("repository/digest populated = %v (%q/%q), want %v", ei.Repository != "" && ei.Digest != "", ei.Repository, ei.Digest, c.wantHasDigit)
			}
			// A non-exact identity always explains itself; an exact one never does.
			if (ei.Reason() == "") != c.wantExact {
				t.Errorf("Reason() = %q, want non-empty=%v", ei.Reason(), !c.wantExact)
			}
		})
	}
	// The unknown/default class Reason is defensive but must be non-empty.
	if (ExactIdentity{Class: "bogus"}).Reason() == "" {
		t.Error("an unknown identity class must still explain itself")
	}
}
