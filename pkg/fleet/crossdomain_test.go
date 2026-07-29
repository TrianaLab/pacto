package fleet

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// crossDomainSnapshot builds a fleet where the SAME service names ("payments",
// "checkout") exist in two different domains, each with its own revision, target,
// dependency and owner. It is the fixture proving domain isolation end to end.
func crossDomainSnapshot(t *testing.T) *FleetSnapshot {
	t.Helper()
	rev := func(domain, name, digest string, deps ...string) RawRevision {
		c := &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "team-" + domain}},
		}
		for _, d := range deps {
			c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, Ref: "oci://x/" + d, Required: true, Compatibility: "^1.0.0"})
		}
		return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: domain, Digest: digest}
	}
	tgt := func(domain, name, service, compliance string) RawTarget {
		return RawTarget{Scope: domain, Kind: "k8s", Name: name, Service: service, Domain: domain, Compliance: compliance, EvidenceAt: ptrTime(fixedNow())}
	}
	col := &Collection{
		Revisions: []RawRevision{
			rev("domain-a", "payments", "sha256:a-pay"),
			rev("domain-a", "checkout", "sha256:a-chk", "payments"),
			rev("domain-b", "payments", "sha256:b-pay"),
			rev("domain-b", "checkout", "sha256:b-chk", "payments"),
		},
		Targets: []RawTarget{
			tgt("domain-a", "payments-app", "payments", StatusNonCompliant),
			tgt("domain-b", "payments-app", "payments", StatusCompliant),
		},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("s", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// TestCrossDomain_Isolation is the §3 acceptance: two same-named services in
// different domains must never cross-contaminate across services, revisions,
// targets, dependents, graph traversal, owners or status.
func TestCrossDomain_Isolation(t *testing.T) {
	snap := crossDomainSnapshot(t)
	aPay := NewServiceKeyDomain("domain-a", "payments")
	bPay := NewServiceKeyDomain("domain-b", "payments")
	aChk := NewServiceKeyDomain("domain-a", "checkout")
	bChk := NewServiceKeyDomain("domain-b", "checkout")

	// Four distinct logical services; same-named ones never merged.
	if len(snap.Services) != 4 {
		t.Fatalf("expected 4 services, got %d", len(snap.Services))
	}
	if snap.Services[aPay] == nil || snap.Services[bPay] == nil || snap.Services[aPay] == snap.Services[bPay] {
		t.Fatal("domain-a/payments and domain-b/payments must be distinct services")
	}

	// A bare "payments" is ambiguous across domains (never silently conflated).
	q := NewQuery(snap)
	if _, err := q.resolveService("payments"); err == nil {
		t.Error("bare 'payments' must be ambiguous across domains")
	} else if _, ok := err.(*AmbiguousError); !ok {
		t.Errorf("want AmbiguousError, got %T", err)
	}

	// Revisions and targets are isolated per domain.
	if snap.Services[aPay].Revisions[0] == snap.Services[bPay].Revisions[0] {
		t.Error("revisions must not be shared across domains")
	}
	if len(snap.Services[aPay].Targets) != 1 || len(snap.Services[bPay].Targets) != 1 {
		t.Fatalf("each payments must have exactly one target")
	}
	if snap.Services[aPay].Targets[0] == snap.Services[bPay].Targets[0] {
		t.Error("targets must not be shared across domains")
	}

	// Status and owners are independent per domain.
	if snap.Services[aPay].Status != StatusNonCompliant || snap.Services[bPay].Status != StatusCompliant {
		t.Errorf("status not isolated: a=%q b=%q", snap.Services[aPay].Status, snap.Services[bPay].Status)
	}
	if snap.Services[aPay].Owner.Team != "team-domain-a" || snap.Services[bPay].Owner.Team != "team-domain-b" {
		t.Errorf("owners not isolated: a=%q b=%q", snap.Services[aPay].Owner.Team, snap.Services[bPay].Owner.Team)
	}

	// Dependents are isolated: domain-a/payments is depended on only by
	// domain-a/checkout, never domain-b/checkout.
	aView, err := q.GetService(string(aPay))
	if err != nil {
		t.Fatal(err)
	}
	if len(aView.Dependents) != 1 || aView.Dependents[0] != aChk {
		t.Errorf("domain-a/payments dependents = %v, want [%s]", aView.Dependents, aChk)
	}

	// Graph dependents traversal is domain-scoped and never leaks a domain-b node.
	g, err := q.Graph(GraphQuery{Service: string(aPay), Direction: DirectionDependents, Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Key != aChk {
		t.Fatalf("domain-a/payments dependents graph = %v, want [%s]", g.Nodes, aChk)
	}
	for _, n := range g.Nodes {
		if n.Key == bChk || n.Key == bPay {
			t.Errorf("domain-a graph leaked a domain-b node: %s", n.Key)
		}
	}
}
