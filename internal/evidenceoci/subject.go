// Package evidenceoci persists accepted evidence records as OCI 1.1 Referrers of
// the immutable contract revisions they report on. The configured contract
// registry IS the durable store: every accepted record is one untagged,
// digest-addressed artifact whose subject is the exact contract digest carried by
// the signed envelope, so evidence lives beside the revision it describes and
// survives any Evidence Server restart without local state.
//
// The package is deliberately narrow. It owns four responsibilities, one per
// file: subject parsing and the ORAS repository/credential factory (subject.go,
// repository.go), artifact codec and descriptor construction (artifact.go), the
// paginated Referrers scan and strict fetch (scan.go), and the
// [evidenceingest.Store] commit/list semantics (store.go).
package evidenceoci

import (
	"errors"
	"fmt"
	"strings"
)

// Scheme is the only accepted subject scheme: a subject is a remote contract
// revision, never a local path.
const Scheme = "oci://"

// ErrInvalidSubject reports a configured subject that is not an exact, immutable
// remote contract revision.
var ErrInvalidSubject = errors.New("evidence oci: invalid contract subject")

// ErrNoSubjects reports an empty subject configuration. There is no implicit
// catalog-wide discovery: with no configured subject there is nowhere to write
// evidence and nothing to read, so the server refuses to start rather than
// silently reporting an empty operational graph.
var ErrNoSubjects = errors.New("evidence oci: at least one contract subject must be configured")

// Subject is one exact, immutable contract revision:
// oci://<registry>/<repository>@sha256:<64 lowercase hex digits>. Evidence for
// this revision is published as a Referrer inside the SAME repository, which is
// what the Referrers API requires.
type Subject struct {
	Registry   string // "ghcr.io", "registry:5000"
	Repository string // "team/contracts/orders"
	Digest     string // "sha256:<64 lowercase hex>"
}

// Ref returns the canonical subject reference. Parsing is exact, so this is
// byte-identical to the configured value and is safe to compare directly.
func (s Subject) Ref() string {
	return Scheme + s.Registry + "/" + s.Repository + "@" + s.Digest
}

// Path is the registry-relative repository path ORAS addresses, "<registry>/<repository>".
func (s Subject) Path() string { return s.Registry + "/" + s.Repository }

// Domain is the contract's OCI domain: everything before the final path segment,
// matching the domain an accepted record carries. It keys the domain-qualified
// service identity, so two services sharing a name across registries stay
// distinct.
func (s Subject) Domain() string {
	full := s.Path()
	// Path always contains the registry/repository separator, so the cut point
	// always exists: a single-segment repository yields the bare registry.
	return full[:strings.LastIndex(full, "/")]
}

// SubjectList is the configured subject set, in configuration order.
type SubjectList []Subject

// Lookup resolves a contract reference to the configured subject it names. It is
// an EXACT match on registry, repository and digest: a sibling revision of the
// same contract, the same digest in another repository and any mutable tag are
// all unconfigured. Configuration membership narrows producer authorization; it
// never widens it.
func (l SubjectList) Lookup(ref string) (Subject, bool) {
	want, err := ParseSubject(ref)
	if err != nil {
		return Subject{}, false
	}
	for _, s := range l {
		if s == want {
			return s, true
		}
	}
	return Subject{}, false
}

// ParseSubjects parses and deduplicates a configured subject list, preserving
// configuration order. An empty list is [ErrNoSubjects]; one bad entry fails the
// whole list rather than silently narrowing the store.
func ParseSubjects(raw []string) (SubjectList, error) {
	if len(raw) == 0 {
		return nil, ErrNoSubjects
	}
	out := make(SubjectList, 0, len(raw))
	seen := make(map[Subject]bool, len(raw))
	for _, r := range raw {
		s, err := ParseSubject(r)
		if err != nil {
			return nil, err
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}

// ParseSubject parses one exact subject reference. It accepts only
// oci://<registry>/<repository>@sha256:<64 lowercase hex>: no local path, no
// mutable tag, no tag-plus-digest, no non-sha256 algorithm and no inferred
// repository.
func ParseSubject(raw string) (Subject, error) {
	body, ok := strings.CutPrefix(raw, Scheme)
	if !ok {
		return Subject{}, fmt.Errorf("%w: %q is not an %s reference", ErrInvalidSubject, raw, strings.TrimSuffix(Scheme, "://"))
	}
	path, digest, ok := strings.Cut(body, "@")
	if !ok {
		return Subject{}, fmt.Errorf("%w: %q has no immutable digest", ErrInvalidSubject, raw)
	}
	if !validSHA256Digest(digest) {
		return Subject{}, fmt.Errorf("%w: %q is not a sha256:<64 lowercase hex> digest", ErrInvalidSubject, digest)
	}
	registry, repository, ok := strings.Cut(path, "/")
	if !ok {
		return Subject{}, fmt.Errorf("%w: %q names no repository", ErrInvalidSubject, raw)
	}
	if registry == "" {
		return Subject{}, fmt.Errorf("%w: %q names no registry", ErrInvalidSubject, raw)
	}
	if err := validRepository(repository); err != nil {
		return Subject{}, fmt.Errorf("%w: %q: %w", ErrInvalidSubject, raw, err)
	}
	return Subject{Registry: registry, Repository: repository, Digest: digest}, nil
}

// validRepository enforces the OCI distribution repository grammar Pacto relies
// on: lowercase path components of alphanumerics and the separators '.', '_' and
// '-', with no empty component. It also rejects a ':' anywhere, so a
// tag-plus-digest reference cannot masquerade as an exact subject.
func validRepository(repo string) error {
	if repo == "" {
		return errors.New("empty repository")
	}
	for component := range strings.SplitSeq(repo, "/") {
		if component == "" {
			return errors.New("empty repository path component")
		}
		for i := 0; i < len(component); i++ {
			c := component[i]
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			case (c == '.' || c == '_' || c == '-') && i > 0 && i < len(component)-1:
			default:
				return fmt.Errorf("invalid repository character %q", string(component[i]))
			}
		}
	}
	return nil
}

// validSHA256Digest reports whether d is a complete "sha256:<64 lowercase hex>"
// digest. Only sha256 is accepted: the contract digest is the identity every
// other Pacto surface compares against, so a second algorithm would create a
// second spelling of the same revision.
func validSHA256Digest(d string) bool {
	hex, ok := strings.CutPrefix(d, "sha256:")
	if !ok || len(hex) != 64 {
		return false
	}
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
