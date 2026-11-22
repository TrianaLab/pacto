// Package evidenceoci persists accepted evidence records in the contract
// registry, which IS the durable store. Every accepted record is one artifact
// whose subject is the exact contract digest carried by the signed envelope:
// an untagged OCI 1.1 Referrer of that revision where the registry serves the
// Referrers API, or, where the probe finds none, a write-once tag in the
// covering subject prefix's <prefix>pacto-evidence ledger. Either way evidence
// survives any Evidence Server restart without local state.
//
// The package is deliberately narrow. It owns what is evidence-specific, one
// responsibility per file: subject and subject-prefix parsing (subject.go), the
// per-prefix subject ledger (ledger.go), opening the repository through the shared
// referrer writer in pkg/ocireferrer (repository.go), the artifact codec
// (artifact.go), the
// paginated Referrers scan and strict fetch (scan.go), and the
// [evidenceingest.Store] commit/list semantics (store.go).
package evidenceoci

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	orasregistry "oras.land/oras-go/v2/registry"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// Scheme is the only accepted subject scheme: a subject is a remote contract
// revision, never a local path.
const Scheme = contract.OCIScheme

// ErrInvalidSubject reports a configured subject that is not an exact, immutable
// remote contract revision.
var ErrInvalidSubject = errors.New("evidence oci: invalid contract subject")

// ErrNoSubjects reports a configuration with neither a subject nor a subject
// prefix. There is no implicit catalog-wide discovery: with nothing configured
// there is nowhere to write evidence and nothing to read, so the server refuses
// to start rather than silently reporting an empty operational graph.
var ErrNoSubjects = errors.New("evidence oci: at least one contract subject or subject prefix must be configured")

// ErrInvalidPrefix reports a configured subject prefix that is not
// oci://<registry>/[<path>/].
var ErrInvalidPrefix = errors.New("evidence oci: invalid subject prefix")

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
// configuration order. An empty list is an empty list (the store refuses a
// configuration with neither subjects nor prefixes); one bad entry fails the
// whole list rather than silently narrowing the store.
func ParseSubjects(raw []string) (SubjectList, error) {
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
// mutable tag, no tag-plus-digest and no non-sha256 algorithm. Only a subject
// prefix refuses Pacto's own and inferred repositories ([Prefix.Covers]).
func ParseSubject(raw string) (Subject, error) {
	body, ok := strings.CutPrefix(raw, Scheme)
	if !ok {
		return Subject{}, fmt.Errorf("%w: %q is not an %s reference", ErrInvalidSubject, raw, strings.TrimSuffix(Scheme, "://"))
	}
	path, digest, ok := strings.Cut(body, "@")
	if !ok {
		return Subject{}, fmt.Errorf("%w: %q has no immutable digest", ErrInvalidSubject, raw)
	}
	if !evidenceingest.ValidSHA256Digest(digest) {
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
	// The reference ORAS opens must parse too, so a parsed subject always opens:
	// a store opening a prefix-covered subject per request has no error to report.
	if _, err := orasregistry.ParseReference(path); err != nil {
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

// reservedRepositories are the repository names a subject prefix never covers:
// Pacto's installation repositories, the inferred contracts (an inferred
// contract never anchors evidence) and the evidence ledger itself.
var reservedRepositories = []string{oci.InferredRepository, oci.TargetsRepository, oci.IndexRepository, oci.EvidenceRepository}

// Prefix is a configured registry prefix, oci://<registry>/[<path>/]: evidence
// may be stored on every immutable revision published under it.
type Prefix struct {
	Registry string // "ghcr.io"
	Path     string // "acme" or "", never ending in "/"
}

// base is the prefix without its scheme and trailing "/".
func (p Prefix) base() string {
	if p.Path == "" {
		return p.Registry
	}
	return p.Registry + "/" + p.Path
}

// String is the canonical prefix, oci://<registry>/[<path>/].
func (p Prefix) String() string { return Scheme + p.base() + "/" }

// Ledger is the repository holding this prefix's subject ledger:
// <registry>/[<path>/]pacto-evidence.
func (p Prefix) Ledger() string { return p.base() + "/" + oci.EvidenceRepository }

// Covers reports whether s sits strictly under p in a repository with no reserved path
// segment (pacto-inferred, pacto-targets, pacto-index or pacto-evidence).
func (p Prefix) Covers(s Subject) bool {
	if !strings.HasPrefix(s.Path(), p.base()+"/") {
		return false
	}
	for seg := range strings.SplitSeq(s.Repository, "/") {
		if slices.Contains(reservedRepositories, seg) {
			return false
		}
	}
	return true
}

// PrefixList is the configured prefix set, in configuration order.
type PrefixList []Prefix

// Covering returns the prefix that covers s. Prefixes never overlap, so there
// is at most one.
func (l PrefixList) Covering(s Subject) (Prefix, bool) {
	for _, p := range l {
		if p.Covers(s) {
			return p, true
		}
	}
	return Prefix{}, false
}

// Ledgers returns every prefix's ledger repository, in configuration order.
func (l PrefixList) Ledgers() []string {
	out := make([]string, 0, len(l))
	for _, p := range l {
		out = append(out, p.Ledger())
	}
	return out
}

// ParsePrefixes parses and deduplicates configured subject prefixes, keeping
// configuration order. Two prefixes where one starts with the other are
// refused, so every subject has one ledger.
func ParsePrefixes(raw []string) (PrefixList, error) {
	var out PrefixList
	for _, r := range raw {
		p, err := parsePrefix(r)
		if err != nil {
			return nil, err
		}
		if slices.Contains(out, p) {
			continue
		}
		for _, q := range out {
			if strings.HasPrefix(p.base()+"/", q.base()+"/") || strings.HasPrefix(q.base()+"/", p.base()+"/") {
				return nil, fmt.Errorf("%w: %s overlaps %s", ErrInvalidPrefix, p, q)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// parsePrefix parses oci://<registry>/[<path>/]: a trailing "/", no digest or
// tag, path components in the repository grammar, and no reserved segment.
func parsePrefix(raw string) (Prefix, error) {
	body, ok := strings.CutPrefix(raw, Scheme)
	if !ok || !strings.HasSuffix(body, "/") {
		return Prefix{}, fmt.Errorf("%w: %q: want oci://<registry>/[<path>/]", ErrInvalidPrefix, raw)
	}
	reg, path, _ := strings.Cut(strings.TrimSuffix(body, "/"), "/")
	ref := reg + "/" + path
	if path == "" {
		ref += "x" // ORAS parses a repository, so check the registry with a stand-in one
	}
	if _, err := orasregistry.ParseReference(ref); err != nil {
		return Prefix{}, fmt.Errorf("%w: %q: %w", ErrInvalidPrefix, raw, err)
	}
	if path != "" {
		if err := validRepository(path); err != nil {
			return Prefix{}, fmt.Errorf("%w: %q: %w", ErrInvalidPrefix, raw, err)
		}
		for seg := range strings.SplitSeq(path, "/") {
			if slices.Contains(reservedRepositories, seg) {
				return Prefix{}, fmt.Errorf("%w: %q names %s, a Pacto repository", ErrInvalidPrefix, raw, seg)
			}
		}
	}
	return Prefix{Registry: reg, Path: path}, nil
}
