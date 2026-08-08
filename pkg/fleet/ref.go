package fleet

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/opencontainers/go-digest"

	"github.com/trianalab/pacto/v3/pkg/graph"
)

// This file owns the single strict evaluation of whether a contract reference
// names EXACT, immutable, resolver-retrievable content. It is the one source of
// truth for the "exact snapshot content" invariant (requirement 2.6): a Product
// Impact request by canonical revision key may only analyze content named by a
// canonical, immutable OCI reference whose digest matches the revision's recorded
// content digest, and an entity detail reports the SAME judgement for a revision's
// or target's resolved ref. There is exactly one classifier, so `immutable`/exact
// means the same thing wherever it appears (requirement, item 9).
//
// Scheme detection reuses graph.ParseDependencyRef — the same parser
// Service.ResolveBundle uses — so any ref this function accepts is guaranteed to
// route through the OCI resolve path (never mistaken for a local filesystem path).
// The repository/reference syntax is validated by go-containerregistry's name
// parser (the SAME grammar the production BundleStore's client uses at its parse
// boundary), and the digest is validated by the OCI go-digest primitive, so a
// short, mis-cased, wrong-length, unsupported-algorithm digest or a
// syntactically invalid repository is rejected (requirement, item 10).

// IdentityClass names how a (resolvedRef, recordedDigest) pair identifies content.
// It carries enough information to distinguish every exact-content failure mode so
// a caller can explain WHY a ref is not exact, while [ExactIdentity.Exact] reduces
// it to the single boolean meaning shared everywhere.
type IdentityClass string

const (
	// IdentityExact: a canonical oci://<repository>@<digest> ref whose repository
	// and digest validate AND whose digest equals the revision's recorded content
	// digest. This is exact, immutable, resolver-retrievable content.
	IdentityExact IdentityClass = "exact"
	// IdentityMissingDigest: a canonical, valid digest-pinned ref, but the revision
	// records no content digest to cross-check. The pinned ref alone names exact
	// immutable retrievable content (the digest IS the content address), so this is
	// treated as exact; the recorded digest is only an additional consistency
	// cross-check when present. See [ExactIdentity.Exact].
	IdentityMissingDigest IdentityClass = "missing-digest"
	// IdentityMutable: an oci:// ref with no @digest (a mutable tag or bare repo).
	IdentityMutable IdentityClass = "mutable"
	// IdentityLocal: a scheme-less or file:// ref. The resolver treats it as a local
	// filesystem path, never retrievable immutable content.
	IdentityLocal IdentityClass = "local"
	// IdentityMalformed: an oci://...@... ref whose repository or content digest is
	// syntactically invalid under the OCI grammar the resolver uses.
	IdentityMalformed IdentityClass = "malformed"
	// IdentityDigestMismatch: a canonical, valid digest-pinned ref whose digest
	// differs from the revision's recorded content digest (internally inconsistent).
	IdentityDigestMismatch IdentityClass = "digest-mismatch"
)

// ExactIdentity is the result of the single exact-content-identity evaluation
// shared by RevisionDetail, TargetDetail and Product Impact eligibility. Exact
// reduces the class to the one boolean meaning of `immutable` used everywhere.
type ExactIdentity struct {
	// Class is the fine-grained classification (for explanations and diagnostics).
	Class IdentityClass
	// Repository and Digest are set only when the ref parsed as a valid canonical
	// OCI digest reference (classes exact, missing-digest and digest-mismatch).
	Repository string
	Digest     digest.Digest
}

// Exact reports whether the identity names exact, immutable, resolver-retrievable
// content. Exactly the exact and missing-digest classes qualify: a canonical
// digest-pinned ref is exact retrievable content, and a recorded content digest,
// WHEN PRESENT, must equal it (a mismatch is not exact); when absent, the pinned
// ref alone is authoritative. This is the single definition of `immutable`.
func (e ExactIdentity) Exact() bool {
	return e.Class == IdentityExact || e.Class == IdentityMissingDigest
}

// Reason explains a non-exact identity for an error or a UI, and is empty when the
// identity is exact.
func (e ExactIdentity) Reason() string {
	switch e.Class {
	case IdentityExact, IdentityMissingDigest:
		return ""
	case IdentityMutable:
		return "the reference is a mutable OCI tag, not digest-pinned content"
	case IdentityLocal:
		return "the reference is a local filesystem path, not retrievable immutable content"
	case IdentityMalformed:
		return "the reference has an invalid repository or content digest under the OCI grammar the resolver uses"
	case IdentityDigestMismatch:
		return fmt.Sprintf("the reference pins %s but the recorded content digest differs", e.Digest)
	default:
		return "the reference does not name exact retrievable content"
	}
}

