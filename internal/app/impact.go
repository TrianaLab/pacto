package app

import (
	"context"
	"fmt"

	"github.com/trianalab/pacto/v3/pkg/impact"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/otelobserver"
	"github.com/trianalab/pacto/v3/pkg/override"
)

// ImpactOptions configures blast-radius analysis of a change from an old to a new
// contract revision, projected onto a fleet snapshot assembled from Fleet.
type ImpactOptions struct {
	OldPath      string
	NewPath      string
	OldOverrides override.Overrides
	NewOverrides override.Overrides
	Fleet        FleetOptions
	// IncludeObserved lets observed (runtime) relationships raise consumer
	// confidence when the graph carries them, and surfaces observed-only
	// consumers derived from Traces.
	IncludeObserved bool
	// Traces is optional OTLP/JSON trace data; its observed caller→provider edges
	// corroborate declared consumers and surface shadow (observed-only) ones.
	Traces []byte
}

// Impact resolves the old and new contract revisions, builds a fleet snapshot
// from the configured sources, and analyzes the change's blast radius over it.
func (s *Service) Impact(ctx context.Context, opts ImpactOptions) (*impact.Result, error) {
	logging.LoggerFromContext(ctx).Debug("resolving old revision", "path", opts.OldPath)
	oldBundle, err := s.resolveBundleWithOverrides(ctx, opts.OldPath, opts.OldOverrides)
	if err != nil {
		return nil, fmt.Errorf("old revision: %w", err)
	}
	logging.LoggerFromContext(ctx).Debug("resolving new revision", "path", opts.NewPath)
	newBundle, err := s.resolveBundleWithOverrides(ctx, opts.NewPath, opts.NewOverrides)
	if err != nil {
		return nil, fmt.Errorf("new revision: %w", err)
	}
	snap, err := s.Fleet(ctx, opts.Fleet)
	if err != nil {
		return nil, fmt.Errorf("build fleet snapshot: %w", err)
	}
	observed, err := observedEdgesFromTraces(opts.Traces)
	if err != nil {
		return nil, fmt.Errorf("parse traces: %w", err)
	}
	return impact.Analyze(ctx, oldBundle.Contract, newBundle.Contract, oldBundle.FS, newBundle.FS, snap,
		impact.Options{IncludeObserved: opts.IncludeObserved, ObservedEdges: observed}), nil
}

// observedEdgesFromTraces derives impact observed edges from OTLP/JSON trace
// data. Empty input yields no edges (declared-only analysis).
func observedEdgesFromTraces(data []byte) ([]impact.ObservedEdge, error) {
	if len(data) == 0 {
		return nil, nil
	}
	td, err := otelobserver.ParseTraces(data)
	if err != nil {
		return nil, err
	}
	edges := otelobserver.DependencyEdges(td)
	out := make([]impact.ObservedEdge, 0, len(edges))
	for _, e := range edges {
		out = append(out, impact.ObservedEdge{Consumer: e.From, Provider: e.To})
	}
	return out, nil
}
