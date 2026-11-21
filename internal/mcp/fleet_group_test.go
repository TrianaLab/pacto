package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const groupedCart = "k8s:shop/Deployment/cart"

// groupedFleet: cart and worker are the Argo CD Application cart, whose
// repository Pacto read from the Application in argocd; ledger is ungrouped.
func groupedFleet(t *testing.T) *fleet.Query {
	t.Helper()
	argo := &fleet.InferredGroup{Rule: "argocd", Name: "cart", From: "label argocd.argoproj.io/instance"}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
		InferredServices: []fleet.RawInferredService{
			{Namespace: "shop", Kind: "Deployment", Name: "cart", Group: argo, Repositories: []fleet.InferredRepository{
				{URL: "https://github.com/acme/shop", From: "Application argocd/cart spec.source.repoURL", Namespace: "argocd", Kind: "Application"}}},
			{Namespace: "shop", Kind: "Deployment", Name: "worker", Group: argo},
			{Namespace: "shop", Kind: "StatefulSet", Name: "ledger"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestMCPFleetEntitiesGroup(t *testing.T) {
	var out fleet.EntityList
	decode(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(groupedFleet(t), false), map[string]any{"group": "argocd//cart"}), &out)
	if out.Total != 2 {
		t.Fatalf("group entities = %+v", out)
	}
	for _, e := range out.Entities {
		if e.Kind != fleet.KindService || e.Group == nil || e.Group.Key != "argocd//cart" {
			t.Errorf("row outside the group: %+v", e)
		}
	}
	if res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(groupedFleet(t), false), map[string]any{"group": "argocd//cart", "kind": "owner"}); !res.IsError {
		t.Errorf("group with kind owner = %s, want an error", resultText(t, res))
	}
}

func TestMCPFleetGetCarriesGroup(t *testing.T) {
	text := resultText(t, callHandler(t, fleetGetTool(), fleetGetHandler(groupedFleet(t)), map[string]any{"service": groupedCart}))
	for _, want := range []string{`"group"`, `"key":"argocd//cart"`, `"label":"Argo CD Application cart"`, `"repositories"`, `"url":"https://github.com/acme/shop"`} {
		if !strings.Contains(strings.ReplaceAll(text, `": "`, `":"`), want) {
			t.Errorf("get lacks %s:\n%s", want, text)
		}
	}
}

// TestMCPFleetEntityRepositoryRestricted: a signed-in session that may not
// list Applications in argocd sees the group but never the URL, and reads why.
func TestMCPFleetEntityRepositoryRestricted(t *testing.T) {
	view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(_, _ string) bool { return true }, func(_, ns, kind string) bool { return ns != "argocd" || kind != "Application" }), nil
	}
	server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(groupedFleet(t)), Opts: FleetOptions{View: view}}})
	for tool, args := range map[string]map[string]any{
		"pacto_fleet_entity":   {"kind": "service", "key": string(fleet.NewServiceKey(groupedCart))},
		"pacto_fleet_get":      {"service": groupedCart},
		"pacto_fleet_entities": {"group": "argocd//cart"},
	} {
		text := resultText(t, callToolOn(t, server, tool, args))
		if strings.Contains(text, "github.com/acme/shop") {
			t.Errorf("%s names the hidden repository:\n%s", tool, text)
		}
		if !strings.Contains(text, "argocd//cart") {
			t.Errorf("%s lost the group:\n%s", tool, text)
		}
		if tool != "pacto_fleet_entities" && !strings.Contains(text, "hidden by your permissions") {
			t.Errorf("%s never says why the repository is missing:\n%s", tool, text)
		}
	}
}
