package fleet

import (
	"fmt"
	"sort"
	"time"
)

// Neighborhood bounds. A neighborhood is always bounded so the graph never opens
// as an unusable whole-fleet hairball (requirement 5). A zero value takes the
// default; a negative value is rejected; a value above the maximum is capped.
const (
	DefaultNeighborhoodDepth = 1
	MaxNeighborhoodDepth     = 6
	DefaultMaxNodes          = 60
	MaxNeighborhoodNodes     = 500
	DefaultMaxEdges          = 120
	MaxNeighborhoodEdges     = 1000
	// MaxEdgeDeclaredClaims caps the per-edge declared-claim preview, so a
	// dependency declared by thousands of historical revisions still yields a
	// bounded edge.
	MaxEdgeDeclaredClaims = 100
	// MaxEdgeObservationSources caps the per-edge observation-source preview, so a
	// dependency observed by thousands of sources still yields a bounded edge.
	MaxEdgeObservationSources = 100
	// MaxUnresolvedDependencies caps the neighborhood's unresolved-dependency
	// preview.
	MaxUnresolvedDependencies = 200
)

// Difference is the backend's explicit declared-vs-observed verdict for an edge.
// The frontend must render it verbatim and never infer a verdict (e.g.
// observed-not-expected) from the Expected/Observed booleans.
// Edge relations (see NeighborhoodEdge.Relation).
const (
	RelationDependency = "dependency"
	RelationRuns       = "runs"
)

const (
	// DifferenceMatched: declared AND corroborated by observation.
	DifferenceMatched = "matched"
	// DifferenceExpectedNotObserved: declared, observation data exists, but this
	// edge was not witnessed (dormant, or the window was too short).
	DifferenceExpectedNotObserved = "expected-not-observed"
	// DifferenceObservedNotExpected: observed at runtime but never declared.
	DifferenceObservedNotExpected = "observed-not-expected"
	// DifferenceInsufficient: declared, but there is no observation data at all, so
	// the edge cannot be reconciled.
	DifferenceInsufficient = "insufficient"
)

// KnowledgeView is a product-facing relationship lens (requirement 6). It maps to
// engine provenance/reconciliation but never leaks those internal terms.
type KnowledgeView string

const (
	// ViewExpected: contract-declared relationships.
	ViewExpected KnowledgeView = "expected"
	// ViewObserved: relationships backed by observation evidence.
	ViewObserved KnowledgeView = "observed"
	// ViewDifferences: the explicit comparison between expected and observed.
	ViewDifferences KnowledgeView = "differences"
)

func validView(v KnowledgeView) bool {
	switch v {
	case ViewExpected, ViewObserved, ViewDifferences:
		return true
	default:
		return false
	}
}

// DirectionBoth traverses incoming and outgoing relationships together — the
// product default, so a focused neighborhood shows the full local situation.
const DirectionBoth Direction = "both"

// Perspective selects which KIND of node the graph projects (requirement, Phase-4
// prerequisite J). The three identities are never flattened, so each perspective is a
// real projection with its own semantics, not a recoloring of service nodes:
//   - service: logical-service nodes (the default; the original neighborhood).
//   - revision: immutable ContractRevision nodes. A revision-scoped dependency points
//     to a specific provider REVISION only when the snapshot resolved one (a lock
//     whose digest matches a known revision); otherwise it points to the logical
//     provider SERVICE (a mixed edge), never a fabricated "provider@latest".
//   - target: concrete operational-target nodes. A target links to the revision it
//     runs and to the SERVICES that revision depends on (mixed edges); it never gets a
//     fabricated target-to-target edge, because the evidence only establishes
//     service-to-service dependency, not which concrete provider target served it.
type Perspective string

const (
	PerspectiveService  Perspective = "service"
	PerspectiveRevision Perspective = "revision"
	PerspectiveTarget   Perspective = "target"
)

