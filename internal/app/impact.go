package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/lock"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/otelobserver"
	"github.com/trianalab/pacto/v4/pkg/override"
)

// ImpactOptions configures blast-radius analysis of a change from an old to a new
// contract revision, projected onto a fleet snapshot assembled from Fleet.
type ImpactOptions struct {
	OldPath string
	NewPath string
	// Domain qualifies the changed service when NewPath cannot: a local path
	// names no registry, so a caller that knows the service's fleet domain
	// (an edit of a checkout of ghcr.io/acme/orders) passes ghcr.io/acme.
	// Empty derives the domain from NewPath.
	Domain string
	// NewDirName is the folder name a local NewPath's service.name is
	// inferred from when NewPath is a staged copy of that folder; empty uses
	// NewPath's own folder.
	NewDirName   string
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
	// TargetSnapshotAuto derives a target snapshot from the change's
	// published ref (D8) when Fleet carries no target input: the CLI, a
	// stdio MCP server and pacto ci set it, the dashboard never does.
	TargetSnapshotAuto bool
	// OnResolved is called by [Service.Impact] once both revisions resolve;
	// nil does nothing. The snapshot paths never call it.
	OnResolved func(old, new *contract.Bundle)
}

// Impact builds a fleet snapshot from the configured sources and analyzes the
// change's blast radius over it (see [Service.ImpactOnSnapshot], with the new
// revision added when no source holds the changed service).
func (s *Service) Impact(ctx context.Context, opts ImpactOptions) (*impact.Result, error) {
	// Resolve the revisions first, so a bad ref fails before a fleet build.
	oldBundle, newBundle, err := s.resolveImpactPair(ctx, opts)
	if err != nil {
		return nil, err
	}
	if opts.OnResolved != nil {
		opts.OnResolved(oldBundle, newBundle)
	}
	opts.Fleet.indexSrc, _ = s.consumerIndex(opts, newBundle)
	opts, look, _ := s.deriveTargetSnapshot(ctx, opts)
	snap, err := s.Fleet(ctx, opts.Fleet)
	if err != nil {
		return nil, fmt.Errorf("build fleet snapshot: %w", err)
	}
	res, err := s.impactOnResolved(ctx, opts, snap, true, oldBundle, newBundle)
	if err != nil {
		return nil, err
	}
	res.Registry = look
	return res, nil
}

// deriveTargetSnapshot adds the change's target snapshot to opts.Fleet when
// opts asks for it and names no target input of its own: the repository
// [Service.findTargetSnapshot] found, or the failing source of a lookup that
// got no answer. It returns where it looked and whether it added anything.
func (s *Service) deriveTargetSnapshot(ctx context.Context, opts ImpactOptions) (ImpactOptions, *impact.RegistryLookup, bool) {
	if !opts.TargetSnapshotAuto || opts.Fleet.hasTargetInput() {
		return opts, nil, false
	}
	found, look, failed := s.findTargetSnapshot(ctx, opts.OldPath, opts.NewPath)
	switch {
	case failed != nil:
		opts.Fleet.extraSources = append(slices.Clone(opts.Fleet.extraSources), failed)
	case found != "":
		opts.Fleet.TargetSnapshot = []string{found}
	default:
		return opts, look, false
	}
	return opts, look, true
}

// ImpactWithSnapshot analyzes a change's blast radius over an ALREADY-BUILT
// snapshot, resolving only the old and new contract revisions. It exists so a
// caller holding a published snapshot (the dashboard's Fleet Manager) can get an
// impact answer bound to that exact snapshot — same snapshotId, same as-of time,
// same completeness — rather than a freshly rebuilt one. opts.Fleet is ignored.
func (s *Service) ImpactWithSnapshot(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot) (*impact.Result, error) {
	return s.ImpactOnSnapshot(ctx, opts, snap, false)
}

