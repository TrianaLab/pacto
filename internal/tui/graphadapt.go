package tui

import (
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/graph"
)

// neighborhoodToTree projects a fleet neighborhood (a flat digraph that may
// contain cycles) onto the rooted pointer tree pkg/graph renders. Cycles are
// broken here and only here: graph.renderChildren has no visited set, and the
// single thing that stops its recursion is an edge marked Shared. Every
// cycle-closing edge is therefore emitted as a childless Shared leaf, and the
// path that closed it is recorded in Result.Cycles so the break is visible
// rather than silent.
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
	b := &treeBuilder{byKey: byKey, out: out, onPath: map[string]bool{}}
	r.Root = b.build(root.Ref.Key, nil)
	r.Cycles = b.cycles
	return r
}

// treeBuilder walks the digraph depth-first, tracking the current path so a
// revisit of a node already on it is a cycle rather than a repeat subtree.
type treeBuilder struct {
	byKey  map[string]fleet.NeighborhoodNode
	out    map[string][]fleet.NeighborhoodEdge
	onPath map[string]bool
	path   []string
	cycles [][]string
}

func (b *treeBuilder) build(key string, _ *graph.Node) *graph.Node {
	nd := b.byKey[key]
	n := &graph.Node{
		Name:    label(nd.Ref),
		Version: nd.Ref.Version,
		Ref:     nd.Ref.Key,
		Local:   nd.Ref.Kind == fleet.KindService,
	}
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
		ge.Shared = true
		ge.Error = "outside the requested neighborhood"
		return ge
	}
	if b.onPath[e.To.Key] {
		b.cycles = append(b.cycles, append(append([]string(nil), b.path...), e.To.Key))
		ge.Shared = true
		return ge
	}
	ge.Node = b.build(e.To.Key, nil)
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
