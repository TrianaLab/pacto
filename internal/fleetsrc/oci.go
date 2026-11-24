package fleetsrc

import (
	"cmp"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

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
	// Provenance, when set, checks what is attached to each revision's digest
	// in the registry. Nil leaves revisions without provenance.
	Provenance ProvenanceFunc
	// OwnerRights, when set with Provenance, stamps the owner rule's verdict
	// on the provenance of each revision whose owner it checks.
	OwnerRights OwnerRightsFunc
	// RegistryProbe, when set, probes each registry host the refs resolved
	// on and adds one Registry row per host. Nil adds no rows.
	RegistryProbe RegistryProbe
	// probeBudget bounds every probe of one Collect; zero is defaultProbeBudget.
	probeBudget time.Duration
}

// RegistryProbe reports what the registry holding ref supports.
type RegistryProbe func(ctx context.Context, ref string) (capabilities.Capabilities, error)

// defaultProbeBudget bounds all registry probes of one Collect.
const defaultProbeBudget = 10 * time.Second

// ProvenanceFunc checks the signature and lists the referrers of the revision
// read from ref by source, whose bundle declares c. It never fails: what it
// cannot check it reports as unknown.
type ProvenanceFunc func(ctx context.Context, source, ref string, c *contract.Contract) *fleet.BundleProvenance

// OwnerRightsFunc judges the owner changes among the revisions read by
// source, in place: it stamps BundleProvenance.OwnerRight through each
// revision's shared provenance.
type OwnerRightsFunc func(ctx context.Context, source string, revs []fleet.ContractRevision)

// provenanceBudget bounds all provenance checks of one Collect, so an
// unreachable registry costs a refresh at most this long.
const provenanceBudget = 20 * time.Second

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

// Collect resolves each configured ref into a revision, then probes each
// registry host a ref resolved on.
func (s *OCISource) Collect(ctx context.Context) (*fleet.Collection, error) {
	col, err := collectRefs(ctx, s.id, s.resolver, s.store, s.refs, s.Provenance, s.OwnerRights)
	if err != nil || s.RegistryProbe == nil {
		return col, err
	}
	col.Providers = s.registryRows(ctx, col.Revisions)
	return col, nil
}

// registryRows probes the host of each distinct resolved ref once, with the
// first ref on that host as the subject, under one budget. A host whose refs
// all failed to resolve has no revision here, so it gets no row.
func (s *OCISource) registryRows(ctx context.Context, revs []fleet.RawRevision) []fleet.Provider {
	var hosts []string
	subject := map[string]string{}
	services := map[string][]fleet.ServiceKey{}
	for _, r := range revs {
		host := registryHost(r.ResolvedRef)
		if _, ok := subject[host]; !ok {
			hosts = append(hosts, host)
			subject[host] = r.ResolvedRef
		}
		if key := fleet.NewServiceKeyDomain(r.Domain, r.Bundle.Contract.Service.Name); !slices.Contains(services[host], key) {
			services[host] = append(services[host], key)
		}
	}
	pctx, cancel := context.WithTimeout(ctx, cmp.Or(s.probeBudget, defaultProbeBudget))
	defer cancel()
	rows := make([]fleet.Provider, 0, len(hosts))
	for _, host := range hosts {
		caps, err := s.RegistryProbe(pctx, subject[host])
		if err != nil {
			// The error names the subject's repository, which may belong to a
			// service a restricted viewer cannot see: it stays in the log.
			slog.WarnContext(ctx, "probing a registry failed", "host", host, "error", err)
			rows = append(rows, registryProbeFailedRow(s.id, host, services[host], cmp.Or(caps.CheckedAt, time.Now())))
			continue
		}
		rows = append(rows, RegistryRow(caps, services[host]))
	}
	return rows
}

// registryHost is the registry of ref as the probe names it: docker.io and
// index.docker.io are one registry, so one row with one ID.
func registryHost(ref string) string {
	ref = contract.TrimOCIScheme(ref)
	if r, err := name.ParseReference(ref); err == nil {
		return r.Context().RegistryStr()
	}
	host, _, _ := strings.Cut(ref, "/")
	return host
}

