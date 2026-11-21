package fleet

import (
	"cmp"
	"slices"
	"strconv"
)

// MaxMapFocusPerColumn bounds the nodes of one focus column.
const MaxMapFocusPerColumn = 9

// MapFocusQuery asks for one service's callers (left) and callees (right),
// one or two hops out, at most PerColumn per column.
type MapFocusQuery struct {
	Service   string
	Hops      int
	PerColumn int
}

// MapFocusAnswer is one service with its callers and callees in columns,
// worst first, and the edges between adjacent columns in the call direction.
// Same-column edges and cycles are left out.
type MapFocusAnswer struct {
	Meta    ProductMeta    `json:"meta"`
	Focus   MapNode        `json:"focus"`
	Group   string         `json:"group" doc:"The focus service's group in the domain grouping"`
	Hops    int            `json:"hops"`
	Columns []MapColumn    `json:"columns"`
	Edges   []MapFocusEdge `json:"edges"`
}

// MapColumn is one column of a focus: Hop -2 and -1 call the focus, 1 and 2
// are called by it. Total counts every candidate before the bound; hop 2
// expands only the nodes hop 1 shows.
type MapColumn struct {
	Hop   int       `json:"hop" enum:"-2,-1,1,2"`
	Total int       `json:"total"`
	Nodes []MapNode `json:"nodes"`
}

// MapFocusEdge is one dependency of a focus, merged by the neighbourhood's
// fold, with what the map labels it with.
type MapFocusEdge struct {
	NeighborhoodEdge
	Interface          string      `json:"interface,omitempty" doc:"The interface the dependency requires, else the provider's only interface"`
	Range              string      `json:"range,omitempty" doc:"The compatibility range the consumer declares"`
	RunningVersion     string      `json:"runningVersion,omitempty" doc:"The version the provider's visible targets run, mixed when they differ"`
	DeclaredIn         *EntityRef  `json:"declaredIn,omitempty" doc:"The consumer revision whose declaration the labels read"`
	EnvironmentUnknown bool        `json:"environmentUnknown,omitempty" doc:"Observed calls whose environments Pacto cannot tell, so no cross-environment call is judged"`
	Problem            *MapProblem `json:"problem,omitempty"`
}

// focusBuild is one focus while it is built.
type focusBuild struct {
	b         *mapBuild
	callers   map[ServiceKey][]ServiceKey
	callees   map[ServiceKey][]ServiceKey
	placed    map[ServiceKey]int
	perColumn int
}

// MapFocus returns the focus of one service. It reads the map's own merged
// edges, so provenance, observation sources, last seen and staleness are the
// neighbourhood's.
func (q *Query) MapFocus(fq MapFocusQuery) (*MapFocusAnswer, error) {
	hops, per, err := mapFocusBounds(fq)
	if err != nil {
		return nil, err
	}
	s, err := q.resolveService(fq.Service)
	if err != nil {
		return nil, err
	}
	if s.Template {
		return nil, &NotFoundError{Kind: "map service", ID: fq.Service}
	}
	b, _ := q.mapMembers(MapQuery{})
	fb := &focusBuild{b: b, callers: map[ServiceKey][]ServiceKey{}, callees: map[ServiceKey][]ServiceKey{}, placed: map[ServiceKey]int{s.Key: 0}, perColumn: per}
	for _, me := range b.edges {
		fb.callers[me.to] = append(fb.callers[me.to], me.from)
		fb.callees[me.from] = append(fb.callees[me.from], me.to)
	}
	near, f := fb.columns([][]ServiceKey{{s.Key}, {s.Key}}, -1, 1)
	out := &MapFocusAnswer{Meta: q.productMeta(), Group: b.resolve(b.svcGroup[s.Key]), Hops: hops, Focus: f.node(string(s.Key)), Columns: near}
	if hops == 2 {
		far, _ := fb.columns([][]ServiceKey{shownKeys(near[0]), shownKeys(near[1])}, -2, 2)
		out.Columns = []MapColumn{far[0], near[0], near[1], far[1]}
	}
	out.Edges = fb.edges()
	return out, nil
}

// mapFocusBounds is the hops (1 by default, 1 or 2) and the nodes per column
// (9 by default and at most).
func mapFocusBounds(fq MapFocusQuery) (hops, per int, err error) {
	if fq.Hops < 0 || fq.Hops > 2 {
		return 0, 0, &InvalidQueryError{Field: "hops", Value: strconv.Itoa(fq.Hops), Reason: "must be 1 or 2"}
	}
	if fq.PerColumn < 0 {
		return 0, 0, &InvalidQueryError{Field: "perColumn", Value: strconv.Itoa(fq.PerColumn), Reason: "must be >= 0"}
	}
	return cmp.Or(fq.Hops, 1), min(cmp.Or(fq.PerColumn, MaxMapFocusPerColumn), MaxMapFocusPerColumn), nil
}

// shownKeys is the services a column shows.
func shownKeys(c MapColumn) []ServiceKey {
	out := make([]ServiceKey, len(c.Nodes))
	for i, n := range c.Nodes {
		out[i] = ServiceKey(n.Key)
	}
	return out
}

