// Package graph builds and traverses a service's dependency graph. It resolves
// dependencies recursively through a pluggable fetcher (siblings in parallel),
// detects cycles and version conflicts, and renders or diffs the resolved graph.
package graph

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/logging"
)

// ContractFetcher fetches a contract bundle for a dependency.
// The full Dependency is passed so implementations can use fields like
// Compatibility for version resolution.
//
// This port cannot express WHO declared the dependency, so a fetcher reached
// through it has to resolve every relative reference against one base -- in
// practice the root's directory, or the process working directory for a root
// that arrived over the network. Implement [OriginContractFetcher] as well to
// resolve each reference against its own declarer.
type ContractFetcher interface {
	Fetch(ctx context.Context, dep contract.Dependency) (*contract.Bundle, error)
}

// OCIBase is the base a fetcher reports for a bundle that arrived over the
// network. A relative reference declared under it must not resolve: honouring
// it would let a remote contract choose which local files Pacto reads.
const OCIBase = "oci://"

// OriginContractFetcher is a [ContractFetcher] that is told which contract
// declared each dependency. [ResolveWithOptions] always prefers it.
//
// Threading the declarer is what makes a relative reference mean the same thing
// at depth three as it does at depth one, and what lets a fetcher refuse a
// local reference that a registry bundle declared.
type OriginContractFetcher interface {
	ContractFetcher
	// RootBase is where the ROOT contract's own relative references resolve
	// from -- its directory, or [OCIBase] when it came from a registry.
	//
	// It belongs to the fetcher rather than to [ResolveOptions] because the
	// fetcher is built from the same root reference it describes: separating the
	// two would let a caller hand [ResolveWithOptions] a fetcher rooted at one
	// contract and a base taken from another. It also reaches [Resolve], which
	// has no options to put it in.
	RootBase() string
	// FetchFrom resolves dep as declared by a contract whose base is base, and
	// returns the base that the FETCHED bundle's own dependencies must resolve
	// against. An empty base is the process working directory; [OCIBase] is a
	// bundle that came from a registry.
	FetchFrom(ctx context.Context, base string, dep contract.Dependency) (*contract.Bundle, string, error)
}

// depKey is the identity of a resolved node. It is not the reference text: two
// contracts in different directories can both declare `../shared` and mean
// different bundles, and two contracts can declare the same registry reference
// under `^1.0.0` and `^2.0.0` and mean different versions. Keying on the text
// alone collapses those into one node and silently discards the second
// declaration -- the same hazard pkg/catalog's memoKey exists to avoid.
type depKey struct{ Base, Ref, Constraint string }

// Node represents a service in the dependency graph.
type Node struct {
	Name         string             `json:"name"`
	Version      string             `json:"version"`
	Ref          string             `json:"ref,omitempty"`
	Local        bool               `json:"local,omitempty"`
	Dependencies []Edge             `json:"dependencies,omitempty"`
	Contract     *contract.Contract `json:"-"`
	FS           fs.FS              `json:"-"`
}

// EdgeType distinguishes dependency vs reference relationships.
const (
	EdgeDependency = "dependency"
	EdgeReference  = "reference"
)

// Edge represents a dependency or reference relationship.
type Edge struct {
	Ref           string `json:"ref"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility"`
	Type          string `json:"type"` // EdgeDependency or EdgeReference
	Node          *Node  `json:"node,omitempty"`
	Error         string `json:"error,omitempty"`
	Shared        bool   `json:"shared,omitempty"`
	Local         bool   `json:"local,omitempty"`

	// key identifies the node this edge points at. A Shared edge carries only a
	// shallow copy of that node, so the cycle pass needs the identity to find the
	// full one -- and Ref alone is not it, since two edges can carry the same ref
	// and mean different bundles. Unexported: it is resolver bookkeeping, not part
	// of the rendered graph.
	key depKey
}

// Result holds the output of graph resolution.
type Result struct {
	Root      *Node      `json:"root"`
	Cycles    [][]string `json:"cycles,omitempty"`
	Conflicts []Conflict `json:"conflicts,omitempty"`
}

