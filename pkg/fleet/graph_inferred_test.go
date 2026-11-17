package fleet

import (
	"slices"
	"testing"
)

func graphOf(t *testing.T, q *Query, gq GraphQuery) *GraphResult {
	t.Helper()
	g, err := q.Graph(gq)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func reachedAs(g *GraphResult, key ServiceKey) string {
	for _, n := range g.Nodes {
		if n.Key == key {
			return n.Provenance
		}
	}
	return ""
}

func TestGraphFollowsInferredWithObserved(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, ledgerName), call("orders", cartName)))
	cart, ledger := NewServiceKey(cartName), NewServiceKey(ledgerName)
	g := graphOf(t, q, GraphQuery{Service: cartName, IncludeObserved: true})
	if got := reachedAs(g, ledger); got != ProvenanceInferred {
		t.Errorf("ledger reached as %q, want inferred: %+v", got, g.Nodes)
	}
	if !slices.ContainsFunc(g.Edges, func(r Relationship) bool {
		return r.FromService == cart && r.ToService == ledger && r.Provenance == ProvenanceInferred && r.ObservedCount == 3
	}) {
		t.Errorf("no inferred edge cart -> ledger: %+v", g.Edges)
	}
	d := graphOf(t, q, GraphQuery{Service: cartName, Direction: DirectionDependents, IncludeObserved: true})
	orders := acmeService(q, "orders").Key
	if got := reachedAs(d, orders); got != ProvenanceInferred {
		t.Errorf("orders reached as %q, want inferred: %+v", got, d.Nodes)
	}
	if !slices.ContainsFunc(d.Edges, func(r Relationship) bool { return r.FromService == orders && r.ToService == cart }) {
		t.Errorf("no inferred edge orders -> cart among dependents: %+v", d.Edges)
	}
}

func TestGraphWithoutObservedHasNoInferredEdges(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, ledgerName), call("orders", cartName)))
	g := graphOf(t, q, GraphQuery{Service: cartName})
	if len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Errorf("a declared-only graph reached %+v over %+v", g.Nodes, g.Edges)
	}
	d := graphOf(t, q, GraphQuery{Service: "orders", Transitive: true})
	if slices.ContainsFunc(d.Edges, func(r Relationship) bool { return r.Provenance == ProvenanceInferred && r.ObservedCount > 0 }) ||
		slices.ContainsFunc(d.Nodes, func(n GraphNode) bool { return n.Key == NewServiceKey(cartName) }) {
		t.Errorf("impact's call shape sees an inferred edge: %+v %+v", d.Nodes, d.Edges)
	}
}

func TestNeighborhoodInferredServiceEdge(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, ledgerName), call(cartName, "api.stripe.com")))
	n, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: cartName, Views: ParseViews("")})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(n.Edges, func(e NeighborhoodEdge) bool { return e.To.Key == string(NewServiceKey(ledgerName)) })
	if i < 0 {
		t.Fatalf("no cart -> ledger edge on the default views: %+v", n.Edges)
	}
	if e := n.Edges[i]; !runtimeInferredEdge(e, 3, "kernel") {
		t.Errorf("edge %+v", e)
	}
	stripe := slices.IndexFunc(n.Edges, func(e NeighborhoodEdge) bool { return e.To.Kind == KindExternal })
	if stripe < 0 || n.Edges[stripe].Provenance != ProvenanceInferred || n.Edges[stripe].Count != 3 {
		t.Errorf("no inferred cart -> stripe edge: %+v", n.Edges)
	}
	if !slices.Contains(n.Nodes[0].Expansions, DirectionDependencies) {
		t.Errorf("cart advertises no dependencies: %+v", n.Nodes[0])
	}
}

func TestNeighborhoodExpectedViewHasNoInferredEdge(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, ledgerName)))
	exp, err := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: cartName, Views: []KnowledgeView{ViewExpected}})
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Edges) != 0 || len(exp.Nodes) != 1 {
		t.Errorf("the expected view reaches an inferred neighbour: %+v %+v", exp.Nodes, exp.Edges)
	}
}

func TestConfigurationInferredEdgeNeedsDifferences(t *testing.T) {
	e := NeighborhoodEdge{Inferred: true, Provenance: ProvenanceInferred}
	if edgeMatchesViews(e, []KnowledgeView{ViewExpected, ViewObserved}) {
		t.Error("a configuration-inferred edge matched the default views")
	}
	projectEdgeForViews(&e, []KnowledgeView{ViewExpected, ViewObserved})
	if e.Inferred {
		t.Error("a configuration-inferred edge kept Inferred without differences")
	}
	r := NeighborhoodEdge{Inferred: true, Count: 2, ObservationSources: ObservationSourcesPreview{Items: []ObservedSourceStat{{Source: "kernel", Count: 2}}}}
	projectEdgeForViews(&r, []KnowledgeView{ViewExpected})
	if r.Inferred || r.Count != 0 {
		t.Errorf("an expected-only projection kept a runtime-inferred edge: %+v", r)
	}
}

// runtimeInferredEdge reports whether e is an inferred edge the source alone
// saw count times, with no declared-versus-observed verdict.
func runtimeInferredEdge(e NeighborhoodEdge, count int, source string) bool {
	return e.Provenance == ProvenanceInferred && e.Inferred && !e.Observed && !e.Expected && e.Count == count && e.Difference == "" &&
		e.ObservationSources.Total == 1 && e.ObservationSources.Items[0].Source == source && !e.Stale
}
