package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/graph"
)

func nbNode(key string, focus bool) fleet.NeighborhoodNode {
	return fleet.NeighborhoodNode{
		Ref:   fleet.EntityRef{Kind: fleet.KindService, Key: key, Label: key},
		Focus: focus,
	}
}

func nbEdge(from, to string) fleet.NeighborhoodEdge {
	return fleet.NeighborhoodEdge{
		ID:       from + "->" + to,
		From:     fleet.EntityRef{Kind: fleet.KindService, Key: from, Label: from},
		To:       fleet.EntityRef{Kind: fleet.KindService, Key: to, Label: to},
		Relation: "dependency",
		Expected: true,
	}
}

// TestNeighborhoodToTreeBuildsFromTheFocusNode roots at the focus node, and the
// focus node is deliberately NOT Nodes[0]. With the fixture the other way round
// the whole focus-selection loop could be deleted — leaving the fall-back
// root := n.Nodes[0] — and this test still passed, so it distinguished nothing.
func TestNeighborhoodToTreeBuildsFromTheFocusNode(t *testing.T) {
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("b", false), nbNode("a", true)},
		Edges: []fleet.NeighborhoodEdge{nbEdge("a", "b")},
	}
	r := neighborhoodToTree(n)
	if r.Root == nil || r.Root.Name != "a" {
		t.Fatalf("root = %+v, want a", r.Root)
	}
	if len(r.Root.Dependencies) != 1 || r.Root.Dependencies[0].Node.Name != "b" {
		t.Fatalf("child = %+v, want b", r.Root.Dependencies)
	}
}

func TestNeighborhoodToTreeBreaksCycles(t *testing.T) {
	// a -> b -> a. graph.renderChildren has no visited set, so the adapter must
	// cut the back edge itself. It cuts it the way pkg/graph's own resolver does
	// (resolver.go:212), with an error the renderer prints, so the reader can
	// tell a broken cycle from a leaf.
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true), nbNode("b", false)},
		Edges: []fleet.NeighborhoodEdge{nbEdge("a", "b"), nbEdge("b", "a")},
	}
	r := neighborhoodToTree(n)
	back := r.Root.Dependencies[0].Node.Dependencies
	if len(back) != 1 {
		t.Fatalf("want the back edge preserved, got %d", len(back))
	}
	if back[0].Node != nil {
		t.Fatal("a cut edge must not carry a child node; rendering it will recurse forever")
	}
	if !strings.Contains(back[0].Error, "cycle") {
		t.Fatalf("the cycle-closing edge reports %q, want it to say it is a cycle", back[0].Error)
	}
	// The real proof: rendering terminates and shows the break.
	done := make(chan string, 1)
	go func() { done <- graph.RenderTreeColored(r, graph.TreeColors{}) }()
	select {
	case out := <-done:
		if !strings.Contains(out, "a") {
			t.Fatalf("render lost the root:\n%s", out)
		}
		if !strings.Contains(out, "cycle") {
			t.Fatalf("the rendered tree hides the cycle it cut:\n%s", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rendering a cyclic neighborhood did not terminate")
	}
	if len(r.Cycles) == 0 {
		t.Fatal("the cycle was broken but never reported")
	}
}

func TestNeighborhoodToTreeSelfLoop(t *testing.T) {
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true)},
		Edges: []fleet.NeighborhoodEdge{nbEdge("a", "a")},
	}
	r := neighborhoodToTree(n)
	if len(r.Root.Dependencies) != 1 {
		t.Fatalf("want the self loop preserved, got %+v", r.Root.Dependencies)
	}
	e := r.Root.Dependencies[0]
	if e.Node != nil || !strings.Contains(e.Error, "cycle") {
		t.Fatalf("a self loop must be a cut leaf reported as a cycle, got %+v", e)
	}
}

func TestNeighborhoodToTreeSharesAReconvergingSubtree(t *testing.T) {
	// A diamond: root -> a, root -> b, and both -> d. d must be built once and
	// referenced the second time, which is what pkg/graph's resolver does and
	// what the renderer's "(shared)" marker exists for. Expanding it twice is
	// not just noisy output, it is the base case of an exponential blowup.
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{
			nbNode("root", true), nbNode("a", false), nbNode("b", false), nbNode("d", false),
		},
		Edges: []fleet.NeighborhoodEdge{
			nbEdge("root", "a"), nbEdge("root", "b"), nbEdge("a", "d"), nbEdge("b", "d"),
		},
	}
	r := neighborhoodToTree(n)
	viaA := r.Root.Dependencies[0].Node.Dependencies[0]
	viaB := r.Root.Dependencies[1].Node.Dependencies[0]
	if viaA.Shared {
		t.Fatal("the first arrival at d must be the real subtree, not a shared marker")
	}
	if !viaB.Shared {
		t.Fatal("the second arrival at d must be Shared; expanding it again duplicates the subtree")
	}
	if viaB.Node == nil || viaB.Node.Name != "d" {
		t.Fatalf("a Shared edge still names its target: %+v", viaB.Node)
	}
	if len(viaB.Node.Dependencies) != 0 {
		t.Fatal("a Shared edge carries a shallow copy; the renderer must not descend into it")
	}
}

