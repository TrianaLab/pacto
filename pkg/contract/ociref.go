package contract

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ociTagRe matches the OCI tag grammar; ociDigestRe matches sha256/sha512
// digests with the correct hex length.
var (
	ociTagRe    = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)
	ociDigestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$|^sha512:[0-9a-f]{128}$`)
)

// InferredRepository is the installation repository that holds the inferred
// contracts of external systems, one per identity: <registry>/pacto-inferred/<identity>.
const InferredRepository = "pacto-inferred"

// InferredRef is the external identity whose inferred contract ref names:
// the path below a pacto-inferred segment, as in
// oci://ghcr.io/acme/pacto-inferred/stripe:0.0.2. ok is false when ref names
// no such repository, when pacto-inferred is the first path segment (an
// organization of that name on a shared registry) or when the path below it
// is no external identity. Only the location marks a contract inferred.
func InferredRef(ref string) (identity string, ok bool) {
	repo, _, _ := SplitOCIRef(TrimOCIScheme(ref))
	segs := strings.Split(repo, "/")
	for i := 2; i < len(segs)-1; i++ { // segs[0] is the registry host
		if segs[i] == InferredRepository {
			identity = strings.Join(segs[i+1:], "/")
			return identity, validIdentity(identity)
		}
	}
	return "", false
}

// ContributionsRepository is the one repository below a producer's
// pacto-inferred path that holds the inferred records it contributes, one tag
// per service version: <registry>/pacto-inferred/<producer>/services. No
// external identity has this shape, so it never shares a repository with the
// inferred contract of an external of the producer's name.
const ContributionsRepository = "services"

// ContributionRepository is the repository producer contributes its records
// to under the registry path registryPath.
func ContributionRepository(registryPath, producer string) string {
	return registryPath + "/" + InferredRepository + "/" + producer + "/" + ContributionsRepository
}

// ContributionRef is the producer whose contribution repository ref names, as
// in oci://ghcr.io/acme/pacto-inferred/my-idp/services:checkout_1.0.0. ok is
// false for any other ref, including one where pacto-inferred is the first
// path segment, as for [InferredRef], which still reports such a ref inferred.
func ContributionRef(ref string) (producer string, ok bool) {
	repo, _, _ := SplitOCIRef(TrimOCIScheme(ref))
	segs := strings.Split(repo, "/")
	for i := 2; i < len(segs)-1; i++ { // segs[0] is the registry host
		if segs[i] == InferredRepository {
			if i == len(segs)-3 && segs[i+2] == ContributionsRepository && validProducer(segs[i+1]) {
				return segs[i+1], true
			}
			return "", false
		}
	}
	return "", false
}

// ContributionGrant reads one allowedContractRepos prefix of a key whose
// producer is producer. A prefix with no pacto-inferred segment after the
// registry and first path segment grants nothing. One that is exactly
// <path>/pacto-inferred/<producer> grants the contribution repository
// returned. Any other prefix under pacto-inferred (the bare root, another
// producer or a deeper path) is an error, so a producer is only ever granted
// its own path.
func ContributionGrant(prefix, producer string) (repository string, grant bool, err error) {
	segs := strings.Split(prefix, "/")
	// segs[0] is the registry host and segs[1] may be an organization named
	// pacto-inferred, as in InferredRef.
	i := slices.Index(segs[min(2, len(segs)):], InferredRepository) + 2
	if i < 2 {
		return "", false, nil
	}
	if i != len(segs)-2 || segs[i+1] != producer || !validProducer(producer) {
		return "", false, fmt.Errorf("allowedContractRepos entry %q is under %s: a producer is granted only %s/%s/<its producerId>", prefix, InferredRepository, strings.Join(segs[:i], "/"), InferredRepository)
	}
	return ContributionRepository(strings.Join(segs[:i], "/"), producer), true, nil
}

// validProducer reports whether p can name a contribution path: one identity
// segment and never host, the one class whose identity has two segments.
func validProducer(p string) bool { return p != "host" && identitySegmentRE.MatchString(p) }

// validIdentity reports whether s has the shape of an external identity.
func validIdentity(s string) bool {
	segs := strings.Split(s, "/")
	return len(s) <= 253 && len(segs) <= 5 && !slices.ContainsFunc(segs, func(seg string) bool { return !identitySegmentRE.MatchString(seg) }) &&
		!strings.Contains(s, "..")
}

// AnnotationProvenance is the manifest annotation that says how a contract
// came to be; ProvenanceInferred marks one Pacto inferred and published itself.
// Registry UIs and third-party readers read it; Pacto reads the location and
// the signer.
const (
	AnnotationProvenance = "run.pacto.contract.provenance"
	ProvenanceInferred   = "inferred"
)

// ValidOCIDigest reports whether d is a complete sha256 or sha512 digest.
func ValidOCIDigest(d string) bool { return ociDigestRe.MatchString(d) }

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
	if ref.Digest != "" && !ValidOCIDigest(ref.Digest) {
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

// DependencyName derives a dependency name from its ref: the last path segment
// of the repository, with any oci:// or file:// scheme, tag and digest dropped
// (oci://ghcr.io/acme/ledger:1.2.0 and file://../ledger are both "ledger").
// It is "" for an empty ref.
func DependencyName(ref string) string {
	ref = strings.TrimPrefix(TrimOCIScheme(ref), "file://")
	repo, _, _ := SplitOCIRef(strings.TrimRight(ref, "/"))
	return repo[strings.LastIndex(repo, "/")+1:]
}
