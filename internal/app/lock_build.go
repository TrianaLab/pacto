package app

import (
	"context"
	"path/filepath"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/graph"
	"github.com/trianalab/pacto/v2/pkg/lock"
)

// buildLock resolves the full dependency + reference closure for a contract and
// records it as a deterministic lock. Dependencies are resolved transitively
// (via the graph) and each gets a pinned digest (OCI) or content hash (local).
// References (config/policy) are pinned TRANSITIVELY via buildReferenceClosure:
// a referenced bundle may itself reference further configs/policies, all of
// which are pinned (deduplicated by declared ref, which terminates cycles).
//
// buildLock fails closed: a graph conflict yields *lock.ConflictError, and any
// required edge that failed to resolve (non-empty Error, nil Node) or any digest
// / content-hash resolution failure yields *lock.UnresolvedError.
//
// onResolved, if non-nil, fires once per unique fetched dependency node (for
// progress reporting). The verify path passes nil so it does not double-count.
func (s *Service) buildLock(ctx context.Context, ref string, bundle *contract.Bundle, onResolved func()) (*lock.Lock, error) {
	fetcher := s.newDepFetcher(ref)
	res := graph.ResolveWithOptions(ctx, bundle.Contract, fetcher, graph.ResolveOptions{IncludeReferences: true, OnResolved: onResolved})

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

	// References are pinned transitively: a referenced config/policy bundle may
	// itself reference further configs/policies, all of which must be pinned.
	refs, err := s.buildReferenceClosure(ctx, bundle.Contract, referenceBaseDir(ref))
	if err != nil {
		return nil, err
	}
	l.References = refs

	return l, nil
}

// referenceBaseDir returns the directory against which the root contract's
// local references are resolved. OCI roots have no filesystem base (""); local
// roots resolve relative to their own directory (the supplied ref). The base is
// only joined onto relative local refs, so a relative root path stays valid.
func referenceBaseDir(ref string) string {
	if isOCIRef(ref) {
		return ""
	}
	base := ref
	if abs, err := filepath.Abs(ref); err == nil {
		base = abs
	}
	return base
}

// buildReferenceClosure pins the full transitive config/policy reference closure.
// Deduplicated by declared ref string, which also terminates cycles.
func (s *Service) buildReferenceClosure(ctx context.Context, root *contract.Contract, baseDir string) ([]lock.Reference, error) {
	seen := map[string]bool{}
	var out []lock.Reference
	var walk func(c *contract.Contract, dir string) error
	walk = func(c *contract.Contract, dir string) error {
		for _, d := range c.ReferenceRefs() {
			if seen[d.Ref] {
				continue
			}
			seen[d.Ref] = true
			entry, child, childDir, err := s.resolveReference(ctx, d, dir)
			if err != nil {
				return err
			}
			out = append(out, entry)
			if child != nil {
				if err := walk(child, childDir); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, baseDir); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveReference pins one reference and returns the referenced bundle's
// contract (for recursion) and its base dir ("" for OCI). Any resolve/pull/
// hash/load failure yields *lock.UnresolvedError (fail closed).
func (s *Service) resolveReference(ctx context.Context, d contract.ReferenceRef, dir string) (lock.Reference, *contract.Contract, string, error) {
	r := lock.Reference{Kind: d.Kind, Name: d.Name}
	parsed := graph.ParseDependencyRef(d.Ref)

	if parsed.IsLocal() {
		if dir == "" {
			return lock.Reference{}, nil, "", &lock.UnresolvedError{Ref: d.Ref, Reason: "local reference inside an OCI bundle cannot be resolved"}
		}
		path := parsed.Location
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		b, err := loadLocalBundle(path)
		if err != nil {
			return lock.Reference{}, nil, "", &lock.UnresolvedError{Ref: d.Ref, Reason: err.Error()}
		}
		h, err := lock.HashFS(b.FS)
		if err != nil {
			return lock.Reference{}, nil, "", &lock.UnresolvedError{Ref: d.Ref, Reason: err.Error()}
		}
		r.Source = "local"
		r.Path = parsed.Location
		r.ContentHash = h
		r.Version = b.Contract.Service.Version
		return r, b.Contract, path, nil
	}

	resolvedRef, digest, err := resolveDigest(ctx, s.BundleStore, parsed.Location, "")
	if err != nil {
		return lock.Reference{}, nil, "", &lock.UnresolvedError{Ref: d.Ref, Reason: err.Error()}
	}
	b, err := s.BundleStore.Pull(ctx, resolvedRef)
	if err != nil {
		return lock.Reference{}, nil, "", &lock.UnresolvedError{Ref: d.Ref, Reason: err.Error()}
	}
	r.Source = "oci"
	r.Ref = d.Ref
	r.Digest = digest
	r.Version = b.Contract.Service.Version
	return r, b.Contract, "", nil
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
