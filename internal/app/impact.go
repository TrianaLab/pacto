package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/otelobserver"
	"github.com/trianalab/pacto/v4/pkg/override"
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
// Each call builds a fresh snapshot; the dashboard uses ImpactWithSnapshot to
// analyze against its already-published snapshot instead (so the impact answer's
// snapshotId matches the graph the user is looking at).
//
// When no source holds the changed service, the snapshot is built again with
// the new revision added as one more source. The services that depend on it
// are then found, where before the answer said "no affected consumers" about a
// service it could not see.
func (s *Service) Impact(ctx context.Context, opts ImpactOptions) (*impact.Result, error) {
	oldBundle, newBundle, err := s.resolveImpactPair(ctx, opts)
	if err != nil {
		return nil, err
	}
	snap, err := s.Fleet(ctx, opts.Fleet)
	if err != nil {
		return nil, fmt.Errorf("build fleet snapshot: %w", err)
	}
	domain := revisionDomain(opts.NewPath)
	added := snap.Services[fleet.NewServiceKeyDomain(domain, newBundle.Contract.Service.Name)] == nil
	if added {
		rev := fleet.RawRevision{Bundle: newBundle, Domain: domain, RequestedRef: opts.NewPath}
		// The content digest the local source gives its bundles, so the added
		// revision has the same identity it would have had from --local.
		if h, err := lock.HashFS(newBundle.FS); err == nil {
			rev.Digest = h
		}
		withNew := opts.Fleet
		withNew.extraSources = []fleet.Source{fleet.NewMemorySource("new-revision", "argument", &fleet.Collection{
			Revisions: []fleet.RawRevision{rev},
		})}
		if snap, err = s.Fleet(ctx, withNew); err != nil {
			return nil, fmt.Errorf("build fleet snapshot: %w", err)
		}
	}
	res, err := s.analyzeImpact(ctx, opts, snap, oldBundle, newBundle)
	if err != nil {
		return nil, err
	}
	res.NewRevisionAdded = added
	return res, nil
}

// ImpactWithSnapshot analyzes a change's blast radius over an ALREADY-BUILT
// snapshot, resolving only the old and new contract revisions. It exists so a
// caller holding a published snapshot (the dashboard's Fleet Manager) can get an
// impact answer bound to that exact snapshot — same snapshotId, same as-of time,
// same completeness — rather than a freshly rebuilt one. opts.Fleet is ignored.
func (s *Service) ImpactWithSnapshot(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot) (*impact.Result, error) {
	oldBundle, newBundle, err := s.resolveImpactPair(ctx, opts)
	if err != nil {
		return nil, err
	}
	return s.analyzeImpact(ctx, opts, snap, oldBundle, newBundle)
}

func (s *Service) resolveImpactPair(ctx context.Context, opts ImpactOptions) (oldBundle, newBundle *contract.Bundle, err error) {
	logging.LoggerFromContext(ctx).Debug("resolving old revision", "path", opts.OldPath)
	if oldBundle, err = s.resolveBundleWithOverrides(ctx, opts.OldPath, opts.OldOverrides); err != nil {
		return nil, nil, fmt.Errorf("old revision: %w", err)
	}
	logging.LoggerFromContext(ctx).Debug("resolving new revision", "path", opts.NewPath)
	if newBundle, err = s.resolveBundleWithOverrides(ctx, opts.NewPath, opts.NewOverrides); err != nil {
		return nil, nil, fmt.Errorf("new revision: %w", err)
	}
	return oldBundle, newBundle, nil
}

func (s *Service) analyzeImpact(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot, oldBundle, newBundle *contract.Bundle) (*impact.Result, error) {
	if err := refuseHidden(snap, opts.OldPath, oldBundle.Contract, opts.NewPath, newBundle.Contract); err != nil {
		return nil, err
	}
	observed, err := observedEdgesFromTraces(opts.Traces)
	if err != nil {
		return nil, fmt.Errorf("parse traces: %w", err)
	}
	return impact.Analyze(ctx, oldBundle.Contract, newBundle.Contract, oldBundle.FS, newBundle.FS, snap,
		impact.Options{
			// Domain-qualify the changed service from its new reference so impact is
			// isolated across domains (a local path yields the default domain).
			Domain:          revisionDomain(opts.NewPath),
			IncludeObserved: opts.IncludeObserved,
			ObservedEdges:   observed,
		}), nil
}

// refuseHidden keeps a raw-ref impact request from reading a service the
// caller's permissions hide. The refs resolve with this process's registry
// credentials, so on a snapshot narrowed to the caller (it carries
// HIDDEN_BY_PERMISSIONS) both sides must be services the caller can see: the
// old contract leaks through the diff values as much as the new one. A service
// the narrowed snapshot does not hold reads as not found, so a refusal says
// nothing about whether it exists.
func refuseHidden(snap *fleet.FleetSnapshot, oldRef string, oldC *contract.Contract, newRef string, newC *contract.Contract) error {
	if snap == nil || !slices.ContainsFunc(snap.Limitations, func(l fleet.Limitation) bool {
		return l.Code == fleet.LimitationHiddenByPermissions
	}) {
		return nil
	}
	for _, side := range []struct {
		ref string
		c   *contract.Contract
	}{{oldRef, oldC}, {newRef, newC}} {
		if side.c == nil || snap.Services[fleet.NewServiceKeyDomain(revisionDomain(side.ref), side.c.Service.Name)] == nil {
			return &oci.ArtifactNotFoundError{Ref: side.ref}
		}
	}
	return nil
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

// revisionDomain derives the impact domain from the reference that resolved the
// new revision, using the SAME classifier that loaded it. fleetsrc.OciDomain
// alone treats only "." "/" and "~" as local, while graph.ParseDependencyRef —
// which actually resolves the bundle — treats only "oci://" as remote. A bare
// multi-segment relative path or a file:// URL therefore loaded locally but was
// keyed under an invented OCI domain, so the domain-qualified identity never
// joined the fleet and every consumer silently went missing.
func revisionDomain(ref string) string {
	parsed := graph.ParseDependencyRef(ref)
	if parsed.IsLocal() {
		return ""
	}
	return contract.OCIDomain(parsed.Location)
}
