package fleet

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func mustFocus(t *testing.T, q *Query, fq MapFocusQuery) *MapFocusAnswer {
	t.Helper()
	f, err := q.MapFocus(fq)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func columnOf(f *MapFocusAnswer, hop int) MapColumn {
	for _, c := range f.Columns {
		if c.Hop == hop {
			return c
		}
	}
	return MapColumn{}
}

func edgeOf(f *MapFocusAnswer, from, to string) *MapFocusEdge {
	for i := range f.Edges {
		if f.Edges[i].From.Key == string(NewServiceKey(from)) && f.Edges[i].To.Key == string(NewServiceKey(to)) {
			return &f.Edges[i]
		}
	}
	return nil
}

func nodeNames(ns []MapNode) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Name
	}
	return out
}

// hubFleet is hub with 23 callers c00..c22, each called by its own cc00..cc22;
// c17 runs non-compliant.
func hubFleet(t *testing.T) *Query {
	t.Helper()
	svcs := []mapSvc{{name: "hub", domain: "core"}}
	var targets []RawTarget
	for i := range 23 {
		c, cc := fmt.Sprintf("c%02d", i), fmt.Sprintf("cc%02d", i)
		svcs = append(svcs, mapSvc{name: c, domain: "edge", deps: []string{"hub"}}, mapSvc{name: cc, domain: "edge", deps: []string{c}})
	}
	targets = append(targets, mapTarget("edge", "c17", "prod"))
	q := mapFleet(t, svcs, targets)
	for _, tr := range q.snap.Targets {
		tr.Compliance = StatusNonCompliant
	}
	return q
}

func TestMapFocusColumnsWorstFirst(t *testing.T) {
	f := mustFocus(t, hubFleet(t), MapFocusQuery{Service: "hub"})
	callers := columnOf(f, -1)
	if len(f.Columns) != 2 || callers.Total != 23 || len(callers.Nodes) != MaxMapFocusPerColumn || f.Hops != 1 {
		t.Fatalf("columns %+v", f.Columns)
	}
	want := []string{"c17", "c00", "c01", "c02", "c03", "c04", "c05", "c06", "c07"}
	if got := nodeNames(callers.Nodes); !slices.Equal(got, want) {
		t.Errorf("callers %v", got)
	}
	if callees := columnOf(f, 1); callees.Total != 0 || len(callees.Nodes) != 0 {
		t.Errorf("callees %+v", callees)
	}
	if f.Focus.Name != "hub" || f.Group != "core" || f.Meta.SnapshotID == "" {
		t.Errorf("focus %+v group %s", f.Focus, f.Group)
	}
	if few := mustFocus(t, hubFleet(t), MapFocusQuery{Service: "hub", PerColumn: 3}); len(columnOf(few, -1).Nodes) != 3 {
		t.Errorf("per column 3: %+v", few.Columns)
	}
}

func TestMapFocusTwoHopsExpandsShownOnly(t *testing.T) {
	f := mustFocus(t, hubFleet(t), MapFocusQuery{Service: "hub", Hops: 2})
	far := columnOf(f, -2)
	if len(f.Columns) != 4 || f.Columns[0].Hop != -2 || f.Columns[3].Hop != 2 || far.Total != MaxMapFocusPerColumn {
		t.Fatalf("columns %+v", f.Columns)
	}
	if !slices.Contains(nodeNames(far.Nodes), "cc17") || slices.Contains(nodeNames(far.Nodes), "cc22") {
		t.Errorf("far callers %v", nodeNames(far.Nodes))
	}
}

func TestMapFocusEdgesFollowCallDirection(t *testing.T) {
	svcs := slices.Clone(mapServices)
	svcs[2].deps = []string{"payments"} // ledger calls payments back: a cycle
	q := mapFleet(t, svcs, nil)
	f := mustFocus(t, q, MapFocusQuery{Service: "payments", Hops: 2})
	if got := nodeNames(columnOf(f, -1).Nodes); !slices.Equal(got, []string{"ledger", "orders"}) {
		t.Fatalf("callers %v", got)
	}
	// payments -> ledger goes from the focus back to a caller: left out.
	if len(f.Edges) != 2 || edgeOf(f, "ledger", "payments") == nil || edgeOf(f, "orders", "payments") == nil {
		t.Errorf("edges %+v", f.Edges)
	}
	// orders -> ledger joins two callers: a same-column edge, left out.
	if e := edgeOf(f, "orders", "ledger"); e != nil {
		t.Errorf("same column %+v", e)
	}
}

