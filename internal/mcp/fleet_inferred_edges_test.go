package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// inferredEdgesFleet is shop's cart Deployment taken over by the cart Pacto
// three minutes ago, and a kernel that saw worker call ledger, two workloads
// no contract describes.
func inferredEdgesFleet(t *testing.T) *fleet.Query {
	t.Helper()
	ago := func(d time.Duration) *time.Time { return new(fixedNow.Add(-d)) }
	k8s := fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{{
		Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "cart", Service: "cart",
		Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "cart"}, CreatedAt: ago(3 * time.Minute),
	}}})
	workloads := fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{
		{Namespace: "shop", Kind: "Deployment", Name: "cart", CreatedAt: ago(72 * time.Hour)},
		{Namespace: "shop", Kind: "Deployment", Name: "worker"},
		{Namespace: "shop", Kind: "StatefulSet", Name: "ledger"},
	}})
	kernel := fleet.NewMemorySource("kernel", "observation", &fleet.Collection{Observed: []fleet.ObservedEdge{
		{From: "k8s:shop/Deployment/worker", To: "k8s:shop/StatefulSet/ledger", Count: 4},
	}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, k8s, workloads, kernel)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func TestFleetGraphToolInferredEdges(t *testing.T) {
	res := callHandler(t, fleetGraphTool(), fleetGraphHandler(inferredEdgesFleet(t)), map[string]any{"service": "k8s:shop/Deployment/worker"})
	var out fleet.GraphResult
	decode(t, res, &out)
	ledger := fleet.NewServiceKey("k8s:shop/StatefulSet/ledger")
	if len(out.Nodes) != 1 || out.Nodes[0].Key != ledger || out.Nodes[0].Provenance != fleet.ProvenanceInferred {
		t.Errorf("nodes %+v, want ledger reached by an inferred edge", out.Nodes)
	}
	if len(out.Edges) != 1 || out.Edges[0].Provenance != fleet.ProvenanceInferred || out.Edges[0].ObservedCount != 4 || len(out.Edges[0].ObservedSources) != 1 {
		t.Errorf("edges %+v, want the inferred call with its source", out.Edges)
	}
}

func TestFleetGetToolReplacedFrom(t *testing.T) {
	res := callHandler(t, fleetGetTool(), fleetGetHandler(inferredEdgesFleet(t)), map[string]any{"service": "k8s:shop/Deployment/cart"})
	var out fleet.ServiceView
	decode(t, res, &out)
	r := out.ReplacedFrom
	if out.Service == nil || out.Service.Key != "cart" || r == nil || r.From != fleet.NewServiceKey("k8s:shop/Deployment/cart") || r.To != "cart" || r.Link != "pacto-target" || r.At == nil {
		t.Fatalf("service %+v replacedFrom %+v", out.Service, r)
	}
	if len(out.Replaces) != 1 || out.ReplacesTotal != 1 {
		t.Errorf("replaces %+v total %d", out.Replaces, out.ReplacesTotal)
	}
}
