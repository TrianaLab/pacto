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
)

// Difference is the backend's explicit declared-vs-observed verdict for an edge.
// The frontend must render it verbatim and never infer a verdict (e.g.
// observed-not-expected) from the Expected/Observed booleans.
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

// NeighborhoodQuery configures a bounded neighborhood (requirement 2.3).
type NeighborhoodQuery struct {
	Kind      EntityKind
	Key       string
	Direction Direction // defaults to both
	Depth     int       // defaults to 1
	Views     []KnowledgeView
	MaxNodes  int
	MaxEdges  int
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
// renders verbatim; DeclaredClaims preserves every revision's declaration.
type NeighborhoodEdge struct {
	ID                 string               `json:"id"`
	From               EntityRef            `json:"from"`
	To                 EntityRef            `json:"to"`
	Expected           bool                 `json:"expected"`
	Observed           bool                 `json:"observed"`
	Provenance         string               `json:"provenance"`
	Difference         string               `json:"difference"`
	DeclaredClaims     []DeclaredClaim      `json:"declaredClaims,omitempty"`
	ObservationSources []ObservedSourceStat `json:"observationSources,omitempty"`
	Count              int                  `json:"count,omitempty"`
	FirstSeen          *time.Time           `json:"firstSeen,omitempty"`
	LastSeen           *time.Time           `json:"lastSeen,omitempty"`
	Stale              bool                 `json:"stale,omitempty"`
	Route              string               `json:"route,omitempty"`
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

// Neighborhood is the bounded, graph-ready neighborhood answer. In this API
// version the graph is honestly a SERVICE neighborhood: RequestedFocus is the
// entity the user selected (a service, revision or target), FocusService is the
// logical service node used as the root, and every node in Nodes is a service
// node. True revision-graph and deployment-graph projections are a later phase.
type Neighborhood struct {
	Meta                   ProductMeta            `json:"meta"`
	RequestedFocus         EntityRef              `json:"requestedFocus"`
	FocusService           EntityRef              `json:"focusService"`
	Direction              Direction              `json:"direction"`
	Depth                  int                    `json:"depth"`
	Views                  []KnowledgeView        `json:"views"`
	Nodes                  []NeighborhoodNode     `json:"nodes"`
	Edges                  []NeighborhoodEdge     `json:"edges"`
	UnresolvedDependencies []UnresolvedDependency `json:"unresolvedDependencies,omitempty"`
	Truncated              bool                   `json:"truncated"`
	MaxNodes               int                    `json:"maxNodes"`
	MaxEdges               int                    `json:"maxEdges"`
}

// Neighborhood returns the bounded local SERVICE neighborhood of the focus entity
// across the requested knowledge views. It is authoritative about graph
// semantics: the requested views drive BOTH traversal and returned edges, so an
// expected-only query never reaches a node through an observed edge (and vice
// versa); the difference verdict is a backend fact; and unresolved declared
// dependencies are surfaced rather than dropped.
func (q *Query) Neighborhood(nq NeighborhoodQuery) (*Neighborhood, error) {
	root, requested, focusService, revState, err := q.resolveNeighborhoodFocus(nq.Kind, nq.Key)
	if err != nil {
		return nil, err
	}
	dir, err := validateNeighborhoodDirection(nq.Direction)
	if err != nil {
		return nil, err
	}
	views, err := resolveViews(nq.Views)
	if err != nil {
		return nil, err
	}
	depth, err := boundNeighborhoodParam("depth", nq.Depth, DefaultNeighborhoodDepth, MaxNeighborhoodDepth)
	if err != nil {
		return nil, err
	}
	maxNodes, err := boundNeighborhoodParam("maxNodes", nq.MaxNodes, DefaultMaxNodes, MaxNeighborhoodNodes)
	if err != nil {
		return nil, err
	}
	maxEdges, err := boundNeighborhoodParam("maxEdges", nq.MaxEdges, DefaultMaxEdges, MaxNeighborhoodEdges)
	if err != nil {
		return nil, err
	}

	// The requested views decide which knowledge kinds the walk may follow, so a
	// node reachable only through an excluded knowledge kind is never in scope.
	wantDeclared, wantObserved := knowledgeFromViews(views)
	nodeDepth, truncatedNodes := q.walkNeighborhood(root, dir, depth, maxNodes, wantDeclared, wantObserved)
	edges, truncatedEdges := q.neighborhoodEdges(nodeDepth, views, maxEdges)
	res := &Neighborhood{
		Meta: q.productMeta(), RequestedFocus: requested, FocusService: focusService,
		Direction: dir, Depth: depth, Views: views, MaxNodes: maxNodes, MaxEdges: maxEdges,
		Nodes: q.neighborhoodNodes(root, nodeDepth, revState), Edges: edges,
		UnresolvedDependencies: q.unresolvedNeighborhoodDeps(nodeDepth, wantDeclared),
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
// key, with the focus node marked and expansion affordances computed.
func (q *Query) neighborhoodNodes(root ServiceKey, depthOf map[ServiceKey]int, rootRevState string) []NeighborhoodNode {
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
		n := NeighborhoodNode{Ref: ref, Depth: depthOf[k], Focus: k == root, Status: ref.Status, Owner: s.Owner.DisplayString(), Expansions: q.expansions(k)}
		if k == root {
			n.RevisionState = rootRevState
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// expansions reports which directions have at least one neighbor in ANY knowledge
// kind, so the UI can offer an accurate "there is more this way" affordance
// regardless of the currently-selected views.
func (q *Query) expansions(key ServiceKey) []Direction {
	var out []Direction
	if len(q.adjacent(key, DirectionDependencies, true, true)) > 0 {
		out = append(out, DirectionDependencies)
	}
	if len(q.adjacent(key, DirectionDependents, true, true)) > 0 {
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
		e.Difference = edgeDifference(*e)
		if !edgeMatchesViews(*e, views) {
			continue
		}
		if len(out) >= maxEdges {
			truncated = true
			break
		}
		out = append(out, *e)
	}
	return out, truncated
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
	for _, c := range e.DeclaredClaims {
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
// then requested ref.
func (q *Query) unresolvedNeighborhoodDeps(depthOf map[ServiceKey]int, wantDeclared bool) []UnresolvedDependency {
	if !wantDeclared {
		return nil
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
	return out
}

// newEdge starts a merged edge between two in-scope services with both endpoint
// references and a stable id.
func (q *Query) newEdge(from, to ServiceKey) *NeighborhoodEdge {
	return &NeighborhoodEdge{
		ID:    string(from) + "|" + string(to),
		From:  q.serviceRef(from),
		To:    q.serviceRef(to),
		Route: RouteForGraph(KindService, string(to)),
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
		e.DeclaredClaims = append(e.DeclaredClaims, DeclaredClaim{
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
		e.ObservationSources = append(e.ObservationSources, cloneObservedStats(rel.ObservedSources)...)
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
