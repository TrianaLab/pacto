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
func (q *Query) revisionNeighborhood(kind EntityKind, key string, bp boundedParams) (*Neighborhood, error) {
	focus, err := q.resolveRevisionFocus(kind, key)
	if err != nil {
		return nil, err
	}
	bySource, byProvider := q.revisionDepIndex()
	b := newProjectionBuilder(bp.maxNodes, bp.maxEdges)
	b.addNode(q.revisionNode(focus, 0, true, bySource, byProvider))

	var unresolved []UnresolvedDependency
	seen := map[RevisionKey]bool{focus.Key: true}
	frontier := []*ContractRevision{focus}
	for d := 0; d < bp.depth && len(frontier) > 0; d++ {
		var next []*ContractRevision
		for _, rev := range frontier {
			if bp.dir == DirectionDependencies || bp.dir == DirectionBoth {
				next = append(next, q.expandRevisionDeps(rev, d, b, bySource, byProvider, seen, &unresolved)...)
			}
			if bp.dir == DirectionDependents || bp.dir == DirectionBoth {
				next = append(next, q.expandRevisionDependents(rev, d, b, bySource, byProvider, seen)...)
			}
		}
		frontier = next
	}
	return &Neighborhood{
		Meta: q.productMeta(), Perspective: PerspectiveRevision,
		RequestedFocus: revisionEntityRef(focus), FocusService: q.serviceRef(focus.ServiceKey),
		Direction: bp.dir, Depth: bp.depth, Views: bp.views, MaxNodes: bp.maxNodes, MaxEdges: bp.maxEdges,
		Nodes: b.sortedNodes(), Edges: b.sortedEdges(),
		UnresolvedDependencies: boundedUnresolved(unresolved),
		Limitations:            projectionLimitations(bp.views, PerspectiveRevision),
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
func (q *Query) expandRevisionDeps(rev *ContractRevision, d int, b *projectionBuilder, bySource, byProvider map[RevisionKey][]Relationship, seen map[RevisionKey]bool, unresolved *[]UnresolvedDependency) []*ContractRevision {
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
			if b.addNode(q.revisionNode(prov, d+1, false, bySource, byProvider)) {
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
func (q *Query) expandRevisionDependents(rev *ContractRevision, d int, b *projectionBuilder, bySource, byProvider map[RevisionKey][]Relationship, seen map[RevisionKey]bool) []*ContractRevision {
	var next []*ContractRevision
	for _, rel := range byProvider[rev.Key] {
		// FromRevision is the revision that declared the dependency, so it always exists.
		consumer := q.snap.Revisions[rel.FromRevision]
		if b.addNode(q.revisionNode(consumer, d+1, false, bySource, byProvider)) {
			b.addEdge(dependencyEdge(revisionEntityRef(consumer), revisionEntityRef(rev), rel))
			if !seen[consumer.Key] {
				seen[consumer.Key] = true
				next = append(next, consumer)
			}
		}
	}
	return next
}

// revisionNode builds a revision graph node with view-agnostic expansion affordances
// derived from whether the revision has any resolved dependency (outgoing) or any
// revision locking it (incoming).
func (q *Query) revisionNode(rev *ContractRevision, depth int, focus bool, bySource, byProvider map[RevisionKey][]Relationship) NeighborhoodNode {
	ref := revisionEntityRef(rev)
	n := NeighborhoodNode{Ref: ref, Depth: depth, Focus: focus, Status: ref.Status, Owner: rev.Owner.DisplayString()}
	if hasResolvedDep(bySource[rev.Key]) {
		n.Expansions = append(n.Expansions, DirectionDependencies)
	}
	if len(byProvider[rev.Key]) > 0 {
		n.Expansions = append(n.Expansions, DirectionDependents)
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

// dependencyEdge builds a declared-dependency edge (revision->revision,
// revision->service or target->service). The single declared claim is preserved, and
// the difference verdict is the backend's own reconciliation for that declared
// dependency (a per-relationship fact), surfaced through finalizeEdge -- never
// inferred by the frontend. Observation is service-scoped, so the edge is marked
// observed only when the backend reconciled the dependency as matched.
func dependencyEdge(from, to EntityRef, rel Relationship) NeighborhoodEdge {
	e := NeighborhoodEdge{
		ID: from.Key + "|" + to.Key, Relation: RelationDependency, From: from, To: to, Expected: true,
	}
	e.DeclaredClaims.Items = []DeclaredClaim{{
		SourceRevision: rel.FromRevision, Required: rel.Required, Compatibility: rel.Compatibility,
		Reconciliation: rel.Reconciliation, RequestedRef: rel.RequestedRef,
		LockedVersion: rel.LockedVersion, LockedDigest: rel.LockedDigest,
	}}
	e.Observed = rel.Reconciliation == ReconciliationMatched
	e.Provenance = edgeProvenance(e)
	finalizeEdge(&e)
	return e
}

// ── Target projection ────────────────────────────────────────────────────────

// targetNeighborhood projects a bounded deployment graph around a focus target: a
// "runs" edge to the revision the target runs, dependency edges to the SERVICES that
// revision requires, and (for the dependents direction) the services that depend on
// the target's service. It never fabricates a target-to-target edge.
func (q *Query) targetNeighborhood(kind EntityKind, key string, bp boundedParams) (*Neighborhood, error) {
	tv, err := q.resolveTargetFocus(kind, key)
	if err != nil {
		return nil, err
	}
	t := tv.Target
	b := newProjectionBuilder(bp.maxNodes, bp.maxEdges)
	tRef := targetEntityRef(t)
	focus := NeighborhoodNode{Ref: tRef, Depth: 0, Focus: true, Status: tRef.Status, Owner: q.serviceOwnerDisplay(t.ServiceKey), RevisionState: t.RevisionMatch}
	b.addNode(focus)

	bySource, _ := q.revisionDepIndex()
	var unresolved []UnresolvedDependency

	// T runs revision A (only when the link is exact or inferred).
	linked := tv.Revision
	linkedKnown := linked != nil && (t.RevisionMatch == revisionMatchExact || t.RevisionMatch == revisionMatchInferred)
	if linkedKnown {
		if b.addNode(q.revisionNode(linked, 1, false, bySource, map[RevisionKey][]Relationship{})) {
			b.addEdge(runsEdge(tRef, revisionEntityRef(linked)))
		}
	}

	if bp.dir == DirectionDependencies || bp.dir == DirectionBoth {
		q.addTargetDependencies(tRef, t, linked, linkedKnown, bySource, b, &unresolved)
	}
	if bp.dir == DirectionDependents || bp.dir == DirectionBoth {
		q.addTargetDependents(tRef, t, b)
	}

	return &Neighborhood{
		Meta: q.productMeta(), Perspective: PerspectiveTarget,
		RequestedFocus: tRef, FocusService: q.serviceRef(t.ServiceKey),
		Direction: bp.dir, Depth: bp.depth, Views: bp.views, MaxNodes: bp.maxNodes, MaxEdges: bp.maxEdges,
		Nodes: b.sortedNodes(), Edges: b.sortedEdges(),
		UnresolvedDependencies: boundedUnresolved(unresolved),
		Limitations:            projectionLimitations(bp.views, PerspectiveTarget),
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

// addTargetDependencies adds T -> service B for each service the target's revision
// (or, when the revision is not linked, its logical service) declares a dependency on.
// The edge is anchored at the target but the dependency itself is the revision's/
// service's declared dependency; an unresolvable dependency is surfaced, not dropped.
func (q *Query) addTargetDependencies(tRef EntityRef, t *TargetRecord, linked *ContractRevision, linkedKnown bool, bySource map[RevisionKey][]Relationship, b *projectionBuilder, unresolved *[]UnresolvedDependency) {
	var rels []Relationship
	if linkedKnown {
		rels = bySource[linked.Key]
	} else {
		rels = q.serviceDeclaredDeps(t.ServiceKey)
	}
	for _, rel := range rels {
		if rel.ToService == "" {
			*unresolved = append(*unresolved, UnresolvedDependency{From: tRef, Ref: rel.To, SourceRevision: rel.FromRevision, RequestedRef: rel.RequestedRef, Reason: rel.Reason})
			continue
		}
		if b.addNode(q.serviceLeafNode(rel.ToService, 1)) {
			b.addEdge(dependencyEdge(tRef, q.serviceRef(rel.ToService), rel))
		}
	}
}

// addTargetDependents adds the SERVICES that depend on the target's service, edge
// service->target. They are honest logical dependents ("this service depends on the
// service this deployment runs"); they are NEVER target-to-target edges, because the
// evidence does not establish which concrete provider target served the traffic.
func (q *Query) addTargetDependents(tRef EntityRef, t *TargetRecord, b *projectionBuilder) {
	for _, rel := range q.serviceReverseDeps(t.ServiceKey) {
		// FromService declared the dependency, so its service record always exists.
		c := q.snap.Services[rel.FromService]
		cRef := serviceEntityRef(c)
		if b.addNode(NeighborhoodNode{Ref: cRef, Depth: 1, Status: cRef.Status, Owner: c.Owner.DisplayString()}) {
			b.addEdge(dependencyEdge(cRef, tRef, rel))
		}
	}
}

// serviceDeclaredDeps aggregates the declared dependency relationships of ALL
// revisions of a service (deduped by provider service), so a target with no exact
// revision link still shows its logical service's dependencies. Deterministic order.
func (q *Query) serviceDeclaredDeps(svc ServiceKey) []Relationship {
	seen := map[ServiceKey]bool{}
	var out []Relationship
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.Provenance != ProvenanceDeclared || rel.FromService != svc {
			continue
		}
		if rel.ToService != "" && seen[rel.ToService] {
			continue
		}
		if rel.ToService != "" {
			seen[rel.ToService] = true
		}
		out = append(out, rel)
	}
	sort.Slice(out, func(i, j int) bool { return relKeyLess(out[i], out[j]) })
	return out
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
	sort.Slice(out, func(i, j int) bool { return relKeyLess(out[i], out[j]) })
	return out
}

// relKeyLess orders relationships deterministically by (from, to) service key.
func relKeyLess(a, b Relationship) bool {
	if a.FromService != b.FromService {
		return a.FromService < b.FromService
	}
	return a.ToService < b.ToService
}

// serviceOwnerDisplay returns the display owner of a service. Every target's service
// is ensured in snap.Services at Build, so the record always exists.
func (q *Query) serviceOwnerDisplay(svc ServiceKey) string {
	return q.snap.Services[svc].Owner.DisplayString()
}

// runsEdge is the observed "target runs revision" link. It is not a declared
// dependency, so it carries no difference verdict; it is an observed fact reported by
// the target.
func runsEdge(from, to EntityRef) NeighborhoodEdge {
	return NeighborhoodEdge{
		ID: from.Key + "|" + to.Key, Relation: RelationRuns, From: from, To: to,
		Observed: true, Provenance: ProvenanceObserved,
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

// projectionLimitations records the honest gaps of a projection when a view that
// needs runtime observation is requested: observation is recorded per service, so it
// is neither revision-scoped nor target-scoped.
// projectionLimitations is only ever called for the revision and target
// perspectives (the service perspective records no such limitation).
func projectionLimitations(views []KnowledgeView, p Perspective) LimitationsPreview {
	if !viewsInclude(views, ViewObserved) && !viewsInclude(views, ViewDifferences) {
		return limitationsPreview(nil)
	}
	if p == PerspectiveTarget {
		return limitationsPreview([]Limitation{{
			Code:    "OBSERVED_NOT_TARGET_SCOPED",
			Message: "Runtime observation establishes service-to-service traffic, not which concrete provider target served it, so no target-to-target dependency edge is drawn.",
		}})
	}
	return limitationsPreview([]Limitation{{
		Code:    "OBSERVED_NOT_REVISION_SCOPED",
		Message: "Runtime observation is recorded per service, not per revision, so observed traffic is not attributed to a specific revision edge; each edge's difference reflects the backend reconciliation of that declared dependency.",
	}})
}

func viewsInclude(views []KnowledgeView, v KnowledgeView) bool {
	for _, x := range views {
		if x == v {
			return true
		}
	}
	return false
}