// classifyOCIRef parses ref and reports whether it is a valid canonical OCI digest
// reference (returning its repository and digest) or WHY it is not (local, mutable
// or malformed). It performs NO recorded-digest cross-check; that is
// [ClassifyExactIdentity]'s job. The returned class is one of IdentityExact (a
// valid canonical ref), IdentityLocal, IdentityMutable or IdentityMalformed.
func classifyOCIRef(ref string) (repository string, dgst digest.Digest, class IdentityClass) {
	dr := graph.ParseDependencyRef(ref)
	if !dr.IsOCI() {
		return "", "", IdentityLocal
	}
	loc := dr.Location // "<repository>[@<digest>]", oci:// already stripped
	at := strings.IndexByte(loc, '@')
	if at < 0 {
		return "", "", IdentityMutable
	}
	if strings.IndexByte(loc[at+1:], '@') >= 0 {
		return "", "", IdentityMalformed // extra @ separators
	}
	repository = loc[:at]
	if repository == "" {
		return "", "", IdentityMalformed
	}
	dgst, err := digest.Parse(loc[at+1:])
	if err != nil {
		return "", "", IdentityMalformed
	}
	// Validate the WHOLE reference (repository grammar + digest) with the same
	// go-containerregistry name parser the production BundleStore client uses at its
	// parse boundary (pkg/oci Client.parseRef -> name.ParseReference). This makes an
	// accepted ref genuinely resolver-compatible instead of merely "some non-empty
	// text before @" (requirement, item 10).
	if _, err := name.ParseReference(loc); err != nil {
		return "", "", IdentityMalformed
	}
	return repository, dgst, IdentityExact
}

// ClassifyExactIdentity is the ONE exact-content-identity evaluation. resolvedRef
// is a revision's or target's resolved reference; recordedDigest is the revision's
// recorded content digest (may be empty). It reuses [classifyOCIRef] for the
// ref-only validation and then cross-checks the recorded digest.
func ClassifyExactIdentity(resolvedRef, recordedDigest string) ExactIdentity {
	repo, dgst, class := classifyOCIRef(resolvedRef)
	if class != IdentityExact {
		return ExactIdentity{Class: class}
	}
	switch {
	case recordedDigest == "":
		return ExactIdentity{Class: IdentityMissingDigest, Repository: repo, Digest: dgst}
	case recordedDigest != dgst.String():
		return ExactIdentity{Class: IdentityDigestMismatch, Repository: repo, Digest: dgst}
	default:
		return ExactIdentity{Class: IdentityExact, Repository: repo, Digest: dgst}
	}
}

// ParseCanonicalOCIRef validates ref as a canonical, immutable,
// resolver-compatible OCI reference of the form
// "oci://<repository>@<algorithm>:<body>" and returns its repository and content
// digest. It proves, in order: the oci:// scheme (so the resolve path treats it as
// an OCI ref, not a local path); exactly one "@" digest separator; a non-empty
// repository; a digest that validates under go-digest; and a repository/reference
// that parses under the go-containerregistry name grammar the resolver uses.
//
// A mutable tag, a local path, a scheme-less ref, an empty or malformed repository
// or any malformed digest is a typed error. It does NOT cross-check a recorded
// content digest; use [ClassifyExactIdentity] for the full exact-content judgement.
func ParseCanonicalOCIRef(ref string) (repository string, dgst digest.Digest, err error) {
	repo, d, class := classifyOCIRef(ref)
	switch class {
	case IdentityExact:
		return repo, d, nil
	case IdentityLocal:
		return "", "", fmt.Errorf("not a canonical OCI reference: %q must use the oci:// scheme (a scheme-less or file:// ref resolves as a local path, never immutable content)", ref)
	case IdentityMutable:
		return "", "", fmt.Errorf("OCI reference %q is not digest-pinned (no @digest); a mutable tag is not immutable content", ref)
	default: // IdentityMalformed
		return "", "", fmt.Errorf("OCI reference %q has an invalid repository or content digest under the OCI grammar the resolver uses", ref)
	}
}

// IsDigestPinnedRef reports whether ref is a canonical, immutable,
// resolver-compatible OCI reference (see [ParseCanonicalOCIRef]). It does NOT
// cross-check a recorded content digest; callers that judge exact snapshot content
// must use [ClassifyExactIdentity] so `immutable` means the same thing everywhere.
func IsDigestPinnedRef(ref string) bool {
	_, _, err := ParseCanonicalOCIRef(ref)
	return err == nil
}
