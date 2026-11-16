package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// externalFleet: checkout in shop and billing in secretns both call stripe;
// checkout's pod spec names it too.
func externalFleet(t *testing.T) *fleet.Query {
	t.Helper()
	col := &fleet.Collection{
		ExternalUses: []fleet.RawExternalUse{{Service: "checkout", Namespace: "shop", Identity: "stripe", Class: "vendor-api", Confidence: "likely",
			Kind: "env", Where: "deployment/checkout env STRIPE_API_BASE", Host: "api.stripe.com", Entry: "stripe"}},
		Observed: []fleet.ObservedEdge{{From: "checkout", To: "api.stripe.com", Count: 3}, {From: "billing", To: "api.stripe.com", Count: 1}},
	}
	for name, ns := range map[string]string{"checkout": "shop", "billing": "secretns"} {
		digest := "sha256:" + name
		col.Revisions = append(col.Revisions, fleet.RawRevision{Digest: digest, Bundle: &contract.Bundle{Contract: &contract.Contract{
			PactoVersion: "2.4", Workload: contract.WorkloadService, Service: contract.Service{Name: name, Version: "1.0.0"}}}})
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: name, Service: name, Digest: digest})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestFleetGetExternalInferredSources(t *testing.T) {
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(externalFleet(t))}})
	text := resultText(t, callToolOn(t, server, "pacto_fleet_entity", map[string]any{"kind": "external", "key": "stripe"}))
	for _, want := range []string{`"inferredSources"`, `"where": "deployment/checkout env STRIPE_API_BASE"`, `"host": "api.stripe.com"`,
		`"confidence": "likely"`, `"entry": "stripe"`, `"observedSources"`, `"billing"`} {
		if !strings.Contains(text, want) {
			t.Errorf("external entity lacks %s:\n%s", want, text)
		}
	}
	explain := resultText(t, callToolOn(t, server, "pacto_fleet_explain", map[string]any{"subject": "external:stripe"}))
	if !strings.Contains(explain, "inferred from env deployment/checkout env STRIPE_API_BASE of checkout (likely, host api.stripe.com, entry stripe") {
		t.Errorf("explain lacks the inferred source:\n%s", explain)
	}
}

func TestFleetGetExternalRestrictedCountsHidden(t *testing.T) {
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(ns string) bool { return ns != "secretns" }), nil
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(externalFleet(t)), Opts: FleetOptions{View: view}}})
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"pacto_fleet_entity", map[string]any{"kind": "external", "key": "stripe"}},
		{"pacto_fleet_explain", map[string]any{"subject": "external:stripe"}},
	} {
		text := resultText(t, callToolOn(t, server, c.tool, c.args))
		if strings.Contains(text, "billing") || strings.Contains(text, "secretns") {
			t.Errorf("%s names the hidden caller:\n%s", c.tool, text)
		}
		if c.tool == "pacto_fleet_entity" && (!strings.Contains(text, `"hiddenConsumers": 1`) || !strings.Contains(text, `"inferredSources"`)) {
			t.Errorf("entity does not count the hidden caller or lost the visible one:\n%s", text)
		}
	}
}
