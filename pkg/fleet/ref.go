package fleet

import (
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/opencontainers/go-digest"

	"github.com/trianalab/pacto/v3/pkg/graph"
)

// This file owns ONE of the two independent identity dimensions the fleet model
// tracks. It answers only "can Pacto retrieve exactly the content this reference
// names?" -- content retrievability / immutable resolver identity. It does NOT
// answer "which fleet revision does this target correspond to, and how confidently?"
// -- that is revision-match certainty (exact / inferred / ambiguous / unresolved),
// computed by matchRevision in build.go and surfaced as a target's LinkState.
//
// The two dimensions are genuinely orthogonal. A runtime source (the k8s operator)
// can report a trusted content digest with no canonical oci://...@digest reference,
// or a scheme-less image ref carrying that digest: it then knows EXACTLY which
// revision is running (an exact revision match) even though Pacto cannot resolve
// that identity as canonical, immutable content (retrievability is false). So a
// target may honestly report LinkState=exact together with Retrievable=false; that
// is not a contradiction. What is NEVER honest is a digest/ref DISAGREEMENT: that is
// an internal inconsistency and yields neither an exact link nor retrievable content.
//
// Product Impact by canonical identity is the one consumer that requires the
// retrievability dimension: it may analyze only content named by a
// canonical, immutable OCI reference whose digest matches the revision's recorded
// content digest. It does not depend on any target's revision-match certainty.
//
// Scheme detection reuses graph.ParseDependencyRef — the same parser
// Service.ResolveBundle uses — so any ref this function accepts is guaranteed to
// route through the OCI resolve path (never mistaken for a local filesystem path).
// The repository/reference syntax is validated by go-containerregistry's name
// parser (the SAME grammar the production BundleStore's client uses at its parse
// boundary), and the digest is validated by the OCI go-digest primitive, so a
// short, mis-cased, wrong-length, unsupported-algorithm digest or a
// syntactically invalid repository is rejected.

// IdentityClass names how a (resolvedRef, recordedDigest) pair identifies content
// along the CONTENT-RETRIEVABILITY dimension only. It distinguishes every
// retrievability outcome so a caller can explain WHY content is or is not
// retrievable, while [ContentIdentity.Retrievable] reduces it to a single boolean.
// It says nothing about revision-match certainty (see LinkState).
type IdentityClass string

const (
	// IdentityExact: a canonical oci://<repository>@<digest> ref whose repository
	// and digest validate AND whose digest equals the revision's recorded content
	// digest. This is exact, immutable, resolver-retrievable content.
	IdentityExact IdentityClass = "exact"
	// IdentityMissingDigest: a canonical, valid digest-pinned ref, but the revision
	// records no content digest to cross-check. The pinned ref alone names exact
	// immutable retrievable content (the digest IS the content address), so this is
	// retrievable; the recorded digest is only an additional consistency cross-check
	// when present. See [ContentIdentity.Retrievable].
	IdentityMissingDigest IdentityClass = "missing-digest"
	// IdentityMutable: an oci:// ref with no @digest (a mutable tag or bare repo).
	IdentityMutable IdentityClass = "mutable"
	// IdentityNoRef: no resolved reference at all. A runtime source may know a target's
	// content digest exactly (an exact revision match) yet carry no canonical ref, so
	// the content is not resolver-retrievable through any reference. This is distinct
	// from IdentityLocal (which names an actual non-canonical ref).
	IdentityNoRef IdentityClass = "no-ref"
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

// ContentIdentity is the result of the content-retrievability evaluation, the
// dimension RevisionDetail, TargetDetail and Product Impact eligibility all read.
// Retrievable reduces the class to a single boolean. It carries NO revision-match
// certainty (that is LinkState), so a caller never mistakes "we can fetch this
// content" for "we know which revision this is".
type ContentIdentity struct {
	// Class is the fine-grained classification (for explanations and diagnostics).
	Class IdentityClass
	// Repository and Digest are set only when the ref parsed as a valid canonical
	// OCI digest reference (classes exact, missing-digest and digest-mismatch).
	Repository string
	Digest     digest.Digest
}

// Retrievable reports whether the identity names exact, immutable, resolver-
// retrievable content. Exactly the exact and missing-digest classes qualify: a
// canonical digest-pinned ref is exact retrievable content, and a recorded content
// digest, WHEN PRESENT, must equal it (a mismatch is not retrievable); when absent,
// the pinned ref alone is authoritative. This is the single definition of the
// content-retrievability boolean; it is NOT revision-match certainty.
func (e ContentIdentity) Retrievable() bool {
	return e.Class == IdentityExact || e.Class == IdentityMissingDigest
}

// Reason explains a non-retrievable identity for an error or a UI, and is empty
// when the content is retrievable.
func (e ContentIdentity) Reason() string {
	switch e.Class {
	case IdentityExact, IdentityMissingDigest:
		return ""
	case IdentityMutable:
		return "the reference is a mutable OCI tag, not digest-pinned content"
	case IdentityNoRef:
		return "there is no canonical reference to retrieve the content through"
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
// reference (returning its repository and digest) or WHY it is not (no-ref, local,
// mutable or malformed). It performs NO recorded-digest cross-check; that is
// [ClassifyContentIdentity]'s job. The returned class is one of IdentityExact (a
// valid canonical ref), IdentityNoRef, IdentityLocal, IdentityMutable or
// IdentityMalformed.
func classifyOCIRef(ref string) (repository string, dgst digest.Digest, class IdentityClass) {
	if ref == "" {
		return "", "", IdentityNoRef
	}
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
	// text before @".
	if _, err := name.ParseReference(loc); err != nil {
		return "", "", IdentityMalformed
	}
	return repository, dgst, IdentityExact
}

// ClassifyContentIdentity is the content-retrievability evaluation. resolvedRef is a
// revision's or target's resolved reference; recordedDigest is the revision's
// recorded content digest (may be empty). It reuses [classifyOCIRef] for the
// ref-only validation and then cross-checks the recorded digest. It answers ONLY
// whether the content is resolver-retrievable, never how confidently the target is
// matched to a fleet revision (that is matchRevision / LinkState).
func ClassifyContentIdentity(resolvedRef, recordedDigest string) ContentIdentity {
	repo, dgst, class := classifyOCIRef(resolvedRef)
	if class != IdentityExact {
		return ContentIdentity{Class: class}
	}
	switch {
	case recordedDigest == "":
		return ContentIdentity{Class: IdentityMissingDigest, Repository: repo, Digest: dgst}
	case recordedDigest != dgst.String():
		return ContentIdentity{Class: IdentityDigestMismatch, Repository: repo, Digest: dgst}
	default:
		return ContentIdentity{Class: IdentityExact, Repository: repo, Digest: dgst}
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
// content digest; use [ClassifyContentIdentity] for the full retrievability judgement.
func ParseCanonicalOCIRef(ref string) (repository string, dgst digest.Digest, err error) {
	repo, d, class := classifyOCIRef(ref)
	switch class {
	case IdentityExact:
		return repo, d, nil
	case IdentityNoRef:
		return "", "", fmt.Errorf("not a canonical OCI reference: the reference is empty, so there is no content to retrieve")
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
// cross-check a recorded content digest; callers that judge retrievable snapshot
// content must use [ClassifyContentIdentity] so retrievability means the same thing
// everywhere.
func IsDigestPinnedRef(ref string) bool {
	_, _, err := ParseCanonicalOCIRef(ref)
	return err == nil
}
