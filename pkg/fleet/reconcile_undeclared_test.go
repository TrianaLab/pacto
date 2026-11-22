package fleet

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestUndeclaredOnlyObservationKeepsDeclaredInsufficient(t *testing.T) {
	a := bundleFor(t, "a")
	a.Contract.Dependencies = []contract.Dependency{{Name: "b", Compatibility: "^1.0.0", Required: true}}
	var revs []RawRevision
	for _, b := range []*contract.Bundle{a, bundleFor(t, "b"), bundleFor(t, "c"), bundleFor(t, "x"), bundleFor(t, "y")} {
		revs = append(revs, RawRevision{Bundle: b, Digest: "sha256:" + b.Contract.Service.Name})
	}
	declared := NewMemorySource("defs", "local", &Collection{Revisions: revs})
	undeclaredOnly := NewMemorySource("k8s", SourceKindKubernetes, &Collection{
		ObservedUndeclaredOnly: true,
		Observed:               []ObservedEdge{{From: "a", To: "c", Count: 1}},
	})
	reconciliationOfAB := func(sources ...Source) string {
		t.Helper()
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
		if err != nil {
			t.Fatal(err)
		}
		if r := relFrom(snap.Relationships, "a", "b"); r != nil {
			return r.Reconciliation
		}
		t.Fatal("no declared a->b relationship")
		return ""
	}
	if got := reconciliationOfAB(declared, undeclaredOnly); got != ReconciliationInsufficient {
		t.Fatalf("undeclared-only observation: a->b = %q, want insufficient", got)
	}
	regular := NewMemorySource("traces", "observation", &Collection{Observed: []ObservedEdge{{From: "x", To: "y", Count: 1}}})
	if got := reconciliationOfAB(declared, undeclaredOnly, regular); got != ReconciliationDeclaredNotObserved {
		t.Fatalf("regular observation: a->b = %q, want declared-not-observed", got)
	}
	// The same edge from both kinds of source: the regular one still counts.
	shared := NewMemorySource("traces", "observation", &Collection{Observed: []ObservedEdge{{From: "a", To: "c", Count: 1}}})
	if got := reconciliationOfAB(declared, undeclaredOnly, shared); got != ReconciliationDeclaredNotObserved {
		t.Fatalf("shared edge: a->b = %q, want declared-not-observed", got)
	}
}

