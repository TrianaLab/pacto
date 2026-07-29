package app

import (
	"context"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/otelobserver"
	"github.com/trianalab/pacto/v3/pkg/reconcile"
)

// ReconcileOptions configures declared-vs-observed reconciliation: the fleet
// sources supply declared dependencies, the OTLP/JSON traces supply observed
// ones.
type ReconcileOptions struct {
	Fleet  FleetOptions
	Traces []byte
}

// Reconcile builds the fleet snapshot (declared dependencies) and the observed
// dependency edges from trace data, then reports where intent and reality
// agree, diverge, or hide undeclared traffic.
func (s *Service) Reconcile(ctx context.Context, opts ReconcileOptions) (reconcile.Report, error) {
	td, err := otelobserver.ParseTraces(opts.Traces)
	if err != nil {
		return reconcile.Report{}, err
	}
	snap, err := s.Fleet(ctx, opts.Fleet)
	if err != nil {
		return reconcile.Report{}, err
	}
	return reconcile.Reconcile(declaredFromSnapshot(snap), observedFromEdges(otelobserver.DependencyEdges(td))), nil
}

// declaredFromSnapshot extracts declared dependency edges from the snapshot's
// relationships, preferring the resolved service identity over the raw
// dependency name so it matches runtime service names when the ref resolves.
func declaredFromSnapshot(snap *fleet.FleetSnapshot) []reconcile.Declared {
	var out []reconcile.Declared
	for _, rel := range snap.Relationships {
		if rel.Type != fleet.RelationshipDependency {
			continue
		}
		dep := rel.ToService
		if dep == "" {
			dep = rel.To
		}
		out = append(out, reconcile.Declared{Service: rel.FromService, Dependency: dep, Required: rel.Required})
	}
	return out
}

// observedFromEdges adapts observer edges to reconciliation inputs.
func observedFromEdges(edges []otelobserver.Edge) []reconcile.Observed {
	out := make([]reconcile.Observed, 0, len(edges))
	for _, e := range edges {
		out = append(out, reconcile.Observed{Service: e.From, Dependency: e.To, Count: e.Count})
	}
	return out
}
