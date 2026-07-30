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

// The section 3 acceptance is split across focused tests (each well under the standalone
// gocyclo -over 15 gate, which scans _test.go): two same-named services in
// different domains must never cross-contaminate across services, revisions,
// targets, dependents, graph traversal, owners or status.

func TestCrossDomain_DistinctServices(t *testing.T) {
	snap := crossDomainSnapshot(t)
	aPay := NewServiceKeyDomain("domain-a", "payments")
	bPay := NewServiceKeyDomain("domain-b", "payments")

	if len(snap.Services) != 4 {
		t.Fatalf("expected 4 services, got %d", len(snap.Services))
	}
	if snap.Services[aPay] == nil || snap.Services[bPay] == nil || snap.Services[aPay] == snap.Services[bPay] {
		t.Fatal("domain-a/payments and domain-b/payments must be distinct services")
	}

	// A bare "payments" is ambiguous across domains (never silently conflated).
	_, err := NewQuery(snap).resolveService("payments")
	if err == nil {
		t.Fatal("bare 'payments' must be ambiguous across domains")
	}
	if _, ok := err.(*AmbiguousError); !ok {
		t.Errorf("want AmbiguousError, got %T", err)
	}
}

func TestCrossDomain_RevisionsAndTargetsIsolated(t *testing.T) {
	snap := crossDomainSnapshot(t)
	a := snap.Services[NewServiceKeyDomain("domain-a", "payments")]
	b := snap.Services[NewServiceKeyDomain("domain-b", "payments")]

	if a.Revisions[0] == b.Revisions[0] {
		t.Error("revisions must not be shared across domains")
	}
	if len(a.Targets) != 1 || len(b.Targets) != 1 {
		t.Fatalf("each payments must have exactly one target: a=%v b=%v", a.Targets, b.Targets)
	}
	if a.Targets[0] == b.Targets[0] {
		t.Error("targets must not be shared across domains")
	}
}

func TestCrossDomain_StatusAndOwnersIsolated(t *testing.T) {
	snap := crossDomainSnapshot(t)
	a := snap.Services[NewServiceKeyDomain("domain-a", "payments")]
	b := snap.Services[NewServiceKeyDomain("domain-b", "payments")]

	if a.Status != StatusNonCompliant || b.Status != StatusCompliant {
		t.Errorf("status not isolated: a=%q b=%q", a.Status, b.Status)
	}
	if a.Owner.Team != "team-domain-a" || b.Owner.Team != "team-domain-b" {
		t.Errorf("owners not isolated: a=%q b=%q", a.Owner.Team, b.Owner.Team)
	}
}

func TestCrossDomain_DependentsIsolated(t *testing.T) {
	snap := crossDomainSnapshot(t)
	q := NewQuery(snap)
	aPay := NewServiceKeyDomain("domain-a", "payments")
	aChk := NewServiceKeyDomain("domain-a", "checkout")

	// Dependents are isolated: domain-a/payments is depended on only by
	// domain-a/checkout, never domain-b/checkout.
	aView, err := q.GetService(string(aPay))
	if err != nil {
		t.Fatal(err)
	}
	if len(aView.Dependents) != 1 || aView.Dependents[0] != aChk {
		t.Fatalf("domain-a/payments dependents = %v, want [%s]", aView.Dependents, aChk)
	}

	// Graph dependents traversal is domain-scoped and never leaks a domain-b node.
	g, err := q.Graph(GraphQuery{Service: string(aPay), Direction: DirectionDependents, Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Key != aChk {
		t.Fatalf("domain-a/payments dependents graph = %v, want [%s]", g.Nodes, aChk)
	}
	if _, name := ParseServiceKey(g.Nodes[0].Key); name != "checkout" {
		t.Errorf("leaked or wrong node: %s", g.Nodes[0].Key)
	}
}
