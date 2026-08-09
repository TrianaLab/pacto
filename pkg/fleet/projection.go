package fleet

import "sort"

// This file implements the per-kind graph projections (Phase-4 prerequisite J). The
// three identities are never flattened, so a revision graph and a deployment graph
// are REAL projections with their own semantics, not service nodes recolored:
//
//   - Revision projection: nodes are immutable ContractRevisions. A revision's
//     declared dependency points to a specific provider REVISION only when the
//     snapshot resolved one (a lock whose digest matches a known revision, via
//     resolveDepRevision); otherwise it points to the logical provider SERVICE (a
//     mixed revision->service edge). It NEVER fabricates a `provider@latest` edge.
//   - Target projection: a target node links (a "runs" edge) to the revision it runs
//     and depends (dependency edges) on the SERVICES that revision requires. It never
//     draws a target-to-target edge, because the evidence establishes
//     service-to-service dependency, not which concrete provider target served it.
//
// Both projections are bounded (node/edge caps + truncation), deterministically
// ordered, route-neutral (the transport adds hrefs) and record honest limitations
// (observation is service-scoped, not revision- or target-scoped).

// projectionBuilder accumulates the bounded, mixed-kind nodes and edges of a
// projection. Nodes are keyed by their canonical ref key (unique across kinds) and
// edges by their id; both respect the caps and record truncation, and neither is ever
// duplicated.
type projectionBuilder struct {
	nodes     map[string]NeighborhoodNode
	edges     map[string]NeighborhoodEdge
	maxNodes  int
	maxEdges  int
	truncated bool
}

func newProjectionBuilder(maxNodes, maxEdges int) *projectionBuilder {
	return &projectionBuilder{nodes: map[string]NeighborhoodNode{}, edges: map[string]NeighborhoodEdge{}, maxNodes: maxNodes, maxEdges: maxEdges}
}

// addNode adds a node if absent and within the cap. It returns true when the node is
// present afterwards (already there or just added), so a caller can decide whether to
// traverse it; a cap hit marks the projection truncated and returns false.
func (b *projectionBuilder) addNode(n NeighborhoodNode) bool {
	if _, ok := b.nodes[n.Ref.Key]; ok {
		return true
	}
	if len(b.nodes) >= b.maxNodes {
		b.truncated = true
		return false
	}
	b.nodes[n.Ref.Key] = n
	return true
}

// addEdge adds an edge if absent and within the cap.
func (b *projectionBuilder) addEdge(e NeighborhoodEdge) {
	if _, ok := b.edges[e.ID]; ok {
		return
	}
	if len(b.edges) >= b.maxEdges {
		b.truncated = true
		return
	}
	b.edges[e.ID] = e
}

// sortedNodes returns the nodes ordered by depth then key (deterministic).
func (b *projectionBuilder) sortedNodes() []NeighborhoodNode {
	out := make([]NeighborhoodNode, 0, len(b.nodes))
	for _, n := range b.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].Ref.Key < out[j].Ref.Key
	})
	return out
}