// columns is the column of from[0]'s callers at hop left and of from[1]'s
// callees at hop right, each worst first, with the facts it read (from
// included). A service shows in one column only: the first that places it.
func (fb *focusBuild) columns(from [][]ServiceKey, left, right int) ([]MapColumn, *nodeFacts) {
	sides := []struct {
		hop int
		adj map[ServiceKey][]ServiceKey
	}{{left, fb.callers}, {right, fb.callees}}
	cands := make([][]ServiceKey, 2)
	keys := map[ServiceKey]bool{}
	for i, side := range sides {
		seen := map[ServiceKey]bool{}
		for _, k := range from[i] {
			keys[k] = true
			for _, n := range side.adj[k] {
				if _, ok := fb.placed[n]; !ok && !seen[n] {
					seen[n] = true
					cands[i] = append(cands[i], n)
					keys[n] = true
				}
			}
		}
	}
	f := fb.b.newNodeFacts(keys, "")
	var out []MapColumn
	for i, side := range sides {
		nodes := make([]MapNode, 0, len(cands[i]))
		for _, k := range cands[i] {
			if _, ok := fb.placed[k]; !ok {
				nodes = append(nodes, f.node(string(k)))
			}
		}
		col := MapColumn{Hop: side.hop, Total: len(nodes), Nodes: []MapNode{}}
		slices.SortFunc(nodes, func(a, c MapNode) int {
			return cmp.Or(cmp.Compare(severityRankOf(a.Severity), severityRankOf(c.Severity)), cmp.Compare(a.Name, c.Name), cmp.Compare(a.Key, c.Key))
		})
		for _, n := range nodes[:min(len(nodes), fb.perColumn)] {
			fb.placed[ServiceKey(n.Key)] = side.hop
			col.Nodes = append(col.Nodes, n)
		}
		out = append(out, col)
	}
	return out, f
}

// severityRankOf orders severities worst first, no item last.
func severityRankOf(severity string) int {
	if r, ok := attentionSeverityRank[severity]; ok {
		return r
	}
	return len(attentionSeverityRank)
}

// edges is every merged edge from a placed service to one placed in the
// next column (the focus is column 0), labelled.
func (fb *focusBuild) edges() []MapFocusEdge {
	q := fb.b.q
	var pf *problemFacts
	out := []MapFocusEdge{}
	for _, me := range fb.b.edges {
		from, okf := fb.placed[me.from]
		to, okt := fb.placed[me.to]
		if !okf || !okt || to != from+1 {
			continue
		}
		if pf == nil {
			pf = new(q.problemFactsOf())
		}
		out = append(out, q.focusEdge(me, *pf))
	}
	return out
}

// focusEdge is me finalized by the neighbourhood's step, with its labels.
func (q *Query) focusEdge(me *mapEdge, pf problemFacts) MapFocusEdge {
	e := *me.e
	finalizeEdge(&e)
	// The columns carry each end in full: the edge names them.
	e.From, e.To = slimRef(e.From), slimRef(e.To)
	fe := MapFocusEdge{NeighborhoodEdge: e, Problem: q.edgeProblem(me, pf), EnvironmentUnknown: e.Observed && !q.environmentsKnown(me)}
	provider := q.snap.Services[me.to]
	fe.RunningVersion, _ = q.runningVersion(provider, "")
	c := q.focusClaim(me)
	if c != nil {
		fe.Range = c.Compatibility
		if r := q.snap.Revisions[c.SourceRevision]; r != nil {
			fe.DeclaredIn = new(slimRef(revisionEntityRef(r)))
		}
		for _, req := range c.Requires {
			if req.Interface != "" {
				fe.Interface = req.Interface
				break
			}
		}
	}
	if rev := q.currentRevision(provider); fe.Interface == "" && rev != nil && rev.Contract != nil && len(rev.Contract.Interfaces) == 1 {
		fe.Interface = rev.Contract.Interfaces[0].Name
	}
	return fe
}

// slimRef is the kind, key, label and version of ref: enough to route and
// read, so a focus of dense columns stays small.
func slimRef(ref EntityRef) EntityRef {
	return EntityRef{Kind: ref.Kind, Key: ref.Key, Label: ref.Label, Version: ref.Version}
}

// focusClaim is the declaration an edge's labels read: the consumer's
// current revision's when it declares the edge, else the first, nil when no
// contract declares it.
func (q *Query) focusClaim(me *mapEdge) *DeclaredClaim {
	claims := me.e.DeclaredClaims.Items
	if len(claims) == 0 {
		return nil
	}
	if cur := q.currentRevision(q.snap.Services[me.from]); cur != nil {
		if i := slices.IndexFunc(claims, func(c DeclaredClaim) bool { return c.SourceRevision == cur.Key }); i >= 0 {
			return &claims[i]
		}
	}
	return &claims[0]
}

// environmentsKnown reports whether one of me's observed calls joins two
// visible targets whose environments Pacto knows.
func (q *Query) environmentsKnown(me *mapEdge) bool {
	return slices.ContainsFunc(me.pairs, func(p envPair) bool {
		from, to := q.snap.Targets[p.from], q.snap.Targets[p.to]
		return from != nil && to != nil && from.Environment != "" && to.Environment != ""
	})
}
