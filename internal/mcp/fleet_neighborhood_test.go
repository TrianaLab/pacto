package mcp

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// neighborhoodFleet: orders declares payments; batch is observed calling
// payments and declares nothing.
func neighborhoodFleet(t *testing.T) *fleet.Query {
	t.Helper()
	mk := func(name, team string, deps ...string) *contract.Contract {
		var d []contract.Dependency
		for _, dep := range deps {
			d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true})
		}
		return &contract.Contract{PactoVersion: "2.5", Dependencies: d,
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}}}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }},
		fleet.NewMemorySource("local", "local", &fleet.Collection{
			Revisions: []fleet.RawRevision{
				{Bundle: &contract.Bundle{Contract: mk("orders", "commerce", "payments")}, Digest: "sha256:o"},
				{Bundle: &contract.Bundle{Contract: mk("payments", "billing")}, Digest: "sha256:p"},
				{Bundle: &contract.Bundle{Contract: mk("batch", "ops")}, Digest: "sha256:b"},
			},
			Observed: []fleet.ObservedEdge{{From: "batch", To: "payments", Count: 4}},
		}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func neighborLabels(nb fleet.Neighborhood) map[string]string {
	out := map[string]string{}
	for _, n := range nb.Nodes {
		if !n.Focus {
			out[n.Ref.Label] = n.Owner
		}
	}
	return out
}

func TestFleetNeighborhoodTool(t *testing.T) {
	server := NewFleetServer("test", staticFleet(neighborhoodFleet(t)), nil, FleetOptions{})

	var nb fleet.Neighborhood
	decode(t, callToolOn(t, server, "pacto_fleet_neighborhood", map[string]any{"service": "payments", "direction": "dependents"}), &nb)
	if nb.RequestedFocus.Key != "payments" || nb.Direction != fleet.DirectionDependents {
		t.Errorf("focus %+v direction %s", nb.RequestedFocus, nb.Direction)
	}
	if got := neighborLabels(nb); got["orders"] != "commerce" || got["batch"] != "ops" || len(got) != 2 {
		t.Errorf("dependents with no views = %v, want orders (commerce) and the observed batch (ops)", got)
	}
	if !slices.Equal(nb.Views, []fleet.KnowledgeView{fleet.ViewExpected, fleet.ViewObserved}) {
		t.Errorf("default views = %v", nb.Views)
	}

	var search fleet.SearchResult
	decode(t, callToolOn(t, server, "pacto_fleet_search", map[string]any{}), &search)
	if nb.Meta.SnapshotID == "" || nb.Meta.SnapshotID != search.Meta.SnapshotID {
		t.Errorf("snapshotId %q, search's %q", nb.Meta.SnapshotID, search.Meta.SnapshotID)
	}

	var obs fleet.Neighborhood
	decode(t, callToolOn(t, server, "pacto_fleet_neighborhood", map[string]any{"service": "payments", "direction": "dependents", "views": " observed, ", "depth": 1, "max_nodes": 10}), &obs)
	if got := neighborLabels(obs); len(got) != 1 || got["batch"] != "ops" {
		t.Errorf("views=observed dependents = %v, want only batch", got)
	}
	for _, e := range obs.Edges {
		if !e.Observed {
			t.Errorf("views=observed kept an unobserved edge %+v", e)
		}
	}

	for _, args := range []map[string]any{
		{"service": "ghost"},
		{"service": "payments", "direction": "sideways"},
		{"service": "payments", "views": "everything"},
	} {
		if res := callToolOn(t, server, "pacto_fleet_neighborhood", args); !res.IsError {
			t.Errorf("%v: want an error result, got %s", args, resultText(t, res))
		}
	}
}

func TestSplitViews(t *testing.T) {
	for in, want := range map[string][]fleet.KnowledgeView{
		"":                       {fleet.ViewExpected, fleet.ViewObserved},
		" , ":                    {fleet.ViewExpected, fleet.ViewObserved},
		"differences":            {fleet.ViewDifferences},
		" expected , observed ,": {fleet.ViewExpected, fleet.ViewObserved},
	} {
		if got := SplitViews(in); !slices.Equal(got, want) {
			t.Errorf("SplitViews(%q) = %v, want %v", in, got, want)
		}
	}
}
