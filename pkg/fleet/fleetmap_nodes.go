package fleet

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Bounds of a page of map nodes.
const (
	DefaultMapNodes = 1000
	MaxMapNodes     = 5000
)

// MapNodesQuery asks for one page of one group's nodes in the grouping
// GroupBy. Environment, outside the environment grouping, marks which nodes
// run there and with which version; it moves no node.
type MapNodesQuery struct {
	GroupBy     string
	Group       string
	Environment string
	Offset      int
	Limit       int
}

// MapNodes is one page of a group's nodes in sunflower order, with their
// coordinates relative to the group's centre, and the edges between the
// nodes of this page and of the earlier pages of the same group.
type MapNodes struct {
	Meta      ProductMeta `json:"meta"`
	GroupBy   string      `json:"groupBy" enum:"domain,team,environment"`
	Group     string      `json:"group"`
	Nodes     []MapNode   `json:"nodes"`
	Edges     []MapEdge   `json:"edges"`
	Offset    int         `json:"offset"`
	Limit     int         `json:"limit"`
	Total     int         `json:"total" doc:"Nodes in the group"`
	Truncated bool        `json:"truncated,omitempty" doc:"More nodes follow this page"`
}

// MapNode is one node of the map: a service, a target in the environment
// grouping (its key names the cluster, namespace and workload), or an
// external system. X and Y are relative to its group's centre; a focus
// column carries none.
type MapNode struct {
	Key             string   `json:"key"`
	Name            string   `json:"name"`
	Label           string   `json:"label,omitempty" doc:"Absent when it reads like the name"`
	X               float64  `json:"x"`
	Y               float64  `json:"y"`
	Sub             int      `json:"sub,omitempty" doc:"Index of the node's sub-cluster in the group's subs; absent for the first one and when the group has none"`
	Status          string   `json:"status,omitempty"`
	Match           string   `json:"match,omitempty" enum:"matched,expected-only,observed-only,insufficient"`
	Severity        string   `json:"severity,omitempty" enum:"error,warning,info" doc:"The worst attention item about the node"`
	Worst           string   `json:"worst,omitempty" doc:"The category of that item"`
	Inferred        bool     `json:"inferred,omitempty"`
	External        bool     `json:"external,omitempty"`
	Version         string   `json:"version,omitempty" doc:"The current version, or with an environment the version running there (mixed when targets differ)"`
	Owner           string   `json:"owner,omitempty"`
	Interfaces      int      `json:"interfaces,omitempty"`
	In              int      `json:"in,omitempty" doc:"Visible services that depend on it"`
	Out             int      `json:"out,omitempty" doc:"Visible services it depends on"`
	PercentComplete *int     `json:"percentComplete,omitempty"`
	Missing         []string `json:"missing,omitempty" doc:"Contract sections it leaves out"`
	InEnvironment   *bool    `json:"inEnvironment,omitempty" doc:"Whether it runs in the requested environment; absent without one and when Pacto cannot tell"`
}

// MapEdge is one dependency between two nodes of a group. From and To are
// the nodes' positions in the group's sunflower order, over every page (the
// node at offset+i of a page is position offset+i), so an edge never repeats
// two long keys.
type MapEdge struct {
	From       int         `json:"from" doc:"The calling node's position in the group, over every page"`
	To         int         `json:"to" doc:"The called node's position in the group, over every page"`
	Difference string      `json:"difference,omitempty" enum:"matched,expected-not-observed,observed-not-expected,insufficient"`
	Match      string      `json:"match,omitempty" enum:"matched,expected-only,observed-only,insufficient" doc:"The match state the ribbons and tallies count it in; absent for a configuration reference"`
	Inferred   bool        `json:"inferred,omitempty"`
	Problem    *MapProblem `json:"problem,omitempty"`
}

// nodeFacts is what a set of nodes reads once: the dependency degrees, the
// attention about their services and the requested environment.
type nodeFacts struct {
	b         *mapBuild
	in, out   map[ServiceKey]int
	attention map[ServiceKey][]AttentionItem
	env       string
	// placementUnknown is set when a node's presence in env is unknown.
	placementUnknown bool
}