// declareOrdersPayments sets what orders' declaration of payments requires.
func declareOrdersPayments(q *Query, compatibility string, requires []contract.Requirement) {
	for i, rel := range q.snap.Relationships {
		if rel.Provenance == ProvenanceDeclared && rel.FromService == NewServiceKey("orders") && rel.ToService == NewServiceKey("payments") {
			q.snap.Relationships[i].Compatibility, q.snap.Relationships[i].Requires = compatibility, requires
		}
	}
}

func TestMapFocusEdgeInterfaceAndRange(t *testing.T) {
	q := defaultMapFleet(t)
	q.currentRevision(q.snap.Services[NewServiceKey("ledger")]).Contract.Interfaces = []contract.Interface{{Name: "books"}}
	q.currentRevision(q.snap.Services[NewServiceKey("payments")]).Contract.Interfaces = []contract.Interface{{Name: "charge"}, {Name: "refund"}}
	declareOrdersPayments(q, "^1.0.0", []contract.Requirement{{}, {Interface: "refund"}})
	f := mustFocus(t, q, MapFocusQuery{Service: "payments"})
	in, out := edgeOf(f, "orders", "payments"), edgeOf(f, "payments", "ledger")
	if in.Interface != "refund" || in.Range != "^1.0.0" || in.DeclaredIn == nil || in.DeclaredIn.Kind != KindRevision || in.RunningVersion != "1.0.0" {
		t.Errorf("orders -> payments %+v", in)
	}
	// ledger offers one interface and payments requires none: that one.
	if out.Interface != "books" || out.Range != "" {
		t.Errorf("payments -> ledger %+v", out)
	}
	// Two interfaces and no requirement: no interface label.
	declareOrdersPayments(q, "", nil)
	q.members = &sync.Map{} // the snapshot changed under the query: drop its page builds
	if e := edgeOf(mustFocus(t, q, MapFocusQuery{Service: "orders"}), "orders", "payments"); e.Interface != "" {
		t.Errorf("no interface %+v", e)
	}
}

func TestMapFocusEdgeRunningVersionMixed(t *testing.T) {
	q := defaultMapFleet(t)
	pay := q.snap.Services[NewServiceKey("payments")]
	other := *q.currentRevision(pay)
	other.Key, other.Version = "payments@2", "2.0.0"
	q.snap.Revisions[other.Key] = &other
	for _, tr := range q.snap.Targets {
		if tr.Service == "payments" && tr.Environment == "staging" {
			tr.ContractRevision = other.Key
		}
	}
	if e := edgeOf(mustFocus(t, q, MapFocusQuery{Service: "orders"}), "orders", "payments"); e.RunningVersion != "mixed" {
		t.Errorf("edge %+v", e)
	}
}

func TestMapFocusBreakingEdgeReason(t *testing.T) {
	q := defaultMapFleet(t)
	pay := q.currentRevision(q.snap.Services[NewServiceKey("payments")])
	pay.Contract.Breaks = []contract.Break{{Consumers: []string{"orders"}, Reason: "removes PUT /cache"}}
	e := edgeOf(mustFocus(t, q, MapFocusQuery{Service: "payments"}), "orders", "payments")
	if e.Problem == nil || e.Problem.Code != "BREAKING" || e.Problem.Severity != severityError || e.Problem.Reason != "removes PUT /cache" {
		t.Errorf("problem %+v", e.Problem)
	}
}

func TestMapFocusOutOfRangeReason(t *testing.T) {
	q := mapFleet(t, mapServices, []RawTarget{mapTarget("shop", "payments", "prod")})
	for i, rel := range q.snap.Relationships {
		if rel.Provenance == ProvenanceDeclared && rel.ToService == NewServiceKey("payments") {
			q.snap.Relationships[i].RangeStatus, q.snap.Relationships[i].Compatibility = RangeUnsatisfied, "^2.3"
		}
	}
	e := edgeOf(mustFocus(t, q, MapFocusQuery{Service: "payments"}), "orders", "payments")
	if e.Problem == nil || e.Problem.Code != LimitationRangeUnsatisfied || e.Problem.Reason != "accepts ^2.3 · runs 1.0.0" {
		t.Errorf("problem %+v", e.Problem)
	}
	// A provider Pacto sees running nowhere: the range alone.
	if r := q.rangeReason("^2.3", NewServiceKey("ledger")); r != "accepts ^2.3" {
		t.Errorf("reason %q", r)
	}
}