// resolvePerspective defaults an empty perspective to service and rejects an unknown.
func resolvePerspective(p Perspective) (Perspective, error) {
	switch p {
	case "", PerspectiveService:
		return PerspectiveService, nil
	case PerspectiveRevision, PerspectiveTarget:
		return p, nil
	default:
		return "", &InvalidQueryError{Field: "perspective", Value: string(p), Reason: "must be service, revision or target"}
	}
}

// NeighborhoodQuery configures a bounded neighborhood (requirement 2.3).
type NeighborhoodQuery struct {
	Kind        EntityKind
	Key         string
	Perspective Perspective // defaults to service
	Direction   Direction   // defaults to both
	Depth       int         // defaults to 1
	Views       []KnowledgeView
	MaxNodes    int
	MaxEdges    int
}

// boundedParams holds the validated, defaulted-and-capped neighborhood bounds shared
// by every projection.
type boundedParams struct {
	dir      Direction
	views    []KnowledgeView
	depth    int
	maxNodes int
	maxEdges int
}

// NeighborhoodNode is one node in the bounded neighborhood. Ref carries identity,
// label and route; Focus marks the queried entity.
type NeighborhoodNode struct {
	Ref           EntityRef   `json:"ref"`
	Depth         int         `json:"depth"`
	Focus         bool        `json:"focus,omitempty"`
	Status        string      `json:"status,omitempty"`
	Owner         string      `json:"owner,omitempty"`
	RevisionState string      `json:"revisionState,omitempty"`
	Expansions    []Direction `json:"expansions,omitempty"`
}

// DeclaredClaim is one revision's declaration of a dependency edge. Multiple
// revisions of the same service can declare the same edge with different values,
// so the claims are preserved per source revision and never collapsed into one
// last-writer value.
type DeclaredClaim struct {
	SourceRevision RevisionKey `json:"sourceRevision,omitempty"`
	Required       bool        `json:"required,omitempty"`
	Compatibility  string      `json:"compatibility,omitempty"`
	Reconciliation string      `json:"reconciliation,omitempty"`
	RequestedRef   string      `json:"requestedRef,omitempty"`
	LockedVersion  string      `json:"lockedVersion,omitempty"`
	LockedDigest   string      `json:"lockedDigest,omitempty"`
}

// NeighborhoodEdge is a merged declared+observed edge between two in-scope
// services. Expected/Observed record which knowledge backs the edge; Difference
// is the backend's explicit declared-vs-observed verdict (matched,
// expected-not-observed, observed-not-expected, insufficient) that the frontend
// renders verbatim; DeclaredClaims and ObservationSources are bounded previews so
// an edge stays bounded no matter how many revisions declare it or how many
// sources observe it. The edge carries no route: the transport adds an href.
type NeighborhoodEdge struct {
	ID   string    `json:"id"`
	From EntityRef `json:"from"`
	To   EntityRef `json:"to"`
	// Relation names what the edge MEANS, so a mixed-kind projection is unambiguous:
	// "dependency" (the source depends on the target) or "runs" (a target runs the
	// revision it points to). A dependency edge carries a difference verdict; a runs
	// edge does not (it is an observed link, not a declared-vs-observed dependency).
	Relation           string                    `json:"relation" enum:"dependency,runs"`
	Expected           bool                      `json:"expected"`
	Observed           bool                      `json:"observed"`
	Provenance         string                    `json:"provenance" enum:"declared,observed"`
	Difference         string                    `json:"difference,omitempty" enum:"matched,expected-not-observed,observed-not-expected,insufficient"`
	DeclaredClaims     DeclaredClaimsPreview     `json:"declaredClaims"`
	ObservationSources ObservationSourcesPreview `json:"observationSources"`
	Count              int                       `json:"count,omitempty"`
	FirstSeen          *time.Time                `json:"firstSeen,omitempty"`
	LastSeen           *time.Time                `json:"lastSeen,omitempty"`
	Stale              bool                      `json:"stale,omitempty"`
}

