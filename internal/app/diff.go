package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/trianalab/pacto/internal/diff"
	"github.com/trianalab/pacto/internal/graph"
)

// DiffOptions holds options for the diff command.
type DiffOptions struct {
	OldPath string
	NewPath string
}

// DependencyDiff holds the diff result for a single dependency.
type DependencyDiff struct {
	Name           string        `json:"name"`
	Classification string        `json:"classification"`
	Changes        []diff.Change `json:"changes"`
}

// DiffResult holds the result of the diff command.
type DiffResult struct {
	OldPath         string           `json:"oldPath"`
	NewPath         string           `json:"newPath"`
	Classification  string           `json:"classification"`
	Changes         []diff.Change    `json:"changes"`
	DependencyDiffs []DependencyDiff `json:"dependencyDiffs,omitempty"`
	GraphDiff       *graph.GraphDiff `json:"graphDiff,omitempty"`
}

// Diff compares two contracts and produces a classified change set.
func (s *Service) Diff(ctx context.Context, opts DiffOptions) (*DiffResult, error) {
	slog.Debug("resolving old contract", "path", opts.OldPath)
	oldBundle, err := s.resolveBundle(ctx, opts.OldPath)
	if err != nil {
		return nil, fmt.Errorf("old contract: %w", err)
	}

	slog.Debug("resolving new contract", "path", opts.NewPath)
	newBundle, err := s.resolveBundle(ctx, opts.NewPath)
	if err != nil {
		return nil, fmt.Errorf("new contract: %w", err)
	}

	slog.Debug("comparing contracts")
	result := diff.Compare(oldBundle.Contract, newBundle.Contract, oldBundle.FS, newBundle.FS)

	slog.Debug("resolving dependency graphs for diff")
	oldFetcher := s.newDepFetcher(opts.OldPath)
	newFetcher := s.newDepFetcher(opts.NewPath)

	oldGraph := graph.Resolve(ctx, oldBundle.Contract, oldFetcher)
	newGraph := graph.Resolve(ctx, newBundle.Contract, newFetcher)
	gd := graph.DiffGraphs(oldGraph, newGraph)

	// Recursively diff dependency contracts (skip root, already diffed above).
	rootName := oldBundle.Contract.Service.Name
	oldNodes := collectNodes(oldGraph.Root)
	newNodes := collectNodes(newGraph.Root)
	overall := result.Classification
	var depDiffs []DependencyDiff
	for name, newNode := range newNodes {
		if name == rootName {
			continue
		}
		oldNode, exists := oldNodes[name]
		if !exists || oldNode.Contract == nil || newNode.Contract == nil {
			continue
		}
		depResult := diff.Compare(oldNode.Contract, newNode.Contract, oldNode.FS, newNode.FS)
		if len(depResult.Changes) == 0 {
			continue
		}
		if depResult.Classification > overall {
			overall = depResult.Classification
		}
		depDiffs = append(depDiffs, DependencyDiff{
			Name:           name,
			Classification: depResult.Classification.String(),
			Changes:        depResult.Changes,
		})
	}

	slog.Debug("diff complete", "classification", overall.String(), "changes", len(result.Changes), "dependencyDiffs", len(depDiffs))

	return &DiffResult{
		OldPath:         opts.OldPath,
		NewPath:         opts.NewPath,
		Classification:  overall.String(),
		Changes:         result.Changes,
		DependencyDiffs: depDiffs,
		GraphDiff:       gd,
	}, nil
}

// collectNodes walks the dependency graph and returns all nodes indexed by name.
func collectNodes(node *graph.Node) map[string]*graph.Node {
	nodes := make(map[string]*graph.Node)
	collectNodesRec(node, nodes)
	return nodes
}

func collectNodesRec(node *graph.Node, nodes map[string]*graph.Node) {
	if node == nil {
		return
	}
	if _, seen := nodes[node.Name]; seen {
		return
	}
	nodes[node.Name] = node
	for _, edge := range node.Dependencies {
		collectNodesRec(edge.Node, nodes)
	}
}