// RegistryRow is the detected-provider row of one probed registry host: what
// the probe saw, the mode Pacto uses there and what it did not check.
// services are the services read from that host; a view that hides every one
// hides the row.
func RegistryRow(c capabilities.Capabilities, services []fleet.ServiceKey) fleet.Provider {
	used := []string{fleet.UseNativeReferrers, fleet.UseIndexListedByCatalog}
	if c.Referrers != capabilities.ReferrersNative {
		used[0] = fleet.UseWriteOnceTags
	}
	if c.Listing != capabilities.ListingCatalog {
		used[1] = fleet.UseIndexListedByTag
	}
	artifactType := "artifactType: not checked (read-only probe)"
	if c.ArtifactType == capabilities.ArtifactTypeAccepted {
		artifactType = "artifactType: accepted"
	}
	at := c.CheckedAt
	return fleet.Provider{
		ID: fleet.ProviderRegistryPrefix + c.Host, Name: "Registry " + c.Host,
		Evidence: c.Evidence(), UsedFor: used, AsOf: &at,
		Details:  []string{"mode: " + c.Mode(), "auth: " + c.Auth, artifactType, "cosign signatures: not checked"},
		Services: services,
	}
}

// registryProbeFailedRow is the row of a host the probe could not read:
// partial, with no mode. at is when the probe was tried. The message never
// carries the probe error: it names the probed repository and tag.
func registryProbeFailedRow(source, host string, services []fleet.ServiceKey, at time.Time) fleet.Provider {
	return fleet.Provider{
		ID: fleet.ProviderRegistryPrefix + host, Name: "Registry " + host, AsOf: &at,
		Evidence: "capabilities unknown", UsedFor: []string{}, Status: fleet.SourcePartial, Services: services,
		Limitations: []fleet.Limitation{{Code: fleet.LimitationRegistryProbeFailed, Source: source,
			Message: "registry " + host + " could not be probed; Pacto does not know which mode it would use"}},
	}
}

// CacheSource enumerates every bundle in the local OCI disk cache and includes
// it as a baseline revision. It is the offline counterpart to [OCISource]: it
// reads strictly from disk, so a disconnected environment still sees every
// service it has ever pulled.
//
// It holds no BundleStore, so there is nothing here that could dial, and a cache entry is read where it is found
// rather than looked up again afterwards by the reference it was found under —
// see [cachedGenerations].
type CacheSource struct {
	id       string
	cacheDir string
	// walk traverses the cache; nil is filepath.WalkDir. A test sets it to
	// inject traversal errors.
	walk func(root string, fn fs.WalkDirFunc) error
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
	unreadable := unreadableDirs{source: s.id, root: s.cacheDir}
	walk := s.walk
	if walk == nil {
		walk = filepath.WalkDir
	}
	gens, err := cachedGenerations(ctx, walk, s.cacheDir, &unreadable)
	if err != nil {
		return nil, err
	}
	col := &fleet.Collection{Limitations: unreadable.limitations()}
	for _, g := range gens {
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
		col.Revisions = append(col.Revisions, revisionOf(g.ref, contract.TrimOCIScheme(g.ref), g.rec, g.bundle))
	}
	return col, nil
}

// collectRefs resolves refs into revisions, turning per-ref failures into
// record-level limitations so a partial result is never mistaken for empty.
func collectRefs(ctx context.Context, id string, resolver *oci.Resolver, store oci.BundleStore, refs []string, check ProvenanceFunc, owners OwnerRightsFunc) (*fleet.Collection, error) {
	col := &fleet.Collection{}
	var contracts []*contract.Contract
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, concrete := range pinnedRefs(ctx, store, ref) {
			bundle, rec, err := resolver.ResolvePinned(ctx, concrete)
			if err != nil {
				col.Limitations = append(col.Limitations, fleet.Limitation{
					Code: fleet.LimitationSourceRecordInvalid, Source: id,
					Message: "ref " + ref + " could not be resolved: " + err.Error(),
				})
				continue
			}
			// The reference is the caller's, deliberately: [oci.Resolver.ResolvePinned]
			// reports only a digest on this path, because a cache entry that states any
			// OTHER reference is a miss there rather than an answer. A branch here for
			// "the record named a different reference" restated that rule as its
			// opposite and could never run.
			col.Revisions = append(col.Revisions, revisionOf(ref, concrete, rec, bundle))
			contracts = append(contracts, bundle.Contract)
		}
	}
	if check == nil {
		return col, nil
	}
	// The budget starts only once every ref is resolved: charging resolution to it
	// would leave the later refs past the deadline on every refresh, unknown forever.
	pctx, cancel := context.WithTimeout(ctx, provenanceBudget)
	defer cancel()
	for i := range col.Revisions {
		col.Revisions[i].Provenance = check(pctx, id, col.Revisions[i].ResolvedRef, contracts[i])
	}
	if owners != nil {
		var views []fleet.ContractRevision
		for _, r := range col.Revisions {
			if v, ok := fleet.OwnerView(r); ok {
				views = append(views, v)
			}
		}
		owners(pctx, id, views)
	}
	return col, nil
}