// DeclaredClaimsPreview is a bounded preview of an edge's per-revision declared
// claims, so a pathological service with thousands of revisions declaring the
// same dependency still produces a bounded edge.
type DeclaredClaimsPreview struct {
	Total     int             `json:"total"`
	Count     int             `json:"count"`
	Truncated bool            `json:"truncated"`
	Items     []DeclaredClaim `json:"items,omitempty"`
}

// ObservationSourcesPreview is a bounded preview of the per-source observation
// stats backing an observed edge.
type ObservationSourcesPreview struct {
	Total     int                  `json:"total"`
	Count     int                  `json:"count"`
	Truncated bool                 `json:"truncated"`
	Items     []ObservedSourceStat `json:"items,omitempty"`
}

// UnresolvedDependency is a declared dependency whose provider is not resolvable
// in the snapshot (no ToService). It is surfaced rather than dropped, so a
// consumer sees the intent even though there is no target node to draw.
type UnresolvedDependency struct {
	From           EntityRef   `json:"from"`
	Ref            string      `json:"ref"`
	SourceRevision RevisionKey `json:"sourceRevision,omitempty"`
	RequestedRef   string      `json:"requestedRef,omitempty"`
	Reason         string      `json:"reason,omitempty"`
}

// UnresolvedDependenciesPreview is a bounded preview of a neighborhood's
// unresolved declared dependencies.
type UnresolvedDependenciesPreview struct {
	Total     int                    `json:"total"`
	Count     int                    `json:"count"`
	Truncated bool                   `json:"truncated"`
	Items     []UnresolvedDependency `json:"items,omitempty"`
}

// Neighborhood is the bounded, graph-ready neighborhood answer. Perspective names
// the projection kind (service / revision / target); nodes and edges carry their own
// kind through their EntityRef, so a revision or target projection is a genuine
// mixed-kind graph, not recolored service nodes. RequestedFocus is the entity the
// user selected; FocusService is the logical service the focus belongs to (a stable
// anchor for every perspective). Limitations records honest gaps in a projection
// (e.g. observation is service-scoped, so it is not attributed to a specific revision
// edge in the revision perspective).
type Neighborhood struct {
	Meta                   ProductMeta                   `json:"meta"`
	Perspective            Perspective                   `json:"perspective" enum:"service,revision,target"`
	RequestedFocus         EntityRef                     `json:"requestedFocus"`
	FocusService           EntityRef                     `json:"focusService"`
	Direction              Direction                     `json:"direction" enum:"dependencies,dependents,both"`
	Depth                  int                           `json:"depth"`
	Views                  []KnowledgeView               `json:"views" enum:"expected,observed,differences"`
	Nodes                  []NeighborhoodNode            `json:"nodes"`
	Edges                  []NeighborhoodEdge            `json:"edges"`
	UnresolvedDependencies UnresolvedDependenciesPreview `json:"unresolvedDependencies"`
	Limitations            LimitationsPreview            `json:"limitations"`
	Truncated              bool                          `json:"truncated"`
	MaxNodes               int                           `json:"maxNodes"`
	MaxEdges               int                           `json:"maxEdges"`
}

// Neighborhood returns the bounded local SERVICE neighborhood of the focus entity
// across the requested knowledge views. It is authoritative about graph
// semantics: the requested views drive BOTH traversal and returned edges, so an
// expected-only query never reaches a node through an observed edge (and vice
// versa); the difference verdict is a backend fact; and unresolved declared
// dependencies are surfaced rather than dropped.
func (q *Query) Neighborhood(nq NeighborhoodQuery) (*Neighborhood, error) {
	perspective, err := resolvePerspective(nq.Perspective)
	if err != nil {
		return nil, err
	}
	bp, err := boundNeighborhoodParams(nq)
	if err != nil {
		return nil, err
	}
	switch perspective {
	case PerspectiveRevision:
		return q.revisionNeighborhood(nq.Kind, nq.Key, bp)
	case PerspectiveTarget:
		return q.targetNeighborhood(nq.Kind, nq.Key, bp)
	default:
		return q.serviceNeighborhood(nq.Kind, nq.Key, bp)
	}
}

