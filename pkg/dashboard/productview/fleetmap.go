package productview

import "github.com/trianalab/pacto/v4/pkg/fleet"

// The fleet map's groups and node pages carry keys, never entity references,
// so they travel as fleet facts: a node page of 1000 stays within its byte
// budget and the browser builds each node's link from its key. The focus is
// small (at most 9 nodes a column), so it carries a link on every node and edge.

// ProductMapNode is a focus node with its service page link.
type ProductMapNode struct {
	fleet.MapNode
	Href string `json:"href"`
}

// ProductMapColumn is a focus column of linked nodes.
type ProductMapColumn struct {
	fleet.MapColumn
	Nodes []ProductMapNode `json:"nodes"`
}

// ProductMapFocusEdge is a focus edge with navigable ends and declaring
// revision.
type ProductMapFocusEdge struct {
	fleet.MapFocusEdge
	From       ProductRef  `json:"from"`
	To         ProductRef  `json:"to"`
	Href       string      `json:"href"`
	DeclaredIn *ProductRef `json:"declaredIn,omitempty"`
}

// ProductMapFocus is one service's focus on the map, every node and edge
// linked.
type ProductMapFocus struct {
	fleet.MapFocusAnswer
	Focus   ProductMapNode        `json:"focus"`
	Columns []ProductMapColumn    `json:"columns"`
	Edges   []ProductMapFocusEdge `json:"edges"`
}

func mapNode(n fleet.MapNode) ProductMapNode {
	return ProductMapNode{MapNode: n, Href: Entity(fleet.KindService, n.Key)}
}

// ToMapFocus wraps a focus answer with links.
func ToMapFocus(f *fleet.MapFocusAnswer) *ProductMapFocus {
	out := &ProductMapFocus{MapFocusAnswer: *f, Focus: mapNode(f.Focus),
		Columns: make([]ProductMapColumn, len(f.Columns)), Edges: make([]ProductMapFocusEdge, len(f.Edges))}
	for i, c := range f.Columns {
		nodes := make([]ProductMapNode, len(c.Nodes))
		for j, n := range c.Nodes {
			nodes[j] = mapNode(n)
		}
		out.Columns[i] = ProductMapColumn{MapColumn: c, Nodes: nodes}
	}
	for i, e := range f.Edges {
		pe := productEdge(e.NeighborhoodEdge)
		out.Edges[i] = ProductMapFocusEdge{MapFocusEdge: e, From: pe.From, To: pe.To, Href: pe.Href, DeclaredIn: RefPtr(e.DeclaredIn)}
	}
	return out
}
