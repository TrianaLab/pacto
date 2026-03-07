package app

import (
	"context"
	"fmt"

	"github.com/trianalab/pacto/internal/graph"
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

	bundle, err := s.BundleStore.Pull(ctx, parsed.Location)
	if err != nil {
		return nil, err
	}

	output := opts.Output
	if output == "" {
		output = bundle.Contract.Service.Name
	}

	if err := extractBundleFS(bundle.FS, output); err != nil {
		return nil, fmt.Errorf("failed to extract bundle: %w", err)
	}

	return &PullResult{
		Ref:     parsed.Location,
		Output:  output,
		Name:    bundle.Contract.Service.Name,
		Version: bundle.Contract.Service.Version,
	}, nil
}