// boundNeighborhoodParams validates and defaults the bounds shared by every
// projection (direction, views, depth, node/edge caps).
func boundNeighborhoodParams(nq NeighborhoodQuery) (boundedParams, error) {
	dir, err := validateNeighborhoodDirection(nq.Direction)
	if err != nil {
		return boundedParams{}, err
	}
	views, err := resolveViews(nq.Views)
	if err != nil {
		return boundedParams{}, err
	}
	depth, err := boundNeighborhoodParam("depth", nq.Depth, DefaultNeighborhoodDepth, MaxNeighborhoodDepth)
	if err != nil {
		return boundedParams{}, err
	}
	maxNodes, err := boundNeighborhoodParam("maxNodes", nq.MaxNodes, DefaultMaxNodes, MaxNeighborhoodNodes)
	if err != nil {
		return boundedParams{}, err
	}
	maxEdges, err := boundNeighborhoodParam("maxEdges", nq.MaxEdges, DefaultMaxEdges, MaxNeighborhoodEdges)
	if err != nil {
		return boundedParams{}, err
	}
	return boundedParams{dir: dir, views: views, depth: depth, maxNodes: maxNodes, maxEdges: maxEdges}, nil
}

// serviceNeighborhood is the logical-service projection (Perspective service): every
// node is a service and every edge is a service-to-service declared/observed edge.
func (q *Query) serviceNeighborhood(kind EntityKind, key string, bp boundedParams) (*Neighborhood, error) {
	root, requested, focusService, revState, err := q.resolveNeighborhoodFocus(kind, key)
	if err != nil {
		return nil, err
	}
	// The requested views decide which knowledge kinds the walk may follow, so a
	// node reachable only through an excluded knowledge kind is never in scope.
	wantDeclared, wantObserved := knowledgeFromViews(bp.views)
	nodeDepth, truncatedNodes := q.walkNeighborhood(root, bp.dir, bp.depth, bp.maxNodes, wantDeclared, wantObserved)
	edges, truncatedEdges := q.neighborhoodEdges(nodeDepth, bp.views, bp.maxEdges)
	res := &Neighborhood{
		Meta: q.productMeta(), Perspective: PerspectiveService, RequestedFocus: requested, FocusService: focusService,
		Direction: bp.dir, Depth: bp.depth, Views: bp.views, MaxNodes: bp.maxNodes, MaxEdges: bp.maxEdges,
		Nodes: q.neighborhoodNodes(root, nodeDepth, revState, wantDeclared, wantObserved), Edges: edges,
		UnresolvedDependencies: q.unresolvedNeighborhoodDeps(nodeDepth, wantDeclared),
		Limitations:            limitationsPreview(nil),
		Truncated:              truncatedNodes || truncatedEdges,
	}
	return res, nil
}

// boundNeighborhoodParam rejects a negative value, defaults a zero, and caps an
// excessive value at max.
func boundNeighborhoodParam(field string, v, def, max int) (int, error) {
	if v < 0 {
		return 0, &InvalidQueryError{Field: field, Value: fmt.Sprint(v), Reason: "must be >= 0"}
	}
	if v == 0 {
		return def, nil
	}
	if v > max {
		return max, nil
	}
	return v, nil
}

// knowledgeFromViews maps the requested product views onto the two engine
// knowledge kinds the traversal may follow.
func knowledgeFromViews(views []KnowledgeView) (declared, observed bool) {
	for _, v := range views {
		switch v {
		case ViewExpected:
			declared = true
		case ViewObserved:
			observed = true
		case ViewDifferences:
			declared, observed = true, true
		}
	}
	return declared, observed
}

