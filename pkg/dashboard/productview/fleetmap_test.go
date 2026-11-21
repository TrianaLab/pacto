package productview

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestToMapFocusLinksEveryNodeAndEdge(t *testing.T) {
	rev := fleet.EntityRef{Kind: fleet.KindRevision, Key: "app@sha256:a"}
	f := &fleet.MapFocusAnswer{
		Focus:   fleet.MapNode{Key: "app", Name: "app"},
		Columns: []fleet.MapColumn{{Hop: 1, Total: 1, Nodes: []fleet.MapNode{{Key: "lib", Name: "lib"}}}},
		Edges: []fleet.MapFocusEdge{
			{NeighborhoodEdge: fleet.NeighborhoodEdge{From: fleet.EntityRef{Kind: fleet.KindService, Key: "app"}, To: fleet.EntityRef{Kind: fleet.KindService, Key: "lib"}}, DeclaredIn: &rev},
			{NeighborhoodEdge: fleet.NeighborhoodEdge{From: fleet.EntityRef{Kind: fleet.KindService, Key: "x"}, To: fleet.EntityRef{Kind: fleet.KindService, Key: "app"}}},
		},
	}
	p := ToMapFocus(f)
	if p.Focus.Href != Entity(fleet.KindService, "app") || p.Columns[0].Nodes[0].Href != Entity(fleet.KindService, "lib") || p.Columns[0].Hop != 1 {
		t.Errorf("nodes = %+v %+v", p.Focus, p.Columns)
	}
	e := p.Edges[0]
	if e.From.Href == "" || e.To.Href != Entity(fleet.KindService, "lib") || e.Href != Graph(fleet.KindService, "lib") || e.DeclaredIn.Href != Entity(fleet.KindRevision, rev.Key) {
		t.Errorf("edge = %+v", e)
	}
	if p.Edges[1].DeclaredIn != nil {
		t.Errorf("an edge no revision declares has %+v", p.Edges[1].DeclaredIn)
	}
}