// newNodeFacts reads the degrees of every visible service and the attention
// about the services keys names, narrowed to them (never the whole fleet's).
func (b *mapBuild) newNodeFacts(keys map[ServiceKey]bool, env string) *nodeFacts {
	f := &nodeFacts{b: b, in: map[ServiceKey]int{}, out: map[ServiceKey]int{}, attention: map[ServiceKey][]AttentionItem{}, env: env}
	for _, me := range b.edges {
		f.out[me.from]++
		f.in[me.to]++
	}
	for _, it := range b.q.attentionFor(keys) {
		f.attention[ServiceKey(it.Service)] = append(f.attention[ServiceKey(it.Service)], it)
	}
	return f
}

// MapNodes returns one page of the group Group in the grouping GroupBy. The
// coordinates come from the group's own sunflower, so a page never packs the
// groups; the browser adds the group's centre from Map.
func (q *Query) MapNodes(nq MapNodesQuery) (*MapNodes, error) {
	limit, err := mapNodesLimit(nq)
	if err != nil {
		return nil, err
	}
	if nq.Environment != "" && nq.GroupBy == MapGroupByEnvironment {
		return nil, &InvalidQueryError{Field: "environment", Value: nq.Environment, Reason: "the environment grouping already places each target in its environment"}
	}
	b, err := q.pageMembers(nq.GroupBy)
	if err != nil {
		return nil, err
	}
	g := b.groups[nq.Group]
	if g == nil {
		return nil, &NotFoundError{Kind: "map group", ID: nq.Group}
	}
	gl := layoutGroup(g.nodes)
	end := min(nq.Offset+limit, len(gl.nodes))
	page := gl.nodes[min(nq.Offset, end):end]
	keys := map[ServiceKey]bool{}
	for _, p := range page {
		keys[b.serviceOfNode(p.key)] = true
	}
	f := b.newNodeFacts(keys, nq.Environment)
	out := &MapNodes{Meta: q.productMeta(), GroupBy: b.mq.GroupBy, Group: g.g.Key, Offset: nq.Offset, Limit: limit,
		Total: len(gl.nodes), Truncated: end < len(gl.nodes), Nodes: make([]MapNode, 0, len(page))}
	for _, p := range page {
		n := f.node(p.key)
		n.X, n.Y, n.Sub = round1(p.x), round1(p.y), max(p.sub, 0)
		out.Nodes = append(out.Nodes, n)
	}
	out.Edges = b.pageEdges(gl.nodes[:end], nq.Offset)
	if f.placementUnknown {
		out.Meta = q.productMetaWith([]Limitation{{Code: LimitationEnvironmentPlacementUnknown,
			Message: "Pacto cannot tell whether some services run in " + nq.Environment + ": their targets are hidden or not every target source is complete"}})
	}
	return out, nil
}

// mapNodesLimit is the page size: 0 takes the default, past the maximum or
// a negative offset or limit is an error.
func mapNodesLimit(nq MapNodesQuery) (int, error) {
	switch {
	case nq.Offset < 0:
		return 0, &InvalidQueryError{Field: "offset", Value: strconv.Itoa(nq.Offset), Reason: "must be >= 0"}
	case nq.Limit < 0 || nq.Limit > MaxMapNodes:
		return 0, &InvalidQueryError{Field: "limit", Value: strconv.Itoa(nq.Limit), Reason: "must be between 0 and " + strconv.Itoa(MaxMapNodes)}
	}
	return cmp.Or(nq.Limit, DefaultMapNodes), nil
}

// serviceOfNode is the service a node key names: a target's service, the
// service itself, or "" for an external system.
func (b *mapBuild) serviceOfNode(key string) ServiceKey {
	if t := b.q.snap.Targets[TargetKey(key)]; t != nil {
		return t.ServiceKey
	}
	if b.q.snap.Services[ServiceKey(key)] != nil {
		return ServiceKey(key)
	}
	return ""
}

// node is the node key names: a target, a service or an external system.
func (f *nodeFacts) node(key string) MapNode {
	q := f.b.q
	if t := q.snap.Targets[TargetKey(key)]; t != nil {
		n := f.serviceNode(q.snap.Services[t.ServiceKey], func(it AttentionItem) bool { return it.Entity.Kind != KindTarget || it.Entity.Key == key })
		n.Key, n.Status, n.Match, n.InEnvironment = key, t.Compliance, q.targetMatch(t), nil
		n.Version = q.targetVersion(t)
		return n
	}
	if s := q.snap.Services[ServiceKey(key)]; s != nil {
		return f.serviceNode(s, func(AttentionItem) bool { return true })
	}
	e := q.snap.Externals[ExternalKey(key)]
	return MapNode{Key: key, Name: key, Label: cmp.Or(e.Label, key), External: true, In: len(e.Consumers)}
}

