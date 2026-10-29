package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestResolve_RecordsUnmetGuarantees(t *testing.T) {
	needs := func(name, ref string, g contract.Guarantees) contract.Dependency {
		return contract.Dependency{Name: name, Ref: ref, Compatibility: "^1.0.0", Requires: []contract.Requirement{{Interface: "events", Guarantees: g}}}
	}
	fetcher := &mockFetcher{contracts: map[string]*contract.Contract{
		"orders": {
			Service:      contract.Service{Name: "orders", Version: "1.0.0"},
			Interfaces:   []contract.Interface{{Name: "events", Guarantees: &contract.Guarantees{DedupeWindow: "1m"}}},
			Dependencies: []contract.Dependency{needs("stock", "stock", contract.Guarantees{Idempotent: true})},
		},
		"stock": {Service: contract.Service{Name: "stock", Version: "1.0.0"}},
	}}
	root := &contract.Contract{
		Service: contract.Service{Name: "checkout", Version: "1.0.0"},
		Dependencies: []contract.Dependency{
			needs("orders", "orders", contract.Guarantees{DedupeWindow: "5m"}),
			needs("orders-again", "orders", contract.Guarantees{DedupeWindow: "1m"}),
			needs("absent", "absent", contract.Guarantees{DedupeWindow: "5m"}),
			{Name: "plain", Ref: "stock", Compatibility: "^1.0.0"},
		},
	}
	res := Resolve(context.Background(), root, fetcher)
	edges := res.Root.Dependencies
	if len(edges[0].Unmet) != 1 || edges[0].Unmet[0].String() != "checkout requires events dedupeWindow >= 5m from orders, which offers 1m" {
		t.Errorf("orders edge: %+v", edges[0].Unmet)
	}
	if len(edges[1].Unmet) != 0 {
		t.Errorf("a met requirement on a shared edge: %+v", edges[1].Unmet)
	}
	if edges[2].Error == "" || len(edges[2].Unmet) != 0 {
		t.Errorf("an unresolved provider records no verdict: %+v", edges[2])
	}
	if len(edges[3].Unmet) != 0 {
		t.Errorf("no requirements: %+v", edges[3].Unmet)
	}
	orders := edges[0].Node
	if edges[0].Shared {
		orders = edges[1].Node
	}
	if u := orders.Dependencies[0].Unmet; len(u) != 1 || u[0].Consumer != "orders" || u[0].Guarantee != "interface" {
		t.Errorf("transitive edge: %+v", u)
	}

	out := RenderTreeColored(res, TreeColors{})
	if !strings.Contains(out, "(refused: checkout requires events dedupeWindow >= 5m from orders, which offers 1m)") {
		t.Errorf("render:\n%s", out)
	}
}
