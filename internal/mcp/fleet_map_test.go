package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetgen"
	"github.com/trianalab/pacto/v4/pkg/dashboard/productview"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func mapFleet(t *testing.T) *fleet.Query {
	t.Helper()
	now := time.Date(2026, 11, 20, 9, 40, 0, 0, time.UTC)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource("fleetgen", fleet.SourceKindKubernetes, fleetgen.Collection(400, 1, now)))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestFleetMapTool(t *testing.T) {
	q := mapFleet(t)
	for _, opts := range []FleetOptions{{}, {DashboardURL: "https://dash.example/"}} {
		server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(q), Opts: opts}})
		var m fleet.FleetMap
		decode(t, callToolOn(t, server, "pacto_fleet_map", map[string]any{"group_by": "environment"}), &m)
		want, _ := q.Map(fleet.MapQuery{GroupBy: fleet.MapGroupByEnvironment})
		if m.GroupBy != fleet.MapGroupByEnvironment || m.Totals != want.Totals || m.EnvironmentOrder != "name" || m.Meta.SnapshotID == "" {
			t.Errorf("map = %s %+v order %s", m.GroupBy, m.Totals, m.EnvironmentOrder)
		}
		var page fleet.MapNodes
		decode(t, callToolOn(t, server, "pacto_fleet_map", map[string]any{"group": "payments", "limit": 2, "offset": 1, "environment": "dev"}), &page)
		if page.Group != "payments" || len(page.Nodes) != 2 || page.Offset != 1 || !page.Truncated {
			t.Errorf("page = %+v", page)
		}
		var f productview.ProductMapFocus
		decode(t, callToolOn(t, server, "pacto_fleet_map", map[string]any{"focus": "platform-00000", "hops": 2}), &f)
		if f.Focus.Name != "platform-00000" || len(f.Columns) != 4 {
			t.Errorf("focus = %+v", f.MapFocusAnswer)
		}
		if linked := opts.DashboardURL != ""; linked != strings.HasPrefix(f.Focus.Href, "https://dash.example/#/fleet/services/") {
			t.Errorf("linked %v: focus href %q", linked, f.Focus.Href)
		}
	}
}

func TestFleetMapToolRejectsBadHops(t *testing.T) {
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(mapFleet(t)), Opts: FleetOptions{}}})
	for _, args := range []map[string]any{
		{"focus": "platform-00000", "hops": 3},
		{"focus": "platform-00000", "hops": -1},
		{"hops": 2},
		{"focus": "platform-00000", "group": "payments"},
		{"focus": "platform-00000", "group_by": "team"},
		{"offset": 5},
		{"group": "ghost"},
		{"focus": "ghost"},
		{"group_by": "planet"},
	} {
		if res := callToolOn(t, server, "pacto_fleet_map", args); !res.IsError {
			t.Errorf("%v: want an error result, got %s", args, resultText(t, res))
		}
	}
}