// maxInferredRevisionsRead is how many tags of an untagged inferred
// contract's repository are read.
//
// ponytail: two is all ExternalProvider.Change reads; read more if an answer
// ever shows older revisions' surfaces.
const maxInferredRevisionsRead = 2

// pinnedRefs is the concrete references ref is read as, without the oci://
// scheme, each pinned to a tag where that was possible.
//
// The digest lookup is a HEAD against the reference AS WRITTEN, and a
// bare repository ("registry/org/svc") parses as ":latest" — a tag a registry
// need not have. So a repository reference resolved fine (the resolver picks
// the highest semver tag) yet came back with no digest and a mutable
// ResolvedRef: content the Product then had to call non-retrievable and refuse
// to analyze, even though it had just read it. Pin the concrete tag FIRST and
// ask about that. An already-explicit tag or digest costs nothing (ResolveRef
// returns it unchanged, no round trip); the oci:// spelling is stripped because
// the registry client parses a bare reference, unlike the resolver.
//
// An untagged inferred contract's repository is read at its two newest
// semver tags, so the fleet can say what changed between them.
func pinnedRefs(ctx context.Context, store oci.BundleStore, ref string) []string {
	concrete := contract.TrimOCIScheme(ref)
	if _, inferred := contract.InferredRef(concrete); inferred && !oci.HasExplicitTag(concrete) {
		// A listing that fails reads as no tag: ResolveRef below says why.
		tags, _ := store.ListTags(ctx, concrete)
		if newest := oci.FilterSemverTags(tags); len(newest) > 0 {
			out := make([]string, 0, maxInferredRevisionsRead)
			for _, t := range newest[:min(len(newest), maxInferredRevisionsRead)] {
				out = append(out, concrete+":"+t)
			}
			return out
		}
	}
	if pinned, err := oci.ResolveRef(ctx, store, concrete, ""); err == nil {
		concrete = pinned
	}
	return []string{concrete}
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
	rev := fleet.RawRevision{Bundle: bundle, Domain: contract.OCIDomain(ref), RequestedRef: ref, ResolvedRef: concrete}
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
	return contract.OCIScheme + oci.PinRefToDigest(ref, digest)
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
// nothing has been pulled yet, and neither is an entry the walk cannot open —
// unreadable collects those so the caller can report them as gaps rather than
// answer that the cache is empty.
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
func cachedGenerations(ctx context.Context, walk func(string, fs.WalkDirFunc) error, cacheDir string, unreadable *unreadableDirs) ([]cachedGeneration, error) {
	if _, err := os.Stat(cacheDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var gens []cachedGeneration
	seen := map[string]bool{}
	err := walk(cacheDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A cache directory is shared, and one entry it will not open is not a
			// reason to report that nothing was ever pulled. Run pacto once under
			// sudo and the root-owned entries refuse every later read; aborting
			// here emptied the entire offline baseline instead of the handful of
			// entries actually out of reach.
			return unreadable.note(path, walkErr)
		}
		if d.IsDir() || d.Name() != oci.CachedBundleFile {
			return nil
		}
		// Cancellation belongs HERE, not after the walk: the cost is the gunzip and
		// untar below, once per entry in a cache that can hold thousands.
		if err := ctx.Err(); err != nil {
			return err
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