// ImpactOnSnapshot resolves the old and new contract revisions and analyzes the
// change's blast radius over snap.
//
// With addMissing set and no source holding the changed service, the snapshot
// is built again from opts.Fleet with the new revision added as one more
// source. The services that depend on it are then found, where before the
// answer said "no affected consumers" about a service it could not see. Without
// it the answer is bound to snap and says SERVICE_NOT_IN_FLEET; a caller
// answering from a restricted view must pass false, so no unrestricted rebuild
// leaks.
func (s *Service) ImpactOnSnapshot(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot, addMissing bool) (*impact.Result, error) {
	oldBundle, newBundle, err := s.resolveImpactPair(ctx, opts)
	if err != nil {
		return nil, err
	}
	var look *impact.RegistryLookup
	if addMissing {
		var derived bool
		if opts, look, derived = s.deriveTargetSnapshot(ctx, opts); derived {
			if snap, err = s.Fleet(ctx, opts.Fleet); err != nil {
				return nil, fmt.Errorf("build fleet snapshot: %w", err)
			}
		}
	}
	res, err := s.impactOnResolved(ctx, opts, snap, addMissing, oldBundle, newBundle)
	if err != nil {
		return nil, err
	}
	res.Registry = look
	return res, nil
}

// impactOnResolved is [Service.ImpactOnSnapshot] with the revisions resolved.
// With addMissing and no index source from the caller, it adds the
// organization index (Decision 12): one rebuild carries the index and, when
// no source holds the changed service, the new revision.
func (s *Service) impactOnResolved(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot, addMissing bool, oldBundle, newBundle *contract.Bundle) (*impact.Result, error) {
	var skipped impact.ConsumerIndex
	created := false
	if opts.Fleet.indexSrc == nil && addMissing {
		opts.Fleet.indexSrc, skipped = s.consumerIndex(opts, newBundle)
		created = opts.Fleet.indexSrc != nil
	}
	domain := opts.domain(snap, newBundle.Contract.Service.Name)
	opts.Domain = domain
	added := addMissing && snap.Services[fleet.NewServiceKeyDomain(domain, newBundle.Contract.Service.Name)] == nil
	if added || created {
		var err error
		if snap, err = s.Fleet(ctx, withNewRevision(opts, newBundle, added)); err != nil {
			return nil, fmt.Errorf("build fleet snapshot: %w", err)
		}
	}
	ix, indexed := opts.Fleet.indexSrc.result(skipped)
	res, err := s.analyzeImpact(ctx, opts, snap, oldBundle, newBundle, ix, indexed)
	if err != nil {
		return nil, err
	}
	res.NewRevisionAdded = added
	return res, nil
}

// withNewRevision is opts.Fleet with the new revision as one more source
// when add is set.
func withNewRevision(opts ImpactOptions, newBundle *contract.Bundle, add bool) FleetOptions {
	withNew := opts.Fleet
	if !add {
		return withNew
	}
	rev := fleet.RawRevision{Bundle: newBundle, Domain: opts.Domain, RequestedRef: opts.NewPath}
	// The content digest the local source gives its bundles, so the added
	// revision has the same identity it would have had from --local.
	if h, err := lock.HashFS(contract.WithoutInferredRecord(newBundle.FS)); err == nil {
		rev.Digest = h
	}
	withNew.extraSources = append(slices.Clone(opts.Fleet.extraSources), fleet.NewMemorySource("new-revision", "argument", &fleet.Collection{
		Revisions: []fleet.RawRevision{rev},
	}))
	return withNew
}

// consumerIndex is the organization index source of a change, pure: none
// for a pair of local folders (nothing to derive it from, nothing printed),
// skipped with a reason for --index none or a --root list, else a source
// reading the candidates of the provider's repository.
func (s *Service) consumerIndex(opts ImpactOptions, newBundle *contract.Bundle) (*indexSource, impact.ConsumerIndex) {
	providerRepo := cmp.Or(ociRepo(opts.OldPath), ociRepo(opts.NewPath))
	skipped := func(reason string) impact.ConsumerIndex {
		return impact.ConsumerIndex{Status: impact.IndexSkipped, Completeness: fleet.CompletenessEmpty, Reason: reason}
	}
	switch {
	case providerRepo == "":
		return nil, impact.ConsumerIndex{}
	case opts.Fleet.Index == IndexNone:
		return nil, skipped("--index none")
	case opts.Fleet.Index == "" && len(opts.Fleet.CatalogRoots) > 0:
		return nil, skipped("--root names the consumers")
	}
	domain := cmp.Or(opts.Domain, revisionDomain(opts.NewPath), revisionDomain(opts.OldPath))
	return &indexSource{
		svc:        s,
		candidates: indexCandidates(opts.Fleet.Index, providerRepo),
		provider:   orgindex.Provider{Repo: providerRepo, Key: fleet.NewServiceKeyDomain(domain, newBundle.Contract.Service.Name)},
	}, impact.ConsumerIndex{}
}

