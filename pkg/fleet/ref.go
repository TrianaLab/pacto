package fleet

import (
	"fmt"
	"strings"

	"github.com/opencontainers/go-digest"

	"github.com/trianalab/pacto/v3/pkg/graph"
)

// This file owns the single strict predicate that decides whether a contract
// reference names IMMUTABLE, resolver-compatible content. It is the one source of
// truth for the "exact snapshot content" invariant (requirement 2.6): a Product
// Impact request by canonical revision key may only analyze content named by a
// canonical, immutable OCI reference, and an entity detail reports whether a
// revision's resolved ref is immutable.
//
// There is exactly ONE parser. Scheme detection reuses graph.ParseDependencyRef
// — the same parser Service.ResolveBundle uses — so any ref this function accepts
// is guaranteed to route through the OCI resolve path (never mistaken for a local
// filesystem path). The digest is validated by the OCI go-digest primitive, so a
// short, mis-cased, wrong-length or unsupported-algorithm digest is rejected.

// ParseCanonicalOCIRef validates ref as a canonical, immutable,
// resolver-compatible OCI reference of the form
// "oci://<repository>@<algorithm>:<body>" and returns its repository and content
// digest. It proves, in order:
//   - the ref carries the oci:// scheme (graph.ParseDependencyRef reports OCI), so
//     the same resolve path Product Impact uses will treat it as an OCI ref and
//     not a local filesystem path;
//   - it carries exactly one "@" digest separator (no extra separators);
//   - it names a non-empty repository, not only "@digest";
//   - the digest body validates under go-digest (a registered algorithm with a
//     correctly sized, correctly cased body).
//
// A mutable tag, a local path, a scheme-less ref, an empty repository or any
// malformed digest is a typed error.
func ParseCanonicalOCIRef(ref string) (repository string, dgst digest.Digest, err error) {
	dr := graph.ParseDependencyRef(ref)
	if !dr.IsOCI() {
		return "", "", fmt.Errorf("not a canonical OCI reference: %q must use the oci:// scheme (a scheme-less or file:// ref resolves as a local path, never immutable content)", ref)
	}
	loc := dr.Location // "<repository>[@<digest>]", oci:// already stripped
	at := strings.IndexByte(loc, '@')
	if at < 0 {
		return "", "", fmt.Errorf("OCI reference %q is not digest-pinned (no @digest); a mutable tag is not immutable content", ref)
	}
	if strings.IndexByte(loc[at+1:], '@') >= 0 {
		return "", "", fmt.Errorf("OCI reference %q has extra @ separators; a canonical immutable ref has exactly one", ref)
	}
	repository = loc[:at]
	if repository == "" {
		return "", "", fmt.Errorf("OCI reference %q names no repository", ref)
	}
	dgst, err = digest.Parse(loc[at+1:])
	if err != nil {
		return "", "", fmt.Errorf("OCI reference %q has an invalid content digest: %w", ref, err)
	}
	return repository, dgst, nil
}

// IsDigestPinnedRef reports whether ref is a canonical, immutable,
// resolver-compatible OCI reference (see [ParseCanonicalOCIRef]). Only such a ref
// may be treated as exact snapshot content.
func IsDigestPinnedRef(ref string) bool {
	_, _, err := ParseCanonicalOCIRef(ref)
	return err == nil
}
