package fleet

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// mapSvc describes one service of a map fixture.
type mapSvc struct {
	name, domain, team string
	envs               []string // declared environments; a "pr-" one is temporary
	deps               []string
}

// mapRev is the revision of s.
func mapRev(s mapSvc) RawRevision {
	c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: s.name, Version: "1.0.0", Domain: s.domain}}
	if s.team != "" {
		c.Service.Owner = contract.Owner{Team: s.team}
	}
	fs := fstest.MapFS{}
	for _, e := range s.envs {
		env := contract.Environment{Name: e, File: "values/" + e + ".yaml"}
		if len(e) > 3 && e[:3] == "pr-" {
			env.Lifetime = contract.EnvironmentTemporary
		}
		c.Environments = append(c.Environments, env)
		fs["values/"+e+".yaml"] = &fstest.MapFile{Data: []byte("a: 1\n")}
	}
	for _, d := range s.deps {
		c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, Ref: "oci://x/" + d, Required: true})
	}
	return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fs}, Digest: "sha256:" + s.name}
}

// mapTarget is the target of service in ns and env, linked to its revision.
func mapTarget(ns, service, env string) RawTarget {
	t := envTarget(ns, service, env)
	t.Digest = "sha256:" + service
	return t
}

// mapServices is the default fixture: orders and payments in shop, ledger
// and idle (running nowhere) in money, nodom with no domain and no owner.
var mapServices = []mapSvc{
	{name: "orders", domain: "shop", team: "a", envs: []string{"prod"}, deps: []string{"payments", "ledger"}},
	{name: "payments", domain: "shop", team: "b", envs: []string{"prod", "staging"}, deps: []string{"ledger"}},
	{name: "ledger", domain: "money", team: "b", envs: []string{"prod"}},
	{name: "idle", domain: "money", team: "a", envs: []string{"prod"}},
	{name: "nodom"},
}

// mapFleet builds svcs with targets and observed calls, plus the inferred
// cart workload.
func mapFleet(t *testing.T, svcs []mapSvc, targets []RawTarget, edges ...ObservedEdge) *Query {
	t.Helper()
	var revs []RawRevision
	for _, s := range svcs {
		revs = append(revs, mapRev(s))
	}
	return NewQuery(buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Revisions: revs, Targets: targets}),
		workloadsSource("k8s-workloads", nil, inferredCart()),
		kernel(edges...)))
}

// defaultMapFleet is mapServices running in prod (payments in staging too,
// ledger in namespace vault), with orders calling payments and payments
// calling ledger in prod, and orders calling payments in staging.
func defaultMapFleet(t *testing.T) *Query {
	t.Helper()
	return mapFleet(t, mapServices,
		[]RawTarget{mapTarget("shop", "orders", "prod"), mapTarget("shop", "payments", "prod"), mapTarget("shop", "payments", "staging"),
			mapTarget("vault", "ledger", "prod"), mapTarget("shop", "nodom", "prod")},
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "prod"), time.Minute),
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "staging"), time.Minute),
		seen(wid("", "shop", "payments", "prod"), wid("", "vault", "ledger", "prod"), time.Minute))
}

