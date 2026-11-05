package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// hrefFleet is checkout (business domain payments) depending on ledger, with
// one target of unknown compliance so status has an item.
func hrefFleet(t *testing.T) *fleet.Query {
	t.Helper()
	mk := func(name string, deps ...string) *contract.Contract {
		var d []contract.Dependency
		for _, dep := range deps {
			d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true})
		}
		return &contract.Contract{PactoVersion: "2.5", Dependencies: d,
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "money"}, Domain: "payments"}}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }},
		fleet.NewMemorySource("local", "local", &fleet.Collection{
			Revisions: []fleet.RawRevision{
				{Bundle: &contract.Bundle{Contract: mk("checkout", "ledger")}, Digest: "sha256:c"},
				{Bundle: &contract.Bundle{Contract: mk("ledger")}, Digest: "sha256:l"},
			},
			Targets: []fleet.RawTarget{{Scope: "prod", Kind: "k8s", Name: "checkout", Service: "checkout", Compliance: fleet.StatusUnknown}},
		}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// TestDashboardHrefs: with a dashboard URL every fleet answer links each
// entity to its dashboard page, built by the dashboard's own route builder;
// without one no answer carries an href anywhere.
func TestDashboardHrefs(t *testing.T) {
	const base = "https://dash.example/#"
	calls := []struct {
		tool string
		args map[string]any
		want []string
	}{
		{"pacto_fleet_entities", map[string]any{"kind": "domain"}, []string{`"href": "` + base + `/fleet/domains/payments"`}},
		{"pacto_fleet_entity", map[string]any{"kind": "service", "key": "checkout"}, []string{`"href": "` + base + `/fleet/services/checkout"`, `"href": "` + base + `/fleet/owners/team:money"`}},
		{"pacto_fleet_attention", map[string]any{}, []string{`"href": "` + base + `/fleet/`}},
		{"pacto_fleet_search", map[string]any{}, []string{`"href": "` + base + `/fleet/services/checkout"`, `"href": "` + base + `/fleet/services/ledger"`}},
		{"pacto_fleet_graph", map[string]any{"service": "checkout"}, []string{`"href": "` + base + `/fleet/services/ledger"`, `"rootHref": "` + base + `/fleet/services/checkout"`}},
		{"pacto_fleet_status", map[string]any{}, []string{`"href": "` + base + `/fleet/`}},
		{"pacto_fleet_get", map[string]any{"service": "checkout"}, nil},
	}
	q := hrefFleet(t)
	linked := NewFleetServer("test", staticFleet(q), nil, FleetOptions{DashboardURL: "https://dash.example/"})
	plain := NewFleetServer("test", staticFleet(q), nil, FleetOptions{})
	for _, c := range calls {
		text := resultText(t, callToolOn(t, linked, c.tool, c.args))
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: missing %s in\n%s", c.tool, w, text)
			}
		}
		if strings.Contains(text, `"href": "/`) {
			t.Errorf("%s: an href lacks the dashboard base:\n%s", c.tool, text)
		}
		if text := resultText(t, callToolOn(t, plain, c.tool, c.args)); strings.Contains(text, "href") {
			t.Errorf("%s without a dashboard URL carries an href:\n%s", c.tool, text)
		}
	}
	// An error answer passes through untouched.
	if res := callToolOn(t, linked, "pacto_fleet_get", map[string]any{"service": "ghost"}); !res.IsError {
		t.Errorf("unknown service must stay an error, got %s", resultText(t, res))
	}
	if got := addLegacyHrefs("pacto_fleet_search", []any{}); len(got.([]any)) != 0 {
		t.Errorf("a non-object answer must pass through, got %v", got)
	}
}
