package fleet

import (
	"fmt"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// setService sets a built service's status and domain by name: the rankings read
// only those two facts, so a fixture states them directly.
func setService(t *testing.T, q *Query, name, status, domain string, unknown bool) {
	t.Helper()
	for _, s := range q.snap.Services {
		if s.Name == name {
			s.Status, s.BusinessDomain, s.BusinessDomainUnknown = status, domain, unknown
			return
		}
	}
	t.Fatalf("no service %s", name)
}

// The non-compliant rankings (B8) partition the matched non-compliant services: the
// ranked rows, what the cut dropped and the services no row can name sum to
// ServiceCompliance.NonCompliant, for owners and for domains alike.
func TestNonCompliantRankings_PartitionTheNonCompliantServices(t *testing.T) {
	q := ownershipFleet(t)
	nc := StatusNonCompliant
	setService(t, q, "agreed", nc, "pay", false)
	setService(t, q, "partly-silent", StatusCompliant, "pay", false)
	setService(t, q, "disputed", nc, "", false)
	setService(t, q, "nobody", nc, "", true)
	setService(t, q, "contacts-only", nc, "ops", false)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})

	if agg.ServiceCompliance.NonCompliant != 4 {
		t.Fatalf("nonCompliant = %d, want 4", agg.ServiceCompliance.NonCompliant)
	}
	if len(agg.NonCompliantByOwner) != 1 || agg.NonCompliantByOwner[0].Key != "team:team-a" ||
		agg.NonCompliantByOwner[0].NonCompliant != 1 || agg.NonCompliantByOwner[0].Services != 1 || agg.NonCompliantOwners != 1 {
		t.Errorf("owner ranking = %+v (%d owners), want team-a 1 of 1", agg.NonCompliantByOwner, agg.NonCompliantOwners)
	}
	// disputed, nobody and contacts-only have no single canonical owner.
	if agg.NonCompliantUnidentified != 3 {
		t.Errorf("unidentified = %d, want 3", agg.NonCompliantUnidentified)
	}
	if len(agg.NonCompliantByDomain) != 2 || agg.NonCompliantByDomain[0] != (DomainCount{Domain: "ops", Services: 1, NonCompliant: 1}) ||
		agg.NonCompliantByDomain[1] != (DomainCount{Domain: "pay", Services: 2, NonCompliant: 1}) {
		t.Errorf("domain ranking = %+v, want ops then pay (ties by name)", agg.NonCompliantByDomain)
	}
	if agg.NonCompliantNoDomain != 1 || agg.NonCompliantDomainUnknown != 1 || agg.NonCompliantDomains != 2 {
		t.Errorf("no domain %d, unknown %d, domains %d", agg.NonCompliantNoDomain, agg.NonCompliantDomainUnknown, agg.NonCompliantDomains)
	}
}

// The unidentified filter lists exactly the services the owner ranking cannot name.
func TestOwnershipUnidentified_ListsWhatTheRankingCannotName(t *testing.T) {
	q := ownershipFleet(t)
	for _, n := range []string{"agreed", "disputed", "nobody", "contacts-only"} {
		setService(t, q, n, StatusNonCompliant, "", false)
	}
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})

	list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipUnidentified, Status: StatusNonCompliant})
	if err != nil || list.Total != agg.NonCompliantUnidentified || list.Total != 3 {
		t.Errorf("ownership=unidentified&status=NonCompliant lists %v (%v), want %d", list, err, agg.NonCompliantUnidentified)
	}
}

// Past the bound the rankings keep what they cut as a count, so the partition holds.
func TestNonCompliantRankings_BoundedAndAccounted(t *testing.T) {
	var revs []inventoryRevision
	for o := 1; o <= 12; o++ {
		revs = append(revs, inventoryRevision{service: fmt.Sprintf("svc-%02d", o), owner: contract.Owner{Team: fmt.Sprintf("team-%02d", o)}})
	}
	q := inventoryFleet(t, revs...)
	for o := 1; o <= 12; o++ {
		setService(t, q, fmt.Sprintf("svc-%02d", o), StatusNonCompliant, fmt.Sprintf("d-%02d", o), false)
	}
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if len(agg.NonCompliantByOwner) != maxOwnerRanking || agg.NonCompliantBeyondRanking != 2 || agg.NonCompliantOwners != 12 {
		t.Errorf("owners: %d rows, beyond %d, owners %d", len(agg.NonCompliantByOwner), agg.NonCompliantBeyondRanking, agg.NonCompliantOwners)
	}
	if len(agg.NonCompliantByDomain) != maxOwnerRanking || agg.NonCompliantDomainsBeyond != 2 || agg.NonCompliantDomains != 12 {
		t.Errorf("domains: %d rows, beyond %d, domains %d", len(agg.NonCompliantByDomain), agg.NonCompliantDomainsBeyond, agg.NonCompliantDomains)
	}
}