func mustMap(t *testing.T, q *Query, mq MapQuery) *FleetMap {
	t.Helper()
	m, err := q.Map(mq)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func groupOf(m *FleetMap, key string) *MapGroup {
	for i := range m.Groups {
		if m.Groups[i].Key == key {
			return &m.Groups[i]
		}
	}
	return nil
}

func ribbonOf(m *FleetMap, from, to string) *MapRibbon {
	for i := range m.Ribbons {
		if m.Ribbons[i].From == from && m.Ribbons[i].To == to {
			return &m.Ribbons[i]
		}
	}
	return nil
}

// groupView is what a test reads of a group; the zero view is no group.
type groupView struct {
	Kind, Label        string
	Services, Inferred int
	Subs               int
	Match              MatchCounts
}

func viewOf(m *FleetMap, key string) groupView {
	g := groupOf(m, key)
	if g == nil {
		return groupView{}
	}
	return groupView{g.Kind, g.Label, g.Services, g.Inferred, len(g.Subs), g.Match.Services}
}

func checkView(t *testing.T, m *FleetMap, key string, want groupView) {
	t.Helper()
	if got := viewOf(m, key); got != want {
		t.Errorf("group %s: %+v, want %+v", key, got, want)
	}
}

func TestMapByDomainDefault(t *testing.T) {
	m := mustMap(t, defaultMapFleet(t), MapQuery{})
	if m.GroupBy != MapGroupByDomain || m.Nested || m.EnvironmentOrder != "" || m.Meta.SnapshotID == "" {
		t.Fatalf("map %+v", m)
	}
	checkView(t, m, "shop", groupView{MapKindDomain, "shop", 2, 0, 0, counts(2, 0, 0, 0)})
	checkView(t, m, "money", groupView{MapKindDomain, "money", 2, 0, 0, counts(1, 1, 0, 0)})
	if want := (MapTotals{Services: 6, Inferred: 1, Targets: 5, Groups: 4, Ribbons: 1}); m.Totals != want {
		t.Errorf("totals %+v", m.Totals)
	}
	if r := ribbonOf(m, "shop", "money"); r == nil || r.Total != 2 {
		t.Errorf("ribbons %+v", m.Ribbons)
	}
	if m.Limitations.Items == nil || hasLimitation(m.Limitations.Items, LimitationEnvironmentOrderUnknown) {
		t.Errorf("limitations %+v", m.Limitations)
	}
}

func TestMapInvalidGroupBy(t *testing.T) {
	var iq *InvalidQueryError
	if _, err := defaultMapFleet(t).Map(MapQuery{GroupBy: "cluster"}); !errors.As(err, &iq) || iq.Field != "groupBy" {
		t.Errorf("err %v", err)
	}
}

func TestMapDomainNoneAndUnknownGroups(t *testing.T) {
	q := defaultMapFleet(t)
	q.snap.Services[NewServiceKey("ledger")].BusinessDomainUnknown = true
	m := mustMap(t, q, MapQuery{GroupBy: MapGroupByDomain})
	checkView(t, m, NoBusinessDomain, groupView{MapKindNone, "No domain", 1, 0, 0, counts(1, 0, 0, 0)})
	checkView(t, m, UnknownBusinessDomain, groupView{MapKindUnknown, "Domain unknown", 2, 1, 0, counts(1, 0, 1, 0)})
	// Special groups are placed after every ordinary group.
	if m.Groups[0].Kind != MapKindDomain {
		t.Errorf("first group %+v", m.Groups[0])
	}
}

func TestMapByTeamFlatUnder40(t *testing.T) {
	q := defaultMapFleet(t)
	m := mustMap(t, q, MapQuery{GroupBy: MapGroupByTeam})
	if m.Nested {
		t.Error("nested")
	}
	checkView(t, m, "team:a", groupView{MapKindTeam, "a", 2, 0, 0, counts(1, 1, 0, 0)})
	checkView(t, m, "team:b", groupView{MapKindTeam, "b", 2, 0, 0, counts(2, 0, 0, 0)})
	checkView(t, m, mapKeyUnowned, groupView{MapKindUnowned, "No identified owner", 1, 0, 0, counts(1, 0, 0, 0)})
	// Pacto reads no contract of the inferred cart: its owner is unknown.
	checkView(t, m, UnknownBusinessDomain, groupView{MapKindUnknown, "Owner unknown", 1, 1, 0, counts(0, 0, 1, 0)})
	// Pacto cannot read nodom's ownership: its owner is unknown, never unowned.
	nodom := q.snap.Services[NewServiceKey("nodom")]
	for _, rk := range nodom.Revisions {
		q.snap.Revisions[rk].validated, q.snap.Revisions[rk].Valid = true, false
	}
	for _, tk := range nodom.Targets {
		q.snap.Targets[tk].domainKnown = false
	}
	m = mustMap(t, q, MapQuery{GroupBy: MapGroupByTeam})
	checkView(t, m, UnknownBusinessDomain, groupView{MapKindUnknown, "Owner unknown", 2, 1, 0, counts(1, 0, 1, 0)})
}

func TestMapByTeamNestsPast40(t *testing.T) {
	var svcs []mapSvc
	for i := range maxFlatTeams + 2 {
		svcs = append(svcs, mapSvc{name: "s" + strconv.Itoa(i), domain: []string{"shop", "money"}[i%2], team: "t" + strconv.Itoa(i)})
	}
	svcs[0].team = ""
	svcs = append(svcs, mapSvc{name: "extra", domain: "shop", team: "t1"})
	m := mustMap(t, mapFleet(t, svcs, nil), MapQuery{GroupBy: MapGroupByTeam})
	if !m.Nested {
		t.Fatal("not nested")
	}
	shop := groupOf(m, "shop")
	if shop == nil || shop.Kind != MapKindDomain || shop.Services != 22 {
		t.Fatalf("groups %+v", m.Groups)
	}
	var unowned, t1 *MapSub
	for i, s := range shop.Subs {
		switch s.Key {
		case mapKeyUnowned:
			unowned = &shop.Subs[i]
		case "team:t1":
			t1 = &shop.Subs[i]
		}
	}
	if unowned == nil || unowned.Label != "No identified owner" || t1 == nil || t1.Label != "t1" || t1.Services != 1 {
		t.Errorf("subs %+v", shop.Subs)
	}
}

func TestMapByEnvironmentTargetsAndVersions(t *testing.T) {
	m := mustMap(t, defaultMapFleet(t), MapQuery{GroupBy: MapGroupByEnvironment, EnvironmentOrder: map[string]int{"staging": 0, "prod": 1}})
	// idle declares prod, runs nowhere and Pacto can tell: expected-only
	// there. prod's targets cluster by domain.
	checkView(t, m, "prod", groupView{MapKindEnvironment, "prod", 4, 0, 3, counts(4, 1, 0, 0)})
	checkView(t, m, "staging", groupView{MapKindEnvironment, "staging", 1, 0, 1, counts(1, 0, 0, 0)})
	checkView(t, m, NoBusinessDomain, groupView{MapKindNone, "No visible target", 1, 0, 0, counts(0, 1, 0, 0)})
	checkView(t, m, UnknownBusinessDomain, groupView{MapKindUnknown, "Environment unknown", 1, 1, 0, counts(0, 0, 1, 0)})
	// Both ends of orders -> payments and payments -> ledger run in prod.
	if prod, staging := groupOf(m, "prod"), groupOf(m, "staging"); prod.Match.Dependencies.Total() != 3 || staging.Match.Dependencies.Total() != 0 {
		t.Errorf("dependencies prod %+v staging %+v", prod.Match.Dependencies, staging.Match.Dependencies)
	}
	// The one call from prod into staging is a ribbon of its own.
	if r := ribbonOf(m, "prod", "staging"); r == nil || r.Total != 1 || r.CrossEnvironment != 1 || r.Problems != 1 {
		t.Errorf("ribbons %+v", m.Ribbons)
	}
	// A target naming no environment sits in the unknown group.
	q := mapFleet(t, mapServices[:1], []RawTarget{mapTarget("shop", "orders", "")})
	if u := groupOf(mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment}), UnknownBusinessDomain); u == nil || u.Services != 2 {
		t.Errorf("unknown %+v", u)
	}
}

