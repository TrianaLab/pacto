package fleet

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// inDomain sets the service's business domain.
func inDomain(d string) func(*contract.Contract, *contract.Bundle) {
	return func(c *contract.Contract, _ *contract.Bundle) { c.Service.Domain = d }
}

// withHealth declares the health capability, in the domain d.
func withHealth(d string) func(*contract.Contract, *contract.Bundle) {
	return func(c *contract.Contract, b *contract.Bundle) {
		inDomain(d)(c, b)
		c.Capabilities = []contract.Capability{{Type: contract.CapabilityHealth}}
	}
}

// failing is a target of name whose health check answered and reports it is failing.
func failing(name string) RawTarget {
	rt := runs(name)
	rt.Compliance, rt.EvidenceAt, rt.Coverage = StatusUnknown, freshAt, evaluated
	rt.Findings = []finding.Finding{healthFinding(finding.CodeHealthFailing, "down")}
	return rt
}

// ok is a Compliant target of name on fresh evidence.
func ok(name string) RawTarget { return withStatus(name, "app", StatusCompliant) }

// withStatus is a target of name named name-suffix with the compliance status.
func withStatus(name, suffix, status string) RawTarget {
	rt := runs(name)
	rt.Name, rt.Compliance, rt.EvidenceAt = name+"-"+suffix, status, freshAt
	return rt
}

func estateFleet(t *testing.T, window time.Duration, revs []RawRevision, targets []RawTarget, extra ...Source) *Query {
	t.Helper()
	src := NewMemorySource("local", "local", &Collection{Revisions: revs, Targets: targets})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: window}, append([]Source{src}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func domainRow(t *testing.T, q *Query, domain string) DomainTally {
	t.Helper()
	for _, d := range q.Overview().Summary.ByDomain {
		if d.Domain == domain {
			return d
		}
	}
	t.Fatalf("no domain row %q", domain)
	return DomainTally{}
}

func TestDomainTallyCountsServicesWithoutTargets(t *testing.T) {
	revs := []RawRevision{knowRev("a", inDomain("dormant")), knowRev("b", inDomain("dormant"))}
	got := domainRow(t, estateFleet(t, 0, revs, nil, emptyTargetSource()), "dormant")
	if got.Services != 2 || got.NotDeployed != 2 || got.Targets != 0 {
		t.Fatalf("dormant = %+v, want 2 services that run nowhere", got)
	}
	partial := NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourcePartial}})
	q := estateFleet(t, 0, revs, nil, partial)
	if got := domainRow(t, q, "dormant"); got.NotDeployed != 0 || got.Services != 2 {
		t.Fatalf("partial source: dormant = %+v, want placement unknown", got)
	}
	if k := q.Overview().Summary.Knowledge; k.Unknown != 2 {
		t.Errorf("partial source: knowledge = %+v, want both unknown", k)
	}
}

func TestDomainTallyOwnerUnknown(t *testing.T) {
	var unowned, unknown int
	for _, d := range ownerBucketFleet(t).Overview().Summary.ByDomain {
		unowned += d.Unowned
		unknown += d.OwnerUnknown
	}
	if unowned != 1 || unknown != 1 {
		t.Fatalf("domain rows: unowned %d, owner unknown %d, want 1 and 1", unowned, unknown)
	}
}

func TestDomainTallySortsByFailingHealthAfterNonCompliant(t *testing.T) {
	q := estateFleet(t, 0, []RawRevision{
		knowRev("a", inDomain("alpha")), knowRev("b", withHealth("beta")),
		knowRev("z", inDomain("zeta")), knowRev("g", func(c *contract.Contract, _ *contract.Bundle) {
			c.Service.Domain, c.Service.Summary = "gamma", ""
		}),
	}, []RawTarget{
		withStatus("a", "1", StatusUnknown), withStatus("a", "2", StatusUnknown),
		failing("b"), withStatus("z", "1", StatusNonCompliant), ok("g"),
	})
	var order []string
	for _, d := range q.Overview().Summary.ByDomain {
		order = append(order, d.Domain)
	}
	// Non-compliant first, then failing health above more unknown targets,
	// then unknown, then a domain whose only trouble is a knowledge gap.
	if want := []string{"zeta", "beta", "alpha", "gamma"}; !slices.Equal(order, want) {
		t.Fatalf("domains = %v, want %v", order, want)
	}
	if b := domainRow(t, q, "beta"); b.FailingHealth != 1 || b.WithGaps != 0 {
		t.Errorf("beta = %+v", b)
	}
	if g := domainRow(t, q, "gamma"); g.WithGaps != 1 {
		t.Errorf("gamma = %+v", g)
	}
}

// ownerTeam is the owner key of knowRev's team.
var ownerTeam = contract.Owner{Team: "t"}.KeyString()