// resolveNeighborhoodFocus resolves a focus entity to its logical service root.
// It returns the root service key, the REQUESTED focus reference (the entity the
// user selected, honestly a service/revision/target), the FOCUS SERVICE reference
// (the logical service node used as the neighborhood root), and the revision-link
// state to surface on the focus node (a target carries its exact/inferred match;
// a revision is exact content; a service has none). Only service, revision and
// target focus onto the service graph; owner and source are not graph nodes.
func (q *Query) resolveNeighborhoodFocus(kind EntityKind, key string) (root ServiceKey, requested, focusService EntityRef, revState string, err error) {
	switch kind {
	case KindService:
		s, e := q.resolveService(key)
		if e != nil {
			return "", EntityRef{}, EntityRef{}, "", e
		}
		ref := serviceEntityRef(s)
		return s.Key, ref, ref, "", nil
	case KindRevision:
		rev := q.snap.Revisions[RevisionKey(key)]
		if rev == nil {
			return "", EntityRef{}, EntityRef{}, "", &NotFoundError{Kind: "revision", ID: key}
		}
		return rev.ServiceKey, revisionEntityRef(rev), q.serviceRef(rev.ServiceKey), revisionMatchExact, nil
	case KindTarget:
		tv, e := q.GetTarget(key)
		if e != nil {
			return "", EntityRef{}, EntityRef{}, "", e
		}
		return tv.Target.ServiceKey, targetEntityRef(tv.Target), q.serviceRef(tv.Target.ServiceKey), tv.Target.RevisionMatch, nil
	default:
		return "", EntityRef{}, EntityRef{}, "", &InvalidQueryError{Field: "kind", Value: string(kind), Reason: "neighborhood focus must be a service, revision or target"}
	}
}

func validateNeighborhoodDirection(d Direction) (Direction, error) {
	switch d {
	case "", DirectionBoth:
		return DirectionBoth, nil
	case DirectionDependencies, DirectionDependents:
		return d, nil
	default:
		return "", &InvalidQueryError{Field: "direction", Value: string(d), Reason: "must be dependencies, dependents or both"}
	}
}

// resolveViews validates the requested views, defaulting to the expected view.
func resolveViews(views []KnowledgeView) ([]KnowledgeView, error) {
	if len(views) == 0 {
		return []KnowledgeView{ViewExpected}, nil
	}
	for _, v := range views {
		if !validView(v) {
			return nil, &InvalidQueryError{Field: "view", Value: string(v), Reason: "must be expected, observed or differences"}
		}
	}
	return views, nil
}