func (c MatchCounts) Total() int { return c.Matched + c.ExpectedOnly + c.ObservedOnly + c.Insufficient }

func TestMapEnvironmentHiddenTargetIsInsufficient(t *testing.T) {
	svcs := []mapSvc{{name: "pay", domain: "shop", envs: []string{"prod", "staging"}}, {name: "web", domain: "shop", envs: []string{"prod"}}}
	targets := []RawTarget{mapTarget("secret", "pay", "prod"), mapTarget("shop", "pay", "staging"), mapTarget("shop", "web", "prod")}
	q := mapFleet(t, svcs, targets).Restrict(func(_, ns string) bool { return ns != "secret" }, nil)
	m := mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment})
	if prod := groupOf(m, "prod"); prod == nil || prod.Match.Services != counts(1, 0, 0, 1) {
		t.Fatalf("prod %+v", prod)
	}
	// Unrestricted, pay with no prod target where Pacto sees every target runs nowhere there.
	q = mapFleet(t, svcs, targets[1:])
	if prod := groupOf(mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment}), "prod"); prod == nil || prod.Match.Services != counts(1, 1, 0, 0) {
		t.Errorf("prod %+v", prod)
	}
}

func TestMapEnvironmentStaleOrQuarantinedTargetIsInsufficient(t *testing.T) {
	q := mapFleet(t, mapServices[1:3], []RawTarget{mapTarget("shop", "payments", "prod"), mapTarget("shop", "payments", "staging"), mapTarget("shop", "ledger", "staging")})
	for _, tr := range q.snap.Targets {
		switch tr.Environment + "/" + tr.Service {
		case "prod/payments":
			tr.Stale = true
		case "staging/payments":
			tr.Quarantined = true
		}
	}
	m := mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment})
	// ledger runs in staging, which it does not declare: observed-only; and it
	// declares prod with no target there: expected-only.
	if prod, staging := groupOf(m, "prod"), groupOf(m, "staging"); prod.Match.Services != counts(0, 1, 0, 1) || staging.Match.Services != counts(0, 0, 1, 1) {
		t.Errorf("prod %+v staging %+v", prod.Match.Services, staging.Match.Services)
	}
}

func TestMapTargetMatchUnknownIsInsufficient(t *testing.T) {
	q := defaultMapFleet(t)
	var pay *TargetRecord
	for _, tr := range q.snap.Targets {
		if tr.Service == "payments" && tr.Environment == "prod" {
			pay = tr
		}
	}
	base := *pay
	if got := q.targetMatch(&base); got != MatchMatched {
		t.Fatalf("exact link in a declared environment = %s", got)
	}
	cases := map[string]func(*TargetRecord){
		"unknown revision": func(tr *TargetRecord) { tr.ContractRevision = "nope" },
		"inferred link":    func(tr *TargetRecord) { tr.RevisionMatch = revisionMatchInferred },
		"no environment":   func(tr *TargetRecord) { tr.Environment = "" },
	}
	for name, mutate := range cases {
		tr := base
		mutate(&tr)
		if got := q.targetMatch(&tr); got != MatchInsufficient {
			t.Errorf("%s = %s, want insufficient", name, got)
		}
	}
	tr := base
	tr.Environment = "dev"
	if got := q.targetMatch(&tr); got != MatchObservedOnly {
		t.Errorf("undeclared environment = %s", got)
	}
	for _, n := range q.snap.Targets {
		if n.Service == "nodom" {
			if got := q.targetMatch(n); got != MatchMatched {
				t.Errorf("a revision declaring no environment = %s", got)
			}
		}
	}
}

