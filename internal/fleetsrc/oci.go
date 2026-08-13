package fleetsrc

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// fsWalkDir is a seam for injecting a traversal error in tests.
var fsWalkDir = filepath.WalkDir

// OCISource resolves a set of registry references into contract revisions. It is
// the published-baseline source: each ref becomes a revision the graph can diff
// local edits or runtime targets against. Resolution is cache-first (the
// underlying store caches pulls), so a ref already pulled resolves offline; a
// ref that cannot be resolved becomes a record-level limitation, never an abort.
type OCISource struct {
	id       string
	resolver *oci.Resolver
	store    oci.BundleStore
	refs     []string
}

// NewOCISource returns an OCI-backed revision source over the given refs.
func NewOCISource(id string, store oci.BundleStore, refs []string) *OCISource {
	if id == "" {
		id = "oci"
	}
	return &OCISource{id: id, resolver: oci.NewResolver(store), store: store, refs: refs}
}

// ID implements [fleet.Source].
func (s *OCISource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *OCISource) Kind() string { return "oci" }

// Collect resolves each configured ref into a revision.
func (s *OCISource) Collect(ctx context.Context) (*fleet.Collection, error) {
	return collectRefs(ctx, s.id, s.resolver, s.store, s.refs)
}

// CacheSource enumerates every bundle in the local OCI disk cache and includes
// it as a baseline revision. It is the offline counterpart to [OCISource]: it
// reads strictly from disk, so a disconnected environment still sees every
// service it has ever pulled.
//
// It holds no BundleStore. Network-freedom used to rest on passing
// [oci.LocalOnly] to a resolver that also knows how to dial; now there is
// nothing here that could dial, and a cache entry is read where it is found
// rather than looked up again afterwards by the reference it was found under —
// see [cachedGenerations].
type CacheSource struct {
	id       string
	cacheDir string
}

// NewCacheSource returns a source over the on-disk OCI cache rooted at cacheDir.
func NewCacheSource(id, cacheDir string) *CacheSource {
	if id == "" {
		id = "cache"
	}
	return &CacheSource{id: id, cacheDir: cacheDir}
}

// ID implements [fleet.Source].
func (s *CacheSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *CacheSource) Kind() string { return "cache" }

// Collect walks the cache directory and turns every generation it finds into a
// baseline revision. An absent cache directory yields an empty collection
// (nothing cached), not an error.
func (s *CacheSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	gens, err := cachedGenerations(s.cacheDir)
	if err != nil {
		return nil, err
	}
	col := &fleet.Collection{}
	for _, g := range gens {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// An entry the walk could SEE and the read could not resolve to one whole
		// generation is a gap in the baseline, and a partial baseline that says so
		// is not the same thing as an empty one.
		if g.bundle == nil {
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: fleet.LimitationSourceRecordInvalid, Source: s.id,
				Message: "cache entry " + g.ref + " could not be read as one coherent generation",
			})
			continue
		}
		col.Revisions = append(col.Revisions, revisionOf(g.ref, strings.TrimPrefix(g.ref, "oci://"), g.rec, g.bundle))
	}
	return col, nil
}

// collectRefs resolves refs into revisions, turning per-ref failures into
// record-level limitations so a partial result is never mistaken for empty.
func collectRefs(ctx context.Context, id string, resolver *oci.Resolver, store oci.BundleStore, refs []string) (*fleet.Collection, error) {
	col := &fleet.Collection{}
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The digest lookup below is a HEAD against the reference AS WRITTEN, and a
		// bare repository ("registry/org/svc") parses as ":latest" — a tag a registry
		// need not have. So a repository reference resolved fine (the resolver picks
		// the highest semver tag) yet came back with no digest and a mutable
		// ResolvedRef: content the Product then had to call non-retrievable and refuse
		// to analyze, even though it had just read it. Pin the concrete tag FIRST and
		// ask about that. An already-explicit tag or digest costs nothing (ResolveRef
		// returns it unchanged, no round trip); the oci:// spelling is stripped because
		// the registry client parses a bare reference, unlike the resolver.
		concrete := strings.TrimPrefix(ref, "oci://")
		if pinned, rerr := oci.ResolveRef(ctx, store, concrete, ""); rerr == nil {
			concrete = pinned
		}
		bundle, rec, err := resolver.ResolvePinned(ctx, concrete, oci.RemoteAllowed)
		if err != nil {
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: fleet.LimitationSourceRecordInvalid, Source: id,
				Message: "ref " + ref + " could not be resolved: " + err.Error(),
			})
			continue
		}
		// A cached entry may have answered this resolution, and an entry that states
		// its own reference states what these bytes ARE — reference, domain and all.
		// The caller's spelling can be an alias the cache resolved through; the
		// record came back from the generation that actually served the bytes.
		if rec.Ref != "" {
			ref = rec.Ref
			concrete = strings.TrimPrefix(rec.Ref, "oci://")
		}
		col.Revisions = append(col.Revisions, revisionOf(ref, concrete, rec, bundle))
	}
	return col, nil
}

