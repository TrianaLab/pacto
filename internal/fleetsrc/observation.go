package fleetsrc

import (
	"context"
	"os"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/otelobserver"
)

// ObservationSource reads runtime-observed dependency edges from an OTLP/JSON
// trace file and contributes them as [fleet.Collection.Observed]. It is the real
// observation pipeline into a snapshot: [fleet.Build] resolves each raw observed
// endpoint name to a unique domain-qualified service and folds the resolved edges
// in as observed relationships, so the operational graph, impact and reconciliation
// all see runtime evidence — not a test fixture. An endpoint that resolves to zero
// or multiple services is preserved as an explicit limitation, never coerced to a
// domain.
//
// This adapter carries NO deployed targets or evaluation results: it is purely a
// source of observed edges. Combine it with definition/target sources to reconcile
// intent against reality.
type ObservationSource struct {
	id   string
	path string
}

// NewObservationSource returns an observation source reading an OTLP/JSON trace
// file at path.
func NewObservationSource(id, path string) *ObservationSource {
	if id == "" {
		id = "observation"
	}
	return &ObservationSource{id: id, path: path}
}

// ID implements [fleet.Source].
func (s *ObservationSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *ObservationSource) Kind() string { return "observation" }

// Collect reads and parses the trace file into observed dependency edges. A
// missing or unparseable file is a source error (the source is unavailable);
// endpoint resolution and cross-domain safety happen later in [fleet.Build].
func (s *ObservationSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	td, err := otelobserver.ParseTraces(data)
	if err != nil {
		return nil, err
	}
	edges := otelobserver.DependencyEdges(td)
	col := &fleet.Collection{Observed: make([]fleet.ObservedEdge, 0, len(edges))}
	for _, e := range edges {
		col.Observed = append(col.Observed, fleet.ObservedEdge{From: e.From, To: e.To, Count: e.Count})
	}
	return col, nil
}