func TestMapRibbonsSkipHiddenRelationships(t *testing.T) {
	q := defaultMapFleet(t).Restrict(func(_, ns string) bool { return ns != "vault" }, nil)
	m := mustMap(t, q, MapQuery{})
	// ledger is hidden: the hidden dependencies on it draw no ribbon, and the
	// money group holds idle alone.
	if r := ribbonOf(m, "shop", "money"); r != nil {
		t.Errorf("ribbon %+v", r)
	}
	if money := groupOf(m, "money"); money == nil || money.Services != 1 {
		t.Errorf("money %+v", money)
	}
	if shop := groupOf(m, "shop"); shop.Match.Dependencies.Total() != 1 {
		t.Errorf("shop dependencies %+v", shop.Match.Dependencies)
	}
}

func TestMapPreviewsCollapse(t *testing.T) {
	svcs := []mapSvc{{name: "pay", domain: "shop", envs: []string{"prod", "pr-1"}}, {name: "web", domain: "shop", envs: []string{"prod", "pr-2", "pr-3"}}}
	m := mustMap(t, mapFleet(t, svcs, []RawTarget{mapTarget("shop", "pay", "prod"), mapTarget("shop", "pay", "pr-1"), mapTarget("shop", "web", "pr-2")}),
		MapQuery{GroupBy: MapGroupByEnvironment})
	p := groupOf(m, mapKeyPreviews)
	if p == nil || p.Kind != MapKindPreviews || !p.Preview || p.Label != "Previews (2)" || len(p.Subs) != 2 || p.Depth == nil || *p.Depth != -1 || p.Services != 2 {
		t.Fatalf("previews %+v", p)
	}
	// web declares pr-3 with no target, and pr-3 is drawn nowhere: it is in no group.
	if p.Match.Services != counts(2, 0, 0, 0) || groupOf(m, "pr-1") != nil || groupOf(m, "pr-3") != nil {
		t.Errorf("previews match %+v groups %+v", p.Match.Services, m.Groups)
	}
	// web declares pr-2 and runs there, pay declares pr-1: each expected only in its own preview.
	if prod := groupOf(m, "prod"); prod.Match.Services != counts(1, 1, 0, 0) {
		t.Errorf("prod %+v", prod.Match.Services)
	}
}

func TestMapEnvironmentOrderFromInput(t *testing.T) {
	m := mustMap(t, defaultMapFleet(t), MapQuery{GroupBy: MapGroupByEnvironment, EnvironmentOrder: map[string]int{"staging": 3, "prod": 7}})
	if m.EnvironmentOrder != "stages" || *groupOf(m, "staging").Depth != 0 || *groupOf(m, "prod").Depth != 1 || hasLimitation(m.Limitations.Items, LimitationEnvironmentOrderUnknown) {
		t.Errorf("order %s groups %+v limitations %+v", m.EnvironmentOrder, m.Groups, m.Limitations)
	}
	// An environment the order leaves out comes after the ordered ones.
	m = mustMap(t, defaultMapFleet(t), MapQuery{GroupBy: MapGroupByEnvironment, EnvironmentOrder: map[string]int{"staging": 0}})
	if *groupOf(m, "staging").Depth != 0 || *groupOf(m, "prod").Depth != 1 {
		t.Errorf("groups %+v", m.Groups)
	}
}

func TestMapEnvironmentOrderByNameSaysSo(t *testing.T) {
	m := mustMap(t, defaultMapFleet(t), MapQuery{GroupBy: MapGroupByEnvironment})
	if m.EnvironmentOrder != "name" || *groupOf(m, "prod").Depth != 0 || *groupOf(m, "staging").Depth != 1 ||
		!hasLimitation(m.Limitations.Items, LimitationEnvironmentOrderUnknown) || !hasLimitation(m.Meta.Limitations, LimitationEnvironmentOrderUnknown) {
		t.Errorf("order %s limitations %+v", m.EnvironmentOrder, m.Limitations)
	}
	m = mustMap(t, defaultMapFleet(t), MapQuery{GroupBy: MapGroupByEnvironment, OrderWhy: "no Stage you may read"})
	if m.Limitations.Items[0].Message != "no Stage you may read" {
		t.Errorf("limitations %+v", m.Limitations)
	}
}

