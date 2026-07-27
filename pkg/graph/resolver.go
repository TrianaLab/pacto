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
type ContractFetcher interface {
	Fetch(ctx context.Context, dep contract.Dependency) (*contract.Bundle, error)
}

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
	fetcher ContractFetcher
	opts    ResolveOptions
	mu      sync.Mutex
	visited map[string]*Node
	errors  map[string]string
	pending map[string]chan struct{}
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

	r := &resolver{
		fetcher: fetcher,
		opts:    opts,
		visited: map[string]*Node{},
		errors:  map[string]string{},
		pending: map[string]chan struct{}{},
	}

	path := []string{c.Service.Name}

	// Build dependency edges (unless only-references mode)
	if !opts.OnlyReferences {
		root.Dependencies = r.resolveChildren(ctx, c.Dependencies, path)
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

// resolveChildren resolves a slice of dependencies concurrently.
func (r *resolver) resolveChildren(ctx context.Context, deps []contract.Dependency, path []string) []Edge {
	if len(deps) == 0 {
		return nil
	}

	edges := make([]Edge, len(deps))

	if r.fetcher == nil || len(deps) == 1 {
		for i, dep := range deps {
			edges[i] = r.resolveEdge(ctx, dep, path)
		}
		return edges
	}

	g, gctx := errgroup.WithContext(ctx)
	for i, dep := range deps {
		g.Go(func() error {
			edges[i] = r.resolveEdge(gctx, dep, path)
			return nil
		})
	}
	_ = g.Wait()

	return edges
}

// resolveEdge resolves a single dependency edge, recursing into its dependencies.
func (r *resolver) resolveEdge(ctx context.Context, dep contract.Dependency, path []string) Edge {
	local := ParseDependencyRef(dep.Ref).IsLocal()
	edge := Edge{
		Ref:           dep.Ref,
		Required:      dep.Required,
		Compatibility: dep.Compatibility,
		Type:          EdgeDependency,
		Local:         local,
	}

	if r.fetcher == nil {
		return edge
	}

	r.mu.Lock()
	if slices.Contains(path, dep.Ref) {
		r.mu.Unlock()
		// Structural early-cut: never fetch a back-edge to an ancestor. This keeps
		// the graph shape stable (and avoids refetching the root's own ref) and
		// terminates this branch. The complete, deterministic cycle SET is derived
		// afterwards by detectCycles; this per-edge mark is idempotent with it.
		edge.Error = fmt.Sprintf("cycle detected: %s", dep.Ref)
		return edge
	}
	if prev := r.visited[dep.Ref]; prev != nil {
		r.mu.Unlock()
		edge.Shared = true
		edge.Node = &Node{Name: prev.Name, Version: prev.Version, Ref: prev.Ref, Local: prev.Local}
		return edge
	}
	if ch, ok := r.pending[dep.Ref]; ok {
		r.mu.Unlock()
		<-ch
		r.mu.Lock()
		prev := r.visited[dep.Ref]
		prevErr := r.errors[dep.Ref]
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
	r.pending[dep.Ref] = ch
	r.mu.Unlock()

	logging.LoggerFromContext(ctx).Debug("fetching dependency", "ref", dep.Ref)
	bundle, err := r.fetcher.Fetch(ctx, dep)
	if err != nil {
		logging.LoggerFromContext(ctx).Debug("dependency fetch failed", "ref", dep.Ref, "error", err)
		r.failEdge(dep.Ref, ch, err.Error())
		edge.Error = err.Error()
		return edge
	}
	if bundle == nil || bundle.Contract == nil {
		errMsg := fmt.Sprintf("fetcher returned nil bundle for %s", dep.Ref)
		r.failEdge(dep.Ref, ch, errMsg)
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
	r.visited[dep.Ref] = node
	delete(r.pending, dep.Ref)
	r.mu.Unlock()
	close(ch)

	// Fire once per unique fetched node (the dedup/commit point above). Shared
	// edges and cycles return earlier and never reach here, so they never refire.
	if r.opts.OnResolved != nil {
		r.opts.OnResolved()
	}

	childPath := append(append([]string{}, path...), dep.Ref)
	node.Dependencies = r.resolveChildren(ctx, bundle.Contract.Dependencies, childPath)

	edge.Node = node
	return edge
}

// failEdge records an error for a dependency and signals waiting goroutines.
func (r *resolver) failEdge(ref string, ch chan struct{}, errMsg string) {
	r.mu.Lock()
	r.errors[ref] = errMsg
	delete(r.pending, ref)
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
// Nodes are keyed by ref; the root is keyed by its service name to match the
// inline path[0] semantics (a dep ref equal to the root name is a cycle back to
// the root). Every fetched node is reachable from the root through dependency
// edges, so one DFS from the root covers the closure. Each back-edge (an edge to
// a node still on the stack) records one cycle and is marked with the same
// "cycle detected: <ref>" error the inline check sets, so buildLock still fails
// closed with LOCK_UNRESOLVED. The cycle set is sorted for a stable, order-
// independent result.
func (r *resolver) detectCycles(root *Node) [][]string {
	nodeByKey := make(map[string]*Node, len(r.visited)+1)
	for ref, n := range r.visited {
		nodeByKey[ref] = n
	}
	nodeByKey[root.Name] = root // root participates by name (matches inline path[0])

	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(nodeByKey))
	var stack []string
	var cycles [][]string

	var dfs func(key string, n *Node)
	dfs = func(key string, n *Node) {
		color[key] = gray
		stack = append(stack, key)
		for i := range n.Dependencies {
			e := &n.Dependencies[i]
			if e.Type != EdgeDependency {
				continue
			}
			switch color[e.Ref] {
			case gray:
				if e.Error == "" {
					e.Error = fmt.Sprintf("cycle detected: %s", e.Ref)
				}
				cycles = append(cycles, append(slices.Clone(stack), e.Ref))
			case white:
				if cn, ok := nodeByKey[e.Ref]; ok {
					dfs(e.Ref, cn)
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[key] = black
	}
	dfs(root.Name, root)

	sort.Slice(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], "\x00") < strings.Join(cycles[j], "\x00")
	})
	return cycles
}
