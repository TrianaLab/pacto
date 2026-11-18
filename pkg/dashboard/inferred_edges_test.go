package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// inferredEdgesServer serves shop's cart Deployment, taken over by the cart
// Pacto three minutes ago, and a kernel that saw cart's old workload and
// worker (no contract) call ledger (no contract either).
func inferredEdgesServer(t *testing.T) string {
	t.Helper()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { return new(now.Add(-d)) }
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{{
			Scope: "shop", Kind: "kubernetes", Name: "cart", Service: "cart",
			Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "cart"}, CreatedAt: ago(3 * time.Minute)}}}),
		fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{InferredServices: []fleet.RawInferredService{
			{Namespace: "shop", Kind: "Deployment", Name: "cart", CreatedAt: ago(72 * time.Hour)},
			{Namespace: "shop", Kind: "Deployment", Name: "worker"},
			{Namespace: "shop", Kind: "StatefulSet", Name: "ledger"}}}),
		fleet.NewMemorySource("kernel", "observation", &fleet.Collection{Observed: []fleet.ObservedEdge{
			{From: "k8s:shop/Deployment/worker", To: "k8s:shop/StatefulSet/ledger", Count: 4}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	t.Cleanup(cancel)
	return base
}

func TestNeighborhoodAPIInferredEdge(t *testing.T) {
	base := inferredEdgesServer(t)
	worker := string(fleet.NewServiceKey("k8s:shop/Deployment/worker"))
	var nb struct {
		Edges []struct {
			To                 struct{ Key string }
			Provenance         string
			Inferred, Observed bool
			Count              int
			Difference         string
			ObservationSources struct{ Total int }
		}
	}
	getJSON(t, base+"/api/fleet/neighborhood?kind=service&key="+url.QueryEscape(worker)+"&views=expected,observed", http.StatusOK, &nb)
	if len(nb.Edges) != 1 {
		t.Fatalf("edges %+v", nb.Edges)
	}
	e := nb.Edges[0]
	if e.To.Key != string(fleet.NewServiceKey("k8s:shop/StatefulSet/ledger")) || e.Provenance != fleet.ProvenanceInferred || !e.Inferred || e.Observed ||
		e.Count != 4 || e.Difference != "" || e.ObservationSources.Total != 1 {
		t.Errorf("edge %+v", e)
	}
}

func TestEntityAPIReplacedKey(t *testing.T) {
	base := inferredEdgesServer(t)
	var d struct {
		Entity       struct{ Key string }
		ReplacedFrom *fleet.Replacement
		Service      struct {
			Replaces      []fleet.Replacement
			ReplacesTotal int
		}
	}
	getJSON(t, base+"/api/fleet/entities/service?key="+url.QueryEscape(string(fleet.NewServiceKey(cartName))), http.StatusOK, &d)
	if d.Entity.Key != "cart" || d.ReplacedFrom == nil || d.ReplacedFrom.From != fleet.NewServiceKey(cartName) || d.ReplacedFrom.Target != fleet.NewTargetKey("shop", "kubernetes", "cart") {
		t.Errorf("entity %+v replacedFrom %+v", d.Entity, d.ReplacedFrom)
	}
	if len(d.Service.Replaces) != 1 || d.Service.ReplacesTotal != 1 {
		t.Errorf("replaces %+v total %d", d.Service.Replaces, d.Service.ReplacesTotal)
	}
}