func TestMapRibbonSplits(t *testing.T) {
	svcs := append(slices.Clone(mapServices), mapSvc{name: "web", domain: "front", deps: []string{"orders", "payments", "ledger"}})
	q := mapFleet(t, svcs,
		[]RawTarget{mapTarget("shop", "orders", "prod"), mapTarget("shop", "payments", "prod"), mapTarget("vault", "ledger", "prod"), mapTarget("front", "web", "prod")},
		seen(wid("", "front", "web", "prod"), wid("", "shop", "orders", "prod"), time.Minute),
		seen(wid("", "shop", "orders", "prod"), wid("", "front", "web", "prod"), time.Minute))
	m := mustMap(t, q, MapQuery{})
	for _, r := range m.Ribbons {
		if r.Matched+r.ExpectedOnly+r.ObservedOnly+r.Insufficient != r.Total || r.Problems > r.Total || r.CrossEnvironment > r.Total {
			t.Errorf("ribbon %+v", r)
		}
	}
	if r := ribbonOf(m, "front", "shop"); r == nil || r.Total != 2 || r.Matched != 1 || r.ExpectedOnly != 1 {
		t.Errorf("front -> shop %+v", r)
	}
	// orders calls web and declares nothing of it: observed-only, a problem.
	if r := ribbonOf(m, "shop", "front"); r == nil || r.ObservedOnly != 1 || r.Problems != 1 {
		t.Errorf("shop -> front %+v", r)
	}
}

func TestMapGroupAttentionWorstFirst(t *testing.T) {
	q := defaultMapFleet(t)
	for _, tr := range q.snap.Targets {
		if tr.Service == "payments" && tr.Environment == "prod" {
			tr.Compliance = StatusNonCompliant
		}
	}
	m := mustMap(t, q, MapQuery{})
	shop := groupOf(m, "shop")
	if len(shop.Attention.Worst) == 0 || shop.Attention.Errors == 0 {
		t.Fatalf("attention %+v", shop.Attention)
	}
	got := worstCategories(map[string]int{categoryKnowledge: 5, categoryNonCompliant: 1, categoryUndeclared: 2, categoryHealth: 3},
		map[string]string{categoryKnowledge: severityInfo, categoryNonCompliant: severityError, categoryUndeclared: severityWarning, categoryHealth: severityError})
	want := []AttentionCategoryCount{{categoryNonCompliant, 1}, {categoryHealth, 3}, {categoryUndeclared, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("worst %+v", got)
	}
	// An item on a target counts in that target's environment.
	m = mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment})
	if prod, staging := groupOf(m, "prod"), groupOf(m, "staging"); prod.Attention.Errors == 0 || staging.Attention.Worst == nil {
		t.Errorf("prod %+v staging %+v", prod.Attention, staging.Attention)
	}
}

func TestMapGroupMatchAndCompleteness(t *testing.T) {
	q := defaultMapFleet(t)
	m := mustMap(t, q, MapQuery{})
	shop := groupOf(m, "shop")
	want := q.matchOf(slices.Values([]ServiceKey{NewServiceKey("orders"), NewServiceKey("payments")}))
	if !reflect.DeepEqual(shop.Match, want) || shop.Match.Percent == nil {
		t.Errorf("shop match %+v, want %+v", shop.Match, want)
	}
	var k KnowledgeTally
	for _, n := range []string{"orders", "payments"} {
		k.addCompleteness(q.serviceCompleteness(q.snap.Services[NewServiceKey(n)]))
	}
	k.finish()
	if shop.PercentComplete == nil || *shop.PercentComplete != *k.ContractCompleteness.PercentComplete {
		t.Errorf("shop complete %v, want %v", shop.PercentComplete, k.ContractCompleteness.PercentComplete)
	}
	if !reflect.DeepEqual(m.Match, q.Match()) || m.Knowledge.Services != q.Overview().Summary.Knowledge.Services {
		t.Errorf("fleet match %+v knowledge %+v", m.Match, m.Knowledge)
	}
}

func TestMapExternalsGroup(t *testing.T) {
	svcs := slices.Clone(mapServices)
	svcs[0].deps = nil
	q := mapFleet(t, svcs, nil)
	q.snap.Externals = map[ExternalKey]*ExternalRecord{"host/api.stripe.com": {Key: "host/api.stripe.com", Consumers: []ServiceKey{NewServiceKey("orders")}}}
	m := mustMap(t, q, MapQuery{})
	g := groupOf(m, mapKeyExternal)
	if g == nil || g.Kind != MapKindExternal || g.Externals != 1 || g.Services != 0 || m.Groups[len(m.Groups)-1].Key != mapKeyExternal && m.Groups[len(m.Groups)-1].Kind == MapKindDomain ||
		m.Totals.Externals != 1 || g.Match.Percent != nil {
		t.Errorf("groups %+v", m.Groups)
	}
}

func TestMapGroupsFoldPastMax(t *testing.T) {
	var svcs []mapSvc
	for i := range MaxMapGroups + 1 {
		svcs = append(svcs, mapSvc{name: "s" + strconv.Itoa(i), domain: "d" + strconv.Itoa(i)})
	}
	svcs = append(svcs, mapSvc{name: "big", domain: "d0"})
	m := mustMap(t, mapFleet(t, svcs, nil), MapQuery{})
	other := groupOf(m, mapKeyOther)
	// 301 domains plus the inferred cart's group: 302, folded to 300.
	if len(m.Groups) != MaxMapGroups || other == nil || other.Kind != MapKindOther || other.Label != "Other (3)" || other.Services != 3 ||
		m.Totals.Groups != MaxMapGroups+2 || !hasLimitation(m.Limitations.Items, LimitationMapGroupsFolded) {
		t.Fatalf("groups %d other %+v totals %+v", len(m.Groups), other, m.Totals)
	}
	if d0 := groupOf(m, "d0"); d0 == nil || d0.Services != 2 {
		t.Errorf("d0 %+v", d0)
	}
}

