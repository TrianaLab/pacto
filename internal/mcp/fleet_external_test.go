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

// externalFleet: app (namespace shop) and vault (namespace secretns) both
// declare Stripe; only vault declares legacy-mq.
func externalFleet(t *testing.T) *fleet.Query {
	t.Helper()
	svc := func(name string, deps ...string) *contract.Contract {
		c := &contract.Contract{PactoVersion: "2.0", Workload: contract.WorkloadService, Service: contract.Service{Name: name, Version: "1.0.0"}}
		for _, d := range deps {
			c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, External: true})
		}
		return c
	}
	k := fleet.SourceKindKubernetes
	src := fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: &contract.Bundle{Contract: svc("app", "Stripe")}, Digest: "sha256:a"},
			{Bundle: &contract.Bundle{Contract: svc("vault", "STRIPE", "legacy-mq")}, Digest: "sha256:v"},
		},
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: k, Name: "app", Service: "app", Compliance: fleet.StatusCompliant},
			{Scope: "secretns", Kind: k, Name: "vault", Service: "vault", Compliance: fleet.StatusCompliant},
		},
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, src)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestMCPFleetGetExternal(t *testing.T) {
	var v fleet.ExternalView
	decode(t, callHandler(t, fleetGetTool(), fleetGetHandler(externalFleet(t)), map[string]any{"service": "external:stripe"}), &v)
	if v.External.Key != "stripe" || v.External.ConsumersTotal != 2 || v.External.Class != "vendor-api" || v.Catalog == nil {
		t.Errorf("view = %+v", v)
	}
	if res := callHandler(t, fleetGetTool(), fleetGetHandler(externalFleet(t)), map[string]any{"service": "external:nobody"}); !res.IsError {
		t.Errorf("an unknown external = %s, want an error result", resultText(t, res))
	}
}

// TestMCPFleetGetExternalRestricted: a signed-in view counts the consumer it
// hides and never names it; an external only hidden services reach is not
// found.
func TestMCPFleetGetExternalRestricted(t *testing.T) {
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(ns string) bool { return ns != "secretns" }), nil
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(externalFleet(t)), Opts: FleetOptions{View: view}}})
	res := callToolOn(t, server, "pacto_fleet_get", map[string]any{"service": "external:stripe"})
	if text := resultText(t, res); strings.Contains(text, "vault") || strings.Contains(text, "STRIPE") {
		t.Errorf("a restricted answer names a hidden consumer:\n%s", text)
	}
	var v fleet.ExternalView
	decode(t, res, &v)
	if v.External.ConsumersTotal != 1 || v.External.HiddenConsumers != 1 {
		t.Errorf("consumers %d hidden %d, want 1 and 1", v.External.ConsumersTotal, v.External.HiddenConsumers)
	}
	if res := callToolOn(t, server, "pacto_fleet_get", map[string]any{"service": "external:legacy-mq"}); !res.IsError {
		t.Errorf("an external only hidden services reach = %s, want not found", resultText(t, res))
	}
}

func TestMCPFleetEntitiesClass(t *testing.T) {
	q := externalFleet(t)
	got := listedKeys(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"kind": "external", "class": "unclassified"}))
	if len(got) != 1 || got[0] != "external/legacy-mq" {
		t.Errorf("class unclassified = %v, want legacy-mq", got)
	}
	if res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"class": "bogus"}); !res.IsError {
		t.Errorf("an unknown class = %s, want an error result", resultText(t, res))
	}
}

func TestMCPFleetSearchExternals(t *testing.T) {
	var res fleet.SearchResult
	decode(t, callHandler(t, fleetSearchTool(), fleetSearchHandler(externalFleet(t)), map[string]any{"text": "stripe"}), &res)
	if res.ExternalsTotal != 1 || len(res.Externals) != 1 || res.Externals[0].Key != "stripe" {
		t.Errorf("externals = %+v (total %d)", res.Externals, res.ExternalsTotal)
	}
	if !strings.Contains(fleetInstructions, "kind external") || !strings.Contains(fleetSearchTool().Description, "externals") {
		t.Error("the instructions or the search tool never mention external systems")
	}
}