func TestNeighborhoodToTreeStaysBoundedOnAReconvergingGraph(t *testing.T) {
	// Chained diamonds inside fleet's own MaxNodes/MaxEdges bounds. Without a
	// global seen set this produced 4,194,301 tree nodes from 61 graph nodes and
	// allocated its way through a gigabyte on the way there. The tree can never
	// have more nodes than the graph it came from.
	const layers = 20
	var nodes []fleet.NeighborhoodNode
	var edges []fleet.NeighborhoodEdge
	nodes = append(nodes, nbNode("n0", true))
	for i := range layers {
		cur := fmt.Sprintf("n%d", i)
		a, b, next := fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i), fmt.Sprintf("n%d", i+1)
		nodes = append(nodes, nbNode(a, false), nbNode(b, false), nbNode(next, false))
		edges = append(edges, nbEdge(cur, a), nbEdge(cur, b), nbEdge(a, next), nbEdge(b, next))
	}
	if len(edges) > fleet.DefaultMaxEdges {
		t.Fatalf("the fixture graph has %d edges, past fleet's own bound of %d; it proves nothing about a real neighborhood",
			len(edges), fleet.DefaultMaxEdges)
	}

	done := make(chan int, 1)
	go func() {
		done <- countTreeNodes(neighborhoodToTree(&fleet.Neighborhood{Nodes: nodes, Edges: edges}).Root)
	}()
	select {
	case got := <-done:
		if got > len(nodes) {
			t.Fatalf("%d graph nodes expanded to %d tree nodes; the subtree sharing is not working", len(nodes), got)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the adapter did not finish in 10s on a %d-node graph", len(nodes))
	}
}

func countTreeNodes(n *graph.Node) int {
	if n == nil {
		return 0
	}
	c := 1
	for _, e := range n.Dependencies {
		if !e.Shared {
			c += countTreeNodes(e.Node)
		}
	}
	return c
}

func TestNeighborhoodToTreeWithNoFocusFallsBackToTheFirstNode(t *testing.T) {
	n := &fleet.Neighborhood{Nodes: []fleet.NeighborhoodNode{nbNode("a", false)}}
	if r := neighborhoodToTree(n); r.Root == nil || r.Root.Name != "a" {
		t.Fatalf("root = %+v, want the first node", r.Root)
	}
}

func TestNeighborhoodToTreeEmpty(t *testing.T) {
	if r := neighborhoodToTree(&fleet.Neighborhood{}); r.Root != nil {
		t.Fatalf("an empty neighborhood must produce a nil root, got %+v", r.Root)
	}
	if r := neighborhoodToTree(nil); r == nil || r.Root != nil {
		t.Fatal("a nil neighborhood must produce an empty result, not a panic")
	}
}

func TestNeighborhoodToTreeMapsRelationToEdgeType(t *testing.T) {
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true), nbNode("b", false)},
		Edges: []fleet.NeighborhoodEdge{{
			ID: "a->b", From: nbNode("a", true).Ref, To: nbNode("b", false).Ref,
			Relation: "runs", Observed: true,
		}},
	}
	r := neighborhoodToTree(n)
	if got := r.Root.Dependencies[0].Type; got != graph.EdgeReference {
		t.Fatalf("a runs edge maps to %q, want %q", got, graph.EdgeReference)
	}
}

func TestNeighborhoodToTreeMarksOutsideNodesAsError(t *testing.T) {
	// An edge to a node not in the bounded projection should carry an error
	// that renders, not a nil pointer crash or silent omission.
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true)},
		Edges: []fleet.NeighborhoodEdge{nbEdge("a", "b")}, // b is not in Nodes
	}
	r := neighborhoodToTree(n)
	if len(r.Root.Dependencies) != 1 {
		t.Fatalf("want one edge, got %d", len(r.Root.Dependencies))
	}
	e := r.Root.Dependencies[0]
	if e.Error == "" {
		t.Fatal("an edge to an outside node must carry an error message")
	}
	out := graph.RenderTree(r)
	if !strings.Contains(out, "outside the requested neighborhood") {
		t.Fatalf("the rendered tree must show the gap:\n%s", out)
	}
}

func TestTreeColorsRendersWithoutPanicking(t *testing.T) {
	// The treeColors wrappers must be callable. The graph renderer itself is
	// tested in pkg/graph; this just proves the wrappers exist and don't crash.
	tc := treeColors()
	if tc.Name("x") != "x" {
		t.Fatal("Name wrapper broken")
	}
	if tc.Version("x") == "" {
		t.Fatal("Version wrapper returned empty")
	}
	if tc.Marker("x") == "" {
		t.Fatal("Marker wrapper returned empty")
	}
	if tc.Error("x") == "" {
		t.Fatal("Error wrapper returned empty")
	}
	if tc.Warn("x") == "" {
		t.Fatal("Warn wrapper returned empty")
	}
}

