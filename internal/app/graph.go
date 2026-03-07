package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/trianalab/pacto/internal/graph"
	"github.com/trianalab/pacto/pkg/contract"
)

// GraphOptions holds options for the graph command.
type GraphOptions struct {
	Path string
}

// GraphResult is the result of the graph command.
type GraphResult = graph.Result

// Graph resolves the dependency graph for a contract.
func (s *Service) Graph(ctx context.Context, opts GraphOptions) (*GraphResult, error) {
	ref := defaultPath(opts.Path)

	bundle, err := s.resolveBundle(ctx, ref)
	if err != nil {
		return nil, err
	}

	fetcher := s.newDepFetcher(ref)
	result := graph.Resolve(ctx, bundle.Contract, fetcher)
	return result, nil
}

// BundlePuller is the subset of oci.BundleStore needed by the fetcher.
// Defined here to avoid importing internal/oci from internal/graph.
type BundlePuller interface {
	Pull(ctx context.Context, ref string) (*contract.Bundle, error)
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

func (f *depFetcher) Fetch(ctx context.Context, ref string) (*contract.Bundle, error) {
	parsed := graph.ParseDependencyRef(ref)
	if parsed.IsLocal() {
		return f.fetchLocal(parsed)
	}
	if f.store == nil {
		return nil, fmt.Errorf("OCI store not configured (cannot fetch %s)", ref)
	}
	return f.store.Pull(ctx, parsed.Location)
}

func (f *depFetcher) fetchLocal(ref graph.DependencyRef) (*contract.Bundle, error) {
	path := ref.Location
	if !filepath.IsAbs(path) && f.baseDir != "" {
		path = filepath.Join(f.baseDir, path)
	}
	return loadLocalBundle(path)
}
