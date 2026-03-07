package app

import (
	"context"
	"fmt"

	"github.com/trianalab/pacto/internal/diff"
	"github.com/trianalab/pacto/internal/graph"
)

// DiffOptions holds options for the diff command.
type DiffOptions struct {
	OldPath string
	NewPath string
}

// DiffResult holds the result of the diff command.
type DiffResult struct {
	OldPath        string           `json:"oldPath"`
	NewPath        string           `json:"newPath"`
	Classification string           `json:"classification"`
	Changes        []diff.Change    `json:"changes"`
	GraphDiff      *graph.GraphDiff `json:"graphDiff,omitempty"`
}

// Diff compares two contracts and produces a classified change set.
func (s *Service) Diff(ctx context.Context, opts DiffOptions) (*DiffResult, error) {
	oldBundle, err := s.resolveBundle(ctx, opts.OldPath)
	if err != nil {
		return nil, fmt.Errorf("old contract: %w", err)
	}

	newBundle, err := s.resolveBundle(ctx, opts.NewPath)
	if err != nil {
		return nil, fmt.Errorf("new contract: %w", err)
	}

	result := diff.Compare(oldBundle.Contract, newBundle.Contract, oldBundle.FS, newBundle.FS)

	oldFetcher := s.newDepFetcher(opts.OldPath)
	newFetcher := s.newDepFetcher(opts.NewPath)

	oldGraph := graph.Resolve(ctx, oldBundle.Contract, oldFetcher)
	newGraph := graph.Resolve(ctx, newBundle.Contract, newFetcher)
	gd := graph.DiffGraphs(oldGraph, newGraph)

	return &DiffResult{
		OldPath:        opts.OldPath,
		NewPath:        opts.NewPath,
		Classification: result.Classification.String(),
		Changes:        result.Changes,
		GraphDiff:      gd,
	}, nil
}