func TestMapFocusCrossEnvironmentEdge(t *testing.T) {
	e := edgeOf(mustFocus(t, defaultMapFleet(t), MapFocusQuery{Service: "orders"}), "orders", "payments")
	if e.Problem == nil || e.Problem.Code != codeCrossEnvironmentCall || e.Problem.Severity != severityError || e.EnvironmentUnknown {
		t.Errorf("edge %+v problem %+v", e, e.Problem)
	}
}

func TestMapFocusEvidenceAndAsOf(t *testing.T) {
	q := mapFleet(t, mapServices,
		[]RawTarget{mapTarget("shop", "orders", "prod"), mapTarget("shop", "payments", "prod"), mapTarget("vault", "ledger", "prod")},
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "prod"), time.Minute),
		seen(wid("", "shop", "payments", "prod"), wid("", "vault", "ledger", "prod"), 72*time.Hour))
	f := mustFocus(t, q, MapFocusQuery{Service: "payments"})
	fresh, old := edgeOf(f, "orders", "payments"), edgeOf(f, "payments", "ledger")
	if fresh.ObservationSources.Total == 0 || fresh.LastSeen == nil || fresh.Stale || fresh.Difference != DifferenceMatched || fresh.Provenance != ProvenanceDeclaredObserved {
		t.Errorf("fresh %+v", fresh)
	}
	if !old.Stale || old.LastSeen == nil {
		t.Errorf("old %+v", old)
	}
	if !f.Meta.AsOf.Equal(q.snap.GeneratedAt) {
		t.Errorf("asOf %v", f.Meta.AsOf)
	}
}

func TestMapFocusRestrictedColumnTotals(t *testing.T) {
	q := defaultMapFleet(t).Restrict(func(_, ns string) bool { return ns != "vault" }, nil)
	f := mustFocus(t, q, MapFocusQuery{Service: "payments", Hops: 2})
	if c := columnOf(f, 1); c.Total != 0 || len(c.Nodes) != 0 {
		t.Errorf("callees %+v", c)
	}
	if c := columnOf(f, -1); c.Total != 1 {
		t.Errorf("callers %+v", c)
	}
	var nf *NotFoundError
	if _, err := q.MapFocus(MapFocusQuery{Service: "ledger"}); !errors.As(err, &nf) {
		t.Errorf("hidden focus %v", err)
	}
}

func TestMapFocusSkipsHiddenRelationships(t *testing.T) {
	q := defaultMapFleet(t).Restrict(func(_, ns string) bool { return ns != "vault" }, nil)
	hidden := slices.ContainsFunc(q.snap.Relationships, func(r Relationship) bool { return r.Hidden })
	f := mustFocus(t, q, MapFocusQuery{Service: "orders"})
	for _, e := range f.Edges {
		if e.To.Key == string(NewServiceKey("ledger")) || e.From.Key == string(NewServiceKey("ledger")) {
			t.Errorf("edge %+v", e)
		}
	}
	if !hidden || len(f.Edges) != 1 {
		t.Errorf("hidden %v edges %+v", hidden, f.Edges)
	}
}

func TestMapFocusCrossEnvironmentUnknownSaysSo(t *testing.T) {
	q := defaultMapFleet(t)
	for i := range q.snap.Relationships {
		q.snap.Relationships[i].envPairs = nil
	}
	e := edgeOf(mustFocus(t, q, MapFocusQuery{Service: "orders"}), "orders", "payments")
	if !e.EnvironmentUnknown || e.Problem != nil {
		t.Errorf("edge %+v problem %+v", e, e.Problem)
	}
	// A declared edge Pacto never observed says nothing about environments.
	if l := edgeOf(mustFocus(t, q, MapFocusQuery{Service: "orders"}), "orders", "ledger"); l.EnvironmentUnknown {
		t.Errorf("unobserved %+v", l)
	}
}