// Owner and domain rows carry their non-compliant services, and sort=nonCompliant
// puts the most first, then the largest, then by name.
func TestEntities_SortNonCompliant(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "a1", owner: contract.Owner{Team: "alpha"}},
		inventoryRevision{service: "a2", owner: contract.Owner{Team: "alpha"}},
		inventoryRevision{service: "b1", owner: contract.Owner{Team: "beta"}},
		inventoryRevision{service: "c1", owner: contract.Owner{Team: "gamma"}},
		inventoryRevision{service: "d1", owner: contract.Owner{Team: "delta"}},
	)
	setService(t, q, "a1", StatusCompliant, "x", false)
	setService(t, q, "a2", StatusCompliant, "x", false)
	setService(t, q, "b1", StatusNonCompliant, "y", false)
	setService(t, q, "c1", StatusCompliant, "z", false)
	setService(t, q, "d1", StatusCompliant, "w", false)
	list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, Sort: SortNonCompliant})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range list.Entities {
		got = append(got, fmt.Sprintf("%s:%d/%d", e.Label, e.NonCompliant, e.Services))
	}
	if want := "[beta:1/1 alpha:0/2 delta:0/1 gamma:0/1]"; fmt.Sprint(got) != want {
		t.Errorf("owners by non-compliant = %v, want %s", got, want)
	}
	list, _ = q.Entities(EntityFilter{Kinds: []EntityKind{KindDomain}, Sort: SortNonCompliant})
	if list.Entities[0].Label != "y" || list.Entities[0].NonCompliant != 1 {
		t.Errorf("domains by non-compliant start with %+v", list.Entities[0])
	}
}

// A restricted view ranks only what the reader may see: in a partly hidden fleet the
// hidden services leave every row and count, the visible ones stay, and the owner
// list, its non-compliant filter and the rankings keep agreeing.
func TestNonCompliantRankings_RestrictedView(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "a1", owner: contract.Owner{Team: "alpha"}, target: "shop", compliance: StatusNonCompliant, domain: "pay"},
		inventoryRevision{service: "a2", owner: contract.Owner{Team: "alpha"}, target: "hush", compliance: StatusNonCompliant, domain: "pay"},
		inventoryRevision{service: "b1", owner: contract.Owner{Team: "beta"}, target: "hush", compliance: StatusNonCompliant, domain: "ops"},
		inventoryRevision{service: "c1", owner: contract.Owner{Team: "gamma"}, target: "shop", domain: "ops"},
	)
	// The fixture's targets come from a local source; the view restricts by
	// Kubernetes namespace.
	for _, tg := range q.snap.Targets {
		tg.Kind = SourceKindKubernetes
	}
	r := q.Restrict(func(ns string) bool { return ns == "shop" || ns == "" })

	full := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if full.ServiceCompliance.NonCompliant != 3 || full.NonCompliantOwners != 2 {
		t.Fatalf("unrestricted: %d non-compliant, %d owners", full.ServiceCompliance.NonCompliant, full.NonCompliantOwners)
	}
	agg := entityAggregateOf(t, r, EntityFilter{Kinds: []EntityKind{KindService}})
	assertRestrictedRankings(t, agg)
	owners, err := r.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, HasNonCompliant: true})
	if err != nil || owners.Total != agg.NonCompliantOwners || owners.Entities[0].Key != "team:alpha" || owners.Entities[0].NonCompliant != 1 {
		t.Errorf("restricted owners with a non-compliant service = %+v (%v)", owners, err)
	}
	all, _ := r.Entities(EntityFilter{Kinds: []EntityKind{KindOwner, KindDomain}})
	for _, e := range all.Entities {
		if e.Key == "team:beta" || (e.Key == "ops" && e.NonCompliant != 0) || (e.Key == "team:alpha" && e.Services != 1) {
			t.Errorf("a hidden service shows in %+v", e)
		}
	}
}

// assertRestrictedRankings checks the rankings of the restricted view above: only a1,
// alpha's one visible non-compliant service in pay, is counted.
func assertRestrictedRankings(t *testing.T, agg EntityAggregate) {
	t.Helper()
	if agg.ServiceCompliance.NonCompliant != 1 || agg.NonCompliantOwners != 1 || agg.NonCompliantUnidentified != 0 {
		t.Errorf("restricted: %d non-compliant, %d owners, %d unidentified", agg.ServiceCompliance.NonCompliant, agg.NonCompliantOwners, agg.NonCompliantUnidentified)
	}
	if want := []OwnerCount{{Key: "team:alpha", Label: "alpha", Kind: "team", Services: 1, Targets: 1, NonCompliant: 1}}; fmt.Sprint(agg.NonCompliantByOwner) != fmt.Sprint(want) {
		t.Errorf("restricted owner ranking = %+v, want %+v", agg.NonCompliantByOwner, want)
	}
	if len(agg.NonCompliantByDomain) != 1 || agg.NonCompliantByDomain[0] != (DomainCount{Domain: "pay", Services: 1, NonCompliant: 1}) || agg.NonCompliantDomains != 1 {
		t.Errorf("restricted domain ranking = %+v", agg.NonCompliantByDomain)
	}
}

