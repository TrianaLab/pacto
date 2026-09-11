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

// depFetcher resolves dependency contracts from both OCI and local sources. A
// relative local reference is resolved against the directory of the contract
// that DECLARED it, not the root's -- see [depFetcher.FetchFrom].
type depFetcher struct {
	store BundlePuller
	// baseDir is where the ROOT contract's own references resolve from: its
	// absolute directory, or [graph.OCIBase] when the root came from a registry.
	baseDir string
}

// newDepFetcher creates a ContractFetcher that can resolve both OCI and local
// dependency references. baseRef is the path/ref of the root contract.
func (s *Service) newDepFetcher(baseRef string) graph.ContractFetcher {
	base := graph.OCIBase
	if !isOCIRef(baseRef) {
		base = ""
		if abs, err := filepath.Abs(baseRef); err == nil {
			base = abs
		}
	}
	return &depFetcher{store: s.BundleStore, baseDir: base}
}

// RootBase implements [graph.OriginContractFetcher].
func (f *depFetcher) RootBase() string { return f.baseDir }

// Fetch implements [graph.ContractFetcher] by resolving dep as if the ROOT had
// declared it. The resolver never takes this path -- it prefers FetchFrom -- but
// the narrower port is still part of the published interface, so it keeps the
// one meaning it can express.
func (f *depFetcher) Fetch(ctx context.Context, dep contract.Dependency) (*contract.Bundle, error) {
	b, _, err := f.FetchFrom(ctx, f.baseDir, dep)
	return b, err
}

// FetchFrom implements [graph.OriginContractFetcher]. base is where the contract
// that declared dep resolves its references from; the returned base is where the
// FETCHED bundle resolves its own: its directory for a local bundle,
// [graph.OCIBase] for one pulled from a registry.
func (f *depFetcher) FetchFrom(ctx context.Context, base string, dep contract.Dependency) (*contract.Bundle, string, error) {
	parsed := graph.ParseDependencyRef(dep.Ref)
	if parsed.IsLocal() {
		logging.LoggerFromContext(ctx).Debug("fetching local dependency", "ref", dep.Ref, "base", base)
		dir, err := depLocalDir(parsed.Location, base)
		if err != nil {
			return nil, "", err
		}
		b, err := loadLocalBundle(dir)
		if err != nil {
			return nil, "", err
		}
		return b, dir, nil
	}
	if f.store == nil {
		return nil, "", fmt.Errorf("OCI store not configured (cannot fetch %s)", dep.Ref)
	}
	logging.LoggerFromContext(ctx).Debug("fetching OCI dependency", "ref", dep.Ref, "compatibility", dep.Compatibility)
	location, err := oci.ResolveRef(ctx, f.store, parsed.Location, dep.Compatibility)
	if err != nil {
		return nil, "", err
	}
	b, err := f.store.Pull(ctx, location)
	if err != nil {
		return nil, "", err
	}
	return b, graph.OCIBase, nil
}

// depLocalDir decides which directory a local reference means. A reference
// declared by a registry bundle means none: honouring it would let a remote
// contract choose which local files Pacto reads. This is the same rule the
// catalog resolver applies in catalogLocalDir, for the same reason.
func depLocalDir(path, base string) (string, error) {
	if base == graph.OCIBase {
		return "", fmt.Errorf("a local reference declared inside a registry bundle cannot be resolved: %s", path)
	}
	if filepath.IsAbs(path) || base == "" {
		return path, nil
	}
	return filepath.Join(base, path), nil
}