func ownerSummary(t *testing.T, q *Query, key string) OwnerSummary {
	t.Helper()
	d, err := q.EntityDetail(KindOwner, key)
	if err != nil {
		t.Fatal(err)
	}
	return d.Owner.Summary
}

func domainSummary(t *testing.T, q *Query, key string) OwnerSummary {
	t.Helper()
	d, err := q.EntityDetail(KindDomain, key)
	if err != nil {
		t.Fatal(err)
	}
	return d.Domain.Summary
}

// depFleet: pay declares a dependency on a service nobody publishes and calls
// orders without declaring it; orders carries no readiness assessment.
func depFleet(t *testing.T) *Query {
	t.Helper()
	revs := []RawRevision{
		knowRev("pay", func(c *contract.Contract, _ *contract.Bundle) {
			c.Dependencies = []contract.Dependency{{Name: "ghost", Compatibility: "^1.0.0", Required: true}}
		}),
		knowRev("orders", func(c *contract.Contract, _ *contract.Bundle) { c.Readiness = nil }),
	}
	src := NewMemorySource("local", "local", &Collection{Revisions: revs, Targets: []RawTarget{runs("pay"), runs("orders")},
		Observed: []ObservedEdge{{From: "pay", To: "orders", Count: 1}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestOwnerSummaryCountsReadinessAndDependencies(t *testing.T) {
	q := depFleet(t)
	for name, sum := range map[string]OwnerSummary{"owner": ownerSummary(t, q, ownerTeam), "domain": domainSummary(t, q, "payments")} {
		if sum.Readiness.Passing != 1 || sum.Readiness.NotDeclared != 1 {
			t.Errorf("%s readiness = %+v", name, sum.Readiness)
		}
		if sum.UnresolvedDependencies != 1 || sum.UndeclaredDependencies != 1 {
			t.Errorf("%s dependencies: unresolved %d, undeclared %d", name, sum.UnresolvedDependencies, sum.UndeclaredDependencies)
		}
	}
}

func TestOwnerSummaryHealthAndKnowledge(t *testing.T) {
	q := estateFleet(t, 0, []RawRevision{
		knowRev("b", withHealth("payments")), knowRev("idle", nil),
	}, []RawTarget{failing("b")})
	sum := ownerSummary(t, q, ownerTeam)
	if sum.Health != (OperationalHealthTally{Unavailable: 1}) {
		t.Errorf("health = %+v", sum.Health)
	}
	if sum.NotDeployed != 1 || knowledgeCountsOf(sum.Knowledge) != (knowledgeCounts{Services: 2, Described: 1, WithGaps: 1, Gaps: KnowledgeGaps{NotDeployed: 1}}) {
		t.Errorf("notDeployed %d, knowledge %+v", sum.NotDeployed, sum.Knowledge)
	}
}

// A partial target source leaves placement unknown: never "runs nowhere",
// counted apart as placement unknown.
func TestOwnerSummaryPlacementUnknown(t *testing.T) {
	partial := NewMemorySource("k8s", SourceKindKubernetes, &Collection{State: &SourceState{Status: SourcePartial}})
	q := estateFleet(t, 0, []RawRevision{knowRev("a", inDomain("dormant")), knowRev("b", inDomain("dormant"))}, []RawTarget{runs("a")}, partial)
	if sum := domainSummary(t, q, "dormant"); sum.NotDeployed != 0 || sum.PlacementUnknown != 1 {
		t.Fatalf("notDeployed %d, placementUnknown %d, want 0 and 1", sum.NotDeployed, sum.PlacementUnknown)
	}
}

func TestDomainDetailNeverObserved(t *testing.T) {
	q := estateFleet(t, 0, []RawRevision{knowRev("a", inDomain("dormant")), knowRev("b", inDomain("dormant"))}, nil, emptyTargetSource())
	sum := domainSummary(t, q, "dormant")
	if sum.Services != 2 || sum.NotDeployed != 2 || sum.Knowledge.Gaps.NotDeployed != 2 || sum.Readiness.Passing != 2 {
		t.Fatalf("dormant summary = %+v, want two services that run nowhere", sum)
	}
}

// rowFleetOf returns the owner or domain list rows keyed by label.
func rowsOf(t *testing.T, q *Query, kind EntityKind, sort string) []EntityRef {
	t.Helper()
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{kind}, Sort: sort})
	if err != nil {
		t.Fatal(err)
	}
	return l.Entities
}

// estateRows: in payments, a unknown, b failing, s stale, g with a gap and
// "shared" disputed by team t and team u; in retail, one clean service.
func estateRows(t *testing.T) *Query {
	t.Helper()
	shared := knowRev("shared", nil)
	other := knowRev("shared", func(c *contract.Contract, _ *contract.Bundle) {
		c.Service.Version, c.Service.Owner = "1.1.0", contract.Owner{Team: "u"}
	})
	other.Digest = "sha256:shared-u"
	sharedT := withStatus("shared", "1", StatusUnknown)
	return estateFleet(t, time.Hour, []RawRevision{
		knowRev("a", nil), knowRev("b", withHealth("payments")), knowRev("s", nil),
		knowRev("g", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Summary = "" }),
		knowRev("clean", inDomain("retail")), shared, other,
	}, []RawTarget{
		withStatus("a", "1", StatusUnknown), failing("b"), ok("g"), ok("clean"), sharedT,
		func() RawTarget {
			rt := withStatus("s", "1", StatusCompliant)
			rt.EvidenceAt = ptrTime(fixedNow().Add(-2 * time.Hour))
			return rt
		}(),
	})
}

func TestOwnerRowCounts(t *testing.T) {
	for _, r := range rowsOf(t, estateRows(t), KindOwner, "") {
		if r.Key != ownerTeam {
			continue
		}
		// b counts as unknown too: a failing check keeps compliance unknown.
		// "shared" is disputed, so it is no part of the owned-outright counts.
		if r.Services != 6 || r.Owned != 5 || r.Unknown != 2 || r.FailingHealth != 1 || r.Stale != 1 || r.WithGaps != 1 {
			t.Fatalf("owner row = %+v", r)
		}
		return
	}
	t.Fatal("no row for team t")
}

func TestDomainRowCounts(t *testing.T) {
	rows := rowsOf(t, estateRows(t), KindDomain, "")
	byKey := map[string]EntityRef{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	if p := byKey["payments"]; p.Services != 5 || p.Unknown != 3 || p.FailingHealth != 1 || p.Stale != 1 || p.WithGaps != 1 {
		t.Fatalf("payments row = %+v", p)
	}
	if r := byKey["retail"]; r.Unknown+r.Stale+r.FailingHealth+r.WithGaps+r.NonCompliant != 0 {
		t.Fatalf("retail row = %+v", r)
	}
}

func TestSortHealthRanksOwnerAndDomainRows(t *testing.T) {
	q := estateFleet(t, 0, []RawRevision{
		knowRev("a", inDomain("aardvark")), knowRev("z", inDomain("zebra")),
	}, []RawTarget{ok("a"), withStatus("z", "1", StatusUnknown)})
	rows := rowsOf(t, q, KindDomain, SortHealth)
	if len(rows) != 2 || rows[0].Key != "zebra" {
		t.Fatalf("health order = %+v, want the domain with an unknown service first", rows)
	}
	// Ranks compare the estate counts in order: non-compliant, failing health,
	// unknown, stale, then gaps.
	cases := []struct{ worse, better EntityRef }{
		{EntityRef{Kind: KindOwner, NonCompliant: 1}, EntityRef{Kind: KindOwner, FailingHealth: 5}},
		{EntityRef{Kind: KindDomain, FailingHealth: 1}, EntityRef{Kind: KindDomain, Unknown: 5}},
		{EntityRef{Kind: KindDomain, Unknown: 1}, EntityRef{Kind: KindDomain, Stale: 5}},
		{EntityRef{Kind: KindOwner, Stale: 1}, EntityRef{Kind: KindOwner, WithGaps: 5}},
		{EntityRef{Kind: KindOwner, WithGaps: 1, Label: "b"}, EntityRef{Kind: KindOwner, Label: "a"}},
	}
	for _, tc := range cases {
		if !healthLess(tc.worse, tc.better) || healthLess(tc.better, tc.worse) {
			t.Errorf("%+v must rank above %+v", tc.worse, tc.better)
		}
	}
	// Other kinds keep their status order and ignore the estate counts.
	if healthLess(EntityRef{Kind: KindService, NonCompliant: 3, Label: "b"}, EntityRef{Kind: KindService, Label: "a"}) {
		t.Error("a golden path's created counts must not reorder services")
	}
}

func TestSortGaps(t *testing.T) {
	q := estateFleet(t, 0, []RawRevision{
		knowRev("a", inDomain("aardvark")),
		knowRev("z", func(c *contract.Contract, _ *contract.Bundle) { c.Service.Domain, c.Service.Summary = "zebra", "" }),
	}, []RawTarget{ok("a"), ok("z")})
	rows := rowsOf(t, q, KindDomain, SortGaps)
	if len(rows) != 2 || rows[0].Key != "zebra" || rows[0].WithGaps != 1 {
		t.Fatalf("gaps order = %+v", rows)
	}
	if _, err := q.Entities(EntityFilter{Sort: "bogus"}); err == nil || !strings.Contains(err.Error(), "gaps") {
		t.Errorf("sort error %v must list gaps", err)
	}
}

// jsonKeys is every JSON key the struct type declares.
func jsonKeys(typ reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := range typ.NumField() {
		if name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}

func TestOverviewJSONKeepsFields(t *testing.T) {
	keep := map[reflect.Type]string{
		reflect.TypeFor[OverviewSummary](): "services servicesNeedingAttention goldenPaths invalidGoldenPaths revisions targets invalidRevisions " +
			"exactTargetLinks inferredTargetLinks ambiguousTargetLinks unresolvedTargetLinks compliantTargets nonCompliantTargets " +
			"unknownTargets warningTargets invalidTargets referenceTargets notEvaluatedTargets otherComplianceTargets staleTargets " +
			"unresolvedRelationships unsatisfiedRanges lifecycleConsumers observedOnlyRelationships uncontractedCallers degradedSources " +
			"staleSources unavailableSources recentEvidence ownership readiness evidence byScope scopes byDomain domains",
		reflect.TypeFor[DomainTally]():     "domain domainUnknown services targets nonCompliant unknown stale unowned",
		reflect.TypeFor[OwnershipTally]():  "consistent conflicting unowned",
		reflect.TypeFor[ComplianceTally](): "compliant nonCompliant unknown warning invalid reference notEvaluated other",
		reflect.TypeFor[EntryPoint]():      "label description view category count severity",
		reflect.TypeFor[AttentionItem]():   "entity service label severity code category summary reason source nextStep",
	}
	for typ, want := range keep {
		have := jsonKeys(typ)
		for _, k := range strings.Fields(want) {
			if !have[k] {
				t.Errorf("%s lost the JSON key %q", typ.Name(), k)
			}
		}
	}
	// The marshalled overview carries every summary key that is never omitted.
	raw, err := json.Marshal(estateRows(t).Overview())
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Summary map[string]json.RawMessage }
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range strings.Fields(keep[reflect.TypeFor[OverviewSummary]()]) {
		if _, ok := got.Summary[k]; !ok {
			t.Errorf("marshalled summary has no %q", k)
		}
	}
}

// staleFleet: pay runs a Compliant target with fresh evidence and one with
// stale evidence; orders runs a Compliant target with stale evidence.
func staleFleet(t *testing.T) *Query {
	t.Helper()
	old := ptrTime(fixedNow().Add(-2 * time.Hour))
	stale := func(name, suffix string) RawTarget {
		rt := withStatus(name, suffix, StatusCompliant)
		rt.EvidenceAt = old
		return rt
	}
	return estateFleet(t, time.Hour, []RawRevision{knowRev("pay", nil), knowRev("orders", nil)},
		[]RawTarget{withStatus("pay", "fresh", StatusCompliant), stale("pay", "old"), stale("orders", "old")})
}

func TestComplianceStaleCompliantIsSubset(t *testing.T) {
	var c ComplianceTally
	c.addStale(StatusCompliant, true)
	c.addStale(StatusCompliant, false)
	c.addStale(StatusUnknown, true)
	if c != (ComplianceTally{Compliant: 2, Unknown: 1, StaleCompliant: 1}) {
		t.Fatalf("tally = %+v", c)
	}
	sum := staleFleet(t).Overview().Summary
	if sum.CompliantTargets != 3 || sum.StaleCompliantTargets != 2 || sum.StaleCompliantTargets > sum.CompliantTargets {
		t.Fatalf("compliant %d, stale compliant %d", sum.CompliantTargets, sum.StaleCompliantTargets)
	}
}

func TestServiceStaleCompliant(t *testing.T) {
	q := staleFleet(t)
	sv, err := q.GetService(string(NewServiceKeyDomain("", "pay")))
	if err != nil {
		t.Fatal(err)
	}
	sum, _ := q.serviceSummary(sv)
	if sum.Compliance.Compliant != 2 || sum.Compliance.StaleCompliant != 1 {
		t.Fatalf("pay compliance = %+v", sum.Compliance)
	}
}

func TestOwnerSummaryStaleCompliant(t *testing.T) {
	q := staleFleet(t)
	for name, sum := range map[string]OwnerSummary{"owner": ownerSummary(t, q, ownerTeam), "domain": domainSummary(t, q, "payments")} {
		if sum.Compliance.Compliant != 3 || sum.Compliance.StaleCompliant != 2 {
			t.Errorf("%s compliance = %+v", name, sum.Compliance)
		}
	}
}

func TestEntityAggregateStaleCompliant(t *testing.T) {
	q := staleFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService, KindTarget}})
	if err != nil {
		t.Fatal(err)
	}
	if c := l.Aggregate.ServiceCompliance; c.Compliant != 2 || c.StaleCompliant != 2 {
		t.Errorf("service compliance = %+v, want both services resting on stale evidence", c)
	}
	if c := l.Aggregate.TargetCompliance; c.Compliant != 3 || c.StaleCompliant != 2 {
		t.Errorf("target compliance = %+v", c)
	}
}