func TestMapRibbonsCapped(t *testing.T) {
	n := 47 // 47 x 46 ordered pairs > MaxMapRibbons
	var svcs []mapSvc
	for i := range n {
		s := mapSvc{name: "s" + strconv.Itoa(i), domain: "d" + strconv.Itoa(i)}
		for j := range n {
			if j != i {
				s.deps = append(s.deps, "s"+strconv.Itoa(j))
			}
		}
		svcs = append(svcs, s)
	}
	m := mustMap(t, mapFleet(t, svcs, nil), MapQuery{})
	if len(m.Ribbons) != MaxMapRibbons || !m.RibbonsTruncated || m.Totals.Ribbons != n*(n-1) || !hasLimitation(m.Limitations.Items, LimitationMapRibbonsTruncated) {
		t.Errorf("ribbons %d truncated %v totals %+v", len(m.Ribbons), m.RibbonsTruncated, m.Totals)
	}
}

func TestMapRestrictedCountsExcludeHidden(t *testing.T) {
	q := defaultMapFleet(t).Restrict(func(_, ns string) bool { return ns == "shop" }, func(_, ns, _ string) bool { return ns == "shop" })
	for _, by := range []string{MapGroupByDomain, MapGroupByTeam, MapGroupByEnvironment} {
		m := mustMap(t, q, MapQuery{GroupBy: by})
		total := 0
		for _, g := range m.Groups {
			total += g.Services
		}
		if m.Totals.Services != 4 || m.Match.Services.Total() != 4 {
			t.Errorf("%s: totals %+v match %+v", by, m.Totals, m.Match.Services)
		}
		if by != MapGroupByEnvironment && total != 4 {
			t.Errorf("%s: %d services in groups", by, total)
		}
	}
	// The view may see no service of money: there is no money group.
	if money := groupOf(mustMap(t, q, MapQuery{}), "money"); money != nil {
		t.Errorf("money %+v", money)
	}
}

func TestMapNoObservationSource(t *testing.T) {
	var revs []RawRevision
	for _, s := range mapServices {
		revs = append(revs, mapRev(s))
	}
	q := NewQuery(buildClusterSnap(t, NewMemorySource("reg", "oci", &Collection{Revisions: revs})))
	m := mustMap(t, q, MapQuery{})
	if len(m.Groups) != 3 || m.Match.Percent != nil || groupOf(m, "shop").Match.Percent != nil {
		t.Errorf("groups %d match %+v", len(m.Groups), m.Match)
	}
	e := mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment})
	if groupOf(e, "prod") == nil || groupOf(e, "prod").Services != 0 {
		t.Errorf("environments %+v", e.Groups)
	}
	// Every declared service sits in No visible target and is judged there
	// once, through the service-to-group index (no scan per service).
	if none := groupOf(e, NoBusinessDomain); none == nil || none.Match.Services.Total() != len(mapServices) {
		t.Errorf("none group %+v", none)
	}
	if empty := mustMap(t, NewQuery(buildClusterSnap(t)), MapQuery{}); len(empty.Groups) != 0 || empty.Bounds != (MapBounds{}) {
		t.Errorf("empty %+v", empty)
	}
}

func TestMapEdgeProblems(t *testing.T) {
	q := defaultMapFleet(t)
	pf := problemFacts{failing: map[ServiceKey]bool{}, breaking: map[[2]ServiceKey]string{}}
	edge := func(e NeighborhoodEdge, state string) *mapEdge {
		return &mapEdge{from: "a", to: "b", e: &e, state: state}
	}
	if p := q.edgeProblem(edge(NeighborhoodEdge{}, MatchMatched), pf); p != nil {
		t.Errorf("no problem %+v", p)
	}
	pf.breaking[[2]ServiceKey{"a", "b"}] = "removes PUT /cache"
	if p := q.edgeProblem(edge(NeighborhoodEdge{}, MatchMatched), pf); p == nil || p.Code != "BREAKING" || p.Reason != "removes PUT /cache" {
		t.Errorf("breaking %+v", p)
	}
	delete(pf.breaking, [2]ServiceKey{"a", "b"})
	pf.failing["b"] = true
	if p := q.edgeProblem(edge(NeighborhoodEdge{}, MatchMatched), pf); p == nil || p.Severity != severityWarning {
		t.Errorf("health %+v", p)
	}
	pf.failing = map[ServiceKey]bool{}
	for _, tc := range []struct {
		e    NeighborhoodEdge
		code string
	}{
		{NeighborhoodEdge{DeclaredClaims: DeclaredClaimsPreview{Items: []DeclaredClaim{{RangeStatus: RangeSatisfied}, {RangeStatus: RangeUnsatisfied, Compatibility: "^2"}}}}, LimitationRangeUnsatisfied},
		{NeighborhoodEdge{GuaranteeStatus: GuaranteeUnmet}, "GUARANTEE_UNMET"},
		{NeighborhoodEdge{DeprecatedInterfaces: []InterfaceDeprecation{{Interface: "api"}}}, "DEPENDENCY_SERVICE_DEPRECATED"},
	} {
		if p := q.edgeProblem(edge(tc.e, MatchMatched), pf); p == nil || p.Code != tc.code {
			t.Errorf("%s: %+v", tc.code, p)
		}
	}
	if p := q.edgeProblem(edge(NeighborhoodEdge{Inferred: true}, MatchObservedOnly), pf); p != nil {
		t.Errorf("runtime-inferred %+v", p)
	}
}

