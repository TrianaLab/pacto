package app

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/diff"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/override"
	"github.com/trianalab/pacto/pkg/sbom"
)

// DiffOptions holds options for the diff command.
type DiffOptions struct {
	OldPath      string
	NewPath      string
	OldOverrides override.Overrides
	NewOverrides override.Overrides
}

// DependencyDiff holds the diff result for a single dependency.
type DependencyDiff struct {
	Name           string        `json:"name"`
	Classification string        `json:"classification"`
	Changes        []diff.Change `json:"changes"`
	SBOMDiff       *sbom.Result  `json:"sbomDiff,omitempty"`
}

// DiffResult holds the result of the diff command.
type DiffResult struct {
	OldPath         string           `json:"oldPath"`
	NewPath         string           `json:"newPath"`
	Classification  string           `json:"classification"`
	Changes         []diff.Change    `json:"changes"`
	DependencyDiffs []DependencyDiff `json:"dependencyDiffs,omitempty"`
	GraphDiff       *graph.GraphDiff `json:"graphDiff,omitempty"`
	SBOMDiff        *sbom.Result     `json:"sbomDiff,omitempty"`
}

// Diff compares two contracts and produces a classified change set.
func (s *Service) Diff(ctx context.Context, opts DiffOptions) (*DiffResult, error) {
	slog.Debug("resolving old contract", "path", opts.OldPath)
	oldBundle, err := s.resolveBundleWithOverrides(ctx, opts.OldPath, opts.OldOverrides)
	if err != nil {
		return nil, fmt.Errorf("old contract: %w", err)
	}

	slog.Debug("resolving new contract", "path", opts.NewPath)
	newBundle, err := s.resolveBundleWithOverrides(ctx, opts.NewPath, opts.NewOverrides)
	if err != nil {
		return nil, fmt.Errorf("new contract: %w", err)
	}

	slog.Debug("comparing contracts")
	result := diff.Compare(oldBundle.Contract, newBundle.Contract, oldBundle.FS, newBundle.FS)

	slog.Debug("resolving dependency graphs for diff")
	oldFetcher := s.newDepFetcher(opts.OldPath)
	newFetcher := s.newDiffFetcher(opts.NewPath)

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
		hasSBOMChanges := depResult.SBOMDiff != nil && len(depResult.SBOMDiff.Changes) > 0
		if len(depResult.Changes) == 0 && !hasSBOMChanges {
			continue
		}
		if depResult.Classification > overall {
			overall = depResult.Classification
		}
		depDiffs = append(depDiffs, DependencyDiff{
			Name:           name,
			Classification: depResult.Classification.String(),
			Changes:        depResult.Changes,
			SBOMDiff:       depResult.SBOMDiff,
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
		SBOMDiff:        result.SBOMDiff,
	}, nil
}

// newDiffFetcher creates a ContractFetcher for the diff command. When the ref
// is a local path, it wraps the normal fetcher to prefer local sibling
// directories over OCI resolution. This ensures that locally modified
// dependencies are used instead of their published OCI versions.
func (s *Service) newDiffFetcher(ref string) graph.ContractFetcher {
	inner := s.newDepFetcher(ref)
	df := inner.(*depFetcher)
	if df.baseDir == "" {
		return inner
	}
	return &localOverrideFetcher{
		inner:     inner,
		parentDir: filepath.Dir(df.baseDir),
	}
}

// localOverrideFetcher wraps a ContractFetcher and resolves OCI dependencies
// from local sibling directories when they exist. For example, if the root
// contract is at /repo/pactos/em-runtime and a dependency references
// oci://registry/em-runtime-governance, it checks for
// /repo/pactos/em-runtime-governance/pacto.yaml before pulling from OCI.
type localOverrideFetcher struct {
	inner     graph.ContractFetcher
	parentDir string
}

func (f *localOverrideFetcher) Fetch(ctx context.Context, dep contract.Dependency) (*contract.Bundle, error) {
	parsed := graph.ParseDependencyRef(dep.Ref)
	if parsed.IsOCI() {
		name := ociRefName(parsed.Location)
		localPath := filepath.Join(f.parentDir, name)
		bundle, err := loadLocalBundle(localPath)
		if err == nil {
			slog.Debug("using local override for dependency", "ref", dep.Ref, "path", localPath)
			return bundle, nil
		}
	}
	return f.inner.Fetch(ctx, dep)
}

// ociRefName extracts the last path segment from an OCI location,
// stripping any tag. e.g. "ghcr.io/org/pactos/my-svc:1.0.0" → "my-svc".
func ociRefName(location string) string {
	if idx := strings.LastIndex(location, ":"); idx > strings.LastIndex(location, "/") {
		location = location[:idx]
	}
	return path.Base(location)
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
