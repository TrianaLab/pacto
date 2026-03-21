package graph

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/trianalab/pacto/pkg/contract"
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

// Edge represents a dependency relationship.
type Edge struct {
	Ref           string `json:"ref"`
	Required      bool   `json:"required"`
	Compatibility string `json:"compatibility"`
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

// resolver holds shared state for a single graph resolution pass.
type resolver struct {
	fetcher ContractFetcher
	mu      sync.Mutex
	visited map[string]*Node
	errors  map[string]string
	pending map[string]chan struct{}
	cycles  [][]string
}

// Resolve builds the dependency graph starting from the given contract.
// It recursively fetches dependencies via the fetcher, detects cycles
// and version conflicts. If fetcher is nil, only direct dependencies
// are shown without resolution. Sibling dependencies at each level are
// fetched concurrently.
func Resolve(ctx context.Context, c *contract.Contract, fetcher ContractFetcher) *Result {
	slog.Debug("starting graph resolution", "root", c.Service.Name, "version", c.Service.Version, "dependencies", len(c.Dependencies))
	root := &Node{
		Name:     c.Service.Name,
		Version:  c.Service.Version,
		Contract: c,
	}

	r := &resolver{
		fetcher: fetcher,
		visited: map[string]*Node{},
		errors:  map[string]string{},
		pending: map[string]chan struct{}{},
	}

	path := []string{c.Service.Name}
	root.Dependencies = r.resolveChildren(ctx, c.Dependencies, path)

	conflicts := detectConflicts(root)
	slog.Debug("graph resolution complete", "root", c.Service.Name, "cycles", len(r.cycles), "conflicts", len(conflicts))

	return &Result{
		Root:      root,
		Cycles:    r.cycles,
		Conflicts: conflicts,
	}
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
		Local:         local,
	}

	if r.fetcher == nil {
		return edge
	}

	r.mu.Lock()
	if inPath(dep.Ref, path) {
		cyclePath := append(append([]string{}, path...), dep.Ref)
		r.cycles = append(r.cycles, cyclePath)
		r.mu.Unlock()
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

	slog.Debug("fetching dependency", "ref", dep.Ref)
	bundle, err := r.fetcher.Fetch(ctx, dep)
	if err != nil {
		slog.Debug("dependency fetch failed", "ref", dep.Ref, "error", err)
		r.mu.Lock()
		r.errors[dep.Ref] = err.Error()
		delete(r.pending, dep.Ref)
		r.mu.Unlock()
		close(ch)
		edge.Error = err.Error()
		return edge
	}
	if bundle == nil || bundle.Contract == nil {
		errMsg := fmt.Sprintf("fetcher returned nil bundle for %s", dep.Ref)
		r.mu.Lock()
		r.errors[dep.Ref] = errMsg
		delete(r.pending, dep.Ref)
		r.mu.Unlock()
		close(ch)
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

	childPath := append(append([]string{}, path...), dep.Ref)
	node.Dependencies = r.resolveChildren(ctx, bundle.Contract.Dependencies, childPath)

	edge.Node = node
	return edge
}

func inPath(ref string, path []string) bool {
	for _, p := range path {
		if p == ref {
			return true
		}
	}
	return false
}