// sortedEdges returns the edges ordered by id (deterministic).
func (b *projectionBuilder) sortedEdges() []NeighborhoodEdge {
	out := make([]NeighborhoodEdge, 0, len(b.edges))
	for _, e := range b.edges {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// revisionDepIndex builds, in a single O(relationships) pass, the declared
// dependencies keyed by their SOURCE revision and the declared dependencies keyed by
// the specific provider revision they resolved to (the "who locks this revision"
// index). Only declared dependency relationships are indexed.
func (q *Query) revisionDepIndex() (bySource, byProvider map[RevisionKey][]Relationship) {
	bySource = map[RevisionKey][]Relationship{}
	byProvider = map[RevisionKey][]Relationship{}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.Provenance != ProvenanceDeclared {
			continue
		}
		if rel.FromRevision != "" {
			bySource[rel.FromRevision] = append(bySource[rel.FromRevision], rel)
		}
		if rel.ResolvedRevision != "" {
			byProvider[rel.ResolvedRevision] = append(byProvider[rel.ResolvedRevision], rel)
		}
	}
	return bySource, byProvider
}

// ── Revision projection ──────────────────────────────────────────────────────

// revisionNeighborhood projects a bounded revision graph around a focus revision.
// The requested views drive the projection exactly as the service projection does:
// the revision graph's only knowledge is DECLARED dependencies (a revision->revision
// or revision->service edge), because runtime observation is recorded per SERVICE and
// so has no revision-scoped edge to draw. The declared traversal, the emitted edges
// AND the focus node's expansion affordances are therefore all gated on the declared
// view, so an observed-only revision query returns just the focus and never traverses
// a declared-only relationship.
func (q *Query) revisionNeighborhood(kind EntityKind, key string, bp boundedParams) (*Neighborhood, error) {
	focus, err := q.resolveRevisionFocus(kind, key)
	if err != nil {
		return nil, err
	}
	wantDeclared, _ := knowledgeFromViews(bp.views)
	bySource, byProvider := q.revisionDepIndex()
	b := newProjectionBuilder(bp.maxNodes, bp.maxEdges)
	b.addNode(q.revisionNode(focus, 0, true, wantDeclared, bySource, byProvider))

	var unresolved []UnresolvedDependency
	if wantDeclared {
		seen := map[RevisionKey]bool{focus.Key: true}
		frontier := []*ContractRevision{focus}
		for d := 0; d < bp.depth && len(frontier) > 0; d++ {
			var next []*ContractRevision
			for _, rev := range frontier {
				if bp.dir == DirectionDependencies || bp.dir == DirectionBoth {
					next = append(next, q.expandRevisionDeps(rev, d, b, wantDeclared, bySource, byProvider, seen, &unresolved)...)
				}
				if bp.dir == DirectionDependents || bp.dir == DirectionBoth {
					next = append(next, q.expandRevisionDependents(rev, d, b, wantDeclared, bySource, byProvider, seen)...)
				}
			}
			frontier = next
		}
	}
	return &Neighborhood{
		Meta: q.productMeta(), Perspective: PerspectiveRevision,
		RequestedFocus: revisionEntityRef(focus), FocusService: q.serviceRef(focus.ServiceKey),
		Direction: bp.dir, Depth: bp.depth, EffectiveDepth: bp.depth, Views: bp.views, MaxNodes: bp.maxNodes, MaxEdges: bp.maxEdges,
		Nodes: b.sortedNodes(), Edges: b.sortedEdges(),
		UnresolvedDependencies: boundedUnresolved(unresolved),
		Limitations:            revisionObservedLimitation(bp.views),
		Truncated:              b.truncated,
	}, nil
}

// resolveRevisionFocus maps the requested focus to a revision. A target focus maps to
// its linked revision ONLY when that link is exact or inferred; an ambiguous or
// unresolved target, or a service/owner/source focus, is rejected with a reason (the
// revision perspective needs a specific revision).
func (q *Query) resolveRevisionFocus(kind EntityKind, key string) (*ContractRevision, error) {
	switch kind {
	case KindRevision:
		rev := q.snap.Revisions[RevisionKey(key)]
		if rev == nil {
			return nil, &NotFoundError{Kind: "revision", ID: key}
		}
		return rev, nil
	case KindTarget:
		tv, err := q.GetTarget(key)
		if err != nil {
			return nil, err
		}
		if tv.Revision == nil || (tv.Target.RevisionMatch != revisionMatchExact && tv.Target.RevisionMatch != revisionMatchInferred) {
			return nil, &InvalidQueryError{Field: "key", Value: key, Reason: "target is not linked to a specific revision; use the target perspective"}
		}
		return tv.Revision, nil
	default:
		return nil, &InvalidQueryError{Field: "kind", Value: string(kind), Reason: "revision perspective requires a revision or a target with a linked revision"}
	}
}

// expandRevisionDeps adds a revision's declared-dependency providers: a specific
// provider revision when one was resolved (a lock matching a known revision), else the
// logical provider service (a terminal mixed node), else an unresolved dependency.
// Only resolved provider revisions are traversed further.
func (q *Query) expandRevisionDeps(rev *ContractRevision, d int, b *projectionBuilder, wantDeclared bool, bySource, byProvider map[RevisionKey][]Relationship, seen map[RevisionKey]bool, unresolved *[]UnresolvedDependency) []*ContractRevision {
	var next []*ContractRevision
	for _, rel := range bySource[rev.Key] {
		if rel.ToService == "" {
			*unresolved = append(*unresolved, UnresolvedDependency{From: revisionEntityRef(rev), Ref: rel.To, SourceRevision: rev.Key, RequestedRef: rel.RequestedRef, Reason: rel.Reason})
			continue
		}
		if rel.ResolvedRevision != "" {
			// ResolvedRevision was derived by resolveDepRevision from snap.Revisions, so
			// the provider revision always exists.
			prov := q.snap.Revisions[rel.ResolvedRevision]
			if b.addNode(q.revisionNode(prov, d+1, false, wantDeclared, bySource, byProvider)) {
				b.addEdge(dependencyEdge(revisionEntityRef(rev), revisionEntityRef(prov), rel))
				if !seen[prov.Key] {
					seen[prov.Key] = true
					next = append(next, prov)
				}
			}
			continue
		}
		// No specific provider revision established: point to the logical service,
		// never a fabricated provider revision. The service node is terminal here.
		if b.addNode(q.serviceLeafNode(rel.ToService, d+1)) {
			b.addEdge(dependencyEdge(revisionEntityRef(rev), q.serviceRef(rel.ToService), rel))
		}
	}
	return next
}

// expandRevisionDependents adds the revisions that lock this exact revision as a
// provider (they establish this revision's content), edge consumer->rev. A consumer
// that depends only on this revision's logical service is NOT attributed to this
// revision (that belongs to the service projection).
func (q *Query) expandRevisionDependents(rev *ContractRevision, d int, b *projectionBuilder, wantDeclared bool, bySource, byProvider map[RevisionKey][]Relationship, seen map[RevisionKey]bool) []*ContractRevision {
	var next []*ContractRevision
	for _, rel := range byProvider[rev.Key] {
		// FromRevision is the revision that declared the dependency, so it always exists.
		consumer := q.snap.Revisions[rel.FromRevision]
		if b.addNode(q.revisionNode(consumer, d+1, false, wantDeclared, bySource, byProvider)) {
			b.addEdge(dependencyEdge(revisionEntityRef(consumer), revisionEntityRef(rev), rel))
			if !seen[consumer.Key] {
				seen[consumer.Key] = true
				next = append(next, consumer)
			}
		}
	}
	return next
}

// revisionNode builds a revision graph node whose expansion affordances are computed
// from the SAME knowledge set as the traversal: the revision graph is declared-only,
// so a node advertises a dependency/dependent expansion only when declared knowledge
// is in view (wantDeclared) and there is a resolved dependency (outgoing) or a
// revision locking it (incoming). An observed-only revision query therefore advertises
// no expansion that exists solely through excluded (declared) knowledge.
func (q *Query) revisionNode(rev *ContractRevision, depth int, focus, wantDeclared bool, bySource, byProvider map[RevisionKey][]Relationship) NeighborhoodNode {
	ref := revisionEntityRef(rev)
	n := NeighborhoodNode{Ref: ref, Depth: depth, Focus: focus, Status: ref.Status, Owner: rev.Owner.DisplayString()}
	if wantDeclared {
		if hasResolvedDep(bySource[rev.Key]) {
			n.Expansions = append(n.Expansions, DirectionDependencies)
		}
		if len(byProvider[rev.Key]) > 0 {
			n.Expansions = append(n.Expansions, DirectionDependents)
		}
	}
	return n
}

// hasResolvedDep reports whether any of the declared dependencies resolved to a
// provider (a service, and possibly a specific revision), i.e. there is an outgoing
// edge to draw.
func hasResolvedDep(rels []Relationship) bool {
	for _, r := range rels {
		if r.ToService != "" {
			return true
		}
	}
	return false
}

// serviceLeafNode is a logical-service node appearing as a terminal provider in the
// revision projection (a dependency whose provider revision is not established). It
// carries no expansions: expanding it would switch to service semantics, which the
// user does by opening the service or switching perspective.
func (q *Query) serviceLeafNode(svc ServiceKey, depth int) NeighborhoodNode {
	s := q.snap.Services[svc]
	ref := serviceEntityRef(s)
	return NeighborhoodNode{Ref: ref, Depth: depth, Status: ref.Status, Owner: s.Owner.DisplayString()}
}

// dependencyEdge builds a declared-dependency edge in a FINE-GRAINED (revision- or
// target-anchored) projection: revision->revision, revision->service, target->service
// or the target projection's consumer->service dependent edge. The single declared
// claim is preserved. Runtime observation is recorded per SERVICE (build.go reconciles
// by the from/to SERVICE pair), so this edge is NEVER marked Observed from it: doing so
// would promote service-scoped telemetry into a false revision- or target-scoped claim.
// Instead the service-to-service reconciliation is surfaced as CONTEXT
// (ObservationScope=service + ServiceCorroboration), so the frontend can say whether
// the LOGICAL service relationship was corroborated without ever claiming this specific
// fine-grained edge was observed. There is no edge-scope difference verdict at this
// scope, so Difference stays empty.
func dependencyEdge(from, to EntityRef, rel Relationship) NeighborhoodEdge {
	e := NeighborhoodEdge{
		ID: from.Key + "|" + to.Key, Relation: RelationDependency, From: from, To: to, Expected: true,
		Observed: false, Provenance: ProvenanceDeclared,
		ObservationScope:     ObservationScopeService,
		ServiceCorroboration: serviceCorroboration(rel.Reconciliation),
	}
	e.DeclaredClaims = declaredClaimsPreview([]DeclaredClaim{{
		SourceRevision: rel.FromRevision, Required: rel.Required, Compatibility: rel.Compatibility,
		Reconciliation: rel.Reconciliation, RequestedRef: rel.RequestedRef,
		LockedVersion: rel.LockedVersion, LockedDigest: rel.LockedDigest,
	}})
	return e
}

// ── Target projection ────────────────────────────────────────────────────────

// targetNeighborhood projects a bounded, one-hop deployment graph around a focus
// target. The three identities are never flattened and the target's specificity is
// never overstated:
//   - "runs" edge: the immutable revision this deployment runs, drawn ONLY when the
//     revision link is authoritative (exact or inferred). It is the target's structural
//     identity link, not a declared-vs-observed dependency.
//   - dependency edges: target -> service B for the LINKED revision's declared
//     dependencies, and ONLY when that link is authoritative. An ambiguous or
//     unresolved target draws NO dependency edges (inheriting one arbitrary revision's
//     dependencies would be a false claim); it surfaces a limitation instead.
//   - dependents: services that depend on the target's LOGICAL SERVICE, drawn as
//     consumer -> service edges (never consumer -> concrete target), so the target is
//     never rendered as the specific routing endpoint the evidence cannot attribute.
//
// It never fabricates a target-to-target edge. The projection is intentionally one hop
// (deeper exploration is the revision perspective's job), so EffectiveDepth is 1 and
// the requested views gate the DECLARED edges exactly as the other projections do.
func (q *Query) targetNeighborhood(kind EntityKind, key string, bp boundedParams) (*Neighborhood, error) {
	tv, err := q.resolveTargetFocus(kind, key)
	if err != nil {
		return nil, err
	}
	t := tv.Target
	wantDeclared, _ := knowledgeFromViews(bp.views)
	b := newProjectionBuilder(bp.maxNodes, bp.maxEdges)
	tRef := targetEntityRef(t)
	focus := NeighborhoodNode{Ref: tRef, Depth: 0, Focus: true, Status: tRef.Status, Owner: q.serviceOwnerDisplay(t.ServiceKey), RevisionState: t.RevisionMatch}
	b.addNode(focus)

	bySource, _ := q.revisionDepIndex()
	var unresolved []UnresolvedDependency
	var extraLimits []Limitation

	// T runs revision A: a structural identity link, drawn only when authoritative.
	linked := tv.Revision
	linkedKnown := linked != nil && (t.RevisionMatch == revisionMatchExact || t.RevisionMatch == revisionMatchInferred)
	if linkedKnown {
		if b.addNode(q.revisionNode(linked, 1, false, false, bySource, map[RevisionKey][]Relationship{})) {
			b.addEdge(runsEdge(tRef, revisionEntityRef(linked)))
		}
	} else {
		// The running revision is ambiguous or unresolved: Pacto cannot attribute a
		// specific revision's declared dependencies to this concrete target, so no
		// dependency edges are drawn from the target. The limitation says why.
		extraLimits = append(extraLimits, Limitation{
			Code:    "TARGET_REVISION_UNRESOLVED",
			Message: "The revision this deployment runs is not authoritatively known, so its declared dependencies are not attributed to this target. Open the logical service to see revision-level dependencies.",
		})
	}

	if wantDeclared && linkedKnown && (bp.dir == DirectionDependencies || bp.dir == DirectionBoth) {
		q.addTargetRevisionDeps(tRef, linked, bySource, b, &unresolved)
	}
	if wantDeclared && (bp.dir == DirectionDependents || bp.dir == DirectionBoth) {
		q.addTargetLogicalDependents(t, b)
	}

	return &Neighborhood{
		Meta: q.productMeta(), Perspective: PerspectiveTarget,
		RequestedFocus: tRef, FocusService: q.serviceRef(t.ServiceKey),
		Direction: bp.dir, Depth: bp.depth, EffectiveDepth: 1, Views: bp.views, MaxNodes: bp.maxNodes, MaxEdges: bp.maxEdges,
		Nodes: b.sortedNodes(), Edges: b.sortedEdges(),
		UnresolvedDependencies: boundedUnresolved(unresolved),
		Limitations:            targetLimitations(bp.views, extraLimits),
		Truncated:              b.truncated,
	}, nil
}

// resolveTargetFocus maps the requested focus to a target view. Only a target focus
// is valid for the target perspective.
func (q *Query) resolveTargetFocus(kind EntityKind, key string) (*TargetView, error) {
	if kind != KindTarget {
		return nil, &InvalidQueryError{Field: "kind", Value: string(kind), Reason: "target perspective requires a target focus"}
	}
	return q.GetTarget(key)
}

// addTargetRevisionDeps adds T -> service B for each service the target's LINKED
// revision declares a dependency on. It is called only for an authoritative
// (exact/inferred) link, so it never inherits an arbitrary revision's dependencies;
// the caller handles an unresolved link with a limitation. An unresolvable dependency
// is surfaced, not dropped. Observation stays service-scoped (dependencyEdge).
func (q *Query) addTargetRevisionDeps(tRef EntityRef, linked *ContractRevision, bySource map[RevisionKey][]Relationship, b *projectionBuilder, unresolved *[]UnresolvedDependency) {
	for _, rel := range bySource[linked.Key] {
		if rel.ToService == "" {
			*unresolved = append(*unresolved, UnresolvedDependency{From: tRef, Ref: rel.To, SourceRevision: rel.FromRevision, RequestedRef: rel.RequestedRef, Reason: rel.Reason})
			continue
		}
		if b.addNode(q.serviceLeafNode(rel.ToService, 1)) {
			b.addEdge(dependencyEdge(tRef, q.serviceRef(rel.ToService), rel))
		}
	}
}

// addTargetLogicalDependents adds the SERVICES that depend on the target's LOGICAL
// service, drawn as consumer -> logical-service edges (never consumer -> concrete
// target): the evidence establishes a dependency on the service, not on this specific
// deployment. The logical-service node is the shared dependents anchor; the target
// stays separately linked to its revision via the runs edge.
func (q *Query) addTargetLogicalDependents(t *TargetRecord, b *projectionBuilder) {
	reverse := q.serviceReverseDeps(t.ServiceKey)
	if len(reverse) == 0 {
		return
	}
	svcRef := q.serviceRef(t.ServiceKey)
	if !b.addNode(NeighborhoodNode{Ref: svcRef, Depth: 1, Status: svcRef.Status, Owner: q.serviceOwnerDisplay(t.ServiceKey)}) {
		return
	}
	for _, rel := range reverse {
		// FromService declared the dependency, so its service record always exists.
		c := q.snap.Services[rel.FromService]
		cRef := serviceEntityRef(c)
		if b.addNode(NeighborhoodNode{Ref: cRef, Depth: 2, Status: cRef.Status, Owner: c.Owner.DisplayString()}) {
			b.addEdge(dependencyEdge(cRef, svcRef, rel))
		}
	}
}

// serviceReverseDeps returns one declared dependency relationship per service that
// depends on svc (deduped by consumer service), deterministically ordered.
func (q *Query) serviceReverseDeps(svc ServiceKey) []Relationship {
	seen := map[ServiceKey]bool{}
	var out []Relationship
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.Provenance != ProvenanceDeclared || rel.ToService != svc {
			continue
		}
		if seen[rel.FromService] {
			continue
		}
		seen[rel.FromService] = true
		out = append(out, rel)
	}
	// Every reverse dependency has the same ToService (svc), so ordering by the
	// consumer's FromService alone is deterministic and complete.
	sort.Slice(out, func(i, j int) bool { return out[i].FromService < out[j].FromService })
	return out
}

