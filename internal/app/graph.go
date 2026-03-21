package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/override"
)

// GraphOptions holds options for the graph command.
type GraphOptions struct {
	Path      string
	Overrides override.Overrides
}

// GraphResult is the result of the graph command.
type GraphResult = graph.Result

// Graph resolves the dependency graph for a contract.
func (s *Service) Graph(ctx context.Context, opts GraphOptions) (*GraphResult, error) {
	ref := defaultPath(opts.Path)

	slog.Debug("resolving contract for graph", "ref", ref)
	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return nil, err
	}

	slog.Debug("resolving dependency graph", "name", bundle.Contract.Service.Name)
	fetcher := s.newDepFetcher(ref)
	result := graph.Resolve(ctx, bundle.Contract, fetcher)
	slog.Debug("graph resolution complete", "dependencies", len(result.Root.Dependencies), "cycles", len(result.Cycles), "conflicts", len(result.Conflicts))
	return result, nil
}

// BundlePuller is the subset of oci.BundleStore needed by the fetcher.
// Defined here to avoid importing internal/oci from internal/graph.
type BundlePuller interface {
	Pull(ctx context.Context, ref string) (*contract.Bundle, error)
	ListTags(ctx context.Context, repo string) ([]string, error)
}

// depFetcher resolves dependency contracts from both OCI and local sources.
// It uses the baseDir of the root contract to resolve relative local paths.
type depFetcher struct {
	store   BundlePuller
	baseDir string
}

// newDepFetcher creates a ContractFetcher that can resolve both OCI and local
// dependency references. baseRef is the path/ref of the root contract.
func (s *Service) newDepFetcher(baseRef string) graph.ContractFetcher {
	base := ""
	if !isOCIRef(baseRef) {
		abs, err := filepath.Abs(baseRef)
		if err == nil {
			base = abs
		}
	}
	return &depFetcher{store: s.BundleStore, baseDir: base}
}

func (f *depFetcher) Fetch(ctx context.Context, dep contract.Dependency) (*contract.Bundle, error) {
	parsed := graph.ParseDependencyRef(dep.Ref)
	if parsed.IsLocal() {
		slog.Debug("fetching local dependency", "ref", dep.Ref)
		return f.fetchLocal(parsed)
	}
	if f.store == nil {
		return nil, fmt.Errorf("OCI store not configured (cannot fetch %s)", dep.Ref)
	}
	slog.Debug("fetching OCI dependency", "ref", dep.Ref, "compatibility", dep.Compatibility)
	location, err := oci.ResolveRef(ctx, f.store, parsed.Location, dep.Compatibility)
	if err != nil {
		return nil, err
	}
	return f.store.Pull(ctx, location)
}

func (f *depFetcher) fetchLocal(ref graph.DependencyRef) (*contract.Bundle, error) {
	path := ref.Location
	if !filepath.IsAbs(path) && f.baseDir != "" {
		path = filepath.Join(f.baseDir, path)
	}
	return loadLocalBundle(path)
}