// An owner row counts the non-compliant services it owns outright, as the rank
// figure does, so the owners the "has a non-compliant service" filter lists are the
// ones the figure counts; a disputed service is no row's.
func TestOwnerRows_CountWhatTheRankingCounts(t *testing.T) {
	q := ownershipFleet(t)
	setService(t, q, "disputed", StatusNonCompliant, "", false)
	setService(t, q, "agreed", StatusNonCompliant, "", false)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, HasNonCompliant: true})
	if err != nil || list.Total != agg.NonCompliantOwners || list.Total != 1 || list.Entities[0].Key != "team:team-a" {
		t.Fatalf("owners with a non-compliant service = %+v (%v), want team-a alone", list, err)
	}
	all, _ := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}})
	for _, e := range all.Entities {
		if e.Key == "team:team-x" && (e.Services != 1 || e.Owned != 0 || e.NonCompliant != 0) {
			t.Errorf("a disputing owner = %+v, want 1 claimed, 0 owned, 0 non-compliant", e)
		}
		if e.Key == "team:team-a" && (e.Owned != 1 || e.NonCompliant != 1 || e.RateBase() != 1) {
			t.Errorf("team-a = %+v, want 1 owned and non-compliant", e)
		}
	}
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, HasNonCompliant: true}); err == nil {
		t.Error("hasNonCompliant on services was accepted")
	}
}

// sort=rate puts the highest share of non-compliant services first, then the most,
// then by name; an owner's share is of the services it owns outright.
func TestEntities_SortRate(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "a1", owner: contract.Owner{Team: "alpha"}},
		inventoryRevision{service: "a2", owner: contract.Owner{Team: "alpha"}},
		inventoryRevision{service: "a3", owner: contract.Owner{Team: "alpha"}},
		inventoryRevision{service: "b1", owner: contract.Owner{Team: "beta"}},
		inventoryRevision{service: "c1", owner: contract.Owner{Team: "gamma"}},
		inventoryRevision{service: "c2", owner: contract.Owner{Team: "gamma"}},
		inventoryRevision{service: "d1", owner: contract.Owner{Team: "delta"}},
		inventoryRevision{service: "d2", owner: contract.Owner{Team: "delta"}},
		inventoryRevision{service: "e1", owner: contract.Owner{Team: "eps"}},
		inventoryRevision{service: "e2", owner: contract.Owner{Team: "eps"}},
		inventoryRevision{service: "e3", owner: contract.Owner{Team: "eps"}},
		inventoryRevision{service: "e4", owner: contract.Owner{Team: "eps"}},
	)
	for name, st := range map[string]string{"a1": StatusNonCompliant, "a2": StatusNonCompliant, "a3": StatusCompliant, "b1": StatusNonCompliant,
		"c1": StatusNonCompliant, "c2": StatusCompliant, "d1": StatusNonCompliant, "d2": StatusCompliant,
		"e1": StatusNonCompliant, "e2": StatusNonCompliant, "e3": StatusCompliant, "e4": StatusCompliant} {
		setService(t, q, name, st, "x", false)
	}
	list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, Sort: SortRate})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range list.Entities {
		got = append(got, fmt.Sprintf("%s:%d/%d", e.Label, e.NonCompliant, e.RateBase()))
	}
	// eps and delta share a rate; the one with more non-compliant services goes first.
	if want := "[beta:1/1 alpha:2/3 eps:2/4 delta:1/2 gamma:1/2]"; fmt.Sprint(got) != want {
		t.Errorf("owners by rate = %v, want %s", got, want)
	}
	d, _ := q.Entities(EntityFilter{Kinds: []EntityKind{KindDomain}, Sort: SortRate, HasNonCompliant: true})
	if d.Total != 1 || d.Entities[0].RateBase() != 12 || d.Entities[0].NonCompliant != 7 {
		t.Errorf("domains by rate = %+v", d.Entities)
	}
}

