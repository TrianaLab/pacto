package tui

import (
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

func TestNeighborhoodToTreeBuildsFromTheFocusNode(t *testing.T) {
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true), nbNode("b", false)},
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
	// mark the repeat edge Shared, which is the only thing that stops recursion.
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true), nbNode("b", false)},
		Edges: []fleet.NeighborhoodEdge{nbEdge("a", "b"), nbEdge("b", "a")},
	}
	r := neighborhoodToTree(n)
	back := r.Root.Dependencies[0].Node.Dependencies
	if len(back) != 1 {
		t.Fatalf("want the back edge preserved, got %d", len(back))
	}
	if !back[0].Shared {
		t.Fatal("the cycle-closing edge is not marked Shared; rendering it will recurse forever")
	}
	if back[0].Node != nil {
		t.Fatal("a Shared edge must not carry a child node")
	}
	// The real proof: rendering terminates.
	done := make(chan string, 1)
	go func() { done <- graph.RenderTreeColored(r, graph.TreeColors{}) }()
	select {
	case out := <-done:
		if !strings.Contains(out, "a") {
			t.Fatalf("render lost the root:\n%s", out)
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
	if len(r.Root.Dependencies) != 1 || !r.Root.Dependencies[0].Shared {
		t.Fatalf("a self loop must be a Shared leaf, got %+v", r.Root.Dependencies)
	}
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

func TestNeighborhoodToTreeMarksOutsideNodesAsShared(t *testing.T) {
	// An edge to a node not in the bounded projection should be a Shared leaf
	// with an error, not a nil pointer crash.
	n := &fleet.Neighborhood{
		Nodes: []fleet.NeighborhoodNode{nbNode("a", true)},
		Edges: []fleet.NeighborhoodEdge{nbEdge("a", "b")}, // b is not in Nodes
	}
	r := neighborhoodToTree(n)
	if len(r.Root.Dependencies) != 1 {
		t.Fatalf("want one edge, got %d", len(r.Root.Dependencies))
	}
	e := r.Root.Dependencies[0]
	if !e.Shared {
		t.Fatal("an edge to an outside node must be Shared")
	}
	if e.Error == "" {
		t.Fatal("an edge to an outside node must carry an error message")
	}
	if e.Node != nil {
		t.Fatal("a Shared edge must not carry a child node")
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