// ResolveOptions controls what edges are included in the graph.
type ResolveOptions struct {
	IncludeReferences bool // include config/policy reference edges
	OnlyReferences    bool // show only reference edges (no dependencies)
	// OnResolved is fired exactly once per UNIQUE fetched node (not per edge:
	// shared edges and cycles re-traverse and must NOT re-fire). nil = no-op.
	// It is invoked from multiple goroutines (sibling deps resolve concurrently),
	// so it MUST be goroutine-safe.
	OnResolved func()
}

// resolver holds shared state for a single graph resolution pass.
type resolver struct {
	// fetch is the fetcher's FetchFrom, or a shim over the legacy Fetch that
	// discards the declarer and reports no base for what it fetched -- which is
	// exactly the pre-origin behaviour, so a plain ContractFetcher resolves the
	// same graph it always did.
	fetch   func(ctx context.Context, base string, dep contract.Dependency) (*contract.Bundle, string, error)
	opts    ResolveOptions
	mu      sync.Mutex
	visited map[depKey]*Node
	errors  map[depKey]string
	pending map[depKey]chan struct{}
}

// Resolve builds the dependency graph starting from the given contract.
// It recursively fetches dependencies via the fetcher, detects cycles
// and version conflicts. If fetcher is nil, only direct dependencies
// are shown without resolution. Sibling dependencies at each level are
// fetched concurrently.
func Resolve(ctx context.Context, c *contract.Contract, fetcher ContractFetcher) *Result {
	return ResolveWithOptions(ctx, c, fetcher, ResolveOptions{})
}

// ResolveWithOptions builds the dependency graph with the given options.
func ResolveWithOptions(ctx context.Context, c *contract.Contract, fetcher ContractFetcher, opts ResolveOptions) *Result {
	logging.LoggerFromContext(ctx).Debug("starting graph resolution", "root", c.Service.Name, "version", c.Service.Version, "dependencies", len(c.Dependencies))
	root := &Node{
		Name:     c.Service.Name,
		Version:  c.Service.Version,
		Contract: c,
	}

	fetch, rootBase := fetchPort(fetcher)
	r := &resolver{
		fetch:   fetch,
		opts:    opts,
		visited: map[depKey]*Node{},
		errors:  map[depKey]string{},
		pending: map[depKey]chan struct{}{},
	}

	path := []string{c.Service.Name}

	// Build dependency edges (unless only-references mode)
	if !opts.OnlyReferences {
		root.Dependencies = r.resolveChildren(ctx, c.Dependencies, rootBase, path)
	}

	// Add reference edges from config/policy refs
	if opts.IncludeReferences || opts.OnlyReferences {
		root.Dependencies = append(root.Dependencies, ExtractReferenceEdges(c)...)
	}

	conflicts := detectConflicts(root)
	// Cycle reporting runs as a deterministic post-resolution pass over the
	// fully-built graph, not inline during concurrent fetching: sibling deps
	// resolve in parallel, so a split cycle can be deduped away before any
	// branch explores its back-edge (see detectCycles).
	cycles := r.detectCycles(root)
	logging.LoggerFromContext(ctx).Debug("graph resolution complete", "root", c.Service.Name, "cycles", len(cycles), "conflicts", len(conflicts))

	return &Result{
		Root:      root,
		Cycles:    cycles,
		Conflicts: conflicts,
	}
}

// ExtractReferenceEdges creates Edge entries for config/policy references in a contract.
func ExtractReferenceEdges(c *contract.Contract) []Edge {
	var edges []Edge
	seen := map[string]bool{}

	addRef := func(ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		edges = append(edges, Edge{
			Ref:  ref,
			Type: EdgeReference,
		})
	}

	for _, cfg := range c.Configurations {
		addRef(cfg.Ref)
	}
	for _, pol := range c.Policies {
		addRef(pol.Ref)
	}
	return edges
}

