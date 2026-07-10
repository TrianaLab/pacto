package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/dashboard"
	"github.com/trianalab/pacto/v2/pkg/doc"
	"github.com/trianalab/pacto/v2/pkg/graph"
	"github.com/trianalab/pacto/v2/pkg/override"
)

// generateDoc is the function used to generate documentation. It is a variable
// so that tests can replace it to simulate errors.
var generateDoc = doc.Generate

// DocOptions holds options for the doc command.
type DocOptions struct {
	Path      string
	OutputDir string
	Overrides override.Overrides
}

// DocResult holds the result of the doc command.
type DocResult struct {
	ServiceName string `json:"serviceName"`
	Markdown    string `json:"markdown"`
	Path        string `json:"path,omitempty"`

	// Bundle contains the contract and filesystem needed for features like
	// the interactive API explorer (--swagger). These are not serialised.
	Bundle *contract.Bundle `json:"-"`

	// Details is the dashboard snapshot the Markdown was rendered from, and Graph
	// is the resolved dependency graph. Both feed richer consumers (e.g. the
	// static HTML export) and are not serialised.
	Details *dashboard.ServiceDetails `json:"-"`
	Graph   *dashboard.GlobalGraph    `json:"-"`
}

// Doc generates Markdown documentation from a contract.
func (s *Service) Doc(ctx context.Context, opts DocOptions) (*DocResult, error) {
	ref := defaultPath(opts.Path)

	slog.Debug("resolving contract for doc generation", "ref", ref)
	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return nil, err
	}

	slog.Debug("resolving dependency graph for documentation", "name", bundle.Contract.Service.Name)
	fetcher := s.newDepFetcher(ref)
	gr := graph.Resolve(ctx, bundle.Contract, fetcher)

	slog.Debug("generating markdown documentation")
	details := dashboard.ServiceDetailsFromBundle(bundle, "local")
	details.GenerateInsights()
	markdown, err := generateDoc(details, gr)
	if err != nil {
		return nil, fmt.Errorf("generating documentation: %w", err)
	}

	result := &DocResult{
		ServiceName: bundle.Contract.Service.Name,
		Markdown:    markdown,
		Bundle:      bundle,
		Details:     details,
		Graph:       dashboard.GlobalGraphFromResult(gr, details),
	}

	if opts.OutputDir != "" {
		slog.Debug("writing documentation to disk", "dir", opts.OutputDir)
		filename := bundle.Contract.Service.Name + ".md"
		outPath := filepath.Join(opts.OutputDir, filename)

		if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
			return nil, fmt.Errorf("creating output directory: %w", err)
		}

		if err := os.WriteFile(outPath, []byte(markdown), 0644); err != nil {
			return nil, fmt.Errorf("writing documentation file: %w", err)
		}

		result.Path = outPath
	}

	return result, nil
}