// serviceNode is s as a node; counts says which attention items about s
// count for the node.
func (f *nodeFacts) serviceNode(s *ServiceRecord, counts func(AttentionItem) bool) MapNode {
	q := f.b.q
	n := MapNode{Key: string(s.Key), Name: s.Name, Status: s.Status, Match: q.serviceMatch(s), Inferred: s.Inferred(),
		Owner: s.Owner.DisplayString(), In: f.in[s.Key], Out: f.out[s.Key]}
	n.Severity, n.Worst = worstItem(slices.DeleteFunc(slices.Clone(f.attention[s.Key]), func(it AttentionItem) bool { return !counts(it) }))
	if rev := q.currentRevision(s); rev != nil {
		n.Version = rev.Version
		if rev.Contract != nil {
			n.Interfaces = len(rev.Contract.Interfaces)
		}
	}
	if !s.Inferred() {
		sc := f.b.completenessOf(s)
		n.PercentComplete = sc.Percent
		for _, sec := range sc.Sections {
			if sec.State == contract.CompletenessMissing {
				n.Missing = append(n.Missing, sec.Section)
			}
		}
	}
	if f.env != "" {
		// A service runs in env when a visible target runs there, and does
		// not only when Pacto can tell where it runs; otherwise it is unknown.
		v, ok := q.runningVersion(s, f.env)
		n.Version = v
		switch {
		case ok:
			n.InEnvironment = new(true)
		case !s.Inferred() && q.placementKnown(s):
			n.InEnvironment = new(false)
		default:
			f.placementUnknown = true
		}
	}
	return n
}

// worstItem is the severity and category of the worst item: by severity,
// then the canonical category order.
func worstItem(items []AttentionItem) (severity, category string) {
	if len(items) == 0 {
		return "", ""
	}
	w := slices.MinFunc(items, func(a, b AttentionItem) int {
		return cmp.Or(cmp.Compare(attentionSeverityRank[a.Severity], attentionSeverityRank[b.Severity]),
			cmp.Compare(slices.Index(attentionCategoryOrder, a.Category), slices.Index(attentionCategoryOrder, b.Category)))
	})
	return w.Severity, w.Category
}

// targetVersion is the version of the revision target t runs, "" when Pacto
// does not know it.
func (q *Query) targetVersion(t *TargetRecord) string {
	if r := q.snap.Revisions[t.ContractRevision]; r != nil {
		return r.Version
	}
	return ""
}

// runningVersion is the version s's visible targets run (in env, or
// anywhere when env is empty), "mixed" when they differ, and whether any
// target runs there.
func (q *Query) runningVersion(s *ServiceRecord, env string) (string, bool) {
	version, any := "", false
	for _, tk := range s.Targets {
		t := q.snap.Targets[tk]
		if t == nil || env != "" && t.Environment != env {
			continue
		}
		v := q.targetVersion(t)
		switch {
		case !any:
			version = v
		case v != version:
			version = "mixed"
		}
		any = true
	}
	return version, any
}

// pageEdges is the edges between nodes, at least one of which is at or past
// offset, so every edge of a group comes once over its pages. In the
// environment grouping an edge joins two targets of the same environment.
func (b *mapBuild) pageEdges(nodes []nodePos, offset int) []MapEdge {
	type at struct {
		env string
		i   int
	}
	bySvc := map[ServiceKey][]at{}
	for i, p := range nodes {
		env := ""
		if t := b.q.snap.Targets[TargetKey(p.key)]; t != nil {
			env = t.Environment
		}
		if k := b.serviceOfNode(p.key); k != "" {
			bySvc[k] = append(bySvc[k], at{env, i})
		}
	}
	var pf *problemFacts
	out := []MapEdge{}
	for _, me := range b.edges {
		for _, from := range bySvc[me.from] {
			for _, to := range bySvc[me.to] {
				if from.env != to.env || max(from.i, to.i) < offset {
					continue
				}
				if pf == nil {
					pf = new(b.q.problemFactsOf())
				}
				out = append(out, MapEdge{From: from.i, To: to.i, Difference: edgeDifference(*me.e), Match: me.state, Inferred: me.e.Inferred, Problem: b.q.edgeProblem(me, *pf)})
			}
		}
	}
	return out
}