// fetchPort adapts whichever port the caller's fetcher implements to the one the
// resolver uses, and reports the root's base. A nil fetcher yields a nil fetch so
// the resolver can keep reporting direct dependencies unresolved.
//
// A plain [ContractFetcher] gets a shim that discards the declarer and reports no
// base for what it fetched, and an empty root base -- exactly the pre-origin
// behaviour, so such a fetcher resolves the same graph it always did.
func fetchPort(fetcher ContractFetcher) (func(context.Context, string, contract.Dependency) (*contract.Bundle, string, error), string) {
	if fetcher == nil {
		return nil, ""
	}
	if of, ok := fetcher.(OriginContractFetcher); ok {
		return of.FetchFrom, of.RootBase()
	}
	return func(ctx context.Context, _ string, dep contract.Dependency) (*contract.Bundle, string, error) {
		b, err := fetcher.Fetch(ctx, dep)
		return b, "", err
	}, ""
}

// resolveChildren resolves a slice of dependencies concurrently. base is the
// declaring contract's base, which every child reference resolves against.
func (r *resolver) resolveChildren(ctx context.Context, deps []contract.Dependency, base string, path []string) []Edge {
	if len(deps) == 0 {
		return nil
	}

	edges := make([]Edge, len(deps))

	if r.fetch == nil || len(deps) == 1 {
		for i, dep := range deps {
			edges[i] = r.resolveEdge(ctx, dep, base, path)
		}
		return edges
	}

	g, gctx := errgroup.WithContext(ctx)
	for i, dep := range deps {
		g.Go(func() error {
			edges[i] = r.resolveEdge(gctx, dep, base, path)
			return nil
		})
	}
	_ = g.Wait()

	return edges
}

// resolveEdge resolves a single dependency edge, recursing into its dependencies.
// base is the declaring contract's base; dep.Ref is resolved against it.
func (r *resolver) resolveEdge(ctx context.Context, dep contract.Dependency, base string, path []string) Edge {
	local := ParseDependencyRef(dep.Ref).IsLocal()
	edge := Edge{
		Ref:           dep.Ref,
		Required:      dep.Required,
		Compatibility: dep.Compatibility,
		Type:          EdgeDependency,
		Local:         local,
	}

	if r.fetch == nil {
		return edge
	}
	key := depKey{Base: base, Ref: dep.Ref, Constraint: dep.Compatibility}
	edge.key = key

	r.mu.Lock()
	// Cycles are detected in REFERENCE space, not node space: an ancestor that
	// declared the same ref under a different base or constraint is a different
	// node, but re-entering the same reference on one path is still a loop worth
	// cutting. Erring towards a reported cycle fails closed -- buildLock turns it
	// into LOCK_UNRESOLVED -- where erring the other way would recurse forever.
	if slices.Contains(path, dep.Ref) {
		r.mu.Unlock()
		// Structural early-cut: never fetch a back-edge to an ancestor. This keeps
		// the graph shape stable (and avoids refetching the root's own ref) and
		// terminates this branch. The complete, deterministic cycle SET is derived
		// afterwards by detectCycles; this per-edge mark is idempotent with it.
		edge.Error = fmt.Sprintf("cycle detected: %s", dep.Ref)
		return edge
	}
	if prev := r.visited[key]; prev != nil {
		r.mu.Unlock()
		edge.Shared = true
		edge.Node = &Node{Name: prev.Name, Version: prev.Version, Ref: prev.Ref, Local: prev.Local}
		return edge
	}
	if ch, ok := r.pending[key]; ok {
		r.mu.Unlock()
		<-ch
		r.mu.Lock()
		prev := r.visited[key]
		prevErr := r.errors[key]
		r.mu.Unlock()
		edge.Shared = true
		if prev != nil {
			edge.Node = &Node{Name: prev.Name, Version: prev.Version, Ref: prev.Ref, Local: prev.Local}
		} else if prevErr != "" {
			edge.Error = prevErr
		} else {
			edge.Error = fmt.Sprintf("resolution completed without result for %s", dep.Ref)
		}
		return edge
	}
	ch := make(chan struct{})
	r.pending[key] = ch
	r.mu.Unlock()

	logging.LoggerFromContext(ctx).Debug("fetching dependency", "ref", dep.Ref, "base", base)
	bundle, childBase, err := r.fetch(ctx, base, dep)
	if err != nil {
		logging.LoggerFromContext(ctx).Debug("dependency fetch failed", "ref", dep.Ref, "error", err)
		r.failEdge(key, ch, err.Error())
		edge.Error = err.Error()
		return edge
	}
	if bundle == nil || bundle.Contract == nil {
		errMsg := fmt.Sprintf("fetcher returned nil bundle for %s", dep.Ref)
		r.failEdge(key, ch, errMsg)
		edge.Error = errMsg
		return edge
	}

	node := &Node{
		Name:     bundle.Contract.Service.Name,
		Version:  bundle.Contract.Service.Version,
		Ref:      dep.Ref,
		Local:    local,
		Contract: bundle.Contract,
		FS:       bundle.FS,
	}

	r.mu.Lock()
	r.visited[key] = node
	delete(r.pending, key)
	r.mu.Unlock()
	close(ch)

	// Fire once per unique fetched node (the dedup/commit point above). Shared
	// edges and cycles return earlier and never reach here, so they never refire.
	if r.opts.OnResolved != nil {
		r.opts.OnResolved()
	}

	childPath := append(append([]string{}, path...), dep.Ref)
	node.Dependencies = r.resolveChildren(ctx, bundle.Contract.Dependencies, childBase, childPath)

	edge.Node = node
	return edge
}

