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

// A restricted view ranks only what the reader may see: hidden services are in no
// row and no count.
func TestNonCompliantRankings_RestrictedView(t *testing.T) {
	q := ownershipFleet(t)
	setService(t, q, "agreed", StatusNonCompliant, "pay", false)
	r := q.Restrict(func(string) bool { return false })
	agg := entityAggregateOf(t, r, EntityFilter{Kinds: []EntityKind{KindService}})
	rows := 0
	for _, o := range agg.NonCompliantByOwner {
		rows += o.NonCompliant
	}
	if rows+agg.NonCompliantBeyondRanking+agg.NonCompliantUnidentified != agg.ServiceCompliance.NonCompliant {
		t.Errorf("restricted owner ranking does not partition %d: %+v", agg.ServiceCompliance.NonCompliant, agg)
	}
}
