package app

import (
	"context"
	"path/filepath"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/lock"
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

// buildReferenceClosure pins the full transitive config/policy reference closure:
// one entry per DECLARATION, tagged with the content identity of the contract
// that declared it (lock.Reference.From, "" for the root).
//
// Three properties matter and none is free:
//
//   - Every declaration is emitted, even when two of them share a declared ref
//     string. A ref string is text a human wrote in one contract; the same text
//     in two contracts is two references. Collapsing them either drops a real
//     closure member or files one contract's resolution under another's name.
//   - The walk is deduplicated by the RESOLVED bundle -- its registry digest, or
//     its resolved absolute path for a local ref -- not by that text. A relative
//     ref is resolved against the directory of the contract that declared it, so
//     "./config" declared in two directories denotes two different bundles;
//     conversely two different ref strings may name one bundle. Resolved identity
//     is what makes the walk finite, so cycles still terminate.
//   - A declaration reached by several routes is ONE declaration. It lives once,
//     inside one immutable contract, so it is emitted once, under an identity
//     that does not mention any route. Emitting it per route would make the lock
//     depend on which route the walk took first, and the routes are recoverable
//     from the entries anyway (see lock.Reference.DestinationID).
//
// It fails closed with *lock.AmbiguousError when one identity would have to hold
// two different resolutions, which is the only way the closure can outgrow what
// the lock can represent.
func (s *Service) buildReferenceClosure(ctx context.Context, root *contract.Contract, baseDir string) ([]lock.Reference, error) {
	walked := map[string]bool{}            // resolved bundle identity -> already recursed into
	resolved := map[string]refResolution{} // (dir, ref text) -> resolution, so a repeat costs no fetch
	seen := map[lock.Occurrence]lock.Reference{}
	var out []lock.Reference
	var walk func(c *contract.Contract, dir, from string) error
	walk = func(c *contract.Contract, dir, from string) error {
		for _, d := range c.ReferenceRefs() {
			memo, ok := resolved[dir+"\x00"+d.Ref]
			if !ok {
				var err error
				if memo, err = s.resolveReference(ctx, d, dir); err != nil {
					return err
				}
				resolved[dir+"\x00"+d.Ref] = memo
			}
			// The memo answers "what does this ref text resolve to from this
			// directory", which is all it can answer: two scopes in one contract may
			// share a ref string, and the declaration is the caller's, not the
			// memo's. Kind and Name come from the declaration every time.
			entry := memo.entry
			entry.From, entry.Kind, entry.Name = from, d.Kind, d.Name
			if prev, dup := seen[entry.Occurrence()]; dup {
				if prev != entry {
					return &lock.AmbiguousError{Occurrence: entry.Occurrence(),
						First: refOrigin(prev), Second: refOrigin(entry)}
				}
				continue // the same declaration, reached again by another route
			}
			seen[entry.Occurrence()] = entry
			out = append(out, entry)
			if memo.child == nil || walked[memo.identity] {
				continue
			}
			walked[memo.identity] = true
			// The child's declarations are tagged with the child's own content
			// identity, not with the route to it, so they are the same wherever
			// the walk arrives from.
			if err := walk(memo.child, memo.childDir, entry.DestinationID()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, baseDir, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// refOrigin renders where a reference entry points, for an ambiguity message:
// the text the author wrote (Ref for OCI, Path for local) plus the identity it
// pinned, since two declarations can share the text and differ in what it
// resolved to -- which is exactly the case that raises the message.
func refOrigin(r lock.Reference) string {
	where := r.Ref
	if where == "" {
		where = r.Path
	}
	return where + " (" + r.DestinationID() + ")"
}

// refResolution is what one ref text resolves to from one directory: the pinned
// half of a lock entry, the referenced bundle's contract and base dir for
// recursion, and the resolved identity that terminates the walk.
//
// entry deliberately carries NO declaration fields (From, Kind, Name). It is
// memoized per (dir, ref text) and several declarations may share that key, so
// filling them here would file the second declaration under the first one's
// name. identity is runtime-only and never serialized.
type refResolution struct {
	entry    lock.Reference
	child    *contract.Contract
	childDir string
	identity string
}

// resolveReference pins one reference and returns the referenced bundle's
// contract (for recursion), its base dir ("" for OCI) and its resolved identity.
// Any resolve/pull/hash/load failure yields *lock.UnresolvedError (fail closed).
func (s *Service) resolveReference(ctx context.Context, d contract.ReferenceRef, dir string) (refResolution, error) {
	var r lock.Reference
	parsed := graph.ParseDependencyRef(d.Ref)

	if parsed.IsLocal() {
		if dir == "" {
			return refResolution{}, &lock.UnresolvedError{Ref: d.Ref, Reason: "local reference inside an OCI bundle cannot be resolved"}
		}
		path := parsed.Location
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		b, err := loadLocalBundle(path)
		if err != nil {
			return refResolution{}, &lock.UnresolvedError{Ref: d.Ref, Reason: err.Error()}
		}
		h, err := lock.HashFS(b.FS)
		if err != nil {
			return refResolution{}, &lock.UnresolvedError{Ref: d.Ref, Reason: err.Error()}
		}
		r.Source = "local"
		r.Path = parsed.Location
		r.ContentHash = h
		r.Version = b.Contract.Service.Version
		// The resolved absolute path, not the content hash: two byte-identical
		// bundle directories still resolve their own relative refs differently.
		return refResolution{entry: r, child: b.Contract, childDir: path, identity: "local:" + path}, nil
	}

	resolvedRef, digest, err := resolveDigest(ctx, s.BundleStore, parsed.Location, "")
	if err != nil {
		return refResolution{}, &lock.UnresolvedError{Ref: d.Ref, Reason: err.Error()}
	}
	b, err := s.BundleStore.Pull(ctx, resolvedRef)
	if err != nil {
		return refResolution{}, &lock.UnresolvedError{Ref: d.Ref, Reason: err.Error()}
	}
	r.Source = "oci"
	r.Ref = d.Ref
	r.Digest = digest
	r.Version = b.Contract.Service.Version
	return refResolution{entry: r, child: b.Contract, childDir: "", identity: "oci:" + digest}, nil
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