// ociRepo is the repository an oci:// reference names, "" for anything else.
func ociRepo(ref string) string {
	parsed := graph.ParseDependencyRef(ref)
	if !parsed.IsOCI() {
		return ""
	}
	repo, _, _ := contract.SplitOCIRef(parsed.Location)
	return repo
}

// domain is the changed service's fleet domain: Domain when set, else the
// domain of the reference that resolved the new revision. A local new
// revision of a published old one (oci://ghcr.io/acme/ledger:2.3.0
// ./services/ledger) takes the old ref's domain only when a target snapshot
// or the organization index is read (their consumers name the published
// domain) and no source holds the service in the default domain, so local
// consumers (--local, --root) keep resolving it (Decision 14).
func (o ImpactOptions) domain(snap *fleet.FleetSnapshot, name string) string {
	if o.Domain != "" {
		return o.Domain
	}
	if d := revisionDomain(o.NewPath); d != "" {
		return d
	}
	if (len(o.Fleet.TargetSnapshot) > 0 || o.Fleet.indexSrc != nil) && snap.Services[fleet.NewServiceKeyDomain("", name)] == nil {
		return revisionDomain(o.OldPath)
	}
	return ""
}

func (s *Service) resolveImpactPair(ctx context.Context, opts ImpactOptions) (oldBundle, newBundle *contract.Bundle, err error) {
	logging.LoggerFromContext(ctx).Debug("resolving old revision", "path", opts.OldPath)
	if oldBundle, err = s.resolveBundleWithOverrides(ctx, opts.OldPath, opts.OldOverrides); err != nil {
		return nil, nil, fmt.Errorf("old revision: %w", err)
	}
	logging.LoggerFromContext(ctx).Debug("resolving new revision", "path", opts.NewPath)
	if opts.NewDirName != "" && !graph.ParseDependencyRef(opts.NewPath).IsOCI() {
		var cat *externalcatalog.Catalog
		if cat, err = s.completionCatalog(ctx); err == nil {
			newBundle, err = loadLocalBundleNamed(graph.ParseDependencyRef(opts.NewPath).Location, opts.NewDirName, opts.NewOverrides, contract.InferredInputs{}, cat)
			err = readIncomplete(ctx, err)
		}
	} else {
		newBundle, err = s.resolveBundleWithOverrides(ctx, opts.NewPath, opts.NewOverrides)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("new revision: %w", err)
	}
	return oldBundle, newBundle, nil
}

func (s *Service) analyzeImpact(ctx context.Context, opts ImpactOptions, snap *fleet.FleetSnapshot, oldBundle, newBundle *contract.Bundle, ix *impact.ConsumerIndex, indexed func(fleet.ServiceKey) bool) (*impact.Result, error) {
	if err := refuseHidden(snap, opts.OldPath, oldBundle.Contract, opts.NewPath, newBundle.Contract); err != nil {
		return nil, err
	}
	observed, err := observedEdgesFromTraces(opts.Traces)
	if err != nil {
		return nil, fmt.Errorf("parse traces: %w", err)
	}
	res := impact.Analyze(ctx, oldBundle.Contract, newBundle.Contract, oldBundle.FS, newBundle.FS, snap,
		impact.Options{
			// Domain-qualify the changed service from its new reference so impact is
			// isolated across domains (a local path yields the default domain).
			Domain:           opts.Domain,
			IncludeObserved:  opts.IncludeObserved,
			ObservedEdges:    observed,
			NameInferredFrom: nameInferredFrom(oldBundle, newBundle),
			Index:            ix,
			Indexed:          indexed,
		})
	res.TargetSnapshots = targetSnapshotStates(snap)
	return res, nil
}

// nameInferredFrom is where the new, else the old, revision's service.name
// was inferred from; empty when both declare it.
func nameInferredFrom(oldBundle, newBundle *contract.Bundle) string {
	for _, b := range []*contract.Bundle{newBundle, oldBundle} {
		if inf, ok := b.NameInferred(); ok {
			return inf.Source
		}
	}
	return ""
}

