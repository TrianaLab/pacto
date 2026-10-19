package contract

import (
	"fmt"
	"regexp"
	"strings"
)

// ociTagRe matches the OCI tag grammar; ociDigestRe matches sha256/sha512
// digests with the correct hex length.
var (
	ociTagRe    = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
	ociDigestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$|^sha512:[0-9a-f]{128}$`)
)

// OCIReference represents a parsed OCI artifact reference.
type OCIReference struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
}

// String returns the full OCI reference string.
func (r OCIReference) String() string {
	s := r.Registry + "/" + r.Repository
	if r.Digest != "" {
		s += "@" + r.Digest
	} else if r.Tag != "" {
		s += ":" + r.Tag
	}
	return s
}

// ParseOCIReference parses an OCI reference string into its components.
// Accepted formats:
//
//	registry/repo
//	registry/repo:tag
//	registry/repo@sha256:<64 hex>
//	registry/repo:tag@sha256:<64 hex>
//
// Tags must match the OCI tag grammar and digests must be a well-formed
// sha256/sha512 digest; malformed tags or digests are rejected.
func ParseOCIReference(s string) (OCIReference, error) {
	if s == "" {
		return OCIReference{}, fmt.Errorf("empty OCI reference")
	}

	ref := OCIReference{}

	s, ref.Tag, ref.Digest = SplitOCIRef(s)

	// What remains is registry/repository
	slashIdx := strings.Index(s, "/")
	if slashIdx == -1 {
		return OCIReference{}, fmt.Errorf("invalid OCI reference: missing repository in %q", s)
	}

	ref.Registry = s[:slashIdx]
	ref.Repository = s[slashIdx+1:]

	if ref.Registry == "" {
		return OCIReference{}, fmt.Errorf("invalid OCI reference: empty registry")
	}
	if ref.Repository == "" {
		return OCIReference{}, fmt.Errorf("invalid OCI reference: empty repository")
	}
	if ref.Tag != "" && !ociTagRe.MatchString(ref.Tag) {
		return OCIReference{}, fmt.Errorf("invalid OCI reference: malformed tag %q", ref.Tag)
	}
	if ref.Digest != "" && !ociDigestRe.MatchString(ref.Digest) {
		return OCIReference{}, fmt.Errorf("invalid OCI reference: malformed digest %q (want sha256:<64 hex> or sha512:<128 hex>)", ref.Digest)
	}
	return ref, nil
}

// OCIScheme is the scheme that marks a reference as a registry reference rather
// than a local path.
const OCIScheme = "oci://"

// TrimOCIScheme strips a leading [OCIScheme], if any.
func TrimOCIScheme(ref string) string { return strings.TrimPrefix(ref, OCIScheme) }

// SplitOCIDigest splits ref at its first '@' into what precedes it and the
// digest after it. digest is "" when ref pins none.
func SplitOCIDigest(ref string) (rest, digest string) {
	rest, digest, _ = strings.Cut(ref, "@")
	return rest, digest
}

// SplitOCITag splits ref into repository and tag. A tag is a ':' after the last
// '/', so a registry port (localhost:5000/svc) is never mistaken for one; tagged
// reports whether there was a tag at all, which "" alone cannot. It does not
// know about digests: split those off first with [SplitOCIDigest].
func SplitOCITag(ref string) (repo, tag string, tagged bool) {
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon], ref[colon+1:], true
	}
	return ref, "", false
}

// SplitOCIRef splits ref into repository, tag and digest, leniently: it never
// fails and validates nothing (use [ParseOCIReference] for that). A scheme, if
// any, stays on repo; call [TrimOCIScheme] first to drop it.
func SplitOCIRef(ref string) (repo, tag, digest string) {
	rest, digest := SplitOCIDigest(ref)
	repo, tag, _ = SplitOCITag(rest)
	return repo, tag, digest
}

// OCIDomain returns the domain a reference's service lives under: the
// repository with its final segment (the service name) removed, scheme, tag and
// digest dropped. A single-segment reference has the default (empty) domain, and
// so does a local filesystem path, which names no registry at all.
//
//	oci://ghcr.io/acme/payments:1.0       -> ghcr.io/acme
//	localhost:5000/acme/payments@sha256:x -> localhost:5000/acme
//	payments:1.0                          -> ""
//	./svc, /abs/path, ~/svc               -> ""
func OCIDomain(ref string) string {
	if strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~") {
		return ""
	}
	repo, _, _ := SplitOCIRef(TrimOCIScheme(ref))
	if slash := strings.LastIndex(repo, "/"); slash >= 0 {
		return repo[:slash]
	}
	return ""
}
