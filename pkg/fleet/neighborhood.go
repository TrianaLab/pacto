package fleet

import (
	"sort"
	"time"
)

// Neighborhood bounds. A neighborhood is always bounded so the graph never opens
// as an unusable whole-fleet hairball (requirement 5).
const (
	DefaultNeighborhoodDepth = 1
	DefaultMaxNodes          = 60
	DefaultMaxEdges          = 120
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

// NeighborhoodEdge is a merged declared+observed edge between two in-scope
// services. Expected/Observed record which knowledge backs the edge;
// Reconciliation is the backend's explicit declared-vs-observed verdict. The
// frontend must never infer "reconciled" from anything but these fields.
type NeighborhoodEdge struct {
	ID                 string               `json:"id"`
	From               EntityRef            `json:"from"`
	To                 EntityRef            `json:"to"`
	Expected           bool                 `json:"expected"`
	Observed           bool                 `json:"observed"`
	Provenance         string               `json:"provenance"`
	Reconciliation     string               `json:"reconciliation,omitempty"`
	Required           bool                 `json:"required,omitempty"`
	Compatibility      string               `json:"compatibility,omitempty"`
	SourceRevision     RevisionKey          `json:"sourceRevision,omitempty"`
	ObservationSources []ObservedSourceStat `json:"observationSources,omitempty"`
	Count              int                  `json:"count,omitempty"`
	FirstSeen          *time.Time           `json:"firstSeen,omitempty"`
	LastSeen           *time.Time           `json:"lastSeen,omitempty"`
	Stale              bool                 `json:"stale,omitempty"`
	Insufficient       bool                 `json:"insufficient,omitempty"`
	Limitations        []Limitation         `json:"limitations,omitempty"`
	Route              string               `json:"route,omitempty"`
}

// Neighborhood is the bounded, graph-ready neighborhood answer.
type Neighborhood struct {
	Meta      ProductMeta        `json:"meta"`
	Focus     EntityRef          `json:"focus"`
	Direction Direction          `json:"direction"`
	Depth     int                `json:"depth"`
	Views     []KnowledgeView    `json:"views"`
	Nodes     []NeighborhoodNode `json:"nodes"`
	Edges     []NeighborhoodEdge `json:"edges"`
	Truncated bool               `json:"truncated"`
	MaxNodes  int                `json:"maxNodes"`
	MaxEdges  int                `json:"maxEdges"`
}

// Neighborhood returns the bounded local neighborhood of the focus entity across
// the requested knowledge views. It is authoritative about graph semantics: an
// observed-only edge is only reported when the observed or differences view is
// requested, and reconciliation is a backend fact carried verbatim.
func (q *Query) Neighborhood(nq NeighborhoodQuery) (*Neighborhood, error) {
	root, focus, revState, err := q.resolveNeighborhoodFocus(nq.Kind, nq.Key)
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
	depth := nq.Depth
	if depth <= 0 {
		depth = DefaultNeighborhoodDepth
	}
	maxNodes := nq.MaxNodes
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}
	maxEdges := nq.MaxEdges
	if maxEdges <= 0 {
		maxEdges = DefaultMaxEdges
	}

	nodeDepth, truncatedNodes := q.walkNeighborhood(root, dir, depth, maxNodes)
	res := &Neighborhood{
		Meta: q.productMeta(), Focus: focus, Direction: dir, Depth: depth,
		Views: views, MaxNodes: maxNodes, MaxEdges: maxEdges,
		Nodes: q.neighborhoodNodes(root, nodeDepth, revState), Edges: []NeighborhoodEdge{},
	}
	edges, truncatedEdges := q.neighborhoodEdges(nodeDepth, views, maxEdges)
	res.Edges = edges
	res.Truncated = truncatedNodes || truncatedEdges
	return res, nil
}

// resolveNeighborhoodFocus resolves a focus entity to its logical service root
// and the revision-link state to surface on the focus node (a target carries its
// exact/inferred match; a revision is exact content; a service has none). Only
// service, revision and target focus onto the service graph; owner and source are
// not graph nodes and are rejected with an actionable error.
func (q *Query) resolveNeighborhoodFocus(kind EntityKind, key string) (root ServiceKey, focus EntityRef, revState string, err error) {
	switch kind {
	case KindService:
		s, e := q.resolveService(key)
		if e != nil {
			return "", EntityRef{}, "", e
		}
		return s.Key, serviceEntityRef(s), "", nil
	case KindRevision:
		rev := q.snap.Revisions[RevisionKey(key)]
		if rev == nil {
			return "", EntityRef{}, "", &NotFoundError{Kind: "revision", ID: key}
		}
		return rev.ServiceKey, revisionEntityRef(rev), revisionMatchExact, nil
	case KindTarget:
		tv, e := q.GetTarget(key)
		if e != nil {
			return "", EntityRef{}, "", e
		}
		return tv.Target.ServiceKey, targetEntityRef(tv.Target), tv.Target.RevisionMatch, nil
	default:
		return "", EntityRef{}, "", &InvalidQueryError{Field: "kind", Value: string(kind), Reason: "neighborhood focus must be a service, revision or target"}
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

// adjacent returns a node's neighbor service keys in the given direction, unioning
// declared and observed adjacency so the neighborhood reflects both knowledge kinds.
func (q *Query) adjacent(key ServiceKey, dir Direction) []ServiceKey {
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
		add(q.snap.forwardDeps[key])
		add(q.snap.observedForward[key])
	}
	if dir == DirectionDependents || dir == DirectionBoth {
		add(q.snap.reverseDeps[key])
		add(q.snap.observedReverse[key])
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// walkNeighborhood does a bounded breadth-first walk from root, returning the
// depth at which each reached node was first seen and whether the node cap was hit.
func (q *Query) walkNeighborhood(root ServiceKey, dir Direction, maxDepth, maxNodes int) (map[ServiceKey]int, bool) {
	depthOf := map[ServiceKey]int{root: 0}
	frontier := []ServiceKey{root}
	truncated := false
	for d := 0; d < maxDepth && len(frontier) > 0; d++ {
		var next []ServiceKey
		for _, node := range frontier {
			for _, nb := range q.adjacent(node, dir) {
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

// expansions reports which directions have at least one neighbor, so the UI can
// offer accurate expand affordances.
func (q *Query) expansions(key ServiceKey) []Direction {
	var out []Direction
	if len(q.adjacent(key, DirectionDependencies)) > 0 {
		out = append(out, DirectionDependencies)
	}
	if len(q.adjacent(key, DirectionDependents)) > 0 {
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
		out = append(out, *e)
	}
	return out, truncated
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
// keeping declared and observed knowledge distinct on the same edge.
func (q *Query) foldRelationshipIntoEdge(e *NeighborhoodEdge, rel Relationship) {
	switch rel.Provenance {
	case ProvenanceDeclared:
		e.Expected = true
		e.Reconciliation = rel.Reconciliation
		e.Required = rel.Required
		e.Compatibility = rel.Compatibility
		e.SourceRevision = rel.FromRevision
		e.Insufficient = rel.Reconciliation == ReconciliationInsufficient
	case ProvenanceObserved:
		e.Observed = true
		e.Count += rel.ObservedCount
		e.ObservationSources = append(e.ObservationSources, rel.ObservedSources...)
		e.FirstSeen = earlier(e.FirstSeen, rel.FirstSeen)
		e.LastSeen = later(e.LastSeen, rel.LastSeen)
		e.Stale = q.observedStale(rel.LastSeen)
	}
	e.Provenance = edgeProvenance(*e)
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
