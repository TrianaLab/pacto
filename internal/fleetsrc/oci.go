package fleetsrc

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	mode     oci.ResolveMode
}

// NewOCISource returns an OCI-backed revision source over the given refs.
func NewOCISource(id string, store oci.BundleStore, refs []string) *OCISource {
	if id == "" {
		id = "oci"
	}
	return &OCISource{id: id, resolver: oci.NewResolver(store), store: store, refs: refs, mode: oci.RemoteAllowed}
}

// ID implements [fleet.Source].
func (s *OCISource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *OCISource) Kind() string { return "oci" }

// Collect resolves each configured ref into a revision.
func (s *OCISource) Collect(ctx context.Context) (*fleet.Collection, error) {
	// A configured ref is a request, not a record: this source is allowed to
	// reach the registry, so it learns the digest there rather than from disk.
	refs := make([]oci.CachedRef, len(s.refs))
	for i, ref := range s.refs {
		refs[i] = oci.CachedRef{Ref: ref}
	}
	return collectRefs(ctx, s.id, s.resolver, s.store, refs, s.mode)
}

// CacheSource enumerates every bundle in the local OCI disk cache and includes
// it as a baseline revision. It is the offline counterpart to [OCISource]: it
// resolves strictly from disk (no network), so a disconnected environment still
// sees every service it has ever pulled.
type CacheSource struct {
	id       string
	cacheDir string
	resolver *oci.Resolver
	store    oci.BundleStore
}

// NewCacheSource returns a source over the on-disk OCI cache rooted at cacheDir.
func NewCacheSource(id, cacheDir string, store oci.BundleStore) *CacheSource {
	if id == "" {
		id = "cache"
	}
	return &CacheSource{id: id, cacheDir: cacheDir, resolver: oci.NewResolver(store), store: store}
}

// ID implements [fleet.Source].
func (s *CacheSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *CacheSource) Kind() string { return "cache" }

// Collect walks the cache directory, reconstructs each cached ref, and resolves
// it from disk. An absent cache directory yields an empty collection (nothing
// cached), not an error.
func (s *CacheSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	refs, err := cachedRefs(s.cacheDir)
	if err != nil {
		return nil, err
	}
	return collectRefs(ctx, s.id, s.resolver, s.store, refs, oci.LocalOnly)
}

// collectRefs resolves refs into revisions, turning per-ref failures into
// record-level limitations so a partial result is never mistaken for empty.
func collectRefs(ctx context.Context, id string, resolver *oci.Resolver, store oci.BundleStore, refs []oci.CachedRef, mode oci.ResolveMode) (*fleet.Collection, error) {
	col := &fleet.Collection{}
	for _, entry := range refs {
		ref := entry.Ref
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
		if mode == oci.RemoteAllowed {
			if pinned, rerr := oci.ResolveRef(ctx, store, concrete, ""); rerr == nil {
				concrete = pinned
			}
		}
		bundle, err := resolver.Resolve(ctx, concrete, mode)
		if err != nil {
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: fleet.LimitationSourceRecordInvalid, Source: id,
				Message: "ref " + ref + " could not be resolved: " + err.Error(),
			})
			continue
		}
		// RequestedRef stays what the caller asked for; only the resolution moves.
		rev := fleet.RawRevision{Bundle: bundle, Domain: OciDomain(ref), RequestedRef: ref, ResolvedRef: concrete}
		// The requested ref may be a MUTABLE tag; the resolved digest is the
		// immutable identity. Pin ResolvedRef to the digest so a Product Impact
		// request by canonical revision key analyzes exactly the content the
		// snapshot captured, never whatever the tag points at later.
		//
		// Resolving a digest is a REGISTRY ROUND TRIP, so it is only done when the
		// caller may reach the network. LocalOnly is the offline path ([CacheSource]
		// over the disk cache): dialing the registry once per cached bundle turned a
		// disk walk into thousands of serial network waits, and because every fleet
		// endpoint waits on the first snapshot, a slow or unreachable registry left
		// the whole dashboard hanging with nothing rendered. Offline we keep the
		// mutable tag, which is honest -- we cannot know the digest without asking.
		//
		// Offline, the digest is not unknowable — it is RECORDED. The disk cache
		// writes the manifest digest beside each bundle at pull time, so the same
		// published artifact keys to the same canonical revision whether it reached
		// the fleet from the registry or from the cache, with no network call and
		// no second identity. A pre-sidecar cache entry still has no digest, and
		// still says so.
		switch {
		case mode == oci.RemoteAllowed:
			if digest, derr := store.Resolve(ctx, concrete); derr == nil {
				rev.Digest = digest
				rev.ResolvedRef = pinRefToDigest(concrete, digest)
			}
		case entry.Digest != "":
			rev.Digest = entry.Digest
			rev.ResolvedRef = pinRefToDigest(concrete, entry.Digest)
		}
		col.Revisions = append(col.Revisions, rev)
	}
	return col, nil
}

// pinRefToDigest rewrites a reference to its CANONICAL immutable digest-pinned
// form "oci://<repository>@<digest>", stripping any existing tag or digest. The
// oci:// scheme is ALWAYS emitted regardless of the input spelling: this source
// only ever produces OCI revisions, and a resolved digest must carry a canonical,
// resolver-compatible reference (a scheme-less "repo@digest" would be resolved as
// a local filesystem path by graph.ParseDependencyRef, breaking a canonical
// Product Impact). A tag is a ':' after the last '/', so a registry port
// (localhost:5000/...) is never mistaken for a tag separator.
func pinRefToDigest(ref, digest string) string {
	r := strings.TrimPrefix(ref, "oci://")
	if i := strings.Index(r, "@"); i >= 0 {
		r = r[:i]
	}
	slash := strings.LastIndex(r, "/")
	if colon := strings.LastIndex(r, ":"); colon > slash {
		r = r[:colon]
	}
	return "oci://" + r + "@" + digest
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

// cachedRefs walks the cache directory and reports what each cached bundle is.
//
// The authority is the sidecar the cache writes beside every bundle: the exact
// pulled reference and its manifest digest. Only an entry written before the
// sidecar existed falls back to reading the path — <cacheDir>/<repo...>/<tag>/
// bundle.tar.gz reconstructed as <repo...>:<tag> — which is approximate, because
// the path spells a registry port and a digest the same way it spells a path
// separator and a tag. Results are sorted for deterministic output.
func cachedRefs(cacheDir string) ([]oci.CachedRef, error) {
	if _, err := os.Stat(cacheDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var refs []oci.CachedRef
	err := fsWalkDir(cacheDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != "bundle.tar.gz" {
			return nil
		}
		if rec, ok := oci.ReadCachedRef(filepath.Dir(path)); ok {
			refs = append(refs, rec)
			return nil
		}
		// path is always under cacheDir (WalkDir guarantees it), so a prefix trim
		// yields the relative path without a Rel error branch.
		rel := strings.TrimPrefix(strings.TrimPrefix(path, cacheDir), string(os.PathSeparator))
		parts := strings.Split(filepath.ToSlash(rel), "/")
		// Need at least repo/tag/bundle.tar.gz.
		if len(parts) < 3 {
			return nil
		}
		parts = parts[:len(parts)-1] // drop bundle.tar.gz
		tag := parts[len(parts)-1]
		repo := strings.Join(parts[:len(parts)-1], "/")
		refs = append(refs, oci.CachedRef{Ref: repo + ":" + tag})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Ref < refs[j].Ref })
	return refs, nil
}
