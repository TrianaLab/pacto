package app

import (
	"context"
	"sync"

	"github.com/trianalab/pacto/v3/pkg/catalog"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// bundleRecorder keeps the bundles a catalog walk resolved, keyed by the content
// identity the catalog knows them by. See [Service.recordingCatalogResolver] for
// why the recording happens during the walk rather than after it.
//
// The lock is not there because pkg/catalog is concurrent -- today it walks
// sequentially. It is there because [catalog.Resolver] is a port, and the port
// documents a purity contract and says nothing about how many goroutines call
// it. An adapter that is only safe under an undocumented property of the caller
// is a data race waiting for the day the caller is parallelised.
type bundleRecorder struct {
	mu      sync.Mutex
	bundles map[catalog.ContentID]*contract.Bundle
}

func newBundleRecorder() *bundleRecorder {
	return &bundleRecorder{bundles: map[catalog.ContentID]*contract.Bundle{}}
}

// record keeps b under id. A nil recorder records nothing, which is what lets
// [Service.CatalogResolver] and [Service.recordingCatalogResolver] share one
// Resolve rather than growing a second copy of the resolution logic.
func (r *bundleRecorder) record(id catalog.ContentID, b *contract.Bundle) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bundles[id] = b
}

// bundle returns the bundle recorded for id, or nil.
func (r *bundleRecorder) bundle(id catalog.ContentID) *contract.Bundle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bundles[id]
}

// catalogSource contributes a set of roots AND their whole dependency closure.
//
// It is what the other definition sources are not. A local source crawls a
// directory and an OCI source pulls exactly the references it was handed, so
// both stop at what someone remembered to list: a bundle that depends on
// oci://ghcr.io/acme/payments:2.1.0 leaves a dangling edge in the snapshot
// unless that reference is separately passed as its own source. This source
// follows the declarations instead, so the fleet holds the same closure
// `pacto mcp --root` already discovers, resolved by the same adapter, and the
// two answers cannot disagree about what a reference means.
//
// It lives in internal/app rather than internal/fleetsrc because it needs the
// service's catalog resolver -- registry credentials, the OCI cache, digest
// pinning, local hashing -- and app already imports fleetsrc, so fleetsrc can
// never import app.
type catalogSource struct {
	id    string
	svc   *Service
	roots []string
}

// ID implements [fleet.Source].
func (s *catalogSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *catalogSource) Kind() string { return "catalog" }

// Collect builds the catalog and projects it into fleet records.
//
// The build happens here rather than in [Service.Fleet] so it honours the
// collection context, runs concurrently with the other sources, and -- when the
// registry is unreachable or a root is nonsense -- surfaces as one unavailable
// source in a partial snapshot instead of failing the whole build. That is the
// contract every other source already keeps.
func (s *catalogSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	rec := newBundleRecorder()
	cat, err := catalog.Build(ctx, catalog.Request{
		Roots:    s.roots,
		Resolver: s.svc.recordingCatalogResolver(rec),
	})
	if err != nil {
		return nil, err
	}
	return catalogCollection(s.id, cat, rec), nil
}

// catalogCollection projects a frozen catalog into one source's contribution.
//
// Conflicts and cycles are deliberately not translated into fleet limitations.
// Both are disagreements between revisions that are all present in the
// collection, so the fleet composes them into its own conflict reporting
// against every other source at once; announcing them a second time here would
// double-count a disagreement the snapshot already states, in a source's voice
// rather than the snapshot's.
func catalogCollection(id string, cat *catalog.Catalog, rec *bundleRecorder) *fleet.Collection {
	meta := cat.Meta()
	revs := cat.Revisions()
	col := &fleet.Collection{Revisions: make([]fleet.RawRevision, 0, len(revs))}
	for _, rev := range revs {
		// The catalog resolved this closure at one instant and then froze it, so
		// its generation time is when every revision in it was fetched. Each gets
		// its own pointer: one shared address would let a later writer move every
		// revision's timestamp at once.
		fetchedAt := meta.GeneratedAt
		col.Revisions = append(col.Revisions, fleet.RawRevision{
			Bundle:       rec.bundle(rev.Content),
			Domain:       rev.Service.Domain,
			RequestedRef: firstRef(rev.RequestedRefs),
			ResolvedRef:  firstRef(rev.ResolvedRefs),
			Digest:       rev.Content.Digest,
			FetchedAt:    &fetchedAt,
		})
	}
	// Every gap the walk hit is already in Meta.Limitations -- an unresolved root,
	// an unresolved dependency, a bound that stopped traversal, a cancelled
	// context -- with codes the fleet shares or passes through, so the snapshot
	// reports the closure as partial for the reason the catalog found rather than
	// serving a truncated closure as a complete one.
	for _, l := range meta.Limitations {
		col.Limitations = append(col.Limitations, fleet.Limitation{
			Code:    l.Code,
			Source:  id,
			Message: catalogLimitationMessage(l),
		})
	}
	return col
}

// catalogLimitationMessage folds the reference a limitation is about into its
// prose, because a fleet limitation has nowhere else to carry one and "a
// dependency did not resolve" without saying which is not actionable.
func catalogLimitationMessage(l catalog.Limitation) string {
	if l.Ref == "" {
		return l.Message
	}
	return l.Message + " (" + l.Ref + ")"
}

// firstRef takes the first reference text the catalog recorded for a revision.
// One revision can be reached through several -- a moving tag and a digest pin
// over the same bytes -- and a fleet revision holds one. The catalog orders them
// deterministically, so this is a stable choice rather than an arbitrary one.
func firstRef(refs []string) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}
