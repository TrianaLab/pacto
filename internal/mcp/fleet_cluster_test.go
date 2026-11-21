package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const (
	clusterEU = "9f1c4e2a-0000-4000-8000-00000000000a"
	clusterUS = "77ad31b0-0000-4000-8000-00000000000b"
)

// twoClusterFleet holds payments in two clusters and orders in one.
func twoClusterFleet(t *testing.T) *fleet.Query {
	t.Helper()
	wl := func(name string) fleet.RawTarget {
		return fleet.RawTarget{Scope: name, Kind: "kubernetes", Name: name, Service: name, Compliance: fleet.StatusCompliant, EvidenceAt: &fixedNow}
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }},
		fleet.NewMemorySource("eu", fleet.SourceKindKubernetes, &fleet.Collection{
			Cluster: &fleet.Cluster{ID: clusterEU, Name: "eu-prod", NameSource: fleet.ClusterNameFromContext},
			Targets: []fleet.RawTarget{wl("payments"), wl("orders")},
		}),
		fleet.NewMemorySource("us", fleet.SourceKindKubernetes, &fleet.Collection{
			Cluster: &fleet.Cluster{ID: clusterUS, Name: "us-prod", NameSource: fleet.ClusterNameFromContext},
			Targets: []fleet.RawTarget{wl("payments")},
		}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestMCPFleetGetAmbiguousTargetKeyListsCandidates(t *testing.T) {
	legacy := string(fleet.NewTargetKey("payments", "kubernetes", "payments"))
	res := callHandler(t, fleetGetTool(), fleetGetHandler(twoClusterFleet(t)), map[string]any{"target": legacy})
	if !res.IsError {
		t.Fatal("an ambiguous target key is an error result, never a guess")
	}
	text := resultText(t, res)
	for _, want := range []string{
		"eu-prod / payments / payments", string(fleet.NewClusterTargetKey(clusterEU, "payments", "kubernetes", "payments")),
		"us-prod / payments / payments", string(fleet.NewClusterTargetKey(clusterUS, "payments", "kubernetes", "payments")),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("error text lacks %q:\n%s", want, text)
		}
	}
}

func TestMCPFleetEntityLegacyKeyAnswersCanonicalKey(t *testing.T) {
	var out fleet.EntityDetail
	decode(t, callHandler(t, fleetEntityTool(), fleetEntityHandler(twoClusterFleet(t), false, nil, fleet.NoTools),
		map[string]any{"kind": "target", "key": string(fleet.NewTargetKey("orders", "kubernetes", "orders"))}), &out)
	if want := string(fleet.NewClusterTargetKey(clusterEU, "orders", "kubernetes", "orders")); out.Entity.Key != want {
		t.Errorf("entity key = %q, want the canonical %q", out.Entity.Key, want)
	}
	if out.Target == nil || out.Target.Cluster == nil || out.Target.Cluster.Name != "eu-prod" {
		t.Errorf("target detail carries its cluster: %+v", out.Target)
	}
}

// pacto_fleet_entities cluster keeps the targets of that cluster.
func TestFleetEntitiesClusterArgument(t *testing.T) {
	res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(twoClusterFleet(t), false), map[string]any{"kind": "target", "cluster": clusterUS})
	text := resultText(t, res)
	if res.IsError || !strings.Contains(text, string(fleet.NewClusterTargetKey(clusterUS, "payments", "kubernetes", "payments"))) || strings.Contains(text, clusterEU+"/") {
		t.Errorf("targets of us-prod:\n%s", text)
	}
}