func TestMapProblemFactsBreaksAndHealth(t *testing.T) {
	svcs := slices.Clone(mapServices)
	q := mapFleet(t, svcs, []RawTarget{mapTarget("shop", "payments", "prod")})
	pay := q.currentRevision(q.snap.Services[NewServiceKey("payments")])
	pay.Contract.Breaks = []contract.Break{{Consumers: []string{"orders", "ghost"}, Reason: "removes PUT /cache"}, {Consumers: []string{"ledger"}, Reason: "old", Expires: "2000-01-01"}}
	for _, tr := range q.snap.Targets {
		tr.Findings = []finding.Finding{healthFinding(finding.CodeHealthFailing, "down")}
	}
	pf := q.problemFactsOf()
	if pf.breaking[[2]ServiceKey{NewServiceKey("orders"), NewServiceKey("payments")}] != "removes PUT /cache" || len(pf.breaking) != 1 || !pf.failing[NewServiceKey("payments")] {
		t.Errorf("facts %+v", pf)
	}
}

func TestMapCrossEnvironmentProblemSeverity(t *testing.T) {
	q := defaultMapFleet(t)
	var pay *mapEdge
	for _, me := range q.dependencyEdges(func(ServiceKey) bool { return true }) {
		if me.from == NewServiceKey("orders") && me.to == NewServiceKey("payments") {
			pay = me
		}
	}
	if p := q.crossEnvironment(pay, nil); p == nil || p.Severity != severityError {
		t.Fatalf("cross %+v", p)
	}
	// A pair into a preview is a warning, unless another pair is an error.
	var stg TargetKey
	for k, tr := range q.snap.Targets {
		if tr.Environment == "staging" {
			stg = k
		}
	}
	pay.pairs = append(pay.pairs, pay.pairs...)
	if p := q.crossEnvironment(pay, map[string]bool{"staging": true}); p == nil || p.Code != codeCrossEnvironmentPreviewCall {
		t.Errorf("preview %+v", p)
	}
	pay.pairs = append(pay.pairs, envPair{from: pay.pairs[0].from, to: stg + "x"})
	if p := q.crossEnvironment(pay, map[string]bool{}); p == nil || p.Severity != severityError {
		t.Errorf("cross %+v", p)
	}
}

func TestLayoutDeterministic(t *testing.T) {
	for _, by := range []string{MapGroupByDomain, MapGroupByTeam, MapGroupByEnvironment} {
		a, b := mustMap(t, defaultMapFleet(t), MapQuery{GroupBy: by}), mustMap(t, defaultMapFleet(t), MapQuery{GroupBy: by})
		if !reflect.DeepEqual(a.Groups, b.Groups) || a.Bounds != b.Bounds {
			t.Errorf("%s: two builds differ", by)
		}
	}
}

func TestMapNodesRefusesEnvironmentInEnvironmentGrouping(t *testing.T) {
	q := defaultMapFleet(t)
	m := mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment})
	_, err := q.MapNodes(MapNodesQuery{GroupBy: MapGroupByEnvironment, Group: m.Groups[0].Key, Environment: "staging"})
	var iq *InvalidQueryError
	if !errors.As(err, &iq) || iq.Field != "environment" {
		t.Fatalf("err = %v, want an invalid environment", err)
	}
}

// checkNoOverlap fails when two groups sit closer than mapGroupGap.
func checkNoOverlap(t *testing.T, gs []MapGroup) {
	t.Helper()
	for i := range gs {
		for j := i + 1; j < len(gs); j++ {
			a, b := gs[i], gs[j]
			if d := math.Hypot(a.X-b.X, a.Y-b.Y); d < a.R+b.R+mapGroupGap-0.3 {
				t.Errorf("%s and %s are %.1f apart, radii %.1f and %.1f", a.Key, b.Key, d, a.R, b.R)
			}
		}
	}
}