// serviceOwnerDisplay returns the display owner of a service. Every target's service
// is ensured in snap.Services at Build, so the record always exists.
func (q *Query) serviceOwnerDisplay(svc ServiceKey) string {
	return q.snap.Services[svc].Owner.DisplayString()
}

// runsEdge is the observed "target runs revision" link. It is not a declared
// dependency, so it carries no difference verdict; it is a genuine TARGET-scoped
// observed fact (which immutable revision this deployment reports running), so its
// ObservationScope is target -- unlike a fine-grained dependency edge, whose only
// corroboration is service-scoped.
func runsEdge(from, to EntityRef) NeighborhoodEdge {
	return NeighborhoodEdge{
		ID: from.Key + "|" + to.Key, Relation: RelationRuns, From: from, To: to,
		Observed: true, Provenance: ProvenanceObserved, ObservationScope: ObservationScopeTarget,
	}
}

// ── shared projection helpers ────────────────────────────────────────────────

// boundedUnresolved orders and bounds a projection's unresolved dependencies.
func boundedUnresolved(u []UnresolvedDependency) UnresolvedDependenciesPreview {
	sort.Slice(u, func(i, j int) bool {
		if u[i].From.Key != u[j].From.Key {
			return u[i].From.Key < u[j].From.Key
		}
		return u[i].Ref < u[j].Ref
	})
	it, total, trunc := boundSlice(u, MaxUnresolvedDependencies)
	return UnresolvedDependenciesPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// revisionObservedLimitation records the honest gap of the revision projection when a
// view that needs runtime observation is requested: observation is recorded per
// SERVICE, so it is never attributed to a specific revision edge. Each fine-grained
// edge instead carries the service-scoped corroboration (ServiceCorroboration).
func revisionObservedLimitation(views []KnowledgeView) LimitationsPreview {
	if !viewsInclude(views, ViewObserved) && !viewsInclude(views, ViewDifferences) {
		return limitationsPreview(nil)
	}
	return limitationsPreview([]Limitation{{
		Code:    "OBSERVED_NOT_REVISION_SCOPED",
		Message: "Runtime observation is recorded per service, not per revision, so observed traffic is not attributed to a specific revision edge; each edge reports the service-scoped corroboration instead.",
	}})
}

// targetLimitations merges the always-on limitations of a target projection (e.g. an
// unresolved revision link) with the view-dependent observation-scope limitation, so
// the honest gaps are stated regardless of which view produced the answer.
func targetLimitations(views []KnowledgeView, extra []Limitation) LimitationsPreview {
	out := append([]Limitation{}, extra...)
	if viewsInclude(views, ViewObserved) || viewsInclude(views, ViewDifferences) {
		out = append(out, Limitation{
			Code:    "OBSERVED_NOT_TARGET_SCOPED",
			Message: "Runtime observation establishes service-to-service traffic, not which concrete provider target served it, so no target-to-target dependency edge is drawn and no edge is attributed to this specific deployment.",
		})
	}
	return limitationsPreview(out)
}

func viewsInclude(views []KnowledgeView, v KnowledgeView) bool {
	for _, x := range views {
		if x == v {
			return true
		}
	}
	return false
}