// failEdge records an error for a dependency and signals waiting goroutines.
func (r *resolver) failEdge(key depKey, ch chan struct{}, errMsg string) {
	r.mu.Lock()
	r.errors[key] = errMsg
	delete(r.pending, key)
	r.mu.Unlock()
	close(ch)
}

// detectCycles reports every dependency cycle in the fully-resolved graph via a
// single deterministic DFS, independent of the concurrent order that built it.
//
// The inline path-check in resolveEdge cannot see split cycles: sibling deps
// resolve in parallel, so a cycle spread across two branches (e.g. 2->3 on one,
// 3->2 on another) can have both nodes deduped to Shared edges before either
// branch explores its back-edge, leaving the cycle unreported. This pass instead
// walks the FINAL graph — every edge (Shared included) still records its target
// ref — so it sees the whole closure.
//
// Cycles are reported in REFERENCE space, not node-identity space, and the root
// participates by its service name -- both to match the inline path[0] semantics
// (a dep ref equal to the root name is a cycle back to the root) and because
// erring here has to fail closed. Two distinct bundles that happen to share a ref
// string can produce a cycle that is not one, and that costs a LOCK_UNRESOLVED;
// keying the report on identity instead would silently drop the back-edge to the
// root, whose target is never fetched and so never has an identity at all.
//
// DESCENT, by contrast, is keyed on identity: an edge names the node it points at
// via [Edge.key], so a Shared edge -- which carries only a shallow copy without
// Dependencies -- still reaches the full node, and two same-ref edges that mean
// different bundles each get their subtree walked exactly once.
//
// Each back-edge (an edge to a ref still on the stack) records one cycle and is
// marked with the same "cycle detected: <ref>" error the inline check sets, so
// buildLock still fails closed with LOCK_UNRESOLVED. The cycle set is sorted for
// a stable, order-independent result.
func (r *resolver) detectCycles(root *Node) [][]string {
	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(r.visited)+1)
	walked := make(map[depKey]bool, len(r.visited))
	var stack []string
	var cycles [][]string

	var dfs func(ref string, n *Node)
	dfs = func(ref string, n *Node) {
		color[ref] = gray
		stack = append(stack, ref)
		for i := range n.Dependencies {
			e := &n.Dependencies[i]
			if e.Type != EdgeDependency {
				continue
			}
			if color[e.Ref] == gray {
				if e.Error == "" {
					e.Error = fmt.Sprintf("cycle detected: %s", e.Ref)
				}
				cycles = append(cycles, append(slices.Clone(stack), e.Ref))
				continue
			}
			cn, ok := r.visited[e.key]
			if !ok || walked[e.key] {
				continue
			}
			walked[e.key] = true
			dfs(e.Ref, cn)
		}
		stack = stack[:len(stack)-1]
		color[ref] = black
	}
	dfs(root.Name, root)

	sort.Slice(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], "\x00") < strings.Join(cycles[j], "\x00")
	})
	return cycles
}