func TestLayoutNoOverlap(t *testing.T) {
	for _, by := range []string{MapGroupByDomain, MapGroupByTeam, MapGroupByEnvironment} {
		checkNoOverlap(t, mustMap(t, defaultMapFleet(t), MapQuery{GroupBy: by}).Groups)
	}
	var nodes []layoutNode
	for i := range 500 {
		nodes = append(nodes, layoutNode{key: strconv.Itoa(i), degree: i % 7, inferred: i%11 == 0, sub: []string{"", "a", "b", "c"}[min(i%13, 3)]})
	}
	for _, ns := range [][]layoutNode{nodes, nodes[:1], slices.DeleteFunc(slices.Clone(nodes), func(n layoutNode) bool { return n.sub != "" })} {
		gl := layoutGroup(ns)
		for _, p := range gl.nodes {
			if math.Hypot(p.x, p.y) > gl.r {
				t.Errorf("node %s outside its group", p.key)
			}
		}
		for i := range gl.subs {
			for j := i + 1; j < len(gl.subs); j++ {
				a, b := gl.subs[i], gl.subs[j]
				if math.Hypot(a.x-b.x, a.y-b.y) < a.r+b.r+mapGapWorld*2-0.1 {
					t.Errorf("subs %s and %s overlap", a.key, b.key)
				}
			}
		}
	}
}

func TestLayoutHubsCentreInferredRim(t *testing.T) {
	gl := layoutGroup([]layoutNode{{key: "leaf", degree: 1}, {key: "rim", degree: 9, inferred: true}, {key: "hub", degree: 8}, {key: "b", degree: 1}})
	order := []string{}
	for _, p := range gl.nodes {
		order = append(order, p.key)
	}
	if !reflect.DeepEqual(order, []string{"hub", "b", "leaf", "rim"}) {
		t.Errorf("order %v", order)
	}
	d := func(i int) float64 { return math.Hypot(gl.nodes[i].x, gl.nodes[i].y) }
	if d(0) >= d(3) {
		t.Errorf("hub at %.1f, inferred at %.1f", d(0), d(3))
	}
}

func TestLayoutEnvironmentColumnsInOrder(t *testing.T) {
	svcs := []mapSvc{{name: "pay", domain: "shop", envs: []string{"dev", "prod", "pr-1"}}, {name: "web", domain: "shop", envs: []string{"dev", "staging"}}}
	q := mapFleet(t, svcs, []RawTarget{mapTarget("shop", "pay", "dev"), mapTarget("shop", "pay", "prod"), mapTarget("shop", "pay", "pr-1"),
		mapTarget("shop", "web", "dev"), mapTarget("shop", "web", "staging")})
	m := mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment, EnvironmentOrder: map[string]int{"dev": 0, "staging": 1, "prod": 2}})
	x := func(k string) float64 { return groupOf(m, k).X }
	if x(mapKeyPreviews) >= x("dev") || x("dev") >= x("staging") || x("staging") >= x("prod") || x("prod") >= x(UnknownBusinessDomain) {
		t.Errorf("groups %+v", m.Groups)
	}
	checkNoOverlap(t, m.Groups)
}

func TestLayoutBestCandidateFallsBackRightOfEverything(t *testing.T) {
	p := &circle{key: "p", r: 1}
	grid := &circleGrid{cell: 10000, cells: map[[2]int][]*circle{}}
	grid.add(&circle{key: "wall", r: 1000})
	x, y := bestCandidate([]*circle{p}, &circle{r: 2}, 1, grid, func(x, y float64) float64 { return 0 })
	if x != 4 || y != 0 {
		t.Errorf("fallback at %.1f, %.1f", x, y)
	}
}

func TestMapSkipsWhatItCannotPlace(t *testing.T) {
	q := defaultMapFleet(t)
	// A golden path's target draws no node.
	q.snap.Services[NewServiceKey("payments")].Template = true
	m := mustMap(t, q, MapQuery{GroupBy: MapGroupByEnvironment})
	if staging := groupOf(m, "staging"); staging == nil || staging.Services != 0 {
		t.Errorf("staging %+v", staging)
	}
	b := q.newMapBuild(MapQuery{GroupBy: MapGroupByDomain})
	b.domainGroups()
	// An item of no grouped service counts nowhere.
	if gs := b.itemGroups(AttentionItem{Service: "nope"}); gs != nil {
		t.Errorf("groups %v", gs)
	}
	// An edge with no match state draws no ribbon, nor a pair of targets
	// with no group.
	b.edges = []*mapEdge{{from: NewServiceKey("orders"), to: NewServiceKey("ledger"), e: &NeighborhoodEdge{}}}
	if rs, _, _ := b.ribbons(); len(rs) != 0 {
		t.Errorf("ribbons %+v", rs)
	}
	rs := map[ribbonKey]*MapRibbon{}
	b.environmentRibbons(rs, &mapEdge{pairs: []envPair{{from: "x", to: "y"}}}, false)
	if len(rs) != 0 {
		t.Errorf("ribbons %+v", rs)
	}
}
