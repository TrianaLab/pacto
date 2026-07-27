package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/override"
)

// GraphOptions holds options for the graph command.
type GraphOptions struct {
	Path              string
	Overrides         override.Overrides
	IncludeReferences bool
	OnlyReferences    bool
	// OnDepResolved, if non-nil, fires once per unique resolved dependency for
	// progress reporting. Must be goroutine-safe. nil = no-op.
	OnDepResolved func()
}

// GraphResult is the result of the graph command.
type GraphResult = graph.Result

// Graph resolves the dependency graph for a contract.
func (s *Service) Graph(ctx context.Context, opts GraphOptions) (*GraphResult, error) {
	ref := defaultPath(opts.Path)

	logging.LoggerFromContext(ctx).Debug("resolving contract for graph", "ref", ref)
	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return nil, err
	}

	if err := s.verifyLockIfPresent(ctx, ref, bundle); err != nil {
		return nil, err
	}

	logging.LoggerFromContext(ctx).Debug("resolving dependency graph", "name", bundle.Contract.Service.Name)
	fetcher := s.newDepFetcher(ref)
	result := graph.ResolveWithOptions(ctx, bundle.Contract, fetcher, graph.ResolveOptions{
		IncludeReferences: opts.IncludeReferences,
		OnlyReferences:    opts.OnlyReferences,
		OnResolved:        opts.OnDepResolved,
	})
	logging.LoggerFromContext(ctx).Debug("graph resolution complete", "dependencies", len(result.Root.Dependencies), "cycles", len(result.Cycles), "conflicts", len(result.Conflicts))
	return result, nil
}

// BundlePuller is the subset of oci.BundleStore needed by the fetcher.
// Defined here to avoid importing pkg/oci from internal/graph.
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
		logging.LoggerFromContext(ctx).Debug("fetching local dependency", "ref", dep.Ref)
		return f.fetchLocal(parsed)
	}
	if f.store == nil {
		return nil, fmt.Errorf("OCI store not configured (cannot fetch %s)", dep.Ref)
	}
	logging.LoggerFromContext(ctx).Debug("fetching OCI dependency", "ref", dep.Ref, "compatibility", dep.Compatibility)
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
