package tui

import (
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/graph"
)

// neighborhoodToTree projects a fleet neighborhood (a flat digraph that may
// contain cycles and reconvergence) onto the rooted pointer tree pkg/graph
// renders. It mirrors the two checks pkg/graph's own resolver makes
// (resolver.go:206-219), because the renderer is written against that shape:
// an edge back to an ancestor is a cycle, and an edge to a node already emitted
// anywhere is Shared.
//
// Both checks are load-bearing. graph.renderChildren has no visited set, so
// without the ancestor check a cycle recurses until the stack blows. And
// without the global check a reconverging graph duplicates whole subtrees:
// sixty-one nodes of chained diamonds — inside fleet's own MaxNodes bound —
// expand to four million.
func neighborhoodToTree(n *fleet.Neighborhood) *graph.Result {
	r := &graph.Result{}
	if n == nil || len(n.Nodes) == 0 {
		return r
	}
	byKey := make(map[string]fleet.NeighborhoodNode, len(n.Nodes))
	out := make(map[string][]fleet.NeighborhoodEdge, len(n.Nodes))
	for _, nd := range n.Nodes {
		byKey[nd.Ref.Key] = nd
	}
	for _, e := range n.Edges {
		out[e.From.Key] = append(out[e.From.Key], e)
	}

	root := n.Nodes[0]
	for _, nd := range n.Nodes {
		if nd.Focus {
			root = nd
			break
		}
	}
	b := &treeBuilder{
		byKey:  byKey,
		out:    out,
		onPath: map[string]bool{},
		seen:   map[string]*graph.Node{},
	}
	r.Root = b.build(root.Ref.Key)
	r.Cycles = b.cycles
	return r
}

// treeBuilder walks the digraph depth-first. onPath holds the ancestors of the
// node being built, so a revisit of one of them is a cycle; seen holds every
// node built so far, so a revisit of anything else is a shared subtree.
type treeBuilder struct {
	byKey  map[string]fleet.NeighborhoodNode
	out    map[string][]fleet.NeighborhoodEdge
	onPath map[string]bool
	seen   map[string]*graph.Node
	path   []string
	cycles [][]string
}

func (b *treeBuilder) build(key string) *graph.Node {
	nd := b.byKey[key]
	n := &graph.Node{
		Name:    label(nd.Ref),
		Version: nd.Ref.Version,
		Ref:     nd.Ref.Key,
		// Local is not set: in pkg/graph it means "resolved from the local filesystem"
		// and a fleet node carries nothing that could tell us that.
	}
	// Recorded before descending so later visits of this key resolve to Shared.
	b.seen[key] = n
	b.onPath[key] = true
	b.path = append(b.path, key)
	for _, e := range b.out[key] {
		n.Dependencies = append(n.Dependencies, b.edge(e))
	}
	b.path = b.path[:len(b.path)-1]
	delete(b.onPath, key)
	return n
}

func (b *treeBuilder) edge(e fleet.NeighborhoodEdge) graph.Edge {
	return terminalOnly(b.classify(e))
}

// terminalOnly downgrades a reference edge that is not really terminal.
// render.go:86 prints EdgeReference as a bare line and skips the error, the
// shared marker and the whole subtree, so the adapter may claim EdgeReference
// only when there is nothing underneath for it to hide.
func terminalOnly(ge graph.Edge) graph.Edge {
	if ge.Type != graph.EdgeReference {
		return ge
	}
	if ge.Error != "" || ge.Shared || (ge.Node != nil && len(ge.Node.Dependencies) > 0) {
		ge.Type = graph.EdgeDependency
	}
	return ge
}

func (b *treeBuilder) classify(e fleet.NeighborhoodEdge) graph.Edge {
	ge := graph.Edge{
		Ref:           label(e.To),
		Type:          edgeType(e.Relation),
		Compatibility: e.Difference,
		Required:      e.Expected,
	}
	if _, known := b.byKey[e.To.Key]; !known {
		// An edge to a node the bounded projection did not include. Say so
		// instead of silently dropping it — a truncated graph that looks whole
		// is worse than one that admits the gap.
		ge.Error = "outside the requested neighborhood"
		return ge
	}
	// Order matters: an ancestor is also in seen, and reporting it as a shared
	// subtree would hide the cycle behind a marker that reads like reuse.
	if b.onPath[e.To.Key] {
		b.cycles = append(b.cycles, append(append([]string(nil), b.path...), e.To.Key))
		ge.Error = "cycle detected: " + e.To.Key
		return ge
	}
	if prev := b.seen[e.To.Key]; prev != nil {
		// A shallow copy, exactly as the resolver builds one: the renderer
		// prints the label and the (shared) marker and does not descend.
		ge.Shared = true
		ge.Node = &graph.Node{Name: prev.Name, Version: prev.Version, Ref: prev.Ref}
		return ge
	}
	ge.Node = b.build(e.To.Key)
	return ge
}

// edgeType maps a fleet relation onto the two edge types pkg/graph understands.
// A "runs" link is not a contract dependency, so it renders as a reference.
func edgeType(relation string) string {
	if relation == "dependency" {
		return graph.EdgeDependency
	}
	return graph.EdgeReference
}

// label prefers the human label and falls back to the canonical key.
func label(r fleet.EntityRef) string {
	if r.Label != "" {
		return r.Label
	}
	return r.Key
}

// treeColors is the TUI's colouriser for the shared tree renderer. It exists
// because internal/cli's equivalent is package-private and emits raw ANSI;
// this one goes through lipgloss so it honours the terminal's colour profile.
func treeColors() graph.TreeColors {
	return graph.TreeColors{
		Name:    func(s string) string { return s },
		Version: func(s string) string { return dimStyle.Render(s) },
		Marker:  func(s string) string { return dimStyle.Render(s) },
		Error:   func(s string) string { return errorStyle.Render(s) },
		Warn:    func(s string) string { return warnStyle.Render(s) },
	}
}
