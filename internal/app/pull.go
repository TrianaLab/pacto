package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/trianalab/pacto/internal/graph"
	"github.com/trianalab/pacto/internal/oci"
)

// PullOptions holds options for the pull command.
type PullOptions struct {
	Ref    string
	Output string
}

// PullResult holds the result of the pull command.
type PullResult struct {
	Ref     string
	Output  string
	Name    string
	Version string
}

// Pull fetches a contract bundle from an OCI registry and extracts it to disk.
func (s *Service) Pull(ctx context.Context, opts PullOptions) (*PullResult, error) {
	if err := s.requireBundleStore(); err != nil {
		return nil, err
	}

	parsed := graph.ParseDependencyRef(opts.Ref)
	if !parsed.IsOCI() {
		return nil, fmt.Errorf("pull requires an OCI reference (oci://...): got %q", opts.Ref)
	}

	slog.Debug("resolving OCI reference", "ref", parsed.Location)
	location, err := oci.ResolveRef(ctx, s.BundleStore, parsed.Location, "")
	if err != nil {
		return nil, err
	}

	slog.Debug("pulling bundle from registry", "ref", location)
	bundle, err := s.BundleStore.Pull(ctx, location)
	if err != nil {
		return nil, err
	}

	output := opts.Output
	if output == "" {
		output = bundle.Contract.Service.Name
	}

	slog.Debug("extracting bundle to disk", "output", output)
	if err := extractBundleFS(bundle.FS, output); err != nil {
		return nil, fmt.Errorf("failed to extract bundle: %w", err)
	}

	slog.Debug("pull complete", "name", bundle.Contract.Service.Name, "version", bundle.Contract.Service.Version)
	return &PullResult{
		Ref:     location,
		Output:  output,
		Name:    bundle.Contract.Service.Name,
		Version: bundle.Contract.Service.Version,
	}, nil
}