// An owner list filtered by a business domain keeps the owners with a service there,
// and a domain list filtered by an owner key the domains where that owner has one:
// the "N more" links of the domain and owner pages.
func TestEntities_OwnersOfADomainAndDomainsOfAnOwner(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "a1", owner: contract.Owner{Team: "alpha"}},
		inventoryRevision{service: "a2", owner: contract.Owner{Team: "alpha"}},
		inventoryRevision{service: "b1", owner: contract.Owner{Team: "beta"}},
		inventoryRevision{service: "n1", owner: contract.Owner{Team: "beta"}},
		inventoryRevision{service: "gp", owner: contract.Owner{Team: "alpha"}},
	)
	setService(t, q, "gp", StatusCompliant, "gpdomain", false)
	for _, s := range q.snap.Services {
		// A golden path is no service of its owner's estate: it relates alpha to
		// no domain, and gpdomain to no owner.
		s.Template = s.Name == "gp"
	}
	setService(t, q, "a1", StatusCompliant, "pay", false)
	setService(t, q, "a2", StatusCompliant, "ops", false)
	setService(t, q, "b1", StatusCompliant, "pay", false)
	setService(t, q, "n1", StatusCompliant, "", false)
	keys := func(f EntityFilter) string {
		l, err := q.Entities(f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range l.Entities {
			out = append(out, e.Key)
		}
		return fmt.Sprint(out)
	}
	if got := keys(EntityFilter{Kinds: []EntityKind{KindOwner}, BusinessDomain: "ops", Sort: SortName}); got != "[team:alpha]" {
		t.Errorf("owners in ops = %s", got)
	}
	if got := keys(EntityFilter{Kinds: []EntityKind{KindOwner}, BusinessDomain: NoBusinessDomain}); got != "[team:beta]" {
		t.Errorf("owners with a service in no domain = %s", got)
	}
	if got := keys(EntityFilter{Kinds: []EntityKind{KindDomain}, OwnerKey: "team:alpha", Sort: SortName}); got != "[ops pay]" {
		t.Errorf("domains of alpha = %s", got)
	}
	if got := keys(EntityFilter{Kinds: []EntityKind{KindDomain}, OwnerKey: "team:beta"}); got != "[pay]" {
		t.Errorf("domains of beta = %s, want no row for the service with no domain", got)
	}
	if got := keys(EntityFilter{Kinds: []EntityKind{KindOwner}, BusinessDomain: "gpdomain"}); got != "[]" {
		t.Errorf("owners in a golden path's domain = %s", got)
	}
	checkEveryKindKeepsCrossKindOff(t, q)
}

// checkEveryKindKeepsCrossKindOff: the CLI and MCP default names every product
// kind, and then a domain query gains no owner rows and an owner query no
// domain rows.
func checkEveryKindKeepsCrossKindOff(t *testing.T, q *Query) {
	t.Helper()
	for _, f := range []EntityFilter{{BusinessDomain: "pay"}, {OwnerKey: "team:alpha"}} {
		f.Kinds, f.Sort = productKinds(), SortName
		l, err := q.Entities(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range l.Entities {
			if f.BusinessDomain != "" && e.Kind == KindOwner || f.OwnerKey != "" && e.Kind == KindDomain {
				t.Errorf("%+v listed %s %s", f, e.Kind, e.Key)
			}
		}
	}
}

// sourceKind keeps the sources of one kind, and the source counts split the
// available ones by kind, so a per-kind tally and the list it opens agree.
func TestEntities_SourceKind(t *testing.T) {
	snap := &FleetSnapshot{Sources: []SourceState{
		{ID: "reg-a", Kind: "oci", Status: SourceAvailable}, {ID: "reg-b", Kind: "oci", Status: SourceAvailable},
		{ID: "cl-a", Kind: SourceKindKubernetes, Status: SourceAvailable}, {ID: "cl-b", Kind: SourceKindKubernetes, Status: SourceStale},
	}}
	q := NewQuery(snap)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, SourceKind: SourceKindKubernetes, SourceHealth: string(SourceAvailable)})
	if err != nil || l.Total != 1 || l.Entities[0].Key != "cl-a" {
		t.Fatalf("available clusters = %+v (%v)", l, err)
	}
	want := []SourceKindCount{{Kind: "oci", Count: 2}, {Kind: SourceKindKubernetes, Count: 1}}
	if got := l.Meta.SourceCounts.AvailableByKind; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("available by kind = %v, want %v", got, want)
	}
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, SourceKind: "oci"}); err == nil {
		t.Error("sourceKind on services was accepted")
	}
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, SourceKind: "Not A Kind"}); err == nil {
		t.Error("a malformed source kind was accepted")
	}
}

// productKinds is every product kind, as the CLI and MCP send them by default.
func productKinds() []EntityKind {
	var out []EntityKind
	for _, k := range ProductKinds() {
		out = append(out, EntityKind(k))
	}
	return out
}
