package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

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

// TestDashboardHrefsUnderView: under a restricted view no answer, href
// included, names the hidden service, its owner or its business domain.
func TestDashboardHrefsUnderView(t *testing.T) {
	mk := func(name, team, domain string, deps ...string) *contract.Contract {
		var d []contract.Dependency
		for _, dep := range deps {
			d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true})
		}
		return &contract.Contract{PactoVersion: "2.5", Dependencies: d,
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: team}, Domain: domain}}
	}
	k := fleet.SourceKindKubernetes
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }},
		fleet.NewMemorySource("k8s", k, &fleet.Collection{
			Revisions: []fleet.RawRevision{
				{Bundle: &contract.Bundle{Contract: mk("checkout", "money", "payments", "vault")}, Digest: "sha256:c"},
				{Bundle: &contract.Bundle{Contract: mk("vault", "hushteam", "hushdomain")}, Digest: "sha256:v"},
			},
			Targets: []fleet.RawTarget{
				{Scope: "shop", Kind: k, Name: "checkout", Service: "checkout", Compliance: fleet.StatusUnknown},
				{Scope: "hushns", Kind: k, Name: "vault", Service: "vault", Compliance: fleet.StatusNonCompliant},
			},
			Observed: []fleet.ObservedEdge{{From: "vault", To: "checkout", Count: 1}},
		}))
	if err != nil {
		t.Fatal(err)
	}
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(ns string) bool { return ns == "shop" }), nil
	}
	server := NewFleetServer("test", staticFleet(fleet.NewQuery(snap)), nil, FleetOptions{View: view, DashboardURL: "https://dash.example/"})
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"pacto_fleet_entities", map[string]any{}},
		{"pacto_fleet_entity", map[string]any{"kind": "service", "key": "checkout"}},
		{"pacto_fleet_attention", map[string]any{}},
		{"pacto_fleet_search", map[string]any{}},
		{"pacto_fleet_graph", map[string]any{"service": "checkout", "direction": "dependents"}},
		{"pacto_fleet_graph", map[string]any{"service": "checkout"}},
		{"pacto_fleet_status", map[string]any{}},
	} {
		text := resultText(t, callToolOn(t, server, c.tool, c.args))
		if !strings.Contains(text, "https://dash.example/#/fleet/") {
			t.Errorf("%s %v: no href in\n%s", c.tool, c.args, text)
		}
		// checkout's own contract declares vault, so a dependencies walk may name
		// it, but never link to it.
		hiddenMarks := []string{"services/vault", "hushteam", "hushdomain", "hushns"}
		if c.args["direction"] != nil || c.tool != "pacto_fleet_graph" {
			hiddenMarks = append(hiddenMarks, "vault")
		}
		for _, hidden := range hiddenMarks {
			if strings.Contains(text, hidden) {
				t.Errorf("%s %v names hidden %q:\n%s", c.tool, c.args, hidden, text)
			}
		}
	}
}
