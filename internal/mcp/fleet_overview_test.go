package mcp

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestMCPFleetOverviewTool: pacto_fleet_overview takes no argument and answers
// the query's overview, meta, health and knowledge included.
func TestMCPFleetOverviewTool(t *testing.T) {
	tool := fleetOverviewTool()
	if req := tool.InputSchema.(map[string]any)["required"]; req != nil {
		t.Errorf("required = %v, want none", req)
	}
	q := buildProductQuery(t)
	var ov fleet.Overview
	decode(t, callHandler(t, tool, fleetOverviewHandler(q, false), map[string]any{}), &ov)
	if ov.Meta.SnapshotID != q.SnapshotID() || ov.Meta.AsOf.IsZero() {
		t.Errorf("meta = %+v", ov.Meta)
	}
	if ov.Summary.Knowledge.Services != 3 || ov.Summary.OperationalHealth != q.Overview().Summary.OperationalHealth {
		t.Errorf("summary = %+v", ov.Summary)
	}
	text := resultText(t, callHandler(t, tool, fleetOverviewHandler(q, false), map[string]any{}))
	for _, key := range []string{`"meta"`, `"operationalHealth"`, `"knowledge"`} {
		if !strings.Contains(text, key) {
			t.Errorf("answer lacks %s:\n%s", key, text)
		}
	}
}

// TestMCPFleetOverviewHrefsOnlyWithDashboard: the entry points link to the
// dashboard exactly when its URL is configured.
func TestMCPFleetOverviewHrefsOnlyWithDashboard(t *testing.T) {
	q := buildProductQuery(t)
	linked := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(q), Opts: FleetOptions{DashboardURL: "https://dash.example/"}}})
	plain := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(q)}})
	if text := resultText(t, callToolOn(t, linked, "pacto_fleet_overview", map[string]any{})); !strings.Contains(text, `"href": "https://dash.example/#/fleet/attention`) {
		t.Errorf("linked overview lacks a dashboard href:\n%s", text)
	}
	if text := resultText(t, callToolOn(t, plain, "pacto_fleet_overview", map[string]any{})); strings.Contains(text, "href") {
		t.Errorf("overview without a dashboard URL carries an href:\n%s", text)
	}
}

// TestMCPFleetOverviewRestricted: a signed-in view counts only the targets it
// may see, never names a hidden one and says it hid something.
func TestMCPFleetOverviewRestricted(t *testing.T) {
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(ns string) bool { return ns != "secretns" }), nil
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(secretFleet(t)), Opts: FleetOptions{View: view}}})
	res := callToolOn(t, server, "pacto_fleet_overview", map[string]any{})
	text := resultText(t, res)
	for _, hidden := range []string{"vaultsvc", "secretteam", "secretdomain", "secretns"} {
		if strings.Contains(text, hidden) {
			t.Errorf("overview names %q:\n%s", hidden, text)
		}
	}
	var ov fleet.Overview
	decode(t, res, &ov)
	if ov.Summary.Targets != 1 || ov.Summary.NonCompliantTargets != 0 {
		t.Errorf("a restricted overview counts only visible targets: %+v", ov.Summary)
	}
	if !slices.ContainsFunc(ov.Meta.Limitations, func(l fleet.Limitation) bool { return l.Code == fleet.LimitationHiddenByPermissions }) {
		t.Errorf("limitations %+v lack %s", ov.Meta.Limitations, fleet.LimitationHiddenByPermissions)
	}
}

// TestFleetInstructionsExplainFailingVersusUnknown: the fleet instructions
// point at the overview and keep failing and unknown apart.
func TestFleetInstructionsExplainFailingVersusUnknown(t *testing.T) {
	for _, want := range []string{
		"Use pacto_fleet_overview first for organization-wide questions",
		"unavailable (failing: its own health check reports it is not healthy, finding HEALTH_FAILING)",
		"unknown (Pacto cannot tell)",
		"notObserved (the revision declares no health capability",
		"degraded (always 0 today)",
		"summary.ownership is consistent (one declared owner), conflicting, unowned (none declared) or unknown",
		"summary.knowledge partitions services into described (nothing left out), withGaps and unknown",
		"(noSummary, noDomain, noInterfaceDocs, noReadiness, notDeployed, unverifiedCapability)",
		"Unknown is never a pass",
	} {
		if !strings.Contains(fleetInstructions, want) {
			t.Errorf("fleetInstructions lack %q", want)
		}
	}
	desc := fleetOverviewTool().Description
	for _, key := range []string{"summary.operationalHealth", "available", "unavailable", "notObserved", "degraded",
		"summary.knowledge", "described", "withGaps", "knowledge.gaps", "summary.ownership", "consistent", "conflicting", "unowned"} {
		if !strings.Contains(desc, key) {
			t.Errorf("pacto_fleet_overview description lacks the key %q", key)
		}
	}
}