// ImpactEventOptions configures a what-if about one fleet service: it
// becomes unavailable, is removed or a revision of it regressed.
type ImpactEventOptions struct {
	Event impact.Event
	Fleet FleetOptions
	// Traces is optional OTLP/JSON trace data whose callers join the answer.
	// An event answer always reads observed callers, so there is no opt-in.
	Traces []byte
	// ReadRegression reads the delivery evidence for a regressed event with
	// the caller's own identity. It is called with the event whose service is
	// already resolved to its fleet key, so a bare name reads the Stages of
	// its domain; nil reads none.
	ReadRegression func(ctx context.Context, ev impact.Event) *impact.Regression
}

// ImpactEvent builds a fleet snapshot from the configured sources and
// answers the event over it (see [Service.ImpactEventOnSnapshot]).
func (s *Service) ImpactEvent(ctx context.Context, opts ImpactEventOptions) (*impact.Result, error) {
	snap, err := s.Fleet(ctx, opts.Fleet)
	if err != nil {
		return nil, fmt.Errorf("build fleet snapshot: %w", err)
	}
	return s.ImpactEventOnSnapshot(ctx, opts, snap)
}

// ImpactEventOnSnapshot answers the event over snap, an already-built
// snapshot (the one on screen, or the caller's view). opts.Fleet is ignored.
// A bare service name resolves to the one service of that name; a name
// several domains share is refused with their keys, a name the snapshot
// does not hold answers SERVICE_NOT_IN_FLEET, and the key of a replaced
// inferred service answers for its successor with SUBJECT_REPLACED.
func (s *Service) ImpactEventOnSnapshot(ctx context.Context, opts ImpactEventOptions, snap *fleet.FleetSnapshot) (*impact.Result, error) {
	observed, err := observedEdgesFromTraces(opts.Traces)
	if err != nil {
		return nil, fmt.Errorf("parse traces: %w", err)
	}
	ev := opts.Event
	if ev.Service != "" { // an offering event names none: impact resolves its providers
		if view, err := fleet.NewQuery(snap).GetService(ev.Service); err == nil && view.ReplacedFrom != nil {
			// The old key of a replaced inferred service: impact follows it
			// itself and adds SUBJECT_REPLACED, never renaming it silently.
			ev.Service = string(view.ReplacedFrom.From)
		} else if err == nil {
			ev.Service = string(view.Service.Key)
		} else if amb := (*fleet.AmbiguousError)(nil); errors.As(err, &amb) {
			return nil, err
		}
	}
	var reg *impact.Regression
	if ev.Kind == impact.EventRegressed && opts.ReadRegression != nil {
		reg = opts.ReadRegression(ctx, ev)
	}
	res := impact.Analyze(ctx, nil, nil, nil, nil, snap, impact.Options{
		Event: &ev, ObservedEdges: observed, Regression: reg,
	})
	res.TargetSnapshots = targetSnapshotStates(snap)
	return res, nil
}

// ParseEventFlag reads the value of an event flag of `pacto impact`: a fleet
// service key, and for regressed the revision after the last "@". For
// unavailable and removed, offering:<name> names every provider of an offering.
func ParseEventFlag(kind, value string) (impact.Event, error) {
	ev := impact.Event{Kind: kind, Service: value}
	name, offering := strings.CutPrefix(value, offeringPrefix)
	switch kind {
	case impact.EventRegressed:
		if offering {
			return impact.Event{}, errors.New("--regressed names one service: a regression belongs to one revision")
		}
		i := strings.LastIndex(value, "@")
		if i <= 0 || i == len(value)-1 {
			return impact.Event{}, fmt.Errorf("--regressed takes <service>@<revision>, got %q", value)
		}
		ev.Service, ev.Revision = value[:i], value[i+1:]
	case impact.EventUnavailable, impact.EventRemoved:
		if offering {
			if name == "" || strings.Contains(name, "@") {
				return impact.Event{}, fmt.Errorf("--%s offering: takes an offering name, got %q", kind, value)
			}
			return impact.Event{Kind: kind, Offering: name}, nil
		}
		if value == "" || strings.Contains(value, "@") {
			return impact.Event{}, fmt.Errorf("--%s takes a service key, got %q", kind, value)
		}
	default:
		return impact.Event{}, fmt.Errorf("unknown event %q: use unavailable, removed or regressed", kind)
	}
	return ev, nil
}

// offeringPrefix marks an event flag value that names an offering.
const offeringPrefix = "offering:"

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
		edge := impact.ObservedEdge{Consumer: e.From, Provider: e.To, Count: e.Count}
		if !e.LastSeen.IsZero() {
			edge.LastSeen = &e.LastSeen
		}
		out = append(out, edge)
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