func TestMapFocusInferredService(t *testing.T) {
	q := mapFleet(t, mapServices, []RawTarget{mapTarget("shop", "payments", "prod")},
		seen(K8sCaller("", "shop", "Deployment", "cart"), wid("", "shop", "payments", "prod"), time.Minute))
	var cart *ServiceRecord
	for _, s := range q.snap.Services {
		if s.Inferred() {
			cart = s
		}
	}
	f := mustFocus(t, q, MapFocusQuery{Service: string(cart.Key)})
	callees := columnOf(f, 1)
	if !f.Focus.Inferred || f.Focus.Match != MatchObservedOnly || len(callees.Nodes) != 1 || callees.Nodes[0].Name != "payments" {
		t.Fatalf("focus %+v callees %+v", f.Focus, callees)
	}
	if len(f.Edges) != 1 || !f.Edges[0].Inferred || f.Edges[0].Problem != nil {
		t.Errorf("edges %+v", f.Edges)
	}
}

func TestMapFocusBadQueries(t *testing.T) {
	q := defaultMapFleet(t)
	for _, fq := range []MapFocusQuery{{Service: "orders", Hops: 3}, {Service: "orders", Hops: -1}, {Service: "orders", PerColumn: -1}} {
		var iq *InvalidQueryError
		if _, err := q.MapFocus(fq); !errors.As(err, &iq) {
			t.Errorf("%+v: %v", fq, err)
		}
	}
	if f := mustFocus(t, q, MapFocusQuery{Service: "orders", PerColumn: 50}); f == nil {
		t.Error("per column above the maximum is capped")
	}
	q.snap.Services[NewServiceKey("idle")].Template = true
	var nf *NotFoundError
	if _, err := q.MapFocus(MapFocusQuery{Service: "idle"}); !errors.As(err, &nf) {
		t.Errorf("golden path %v", err)
	}
}

func TestMapFocusClaimAndRankFallbacks(t *testing.T) {
	q := defaultMapFleet(t)
	me := &mapEdge{from: NewServiceKey("orders"), e: &NeighborhoodEdge{DeclaredClaims: DeclaredClaimsPreview{Items: []DeclaredClaim{{SourceRevision: "old", Compatibility: "^1"}}}}}
	if c := q.focusClaim(me); c == nil || c.SourceRevision != "old" {
		t.Errorf("claim %+v", c)
	}
	if severityRankOf("") <= severityRankOf(severityInfo) {
		t.Error("no item ranks after info")
	}
}

// A pair whose target the view may not see raises no cross-environment problem
// on the map or in focus, and draws no ribbon, though the relationship stays.
func TestMapRestrictedDropsHiddenTargetPairs(t *testing.T) {
	q := mapFleet(t, mapServices,
		[]RawTarget{mapTarget("shop", "orders", "prod"), mapTarget("shop", "payments", "prod"), mapTarget("secret", "payments", "staging")},
		seen(wid("", "shop", "orders", "prod"), wid("", "secret", "payments", "staging"), time.Minute))
	if e := edgeOf(mustFocus(t, q, MapFocusQuery{Service: "orders"}), "orders", "payments"); e == nil || e.Problem == nil || e.Problem.Code != codeCrossEnvironmentCall {
		t.Fatalf("unrestricted edge %+v", e)
	}
	if rbs := mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment}).Ribbons; !slices.ContainsFunc(rbs, func(rb MapRibbon) bool { return rb.CrossEnvironment > 0 }) {
		t.Fatalf("unrestricted ribbons %+v", rbs)
	}
	r := q.Restrict(func(_, ns string) bool { return ns != "secret" }, nil)
	e := edgeOf(mustFocus(t, r, MapFocusQuery{Service: "orders"}), "orders", "payments")
	if e == nil || (e.Problem != nil && e.Problem.Code == codeCrossEnvironmentCall) {
		t.Errorf("restricted edge %+v", e)
	}
	for _, rb := range mustMap(t, r, MapQuery{GroupBy: MapGroupByEnvironment}).Ribbons {
		if rb.CrossEnvironment != 0 || rb.To == "staging" {
			t.Errorf("restricted ribbon %+v", rb)
		}
	}
}
