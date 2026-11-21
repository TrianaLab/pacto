package fleet

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustNodes(t *testing.T, q *Query, nq MapNodesQuery) *MapNodes {
	t.Helper()
	n, err := q.MapNodes(nq)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func nodeOf(ns []MapNode, key string) *MapNode {
	for i := range ns {
		if ns[i].Key == key {
			return &ns[i]
		}
	}
	return nil
}

func TestMapNodesSunflowerOrderAndCoordinates(t *testing.T) {
	q := defaultMapFleet(t)
	m := mustMap(t, q, MapQuery{})
	shop := groupOf(m, "shop")
	page := mustNodes(t, q, MapNodesQuery{Group: "shop"})
	// orders and payments both have two edges: the tie goes by key.
	if page.Total != 2 || page.Truncated || page.Nodes[0].Key != string(NewServiceKey("orders")) || page.Nodes[1].Key != string(NewServiceKey("payments")) {
		t.Fatalf("page %+v", page)
	}
	for i, n := range page.Nodes {
		x, y := sunflower(i, shop.X, shop.Y)
		if math.Abs(n.X+shop.X-x) > 0.11 || math.Abs(n.Y+shop.Y-y) > 0.11 {
			t.Errorf("%s at (%v, %v) + (%v, %v), want (%v, %v)", n.Key, n.X, n.Y, shop.X, shop.Y, x, y)
		}
	}
	// In the environment grouping a node is a target, clustered by domain.
	prod := mustNodes(t, q, MapNodesQuery{GroupBy: MapGroupByEnvironment, Group: "prod"})
	if prod.Total != 4 || prod.Nodes[0].Label != "" || prod.Nodes[0].Version != "1.0.0" || prod.GroupBy != MapGroupByEnvironment {
		t.Errorf("prod %+v", prod.Nodes)
	}
	if len(prod.Edges) != 3 {
		t.Errorf("prod edges %+v", prod.Edges)
	}
}

func TestMapNodesSeverityMatchesAttention(t *testing.T) {
	q := defaultMapFleet(t)
	for _, tr := range q.snap.Targets {
		if tr.Service == "payments" && tr.Environment == "prod" {
			tr.Compliance = StatusNonCompliant
		}
	}
	all := q.collectAttention()
	for _, g := range mustMap(t, q, MapQuery{}).Groups {
		for _, n := range mustNodes(t, q, MapNodesQuery{Group: g.Key}).Nodes {
			sev, cat := worstItem(slices.DeleteFunc(slices.Clone(all), func(it AttentionItem) bool { return it.Service != n.Key }))
			if n.Severity != sev || n.Worst != cat {
				t.Errorf("%s: %s %s, want %s %s", n.Key, n.Severity, n.Worst, sev, cat)
			}
		}
	}
	pay := nodeOf(mustNodes(t, q, MapNodesQuery{Group: "shop"}).Nodes, string(NewServiceKey("payments")))
	if pay.Severity != severityError || pay.Worst != categoryNonCompliant {
		t.Errorf("payments %+v", pay)
	}
	// A target node counts its own target's items, never its namesake's.
	staging := mustNodes(t, q, MapNodesQuery{GroupBy: MapGroupByEnvironment, Group: "staging"})
	if staging.Nodes[0].Severity == severityError {
		t.Errorf("staging payments %+v", staging.Nodes[0])
	}
	if s, c := worstItem(nil); s != "" || c != "" {
		t.Errorf("no item %s %s", s, c)
	}
}

func TestMapNodesDoesNotPackGroups(t *testing.T) {
	q := defaultMapFleet(t)
	saved := packMapGroups
	defer func() { packMapGroups = saved }()
	packed := 0
	packMapGroups = func(b *mapBuild, accs []*mapGroupAcc, cs []*circle, index map[string]int) {
		packed++
		saved(b, accs, cs, index)
	}
	mustMap(t, q, MapQuery{})
	if packed != 1 {
		t.Fatalf("the map packed %d times", packed)
	}
	for _, by := range []string{MapGroupByDomain, MapGroupByTeam, MapGroupByEnvironment} {
		m := mustMap(t, q, MapQuery{GroupBy: by})
		packed = 0
		for _, g := range m.Groups {
			mustNodes(t, q, MapNodesQuery{GroupBy: by, Group: g.Key})
		}
		if packed != 0 {
			t.Errorf("%s: pages packed %d times", by, packed)
		}
	}
}

// checkPage fails when page p at off is not the next one of a 10-node group
// or holds an edge outside itself and the earlier pages; it adds the page's
// keys and edges.
func checkPage(t *testing.T, p *MapNodes, off int, keys *[]string, seen map[[2]int]int) {
	t.Helper()
	if p.Total != 10 || p.Truncated != (off+4 < 10) || p.Limit != 4 || p.Offset != off {
		t.Errorf("offset %d: %+v", off, p)
	}
	for _, n := range p.Nodes {
		*keys = append(*keys, n.Key)
	}
	for _, e := range p.Edges {
		if e.From >= len(*keys) || e.To >= len(*keys) || max(e.From, e.To) < off {
			t.Errorf("offset %d: edge %+v is outside this page and the earlier ones", off, e)
		}
		seen[[2]int{e.From, e.To}]++
	}
}

func TestMapNodesPaging(t *testing.T) {
	var svcs []mapSvc
	for i := range 10 {
		s := mapSvc{name: "s" + strconv.Itoa(i), domain: "d"}
		if i < 9 {
			s.deps = []string{"s" + strconv.Itoa(i+1)}
		}
		svcs = append(svcs, s)
	}
	q := mapFleet(t, svcs, nil)
	seen := map[[2]int]int{}
	var keys []string
	for off := 0; off < 10; off += 4 {
		checkPage(t, mustNodes(t, q, MapNodesQuery{Group: "d", Offset: off, Limit: 4}), off, &keys, seen)
	}
	if len(keys) != 10 || len(seen) != 9 {
		t.Errorf("%d nodes, %d edges", len(keys), len(seen))
	}
	for e, n := range seen {
		if n != 1 {
			t.Errorf("edge %v on %d pages", e, n)
		}
	}
	if p := mustNodes(t, q, MapNodesQuery{Group: "d", Offset: 50}); len(p.Nodes) != 0 || p.Truncated {
		t.Errorf("past the end %+v", p)
	}
}

func TestMapNodesLimitBounds(t *testing.T) {
	q := defaultMapFleet(t)
	if p := mustNodes(t, q, MapNodesQuery{Group: "shop"}); p.Limit != DefaultMapNodes {
		t.Errorf("limit %d", p.Limit)
	}
	for _, nq := range []MapNodesQuery{{Group: "shop", Limit: 6000}, {Group: "shop", Limit: -1}, {Group: "shop", Offset: -1}, {GroupBy: "colour", Group: "shop"}} {
		var iq *InvalidQueryError
		if _, err := q.MapNodes(nq); !errors.As(err, &iq) {
			t.Errorf("%+v: %v", nq, err)
		}
	}
	if p := mustNodes(t, q, MapNodesQuery{Group: "shop", Limit: MaxMapNodes}); p.Limit != MaxMapNodes {
		t.Errorf("max limit %d", p.Limit)
	}
}

func TestMapNodesEnvironmentPicker(t *testing.T) {
	q := defaultMapFleet(t)
	plain := mustNodes(t, q, MapNodesQuery{Group: "shop"})
	staging := mustNodes(t, q, MapNodesQuery{Group: "shop", Environment: "staging"})
	for i, n := range staging.Nodes {
		if n.X != plain.Nodes[i].X || n.Y != plain.Nodes[i].Y {
			t.Errorf("%s moved", n.Key)
		}
	}
	orders, pay := nodeOf(staging.Nodes, string(NewServiceKey("orders"))), nodeOf(staging.Nodes, string(NewServiceKey("payments")))
	if orders.InEnvironment == nil || *orders.InEnvironment || orders.Version != "" || pay.InEnvironment == nil || !*pay.InEnvironment || pay.Version != "1.0.0" {
		t.Errorf("orders %+v payments %+v", orders, pay)
	}
	if plain.Nodes[0].InEnvironment != nil || plain.Nodes[0].Version != "1.0.0" {
		t.Errorf("no environment %+v", plain.Nodes[0])
	}
}

func TestMapNodesRunningVersionMixed(t *testing.T) {
	q := defaultMapFleet(t)
	pay := q.snap.Services[NewServiceKey("payments")]
	other := *q.snap.Revisions[q.currentRevision(pay).Key]
	other.Key, other.Version = "payments@2", "2.0.0"
	q.snap.Revisions[other.Key] = &other
	for _, tr := range q.snap.Targets {
		if tr.Service == "payments" && tr.Environment == "staging" {
			tr.ContractRevision = other.Key
		}
	}
	if v, ok := q.runningVersion(pay, ""); v != "mixed" || !ok {
		t.Errorf("anywhere %q %v", v, ok)
	}
	if v, ok := q.runningVersion(pay, "staging"); v != "2.0.0" || !ok {
		t.Errorf("staging %q %v", v, ok)
	}
	if v, ok := q.runningVersion(pay, "dev"); v != "" || ok {
		t.Errorf("dev %q %v", v, ok)
	}
	if v := q.targetVersion(&TargetRecord{ContractRevision: "nope"}); v != "" {
		t.Errorf("unknown revision %q", v)
	}
}

func TestMapNodesMissingSections(t *testing.T) {
	q := defaultMapFleet(t)
	m := mustMap(t, q, MapQuery{})
	for _, g := range m.Groups {
		for _, n := range mustNodes(t, q, MapNodesQuery{Group: g.Key}).Nodes {
			s := q.snap.Services[ServiceKey(n.Key)]
			if s.Inferred() {
				if n.PercentComplete != nil || n.Missing != nil || !n.Inferred || n.Match != MatchObservedOnly {
					t.Errorf("inferred %+v", n)
				}
				continue
			}
			sc := q.serviceCompleteness(s)
			var want []string
			for _, sec := range sc.Sections {
				if sec.State == "missing" {
					want = append(want, sec.Section)
				}
			}
			if !reflect.DeepEqual(n.Missing, want) || !reflect.DeepEqual(n.PercentComplete, sc.Percent) {
				t.Errorf("%s: missing %v %v, want %v %v", n.Key, n.Missing, n.PercentComplete, want, sc.Percent)
			}
		}
	}
	if orders := nodeOf(mustNodes(t, q, MapNodesQuery{Group: "shop"}).Nodes, string(NewServiceKey("orders"))); len(orders.Missing) == 0 {
		t.Errorf("orders %+v", orders)
	}
}

func TestMapNodesRestricted(t *testing.T) {
	full := defaultMapFleet(t)
	q := defaultMapFleet(t).Restrict(func(_, ns string) bool { return ns != "vault" }, nil)
	money := mustNodes(t, q, MapNodesQuery{Group: "money"})
	if money.Total != 1 || money.Nodes[0].Key != string(NewServiceKey("idle")) {
		t.Errorf("money %+v", money.Nodes)
	}
	key := string(NewServiceKey("payments"))
	if p, f := nodeOf(mustNodes(t, q, MapNodesQuery{Group: "shop"}).Nodes, key), nodeOf(mustNodes(t, full, MapNodesQuery{Group: "shop"}).Nodes, key); p.Out != 0 || f.Out != 1 {
		t.Errorf("payments out %d, unrestricted %d", p.Out, f.Out)
	}
}

func TestMapNodesUnknownGroup404(t *testing.T) {
	var nf *NotFoundError
	if _, err := defaultMapFleet(t).MapNodes(MapNodesQuery{Group: "nowhere"}); !errors.As(err, &nf) {
		t.Errorf("err %v", err)
	}
}

func TestMapNodesExternals(t *testing.T) {
	q := defaultMapFleet(t)
	q.snap.Externals = map[ExternalKey]*ExternalRecord{"host/api.stripe.com": {Key: "host/api.stripe.com", Label: "Stripe", Consumers: []ServiceKey{NewServiceKey("orders")}}}
	p := mustNodes(t, q, MapNodesQuery{Group: mapKeyExternal})
	if len(p.Nodes) != 1 || !p.Nodes[0].External || p.Nodes[0].Label != "Stripe" || p.Nodes[0].In != 1 || len(p.Edges) != 0 {
		t.Errorf("externals %+v", p)
	}
}

func TestAttentionForNarrowsEveryProducer(t *testing.T) {
	// payments calls orders, which no contract declares.
	q := mapFleet(t, mapServices, []RawTarget{mapTarget("shop", "orders", "prod"), mapTarget("shop", "payments", "prod")},
		seen(wid("", "shop", "payments", "prod"), wid("", "shop", "orders", "prod"), time.Minute))
	pay, orders := NewServiceKey("payments"), NewServiceKey("orders")
	q.snap.UncontractedCallers = append(q.snap.UncontractedCallers, UncontractedCaller{Caller: "ghost", ToService: pay})
	q.snap.Replacements = append(q.snap.Replacements, Replacement{From: "shop/cart", To: pay, Link: "inferred-from"})
	all := q.collectAttention()
	for _, keys := range []map[ServiceKey]bool{{pay: true}, {orders: true}, {pay: true, orders: true}} {
		want := slices.DeleteFunc(slices.Clone(all), func(it AttentionItem) bool { return !keys[ServiceKey(it.Service)] })
		got := q.attentionFor(keys)
		sortItems := func(items []AttentionItem) {
			slices.SortFunc(items, func(a, b AttentionItem) int {
				return strings.Compare(a.Service+a.Code+a.Entity.Key+a.Label, b.Service+b.Code+b.Entity.Key+b.Label)
			})
		}
		sortItems(want)
		sortItems(got)
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("%v: %d items, want %d", keys, len(got), len(want))
		}
	}
	if q.scope != nil {
		t.Error("attentionFor scoped the query itself")
	}
}
