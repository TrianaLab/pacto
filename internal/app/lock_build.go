package app

import (
	"context"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/lock"
)

// buildLock resolves the full dependency + reference closure for a contract and
// records it as a deterministic lock. Dependencies are resolved transitively
// (via the graph) and each gets a pinned digest (OCI) or content hash (local).
// References (config/policy) are DIRECT-ONLY: the resolver attaches reference
// edges to the root node without recursing into them (see
// graph.ExtractReferenceEdges), so they are handled separately here.
//
// buildLock fails closed: a graph conflict yields *lock.ConflictError, and any
// required edge that failed to resolve (non-empty Error, nil Node) or any digest
// / content-hash resolution failure yields *lock.UnresolvedError.
func (s *Service) buildLock(ctx context.Context, ref string, bundle *contract.Bundle) (*lock.Lock, error) {
	fetcher := s.newDepFetcher(ref)
	res := graph.ResolveWithOptions(ctx, bundle.Contract, fetcher, graph.ResolveOptions{IncludeReferences: true})

	if len(res.Conflicts) > 0 {
		return nil, &lock.ConflictError{Service: res.Conflicts[0].Name}
	}

	// Fail closed on any edge that failed to resolve. walkClosure skips
	// nil-Node edges, so unresolved dependencies would otherwise be dropped.
	if e, ok := firstFailedEdge(res.Root); ok {
		return nil, &lock.UnresolvedError{Ref: e.Ref, Reason: e.Error}
	}

	l := &lock.Lock{
		LockVersion: lock.CurrentLockVersion,
		Pacto:       lock.PactoInfo{Version: BuildVersion},
		Root:        lock.RootInfo{Name: bundle.Contract.Service.Name, Version: bundle.Contract.Service.Version},
	}

	var buildErr error
	walkClosure(res.Root, func(_ *graph.Node, e graph.Edge, n *graph.Node) {
		if buildErr != nil {
			return
		}
		entry, err := s.entryFromEdge(ctx, e, n)
		if err != nil {
			buildErr = err
			return
		}
		l.Dependencies = append(l.Dependencies, entry)
	})
	if buildErr != nil {
		return nil, buildErr
	}

	// References are direct-only and carry no resolved Node; build them from the
	// root's reference edges, deriving kind from the contract's config/policy lists.
	for _, e := range res.Root.Dependencies {
		if e.Type != graph.EdgeReference {
			continue
		}
		r, err := s.referenceFromEdge(ctx, bundle.Contract, e)
		if err != nil {
			return nil, err
		}
		l.References = append(l.References, r)
	}

	return l, nil
}

// entryFromEdge builds a dependency lock entry from a resolved graph node,
// pinning a digest (OCI) or content hash (local). It returns *lock.UnresolvedError
// when the digest / content-hash cannot be resolved (fail closed).
func (s *Service) entryFromEdge(ctx context.Context, e graph.Edge, n *graph.Node) (lock.Entry, error) {
	entry := lock.Entry{Name: n.Name, Constraint: e.Compatibility, Version: n.Version}
	for _, child := range n.Dependencies {
		// firstFailedEdge has already aborted on any unresolved edge, so every
		// dependency edge in the closure has a resolved Node here.
		if child.Type == graph.EdgeDependency {
			entry.DependsOn = append(entry.DependsOn, child.Node.Name)
		}
	}

	parsed := graph.ParseDependencyRef(e.Ref)
	if parsed.IsLocal() {
		entry.Source = "local"
		entry.Path = parsed.Location
		h, err := lock.HashFS(n.FS)
		if err != nil {
			return lock.Entry{}, &lock.UnresolvedError{Ref: e.Ref, Reason: err.Error()}
		}
		entry.ContentHash = h
		return entry, nil
	}

	entry.Source = "oci"
	entry.Ref = e.Ref
	_, digest, err := resolveDigest(ctx, s.BundleStore, parsed.Location, e.Compatibility)
	if err != nil {
		return lock.Entry{}, &lock.UnresolvedError{Ref: e.Ref, Reason: err.Error()}
	}
	entry.Digest = digest
	return entry, nil
}

// referenceFromEdge builds a config/policy reference lock entry. The graph does
// not distinguish config from policy on reference edges (they carry only the
// ref), so the kind and name are derived by matching the ref against the
// contract's Policies (→ "policy") then Configurations (→ "config").
func (s *Service) referenceFromEdge(ctx context.Context, c *contract.Contract, e graph.Edge) (lock.Reference, error) {
	kind, name := referenceKindAndName(c, e.Ref)
	r := lock.Reference{Kind: kind, Name: name}

	parsed := graph.ParseDependencyRef(e.Ref)
	if parsed.IsLocal() {
		r.Source = "local"
		r.Path = parsed.Location
		bundle, err := loadLocalBundle(parsed.Location)
		if err != nil {
			return lock.Reference{}, &lock.UnresolvedError{Ref: e.Ref, Reason: err.Error()}
		}
		h, err := lock.HashFS(bundle.FS)
		if err != nil {
			return lock.Reference{}, &lock.UnresolvedError{Ref: e.Ref, Reason: err.Error()}
		}
		r.ContentHash = h
		return r, nil
	}

	r.Source = "oci"
	r.Ref = e.Ref
	_, digest, err := resolveDigest(ctx, s.BundleStore, parsed.Location, "")
	if err != nil {
		return lock.Reference{}, &lock.UnresolvedError{Ref: e.Ref, Reason: err.Error()}
	}
	r.Digest = digest
	return r, nil
}

// referenceKindAndName resolves a reference edge's ref to its kind and declared
// name. Policies take precedence over configurations when a ref appears in both.
// Defaults to ("config", "") for refs not found in either list (defensive; the
// resolver only emits edges for declared refs).
func referenceKindAndName(c *contract.Contract, ref string) (string, string) {
	for _, p := range c.Policies {
		if p.Ref == ref {
			return "policy", p.Name
		}
	}
	for _, cfg := range c.Configurations {
		if cfg.Ref == ref {
			return "config", cfg.Name
		}
	}
	return "config", ""
}

// firstFailedEdge returns the first edge in the graph (depth-first) whose
// resolution failed (non-empty Error, no Node). Reference edges are never
// fetched, so this only surfaces failed dependency edges.
func firstFailedEdge(root *graph.Node) (graph.Edge, bool) {
	var rec func(n *graph.Node) (graph.Edge, bool)
	rec = func(n *graph.Node) (graph.Edge, bool) {
		for _, e := range n.Dependencies {
			if e.Error != "" {
				return e, true
			}
			if e.Node != nil {
				if fe, ok := rec(e.Node); ok {
					return fe, true
				}
			}
		}
		return graph.Edge{}, false
	}
	return rec(root)
}
