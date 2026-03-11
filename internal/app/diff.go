package app

import (
	"context"
	"fmt"
	"log/slog"

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
	slog.Debug("resolving old contract", "path", opts.OldPath)
	oldBundle, err := s.resolveBundle(ctx, opts.OldPath)
	if err != nil {
		return nil, fmt.Errorf("old contract: %w", err)
	}

	slog.Debug("resolving new contract", "path", opts.NewPath)
	newBundle, err := s.resolveBundle(ctx, opts.NewPath)
	if err != nil {
		return nil, fmt.Errorf("new contract: %w", err)
	}

	slog.Debug("comparing contracts")
	result := diff.Compare(oldBundle.Contract, newBundle.Contract, oldBundle.FS, newBundle.FS)

	slog.Debug("resolving dependency graphs for diff")
	oldFetcher := s.newDepFetcher(opts.OldPath)
	newFetcher := s.newDepFetcher(opts.NewPath)

	oldGraph := graph.Resolve(ctx, oldBundle.Contract, oldFetcher)
	newGraph := graph.Resolve(ctx, newBundle.Contract, newFetcher)
	gd := graph.DiffGraphs(oldGraph, newGraph)
	slog.Debug("diff complete", "classification", result.Classification.String(), "changes", len(result.Changes))

	return &DiffResult{
		OldPath:        opts.OldPath,
		NewPath:        opts.NewPath,
		Classification: result.Classification.String(),
		Changes:        result.Changes,
		GraphDiff:      gd,
	}, nil
}
