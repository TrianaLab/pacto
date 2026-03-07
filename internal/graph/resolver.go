package graph

import (
	"context"
	"fmt"

	"github.com/trianalab/pacto/pkg/contract"
)

// ContractFetcher fetches a contract from a remote reference.
// Satisfied by an adapter over oci.BundleStore in the app layer.
type ContractFetcher interface {
	Fetch(ctx context.Context, ref string) (*contract.Contract, error)
}

// Node represents a service in the dependency graph.
type Node struct {
	Name         string             `json:"name"`
	Version      string             `json:"version"`
	Ref          string             `json:"ref,omitempty"`
	Local        bool               `json:"local,omitempty"`
	Dependencies []Edge             `json:"dependencies,omitempty"`
	Contract     *contract.Contract `json:"-"`
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

// Resolve builds the dependency graph starting from the given contract.
// It recursively fetches dependencies via the fetcher, detects cycles
// and version conflicts. If fetcher is nil, only direct dependencies
// are shown without resolution.
func Resolve(ctx context.Context, c *contract.Contract, fetcher ContractFetcher) *Result {
	root := &Node{
		Name:     c.Service.Name,
		Version:  c.Service.Version,
		Contract: c,
	}

	visited := map[string]*Node{}
	path := []string{c.Service.Name}

	var cycles [][]string

	for _, dep := range c.Dependencies {
		edge := resolveEdge(ctx, dep, fetcher, visited, path, &cycles)
		root.Dependencies = append(root.Dependencies, edge)
	}

	conflicts := detectConflicts(root)

	return &Result{
		Root:      root,
		Cycles:    cycles,
		Conflicts: conflicts,
	}
}

// resolveEdge resolves a single dependency edge, recursing into its dependencies.
func resolveEdge(ctx context.Context, dep contract.Dependency, fetcher ContractFetcher, visited map[string]*Node, path []string, cycles *[][]string) Edge {
	local := ParseDependencyRef(dep.Ref).IsLocal()
	edge := Edge{
		Ref:           dep.Ref,
		Required:      dep.Required,
		Compatibility: dep.Compatibility,
		Local:         local,
	}

	if fetcher == nil {
		return edge
	}

	// Cycle detection: if this ref is already in the current path, it's a cycle.
	if inPath(dep.Ref, path) {
		cyclePath := append(append([]string{}, path...), dep.Ref)
		*cycles = append(*cycles, cyclePath)
		edge.Error = fmt.Sprintf("cycle detected: %s", dep.Ref)
		return edge
	}

	// If already resolved, return a shared reference (avoid redundant fetches).
	if prev := visited[dep.Ref]; prev != nil {
		edge.Shared = true
		edge.Node = &Node{Name: prev.Name, Version: prev.Version, Ref: prev.Ref, Local: prev.Local}
		return edge
	}

	depContract, err := fetcher.Fetch(ctx, dep.Ref)
	if err != nil {
		edge.Error = err.Error()
		return edge
	}

	node := &Node{
		Name:     depContract.Service.Name,
		Version:  depContract.Service.Version,
		Ref:      dep.Ref,
		Local:    local,
		Contract: depContract,
	}
	visited[dep.Ref] = node

	childPath := append(append([]string{}, path...), dep.Ref)
	for _, childDep := range depContract.Dependencies {
		childEdge := resolveEdge(ctx, childDep, fetcher, visited, childPath, cycles)
		node.Dependencies = append(node.Dependencies, childEdge)
	}

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
