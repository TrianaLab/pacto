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
	observed, unresolved := observedFromEdges(snap, otelobserver.DependencyEdges(td))
	rep := reconcile.Reconcile(declaredFromSnapshot(snap), observed)
	rep.Unresolved = unresolved
	rep.Summary.Unresolved = len(unresolved)
	return rep, nil
}

// declaredFromSnapshot extracts declared dependency edges from the snapshot's
// relationships as DOMAIN-QUALIFIED identities. Both endpoints keep their full
// service key so a dependency declared by one domain's service can never match
// observed traffic attributed to a same-named service in another domain. When a
// dependency ref did not resolve to a fleet service, the raw name is used as-is
// (it stays unqualified and simply won't match a domain-qualified observation).
func declaredFromSnapshot(snap *fleet.FleetSnapshot) []reconcile.Declared {
	var out []reconcile.Declared
	for _, rel := range snap.Relationships {
		if rel.Type != fleet.RelationshipDependency {
			continue
		}
		dep := string(rel.ToService)
		if dep == "" {
			dep = rel.To
		}
		out = append(out, reconcile.Declared{Service: string(rel.FromService), Dependency: dep, Required: rel.Required})
	}
	return out
}

// observedFromEdges adapts observer edges to reconciliation inputs. The CALLER
// name must resolve to a UNIQUE domain-qualified fleet service — an unknown or
// ambiguous caller cannot be attributed to any contract, so its traffic is
// preserved as unresolved observed knowledge rather than being forced into the
// default domain. The CALLEE is resolved WITHIN the caller's domain, mirroring how
// declared dependencies resolve, so a dependency edge matches consistently and is
// never mis-attributed to a same-named service in another domain. A callee that is
// not a fleet service in that domain keeps its raw name (an external or undeclared
// dependency), which is expected and not itself an ambiguity.
func observedFromEdges(snap *fleet.FleetSnapshot, edges []otelobserver.Edge) ([]reconcile.Observed, []reconcile.Unresolved) {
	resolve := snap.ObservedNameResolver()
	var out []reconcile.Observed
	var unresolved []reconcile.Unresolved
	for _, e := range edges {
		callerKey, res := resolve(e.From)
		if res != fleet.ObservedResolved {
			reason := "unknown"
			if res == fleet.ObservedAmbiguous {
				reason = "ambiguous"
			}
			unresolved = append(unresolved, reconcile.Unresolved{Service: e.From, Dependency: e.To, Count: e.Count, Reason: reason})
			continue
		}
		domain, _ := fleet.ParseServiceKey(callerKey)
		dep := e.To
		if k := fleet.NewServiceKeyDomain(domain, e.To); snap.Services[k] != nil {
			dep = string(k)
		}
		out = append(out, reconcile.Observed{Service: string(callerKey), Dependency: dep, Count: e.Count})
	}
	return out, unresolved
}