// revisionOf shapes ONE read artifact into a revision: the bundle, the reference
// it is known by, and — when the read recorded one — its immutable digest. Both
// sources shape their revisions here, so a published artifact keys to the same
// canonical revision whether it reached the fleet from the registry or from the
// disk cache.
//
// ref is what this revision is known by: what the caller asked for, or what the
// entry that answered says it was pulled under. concrete is the same reference
// without the oci:// scheme, already pinned to a concrete tag where that was
// possible. Only the resolution moves.
//
// The reference may be a MUTABLE tag; the digest is the immutable identity, so
// ResolvedRef is pinned to it and a Product Impact request by canonical revision
// key analyzes exactly the content the snapshot captured, never whatever the tag
// points at later.
//
// rec must come from the read that produced bundle, never from a second look at
// the same reference:
//
//   - Online, the digest comes back WITH the bundle from the one pull that
//     fetched it. Asking the registry again afterwards is a second observation of
//     a mutable tag: re-pushed in between, it answers with the digest of an
//     artifact this snapshot never read, and the revision then claims an
//     immutable identity for content that does not have it.
//   - Offline, the digest is not unknowable — it is RECORDED, written beside the
//     bundle at pull time and read from the same generation that served the
//     bytes. Re-reading the sidecar after the walk found the entry would pair
//     what the walker saw with whatever a concurrent writer has since installed:
//     bundle B under digest A.
//
// A pre-sidecar cache entry still has no digest, and still says so.
func revisionOf(ref, concrete string, rec oci.CachedRef, bundle *contract.Bundle) fleet.RawRevision {
	rev := fleet.RawRevision{Bundle: bundle, Domain: OciDomain(ref), RequestedRef: ref, ResolvedRef: concrete}
	if rec.Digest != "" {
		rev.Digest = rec.Digest
		rev.ResolvedRef = pinRefToDigest(concrete, rec.Digest)
	}
	return rev
}

// pinRefToDigest rewrites a reference to its CANONICAL immutable digest-pinned
// form "oci://<repository>@<digest>". The oci:// scheme is ALWAYS emitted
// regardless of the input spelling: this source only ever produces OCI revisions,
// and a resolved digest must carry a canonical, resolver-compatible reference (a
// scheme-less "repo@digest" would be resolved as a local filesystem path by
// graph.ParseDependencyRef, breaking a canonical Product Impact).
func pinRefToDigest(ref, digest string) string {
	return "oci://" + oci.PinRefToDigest(ref, digest)
}

// OciDomain derives the logical-service domain (the registry+org/repo scope) from
// a reference, so the same service name published to two different registries or
// organizations stays distinct in the operational graph. It is the repo path with
// its final segment (the artifact/service name) removed; a bare single-segment ref
// (no registry/org) or a local filesystem path is the default (empty) domain.
//
//	oci://ghcr.io/acme/payments:1.0       -> ghcr.io/acme
//	localhost:5000/acme/payments@sha256:x -> localhost:5000/acme
//	payments:1.0                          -> "" (default domain)
//	./svc  or  /abs/path                  -> "" (local path, no domain)
func OciDomain(ref string) string {
	// A local filesystem path has no registry/org domain.
	if strings.HasPrefix(ref, ".") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~") {
		return ""
	}
	r := strings.TrimPrefix(ref, "oci://")
	if i := strings.Index(r, "@"); i >= 0 {
		r = r[:i] // strip digest
	}
	// Strip a tag: a ':' in the final path segment (after the last '/'), so a
	// registry port (localhost:5000/...) is never mistaken for a tag separator.
	slash := strings.LastIndex(r, "/")
	if colon := strings.LastIndex(r, ":"); colon > slash {
		r = r[:colon]
	}
	if slash = strings.LastIndex(r, "/"); slash >= 0 {
		return r[:slash]
	}
	return ""
}

