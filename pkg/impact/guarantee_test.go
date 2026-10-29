package impact

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestAnalyze_UnmetGuaranteesBlock(t *testing.T) {
	events := func(window string) []contract.Interface {
		return []contract.Interface{{Name: "events", Type: contract.InterfaceTypeAsyncAPI, Ref: "e.yaml", Guarantees: &contract.Guarantees{DedupeWindow: window}}}
	}
	orders := svcContract("orders", "1.0.0", contract.Owner{Team: "orders"})
	orders.Interfaces = events("10m")
	dep := contract.Dependency{Name: "orders", Ref: "ghcr.io/x/orders:1.0.0", Required: true, Compatibility: "^1.0.0",
		Requires: []contract.Requirement{{Interface: "events", Guarantees: contract.Guarantees{DedupeWindow: "5m"}}}}
	other := contract.Dependency{Name: "stock", Ref: "ghcr.io/x/stock:1.0.0", Compatibility: "^1.0.0"}
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: mkBundle(orders)},
			{Bundle: mkBundle(svcContract("checkout", "1.0.0", contract.Owner{Team: "web"}, dep, other))},
			{Bundle: mkBundle(svcContract("checkout", "1.1.0", contract.Owner{Team: "web"}, dep))},
		},
		Targets: []fleet.RawTarget{{Scope: "prod", Kind: "deployment", Name: "checkout", Service: "checkout", Compliance: fleet.StatusCompliant}},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	// Edges that carry no declared requirement: an observed one, one from a
	// revision the snapshot does not hold and one from a revision with no contract.
	snap.Revisions["empty"] = &fleet.ContractRevision{}
	snap.Relationships = append(snap.Relationships, []fleet.Relationship{
		{FromService: "checkout", ToService: "orders", To: "orders", Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceObserved, Resolved: true},
		{FromService: "checkout", FromRevision: "unknown", ToService: "orders", To: "orders", Compatibility: "^1.0.0", Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared, Resolved: true},
		{FromService: "checkout", FromRevision: "empty", ToService: "orders", To: "orders", Compatibility: "^1.0.0", Type: fleet.RelationshipDependency, Provenance: fleet.ProvenanceDeclared, Resolved: true},
	}...)

	weaker := svcContract("orders", "1.1.0", contract.Owner{Team: "orders"})
	weaker.Interfaces = events("1m")
	res := Analyze(context.Background(), orders, weaker, fstest.MapFS{}, fstest.MapFS{}, snap, Options{})
	if res.Classification != "BREAKING" || len(res.Consumers) != 1 {
		t.Fatalf("classification %s, consumers %+v", res.Classification, res.Consumers)
	}
	c := res.Consumers[0]
	if c.CompatibilityVerdict != CompatibilityCompatible || len(c.Unmet) != 1 ||
		c.Unmet[0].String() != "checkout requires events dedupeWindow >= 5m from orders, which offers 1m" {
		t.Fatalf("consumer %+v", c)
	}
	if res.Gate != GateBlocked || !strings.Contains(res.GateReason, "or require a guarantee it does not meet: checkout") {
		t.Fatalf("gate %s: %s", res.Gate, res.GateReason)
	}

	stronger := svcContract("orders", "1.1.0", contract.Owner{Team: "orders"})
	stronger.Interfaces = events("1h")
	if res := Analyze(context.Background(), orders, stronger, fstest.MapFS{}, fstest.MapFS{}, snap, Options{}); len(res.Consumers[0].Unmet) != 0 || res.Gate != GateClear {
		t.Fatalf("a stronger guarantee: %+v, gate %s", res.Consumers[0], res.Gate)
	}
}
