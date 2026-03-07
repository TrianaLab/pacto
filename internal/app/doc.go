package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/trianalab/pacto/internal/doc"
	"github.com/trianalab/pacto/internal/graph"
)

// generateDoc is the function used to generate documentation. It is a variable
// so that tests can replace it to simulate errors.
var generateDoc = doc.Generate

// DocOptions holds options for the doc command.
type DocOptions struct {
	Path      string
	OutputDir string
}

// DocResult holds the result of the doc command.
type DocResult struct {
	ServiceName string `json:"serviceName"`
	Markdown    string `json:"markdown"`
	Path        string `json:"path,omitempty"`
}

// Doc generates Markdown documentation from a contract.
func (s *Service) Doc(ctx context.Context, opts DocOptions) (*DocResult, error) {
	ref := defaultPath(opts.Path)

	bundle, err := s.resolveBundle(ctx, ref)
	if err != nil {
		return nil, err
	}

	fetcher := s.newDepFetcher(ref)
	gr := graph.Resolve(ctx, bundle.Contract, fetcher)

	markdown, err := generateDoc(bundle.Contract, bundle.FS, gr)
	if err != nil {
		return nil, fmt.Errorf("generating documentation: %w", err)
	}

	result := &DocResult{
		ServiceName: bundle.Contract.Service.Name,
		Markdown:    markdown,
	}

	if opts.OutputDir != "" {
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
