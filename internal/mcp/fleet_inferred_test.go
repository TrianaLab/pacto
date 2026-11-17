package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// inferredFleet: orders is declared in shop, cart an inferred Deployment in
// shop and vault an inferred Deployment in secretns.
func inferredFleet(t *testing.T) *fleet.Query {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{
			Targets: []fleet.RawTarget{{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "orders", Service: "orders"}}}),
		fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{
			{Namespace: "shop", Kind: "Deployment", Name: "cart"}, {Namespace: "secretns", Kind: "Deployment", Name: "vault"}}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestMCPFleetSearchDeclaration(t *testing.T) {
	q := inferredFleet(t)
	for decl, want := range map[string][]string{"": {"orders", "k8s:shop/Deployment/cart"}, "inferred": {"k8s:secretns/Deployment/vault", "k8s:shop/Deployment/cart"}, "declared": {"orders"}} {
		args := map[string]any{}
		if decl != "" {
			args["declaration"] = decl
		}
		var out fleet.SearchResult
		decode(t, callHandler(t, fleetSearchTool(), fleetSearchHandler(q), args), &out)
		var names []string
		for _, s := range out.Services {
			names = append(names, s.Name)
			if (s.Declaration == fleet.DeclarationInferred) != strings.HasPrefix(s.Name, "k8s:") {
				t.Errorf("%s is marked %q", s.Name, s.Declaration)
			}
		}
		for _, w := range want {
			if !strings.Contains(strings.Join(names, " "), w) {
				t.Errorf("declaration %q: %v lacks %s", decl, names, w)
			}
		}
		if decl == "declared" && len(names) != 1 {
			t.Errorf("declared lists %v", names)
		}
	}
	if res := callHandler(t, fleetSearchTool(), fleetSearchHandler(q), map[string]any{"declaration": "bogus"}); !res.IsError {
		t.Errorf("an unknown declaration = %s, want an error", resultText(t, res))
	}
}

func TestMCPFleetEntitiesDeclaration(t *testing.T) {
	var out fleet.EntityList
	decode(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(inferredFleet(t), false), map[string]any{"declaration": "inferred"}), &out)
	if out.Total != 2 || out.Entities[0].Declaration != fleet.DeclarationInferred || out.Entities[0].Scope == "" {
		t.Errorf("inferred entities = %+v", out)
	}
}

// TestMCPFleetSearchInferredRestricted: a restricted session never names an
// inferred service in a hidden namespace, or one whose workload kind the
// viewer may not list.
func TestMCPFleetSearchInferredRestricted(t *testing.T) {
	for name, workloads := range map[string]func(ns, kind string) bool{
		"hidden namespace": func(string, string) bool { return true },
		"no workload read": func(ns, _ string) bool { return ns != "shop" },
	} {
		view := func(_ context.Context, _ *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(ns string) bool { return ns != "secretns" }, workloads), nil
		}
		server := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(inferredFleet(t)), Opts: FleetOptions{View: view}}})
		for _, args := range []map[string]any{{}, {"declaration": "inferred"}, {"text": "vault"}} {
			text := resultText(t, callToolOn(t, server, "pacto_fleet_search", args))
			if strings.Contains(text, "vault") || strings.Contains(text, "secretns") {
				t.Errorf("%s: search %v names a hidden inferred service:\n%s", name, args, text)
			}
			if name == "no workload read" && strings.Contains(text, "cart") {
				t.Errorf("search %v names a workload the viewer may not list:\n%s", args, text)
			}
		}
	}
}