func TestLabelFallbackToKey(t *testing.T) {
	// When Label is empty, label() should return Key
	ref := fleet.EntityRef{Key: "service-key", Label: ""}
	if got := label(ref); got != "service-key" {
		t.Fatalf("label() = %q, want %q", got, "service-key")
	}
}

func TestNeighborhoodToTreeDoesNotMarkServicesAsLocal(t *testing.T) {
	// Local in pkg/graph means "resolved from local filesystem", which a fleet
	// node cannot tell us, so service nodes must not render "[local]".
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true), nbNode("b", false)},
		Edges: []fleet.NeighborhoodEdge{nbEdge("a", "b")},
	}
	r := neighborhoodToTree(n)
	out := graph.RenderTree(r)
	if strings.Contains(out, "[local]") {
		t.Fatalf("service nodes must not render as [local]:\n%s", out)
	}
}

func TestNeighborhoodToTreeRunsEdgeOutsideNeighborhoodRendersError(t *testing.T) {
	// A "runs" edge to a node outside the bounded projection must render its error,
	// not short-circuit as a terminal [ref] that hides the gap.
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true)},
		Edges: []fleet.NeighborhoodEdge{{
			ID: "a->b", From: nbNode("a", true).Ref, To: nbNode("b", false).Ref,
			Relation: "runs", // maps to EdgeReference
		}},
	}
	r := neighborhoodToTree(n)
	out := graph.RenderTreeColored(r, graph.TreeColors{})
	if !strings.Contains(out, "outside the requested neighborhood") {
		t.Fatalf("runs edge to outside node must render error, got:\n%s", out)
	}
}

func TestNeighborhoodToTreeRunsEdgeCycleRendersError(t *testing.T) {
	// A "runs" edge that closes a cycle must render the cycle error, not hide it
	// behind a terminal [ref] marker.
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true), nbNode("b", false)},
		Edges: []fleet.NeighborhoodEdge{
			{ID: "a->b", From: nbNode("a", true).Ref, To: nbNode("b", false).Ref, Relation: "dependency"},
			{ID: "b->a", From: nbNode("b", false).Ref, To: nbNode("a", true).Ref, Relation: "runs"},
		},
	}
	r := neighborhoodToTree(n)
	out := graph.RenderTreeColored(r, graph.TreeColors{})
	if !strings.Contains(out, "cycle") {
		t.Fatalf("runs edge in a cycle must render cycle error, got:\n%s", out)
	}
}

func TestNeighborhoodToTreeRunsEdgeWithChildrenRendersSubtree(t *testing.T) {
	// A "runs" edge to a node that has children must render the whole subtree,
	// not short-circuit and drop the grandchildren.
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true), nbNode("b", false), nbNode("c", false)},
		Edges: []fleet.NeighborhoodEdge{
			{ID: "a->b", From: nbNode("a", true).Ref, To: nbNode("b", false).Ref, Relation: "runs"},
			{ID: "b->c", From: nbNode("b", false).Ref, To: nbNode("c", false).Ref, Relation: "dependency"},
		},
	}
	r := neighborhoodToTree(n)
	out := graph.RenderTreeColored(r, graph.TreeColors{})
	if !strings.Contains(out, "c") {
		t.Fatalf("runs edge with children must render grandchild, got:\n%s", out)
	}
}

func TestNeighborhoodToTreeRunsEdgeSharedRendersMarker(t *testing.T) {
	// A shared "runs" edge must render the (shared) marker, not short-circuit as a terminal [ref].
	// Diamond: root -> a -> d, root -> b (runs) -> d (shared)
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{
			nbNode("root", true), nbNode("a", false), nbNode("b", false), nbNode("d", false),
		},
		Edges: []fleet.NeighborhoodEdge{
			{ID: "root->a", From: nbNode("root", true).Ref, To: nbNode("a", false).Ref, Relation: "dependency"},
			{ID: "root->b", From: nbNode("root", true).Ref, To: nbNode("b", false).Ref, Relation: "dependency"},
			{ID: "a->d", From: nbNode("a", false).Ref, To: nbNode("d", false).Ref, Relation: "dependency"},
			{ID: "b->d", From: nbNode("b", false).Ref, To: nbNode("d", false).Ref, Relation: "runs"},
		},
	}
	r := neighborhoodToTree(n)
	out := graph.RenderTreeColored(r, graph.TreeColors{})
	if !strings.Contains(out, "(shared)") {
		t.Fatalf("shared runs edge must render (shared) marker, got:\n%s", out)
	}
}
