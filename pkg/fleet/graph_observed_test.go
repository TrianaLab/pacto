package fleet

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// observedGraphFleet: orders declares payments; billing declares payments and is
// also observed calling it; audit-log is only observed calling payments; reporter
// declares audit-log. payments declares ledger and is only observed calling tracer.
func observedGraphFleet(t *testing.T) *Query {
	t.Helper()
	declares := func(id string, deps ...string) *contract.Bundle {
		b := bundleFor(t, id)
		for _, d := range deps {
			b.Contract.Dependencies = append(b.Contract.Dependencies, contract.Dependency{Name: d, Compatibility: "^1.0.0", Required: true})
		}
		return b
	}
	var revs []RawRevision
	for _, b := range []*contract.Bundle{
		declares("payments", "ledger"), declares("orders", "payments"), declares("billing", "payments"),
		declares("audit-log"), declares("reporter", "audit-log"), declares("ledger"), declares("tracer"),
	} {
		revs = append(revs, RawRevision{Bundle: b, Digest: "sha256:" + b.Contract.Service.Name})
	}
	src := NewMemorySource("local", "local", &Collection{Revisions: revs, Observed: []ObservedEdge{
		{From: "audit-log", To: "payments", Count: 1},
		{From: "billing", To: "payments", Count: 1},
		{From: "payments", To: "tracer", Count: 1},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// nodeProvenance flattens the nodes to "key:provenance" in answer order.
func nodeProvenance(t *testing.T, q *Query, gq GraphQuery) ([]string, *GraphResult) {
	t.Helper()
	res, err := q.Graph(gq)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(res.Nodes))
	for _, n := range res.Nodes {
		out = append(out, string(n.Key)+":"+n.Provenance)
	}
	return out, res
}

func assertNodes(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("nodes = %v, want %v", got, want)
		}
	}
}

func TestGraphDeclaredOnlyByDefault(t *testing.T) {
	got, _ := nodeProvenance(t, observedGraphFleet(t), GraphQuery{Service: "payments", Direction: DirectionDependents})
	assertNodes(t, got, "billing:declared", "orders:declared")
}

func TestGraphIncludesObservedNeighbours(t *testing.T) {
	got, res := nodeProvenance(t, observedGraphFleet(t), GraphQuery{Service: "payments", Direction: DirectionDependents, IncludeObserved: true})
	assertNodes(t, got, "audit-log:observed", "billing:declared+observed", "orders:declared")
	for _, e := range res.Edges {
		if e.FromService == "audit-log" && e.ToService == "payments" && e.Provenance == ProvenanceObserved {
			return
		}
	}
	t.Fatalf("audit-log -> payments missing from edges %+v", res.Edges)
}

func TestGraphRevisionScopedObserved(t *testing.T) {
	q := observedGraphFleet(t)
	rev := q.snap.Services["payments"].Revisions[0]
	got, _ := nodeProvenance(t, q, GraphQuery{Revision: rev, Direction: DirectionDependencies, IncludeObserved: true})
	assertNodes(t, got, "ledger:declared")
	got, _ = nodeProvenance(t, q, GraphQuery{Service: "payments", Direction: DirectionDependencies, IncludeObserved: true})
	assertNodes(t, got, "ledger:declared", "tracer:observed")
	got, _ = nodeProvenance(t, q, GraphQuery{Revision: rev, Direction: DirectionDependents, IncludeObserved: true})
	assertNodes(t, got, "audit-log:observed", "billing:declared+observed", "orders:declared")
}

func TestGraphObservedTransitiveAndCap(t *testing.T) {
	q := observedGraphFleet(t)
	got, res := nodeProvenance(t, q, GraphQuery{Service: "payments", Direction: DirectionDependents, Transitive: true, IncludeObserved: true})
	assertNodes(t, got, "audit-log:observed", "billing:declared+observed", "orders:declared", "reporter:declared")
	if res.Truncated {
		t.Fatal("uncapped walk must not be truncated")
	}
	got, res = nodeProvenance(t, q, GraphQuery{Service: "payments", Direction: DirectionDependents, Transitive: true, IncludeObserved: true, MaxNodes: 1})
	assertNodes(t, got, "audit-log:observed")
	if !res.Truncated {
		t.Fatal("MaxNodes 1 must set Truncated")
	}
}
