package dashboard

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// inferredSnap holds orders, declared by a Pacto target, and cart, a workload
// no contract describes.
func inferredSnap(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{{Scope: "shop", Kind: "kubernetes", Name: "orders", Service: "orders"}}}),
		fleet.NewMemorySource("k8s-workloads", "kubernetes-workloads", &fleet.Collection{InferredServices: []fleet.RawInferredService{{Namespace: "shop", Kind: "Deployment", Name: "cart"}}}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestContractShareExcludesInferred(t *testing.T) {
	snap := inferredSnap(t)
	if len(snap.Services) != 2 || declaredServices(snap) != 1 {
		t.Errorf("services %d, with a contract %d", len(snap.Services), declaredServices(snap))
	}
}

func TestStatsLinksAndOfferingsExcludeInferred(t *testing.T) {
	snap := inferredSnap(t)
	l := newStatsLinks(fleet.NewQuery(snap))
	if _, ok := l.keys["k8s:shop/Deployment/cart"]; ok || len(l.keys["orders"]) != 1 {
		t.Errorf("links %v", l.keys)
	}
	delete(snap.Services, fleet.NewServiceKey("orders"))
	if providesOffering(snap, "") {
		t.Error("an inferred service provides an offering")
	}
}