// cachedGeneration is ONE cache entry read whole: the bundle bytes and the
// identity recorded beside them, both taken from a single installed generation
// of the entry directory. bundle is nil when the entry could not be read that
// way at all — a gap the caller reports, not a revision.
type cachedGeneration struct {
	// ref is the reference this artifact was pulled under, as the generation
	// itself recorded it — or, only for an entry written before the sidecar
	// existed, one approximated from the pathname.
	ref    string
	rec    oci.CachedRef
	bundle *contract.Bundle
}

// cachedGenerations walks the cache directory and reads every entry it finds
// into one whole generation each. An absent cache directory is not an error:
// nothing has been pulled yet.
//
// The walk DISCOVERS entry directories and does nothing else. It does not decide
// what an entry holds, and it does not hand a reference onward for someone to
// look up again afterwards: a reference is not an entry. Two entries can carry
// the same reference and different content — the same tag pulled under the
// legacy layout and again under the current one, republished in between — and
// neither may be deleted, because retiring an entry means removing a directory
// of a SHARED cache by pathname. Resolving by reference after the walk would
// read whichever of the two the store happened to find, twice, and the other
// generation would silently not exist. So each entry is read where it was found,
// through [oci.ReadCacheEntry], which returns the bundle and the identity from
// one installed generation or neither.
//
// Duplicate suppression is therefore on the COMPLETE recorded identity, ref and
// digest together: legacy and current copies of one artifact collapse to one
// revision, while two generations of one reference stay two.
//
// Only an entry with no recorded reference falls back to the path —
// <cacheDir>/<repo...>/<tag>/bundle.tar.gz read as <repo...>:<tag> — which is
// approximate, because a path spells a registry port and a tag with the same
// characters it spells itself with. Results are sorted for deterministic output.
func cachedGenerations(cacheDir string) ([]cachedGeneration, error) {
	if _, err := os.Stat(cacheDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var gens []cachedGeneration
	seen := map[string]bool{}
	err := fsWalkDir(cacheDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != oci.CachedBundleFile {
			return nil
		}
		bundle, rec, ok := oci.ReadCacheEntry(filepath.Dir(path))
		ref := rec.Ref
		if ref == "" {
			approx, valid := refFromCachePath(cacheDir, path)
			if !valid {
				return nil
			}
			ref = approx
		}
		if !ok {
			gens = append(gens, cachedGeneration{ref: ref})
			return nil
		}
		identity := ref + "@" + rec.Digest
		if seen[identity] {
			return nil
		}
		seen[identity] = true
		gens = append(gens, cachedGeneration{ref: ref, rec: rec, bundle: bundle})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(gens, func(i, j int) bool {
		return gens[i].ref+"@"+gens[i].rec.Digest < gens[j].ref+"@"+gens[j].rec.Digest
	})
	return gens, nil
}

// refFromCachePath approximates the reference of an entry that recorded none,
// from where it sits under cacheDir. False means the path is too short to be an
// entry at all.
func refFromCachePath(cacheDir, path string) (string, bool) {
	// path is always under cacheDir (WalkDir guarantees it), so a prefix trim
	// yields the relative path without a Rel error branch.
	rel := strings.TrimPrefix(strings.TrimPrefix(path, cacheDir), string(os.PathSeparator))
	parts := strings.Split(filepath.ToSlash(rel), "/")
	// Need at least repo/tag/bundle.tar.gz.
	if len(parts) < 3 {
		return "", false
	}
	parts = parts[:len(parts)-1] // drop bundle.tar.gz
	tag := parts[len(parts)-1]
	repo := strings.Join(parts[:len(parts)-1], "/")
	return repo + ":" + tag, true
}
