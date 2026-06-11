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

	// Split digest first
	if idx := strings.Index(s, "@"); idx != -1 {
		ref.Digest = s[idx+1:]
		s = s[:idx]
	}

	// Split tag
	// Find last colon that's not part of port — we look for colon after the last slash
	lastSlash := strings.LastIndex(s, "/")
	if colonIdx := strings.LastIndex(s, ":"); colonIdx > lastSlash {
		ref.Tag = s[colonIdx+1:]
		s = s[:colonIdx]
	}

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