// miraLikeSnapshot builds declared a->b and a->d plus an undeclared-only source of
// kind mira that witnessed a->b and a->c.
func miraLikeSnapshot(t *testing.T) *FleetSnapshot {
	t.Helper()
	a := bundleFor(t, "a")
	a.Contract.Dependencies = []contract.Dependency{
		{Name: "b", Compatibility: "^1.0.0", Required: true},
		{Name: "d", Compatibility: "^1.0.0", Required: true},
	}
	var revs []RawRevision
	for _, b := range []*contract.Bundle{a, bundleFor(t, "b"), bundleFor(t, "c"), bundleFor(t, "d")} {
		revs = append(revs, RawRevision{Bundle: b, Digest: "sha256:" + b.Contract.Service.Name})
	}
	declared := NewMemorySource("defs", "local", &Collection{Revisions: revs})
	mira := NewMemorySource("mira", SourceKindMira, &Collection{
		ObservedUndeclaredOnly: true,
		Observed:               []ObservedEdge{{From: "a", To: "b", Count: 3}, {From: "a", To: "c", Count: 1}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, declared, mira)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestWitnessedDeclaredEdgeMatches(t *testing.T) {
	got := map[string]string{}
	for _, r := range miraLikeSnapshot(t).Relationships {
		if r.Provenance == ProvenanceDeclared {
			got[string(r.FromService)+"->"+r.To] = r.Reconciliation
		}
	}
	if got["a->b"] != ReconciliationMatched || got["a->d"] != ReconciliationInsufficient {
		t.Fatalf("declared reconciliation = %v, want a->b matched and a->d insufficient", got)
	}
}

func TestMiraObservationNeverMakesDeclaredUnobserved(t *testing.T) {
	for _, r := range miraLikeSnapshot(t).Relationships {
		if r.Reconciliation == ReconciliationDeclaredNotObserved {
			t.Fatalf("%s->%s is declared-not-observed from a partial-coverage source", r.FromService, r.ToService)
		}
	}
}

// receiverSource is a caution-only source like the OTLP receiver.
func receiverSource(edges ...ObservedEdge) Source {
	return NewMemorySource("otlp-receiver", SourceKindOTLPReceiver, &Collection{
		Advisory: true, ObservedUndeclaredOnly: true, ObservedCautionOnly: true, Observed: edges,
	})
}

func TestCautionOnlyEdgeNeverMatches(t *testing.T) {
	a := bundleFor(t, "a")
	a.Contract.Dependencies = []contract.Dependency{{Name: "b", Compatibility: "^1.0.0", Required: true}}
	declared := NewMemorySource("defs", "local", &Collection{Revisions: []RawRevision{
		{Bundle: a, Digest: "sha256:a"}, {Bundle: bundleFor(t, "b"), Digest: "sha256:b"},
	}})
	build := func(sources ...Source) *FleetSnapshot {
		t.Helper()
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	snap := build(declared, receiverSource(ObservedEdge{From: "a", To: "b", Count: 5}))
	if r := relFrom(snap.Relationships, "a", "b"); r == nil || r.Reconciliation != ReconciliationInsufficient {
		t.Fatalf("a->b = %+v, want insufficient", r)
	}
	if st := sourceState(t, snap, "otlp-receiver"); !st.CautionOnly || !st.Advisory {
		t.Fatalf("receiver state %+v", st)
	}
	// The observed relationship stays caution-only; another source that saw
	// it too corroborates.
	for _, r := range snap.Relationships {
		if r.Provenance == ProvenanceObserved && !snap.CautionOnly(r) {
			t.Fatalf("observed %+v is not caution-only", r)
		}
	}
	mira := NewMemorySource("mira", SourceKindMira, &Collection{ObservedUndeclaredOnly: true, Observed: []ObservedEdge{{From: "a", To: "b", Count: 1}}})
	if r := relFrom(build(declared, receiverSource(ObservedEdge{From: "a", To: "b", Count: 5}), mira).Relationships, "a", "b"); r.Reconciliation != ReconciliationMatched {
		t.Fatalf("with a corroborating source a->b = %q", r.Reconciliation)
	}
	if snap.CautionOnly(Relationship{}) {
		t.Fatal("a relationship no source saw is caution-only")
	}
}

// A receiver-only edge on a declared dependency is no corroboration in the
// neighborhood or the graph either; a corroborating source makes it one, and
// an undeclared receiver edge still reads observed.
func TestCautionOnlyEdgeNeverMatchesInGraphs(t *testing.T) {
	a := bundleFor(t, "a")
	a.Contract.Dependencies = []contract.Dependency{{Name: "b", Compatibility: "^1.0.0", Required: true}}
	declared := NewMemorySource("defs", "local", &Collection{Revisions: []RawRevision{
		{Bundle: a, Digest: "sha256:a"}, {Bundle: bundleFor(t, "b"), Digest: "sha256:b"}, {Bundle: bundleFor(t, "c"), Digest: "sha256:c"},
	}})
	ab, ac, prov := edgesAndGraphLabel(t, declared, receiverSource(ObservedEdge{From: "a", To: "b", Count: 5}, ObservedEdge{From: "a", To: "c", Count: 2}))
	if ab.Difference == DifferenceMatched || ab.Observed || ab.Provenance != ProvenanceDeclared || ab.Count != 5 {
		t.Errorf("receiver-only a->b = %+v", ab)
	}
	if prov != ProvenanceDeclared {
		t.Errorf("graph labels receiver-only a->b %q", prov)
	}
	if ac.Difference != DifferenceObservedNotExpected || !ac.Observed {
		t.Errorf("undeclared receiver a->c = %+v", ac)
	}
	mira := NewMemorySource("mira", SourceKindMira, &Collection{ObservedUndeclaredOnly: true, Observed: []ObservedEdge{{From: "a", To: "b", Count: 1}}})
	ab, _, prov = edgesAndGraphLabel(t, declared, receiverSource(ObservedEdge{From: "a", To: "b", Count: 5}), mira)
	if ab.Difference != DifferenceMatched || prov != ProvenanceDeclaredObserved {
		t.Errorf("corroborated a->b = %+v, graph %q", ab, prov)
	}
}

// edgesAndGraphLabel builds a snapshot from sources and returns a's a|b and
// a|c neighborhood edges plus the single label the graph gives the a-b pair
// from both ends.
func edgesAndGraphLabel(t *testing.T, sources ...Source) (NeighborhoodEdge, NeighborhoodEdge, string) {
	t.Helper()
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	nb, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: "a", Direction: DirectionBoth, Views: allViews()})
	if err != nil {
		t.Fatal(err)
	}
	var ab, ac NeighborhoodEdge
	for _, e := range nb.Edges {
		switch e.ID {
		case "a|b":
			ab = e
		case "a|c":
			ac = e
		}
	}
	return ab, ac, abGraphLabel(t, q)
}

// abGraphLabel returns the provenance the graph gives the a-b pair, failing
// unless both directions agree.
func abGraphLabel(t *testing.T, q *Query) string {
	t.Helper()
	provs := map[string]bool{}
	for _, gq := range []GraphQuery{{Service: "a", Direction: DirectionDependencies}, {Service: "b", Direction: DirectionDependents}} {
		gq.IncludeObserved = true
		g, err := q.Graph(gq)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range g.Nodes {
			if n.Depth == 1 && (n.Key == "a" || n.Key == "b") {
				provs[n.Provenance] = true
			}
		}
	}
	if len(provs) != 1 {
		t.Fatalf("graph labels %v", provs)
	}
	for p := range provs {
		return p
	}
	return ""
}