// adjacent returns a node's neighbor service keys in the given direction. Only
// the requested knowledge kinds are followed: declared adjacency is included when
// wantDeclared, observed adjacency when wantObserved. This is what makes an
// expected-only walk never reach a node that is only an observed neighbor.
func (q *Query) adjacent(key ServiceKey, dir Direction, wantDeclared, wantObserved bool) []ServiceKey {
	seen := map[ServiceKey]bool{}
	var out []ServiceKey
	add := func(ks []ServiceKey) {
		for _, k := range ks {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	if dir == DirectionDependencies || dir == DirectionBoth {
		if wantDeclared {
			add(q.snap.forwardDeps[key])
		}
		if wantObserved {
			add(q.snap.observedForward[key])
		}
	}
	if dir == DirectionDependents || dir == DirectionBoth {
		if wantDeclared {
			add(q.snap.reverseDeps[key])
		}
		if wantObserved {
			add(q.snap.observedReverse[key])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// walkNeighborhood does a bounded breadth-first walk from root over the requested
// knowledge kinds, returning the depth at which each reached node was first seen
// and whether the node cap was hit.
func (q *Query) walkNeighborhood(root ServiceKey, dir Direction, maxDepth, maxNodes int, wantDeclared, wantObserved bool) (map[ServiceKey]int, bool) {
	depthOf := map[ServiceKey]int{root: 0}
	frontier := []ServiceKey{root}
	truncated := false
	for d := 0; d < maxDepth && len(frontier) > 0; d++ {
		var next []ServiceKey
		for _, node := range frontier {
			for _, nb := range q.adjacent(node, dir, wantDeclared, wantObserved) {
				if _, ok := depthOf[nb]; ok {
					continue
				}
				if len(depthOf) >= maxNodes {
					truncated = true
					continue
				}
				depthOf[nb] = d + 1
				next = append(next, nb)
			}
		}
		frontier = next
	}
	return depthOf, truncated
}

// neighborhoodNodes builds the node list, deterministically ordered by depth then
// key, with the focus node marked and expansion affordances computed from the
// SAME requested knowledge views as the traversal (so an expansion is never
// advertised toward a neighbor the selected views cannot reach).
func (q *Query) neighborhoodNodes(root ServiceKey, depthOf map[ServiceKey]int, rootRevState string, wantDeclared, wantObserved bool) []NeighborhoodNode {
	keys := make([]ServiceKey, 0, len(depthOf))
	for k := range depthOf {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if depthOf[keys[i]] != depthOf[keys[j]] {
			return depthOf[keys[i]] < depthOf[keys[j]]
		}
		return keys[i] < keys[j]
	})
	nodes := make([]NeighborhoodNode, 0, len(keys))
	for _, k := range keys {
		// Every in-scope key comes from the declared/observed adjacency indexes,
		// which only hold resolved keys, so the service always exists.
		s := q.snap.Services[k]
		ref := serviceEntityRef(s)
		n := NeighborhoodNode{Ref: ref, Depth: depthOf[k], Focus: k == root, Status: ref.Status, Owner: s.Owner.DisplayString(), Expansions: q.expansions(k, wantDeclared, wantObserved)}
		if k == root {
			n.RevisionState = rootRevState
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// expansions reports which directions have at least one neighbor reachable
// through the REQUESTED knowledge views, so the "there is more this way"
// affordance never leaks a neighbor the selected views exclude. An expected-only
// query never advertises an expansion that exists solely because of observed
// knowledge (and vice versa); a differences query (both flags) advertises either.
func (q *Query) expansions(key ServiceKey, wantDeclared, wantObserved bool) []Direction {
	var out []Direction
	if len(q.adjacent(key, DirectionDependencies, wantDeclared, wantObserved)) > 0 {
		out = append(out, DirectionDependencies)
	}
	if len(q.adjacent(key, DirectionDependents, wantDeclared, wantObserved)) > 0 {
		out = append(out, DirectionDependents)
	}
	return out
}

// neighborhoodEdges builds merged declared+observed edges among in-scope nodes,
// including only edges that match a requested view, bounded by maxEdges.
func (q *Query) neighborhoodEdges(depthOf map[ServiceKey]int, views []KnowledgeView, maxEdges int) ([]NeighborhoodEdge, bool) {
	inScope := func(k ServiceKey) bool { _, ok := depthOf[k]; return ok }
	merged := map[[2]ServiceKey]*NeighborhoodEdge{}
	var order [][2]ServiceKey
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.ToService == "" || !inScope(rel.FromService) || !inScope(rel.ToService) {
			continue
		}
		if rel.Type != RelationshipDependency {
			continue
		}
		pair := [2]ServiceKey{rel.FromService, rel.ToService}
		e := merged[pair]
		if e == nil {
			e = q.newEdge(rel.FromService, rel.ToService)
			merged[pair] = e
			order = append(order, pair)
		}
		q.foldRelationshipIntoEdge(e, rel)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i][0] != order[j][0] {
			return order[i][0] < order[j][0]
		}
		return order[i][1] < order[j][1]
	})
	out := make([]NeighborhoodEdge, 0, len(order))
	truncated := false
	for _, pair := range order {
		e := merged[pair]
		if !edgeMatchesViews(*e, views) {
			continue
		}
		if len(out) >= maxEdges {
			truncated = true
			break
		}
		finalizeEdge(e)
		out = append(out, *e)
	}
	return out, truncated
}

// finalizeEdge computes an edge's declared-vs-observed difference verdict over its
// FULL declared claims, then bounds its per-edge nested collections (declared
// claims and observation sources) so the emitted edge is always bounded. It is
// the single finalize step every emitted edge passes through (neighborhood and
// revision detail alike).
func finalizeEdge(e *NeighborhoodEdge) {
	e.Difference = edgeDifference(*e)
	e.DeclaredClaims = declaredClaimsPreview(e.DeclaredClaims.Items)
	e.ObservationSources = observationSourcesPreview(e.ObservationSources.Items)
}

// declaredClaimsPreview bounds an edge's declared claims.
func declaredClaimsPreview(cs []DeclaredClaim) DeclaredClaimsPreview {
	it, total, trunc := boundSlice(cs, MaxEdgeDeclaredClaims)
	return DeclaredClaimsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// observationSourcesPreview bounds an edge's observation sources.
func observationSourcesPreview(ss []ObservedSourceStat) ObservationSourcesPreview {
	it, total, trunc := boundSlice(ss, MaxEdgeObservationSources)
	return ObservationSourcesPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// edgeDifference is the backend's explicit declared-vs-observed verdict for a
// merged edge. It is a principled aggregate over the edge's declared claims and
// observed evidence, not a last-writer collapse: observed-not-expected when only
// observed; matched when declared and observed; and, when declared but not
// observed, insufficient (no observation data at all, every claim insufficient)
// or expected-not-observed (observation data exists but did not witness it).
func edgeDifference(e NeighborhoodEdge) string {
	// Every merged edge has at least one of Expected/Observed, so !Expected implies
	// observed-only.
	if !e.Expected {
		return DifferenceObservedNotExpected
	}
	if e.Observed {
		return DifferenceMatched
	}
	// Declared but not observed: expected-not-observed if any claim had observation
	// data to reconcile against, otherwise insufficient (no observation at all).
	for _, c := range e.DeclaredClaims.Items {
		if c.Reconciliation != ReconciliationInsufficient {
			return DifferenceExpectedNotObserved
		}
	}
	return DifferenceInsufficient
}

// unresolvedNeighborhoodDeps surfaces the declared dependencies of in-scope
// services whose provider is not resolvable in the snapshot (empty ToService).
// They are reported only when declared knowledge is in view; they carry no target
// node, so they would otherwise vanish. Deterministically ordered by from key
// then requested ref, and bounded so a high-fanout service cannot produce an
// unbounded list.
func (q *Query) unresolvedNeighborhoodDeps(depthOf map[ServiceKey]int, wantDeclared bool) UnresolvedDependenciesPreview {
	if !wantDeclared {
		return UnresolvedDependenciesPreview{}
	}
	var out []UnresolvedDependency
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.Provenance != ProvenanceDeclared || rel.ToService != "" {
			continue
		}
		if _, ok := depthOf[rel.FromService]; !ok {
			continue
		}
		out = append(out, UnresolvedDependency{
			From:           q.serviceRef(rel.FromService),
			Ref:            rel.To,
			SourceRevision: rel.FromRevision,
			RequestedRef:   rel.RequestedRef,
			Reason:         rel.Reason,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From.Key != out[j].From.Key {
			return out[i].From.Key < out[j].From.Key
		}
		return out[i].Ref < out[j].Ref
	})
	it, total, trunc := boundSlice(out, MaxUnresolvedDependencies)
	return UnresolvedDependenciesPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// newEdge starts a merged edge between two in-scope services with both endpoint
// references and a stable id. The edge is route-neutral; the transport adds an
// href.
func (q *Query) newEdge(from, to ServiceKey) *NeighborhoodEdge {
	return &NeighborhoodEdge{
		ID:       string(from) + "|" + string(to),
		Relation: RelationDependency,
		From:     q.serviceRef(from),
		To:       q.serviceRef(to),
	}
}

// serviceRef returns the reference for a service that is known to be in the
// snapshot (every caller passes a resolved adjacency key).
func (q *Query) serviceRef(key ServiceKey) EntityRef {
	return serviceEntityRef(q.snap.Services[key])
}

// foldRelationshipIntoEdge merges one relationship's facts into a merged edge,
// keeping declared and observed knowledge distinct. A declared relationship
// contributes one DeclaredClaim (its own revision's declaration) rather than
// overwriting a shared last-writer value, so multiple revisions declaring the
// same edge are all preserved.
func (q *Query) foldRelationshipIntoEdge(e *NeighborhoodEdge, rel Relationship) {
	switch rel.Provenance {
	case ProvenanceDeclared:
		e.Expected = true
		e.DeclaredClaims.Items = append(e.DeclaredClaims.Items, DeclaredClaim{
			SourceRevision: rel.FromRevision,
			Required:       rel.Required,
			Compatibility:  rel.Compatibility,
			Reconciliation: rel.Reconciliation,
			RequestedRef:   rel.RequestedRef,
			LockedVersion:  rel.LockedVersion,
			LockedDigest:   rel.LockedDigest,
		})
	case ProvenanceObserved:
		e.Observed = true
		e.Count += rel.ObservedCount
		// Deep-copy the observed source stats so the returned edge never aliases the
		// snapshot's per-source time pointers.
		e.ObservationSources.Items = append(e.ObservationSources.Items, cloneObservedStats(rel.ObservedSources)...)
		e.FirstSeen = earlier(e.FirstSeen, rel.FirstSeen)
		e.LastSeen = later(e.LastSeen, rel.LastSeen)
		e.Stale = e.Stale || q.observedStale(rel.LastSeen)
	}
	e.Provenance = edgeProvenance(*e)
}

// cloneObservedStats deep-copies observed source stats, including their FirstSeen
// and LastSeen pointer targets, so a returned edge is independent of the snapshot.
// A nil or empty input yields an empty slice, which append treats as a no-op.
func cloneObservedStats(ss []ObservedSourceStat) []ObservedSourceStat {
	out := make([]ObservedSourceStat, len(ss))
	for i, s := range ss {
		out[i] = ObservedSourceStat{
			Source: s.Source, Count: s.Count,
			FirstSeen: copyTime(s.FirstSeen), LastSeen: copyTime(s.LastSeen),
		}
	}
	return out
}

// edgeProvenance names the combined provenance of a merged edge.
func edgeProvenance(e NeighborhoodEdge) string {
	switch {
	case e.Expected && e.Observed:
		return ProvenanceDeclared + "+" + ProvenanceObserved
	case e.Observed:
		return ProvenanceObserved
	default:
		return ProvenanceDeclared
	}
}

// observedStale reports whether an observed edge's last-seen time is older than
// the recent window relative to the snapshot's as-of time.
func (q *Query) observedStale(lastSeen *time.Time) bool {
	return lastSeen != nil && lastSeen.Before(q.snap.GeneratedAt.Add(-RecentEvidenceWindow))
}

// edgeMatchesViews reports whether a merged edge should be included for the
// requested views. Expected includes declared edges; observed includes observed
// edges; differences includes any edge (so observed-only shadow edges surface).
func edgeMatchesViews(e NeighborhoodEdge, views []KnowledgeView) bool {
	for _, v := range views {
		switch v {
		case ViewExpected:
			if e.Expected {
				return true
			}
		case ViewObserved:
			if e.Observed {
				return true
			}
		case ViewDifferences:
			if e.Expected || e.Observed {
				return true
			}
		}
	}
	return false
}

func earlier(a, b *time.Time) *time.Time {
	if a == nil {
		return copyTime(b)
	}
	if b == nil || a.Before(*b) {
		return copyTime(a)
	}
	return copyTime(b)
}

func later(a, b *time.Time) *time.Time {
	if a == nil {
		return copyTime(b)
	}
	if b == nil || a.After(*b) {
		return copyTime(a)
	}
	return copyTime(b)
}
