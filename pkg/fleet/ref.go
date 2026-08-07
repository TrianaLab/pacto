package fleet

import "strings"

// This file owns the pure predicates that decide whether a contract reference
// names IMMUTABLE content. They are the single source of truth for the
// "exact snapshot content" invariant: a Product Impact request by canonical
// revision key may only analyze content named by an immutable, digest-pinned ref
// (requirement 2.6), and an entity detail reports whether a revision's resolved
// ref is immutable. A mutable OCI tag or a local filesystem path is never
// immutable, even when a content digest is known separately.

// IsDigestPinnedRef reports whether ref pins immutable content by digest, i.e.
// it carries an "@<algorithm>:<hex>" suffix such as
// "oci://registry/repo@sha256:...". A bare tag ("repo:1.0"), a plain name or a
// local path ("file:///x", "./x") is not digest-pinned.
func IsDigestPinnedRef(ref string) bool {
	_, ok := DigestFromRef(ref)
	return ok
}

// DigestFromRef returns the digest a ref is pinned to and whether it is
// digest-pinned. The digest is everything after the last "@" and must look like
// "<algorithm>:<hex>" (a non-empty algorithm, a ":", and a non-empty digest
// body). Only the last "@" is considered so a userinfo-bearing URL never forges a
// digest.
func DigestFromRef(ref string) (digest string, ok bool) {
	at := strings.LastIndex(ref, "@")
	if at < 0 {
		return "", false
	}
	d := ref[at+1:]
	if !looksLikeDigest(d) {
		return "", false
	}
	return d, true
}

// looksLikeDigest reports whether s is "<algorithm>:<hex>" with a non-empty
// algorithm and body and no stray separators (a registry port like ":5000" or a
// bare tag is not a digest).
func looksLikeDigest(s string) bool {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i >= len(s)-1 {
		return false
	}
	// Exactly one ':' separates algorithm from body; a second ':' means this is
	// not a canonical digest (avoids treating "host:port/..." fragments as one).
	return strings.IndexByte(s[i+1:], ':') < 0
}
