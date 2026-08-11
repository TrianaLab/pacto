package fleet

import (
	"fmt"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Three Product answers that were each true in isolation and misleading on screen.
// A ranking label that means a different owner depending on how many rows fit; a
// declared contact block with nowhere in the product to read it; and a source
// reporting two different numbers for "how much did this contribute" without
// saying they measure different things.

// rankedFleet fills the owner ranking to its cap with same-sized owners, then adds
// the counterexample: a DRI whose name collides with a team already on screen, one
// service short of ranking and therefore invisible.
func rankedFleet(t *testing.T, withCollider bool) *Query {
	t.Helper()
	var revs []inventoryRevision
	owners := []contract.Owner{{Team: "alice"}}
	for i := 1; i <= 9; i++ {
		owners = append(owners, contract.Owner{Team: fmt.Sprintf("team-%d", i)})
	}
	for i, o := range owners {
		for s := 0; s < 2; s++ {
			revs = append(revs, inventoryRevision{
				service: fmt.Sprintf("svc-%d-%d", i, s), owner: o, distinct: "1"})
		}
	}
	if withCollider {
		revs = append(revs, inventoryRevision{
			service: "collider", owner: contract.Owner{DRI: "alice"}, distinct: "1"})
	}
	return inventoryFleet(t, revs...)
}

func aliceRow(t *testing.T, agg EntityAggregate) OwnerCount {
	t.Helper()
	for _, row := range agg.ByOwner {
		if row.Key == "team:alice" {
			return row
		}
	}
	t.Fatalf("team:alice did not rank: %+v", agg.ByOwner)
	return OwnerCount{}
}

// THE counterexample. `team:alice` ranks; `dri:alice` places eleventh and is cut.
// A reader looking at the visible rows sees one `alice` and cannot tell that the
// label is shared, so the row must carry its namespace — and whether it does cannot
// be decided from the rows that survived the cut, because the row that makes it
// ambiguous is precisely the one that did not.
func TestOwnerRanking_AmbiguityIsDecidedByThePopulationNotTheVisibleRows(t *testing.T) {
	agg := entityAggregateOf(t, rankedFleet(t, true), EntityFilter{Kinds: []EntityKind{KindService}})
	if len(agg.ByOwner) != maxOwnerRanking || agg.BeyondRanking != 1 {
		t.Fatalf("ranking = %d rows / %d beyond, want a full ranking with the collider cut off",
			len(agg.ByOwner), agg.BeyondRanking)
	}
	for _, row := range agg.ByOwner {
		if row.Key == "dri:alice" {
			t.Fatal("the collider ranked, so the fixture no longer tests the cut")
		}
	}
	row := aliceRow(t, agg)
	if !row.Ambiguous {
		t.Errorf("team:alice ranks as an unqualified %q while a DRI of the same name sits one row past the cut", row.Label)
	}
	if row.Label != "alice" || row.Kind != "team" {
		t.Errorf("row = %+v, want the label and its namespace kept apart", row)
	}
}

// And the other half: a namespace shown on every row would be noise. With no
// collider anywhere in the population the same owner is unambiguous, and nothing
// about the ranking changed.
func TestOwnerRanking_AnUncontestedLabelIsNotQualified(t *testing.T) {
	agg := entityAggregateOf(t, rankedFleet(t, false), EntityFilter{Kinds: []EntityKind{KindService}})
	if row := aliceRow(t, agg); row.Ambiguous {
		t.Errorf("team:alice is qualified with nobody to be confused with: %+v", row)
	}
}

// An owner in a dispute is still an owner in the population, so a label it shares
// is still ambiguous. Ranking the other alice unqualified because this one cannot
// rank would hide the collision behind the very classification that caused it.
func TestOwnerRanking_ACollidingLabelCountsEvenWhenItCannotRank(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "ledger", owner: contract.Owner{Team: "alice"}, distinct: "1"},
		inventoryRevision{service: "disputed", owner: contract.Owner{DRI: "alice"}, distinct: "1"},
		inventoryRevision{service: "disputed", owner: contract.Owner{Team: "carol"}, distinct: "2"},
	)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if agg.Ownership.Conflicting != 1 {
		t.Fatalf("ownership = %+v, want the disputed service conflicting", agg.Ownership)
	}
	if row := aliceRow(t, agg); !row.Ambiguous {
		t.Errorf("team:alice is unqualified though a DRI alice claims a disputed service: %+v", row)
	}
}

// CONTACTS ARE METADATA, NOT IDENTITY, AND THEY LIVE ON THE REVISION.
//
// An owner block of contacts alone is a real declaration with real content. Saying
// only "ownership declared, no identity" and leaving the block unreachable replaces
// one wrong answer with a vaguer one: the reader still cannot find out how to reach
// anybody. The contract revision is the contract inspector, so that is where the
// declaration is read — and it is read there without ever becoming a link.
func TestRevisionOwnership_CarriesTheDeclaredContactsWithoutInventingAnIdentity(t *testing.T) {
	pager := contract.OwnerContact{Type: "chat", Value: "#pager", Purpose: "escalation"}
	mail := contract.OwnerContact{Type: "email", Value: "ops@acme.com"}
	q := inventoryFleet(t, inventoryRevision{service: "reachable", distinct: "1", target: "prod",
		// Declared twice in the file, and out of set order.
		owner: contract.Owner{Contacts: []contract.OwnerContact{pager, mail, pager}}})

	d, err := q.EntityDetail(KindRevision, "reachable@sha256:rev0")
	if err != nil {
		t.Fatalf("EntityDetail(revision): %v", err)
	}
	own := d.Revision.Ownership
	if !own.Declared || own.Ref != nil || own.Owner != "" {
		t.Fatalf("ownership = %+v, want declared with no canonical identity", own)
	}
	if own.Contacts == nil {
		t.Fatal("the revision carries no contacts, so the whole declaration is unreadable in the product")
	}
	want := []OwnerContactPoint{
		{Type: "chat", Value: "#pager", Purpose: "escalation"},
		{Type: "email", Value: "ops@acme.com"},
	}
	if fmt.Sprint(own.Contacts.Items) != fmt.Sprint(want) {
		t.Errorf("contacts = %v, want the normalized set %v", own.Contacts.Items, want)
	}
	if own.Contacts.Total != 2 || own.Contacts.Truncated {
		t.Errorf("contacts preview = %+v, want a complete two-member set", own.Contacts)
	}

	// The service and the target show a DERIVED owner. Printing this revision's
	// contacts beside them would present one revision's escalation route as theirs.
	for _, tc := range []struct {
		kind EntityKind
		key  string
		own  func(*EntityDetail) *OwnershipInfo
	}{
		{KindService, "reachable", func(d *EntityDetail) *OwnershipInfo { return d.Service.Ownership }},
		{KindTarget, "prod/k8s/reachable", func(d *EntityDetail) *OwnershipInfo { return d.Target.Ownership }},
	} {
		d, err := q.EntityDetail(tc.kind, tc.key)
		if err != nil {
			t.Fatalf("EntityDetail(%s, %s): %v", tc.kind, tc.key, err)
		}
		got := tc.own(d)
		if got == nil || !got.Declared {
			t.Fatalf("%s ownership = %+v, want the declaration acknowledged", tc.kind, got)
		}
		if got.Contacts != nil {
			t.Errorf("%s carries contacts %+v; the declaration belongs to the revision", tc.kind, got.Contacts)
		}
	}
}

// DIRECT SOURCE RECORDS ARE NOT CONTRIBUTED PRODUCT ENTITIES.
//
// A source sends revisions and targets. The product also shows services, which no
// source ever sends and which are derived from those records. So "5 records" and
// "7 entities" are both true of the same source and neither is the other's total.
// The two are reported side by side, and this pins that they are allowed to differ.
func TestSourceDetail_CountsRawRecordsAndContributedEntitiesSeparately(t *testing.T) {
	q := inventoryFleet(t,
		inventoryRevision{service: "checkout", distinct: "1", target: "prod"},
		inventoryRevision{service: "checkout", distinct: "2"},
		inventoryRevision{service: "billing", distinct: "1"},
		inventoryRevision{service: "billing", distinct: "2"},
	)
	d, err := q.EntityDetail(KindSource, "local")
	if err != nil {
		t.Fatalf("EntityDetail(source, local): %v", err)
	}
	src := d.Source
	if src.RevisionCount != 4 || src.TargetCount != 1 {
		t.Errorf("records = %d revisions / %d targets, want the 4 and 1 the source sent",
			src.RevisionCount, src.TargetCount)
	}
	want := SourceContribution{Services: 2, Revisions: 4, Targets: 1}
	if src.Contributed != want {
		t.Errorf("contributed = %+v, want %+v", src.Contributed, want)
	}
	if src.Contributed.Services == 0 {
		t.Error("no service is attributed to the only source that could have produced one")
	}
	// The breakdown is the whole population's, and it accounts for the preview exactly.
	if sum := src.Contributed.Services + src.Contributed.Revisions + src.Contributed.Targets; sum != src.Entities.Total {
		t.Errorf("the breakdown sums to %d and the entity total is %d", sum, src.Entities.Total)
	}
	if records := src.RevisionCount + src.TargetCount; records == src.Entities.Total {
		t.Errorf("records and contributed entities both total %d, so the fixture no longer "+
			"distinguishes them", records)
	}
}

// FLEET-WIDE SOURCE HEALTH IS NOT THE HEALTH OF THE SOURCES THE META HAPPENED TO CARRY.
//
// Meta.Sources is capped, and past the cap it keeps the LEAST healthy first. Tallying
// that slice would answer a question nobody asked: a fleet of 60 healthy sources and
// one that is down would report every source it shows as degraded. The counts are
// taken over the whole population, so a consumer can say "1 of 61 is unavailable"
// while the list beside it shows only the worst 50.
func TestProductMeta_SourceCountsSpanThePopulationTheListIsCutFrom(t *testing.T) {
	snap := &FleetSnapshot{}
	for i := 0; i < MaxMetaSources+11; i++ {
		st := SourceAvailable
		if i == 0 {
			st = SourceUnavailable
		}
		snap.Sources = append(snap.Sources, SourceState{ID: fmt.Sprintf("src-%02d", i), Status: st})
	}
	m := NewQuery(snap).ProductMeta()
	if !m.SourcesTruncated || len(m.Sources) != MaxMetaSources {
		t.Fatalf("sources = %d carried / truncated=%v, want the cut this test exists to survive",
			len(m.Sources), m.SourcesTruncated)
	}
	want := SourceCounts{Total: MaxMetaSources + 11, Available: MaxMetaSources + 10, Unavailable: 1}
	if m.SourceCounts != want {
		t.Errorf("sourceCounts = %+v, want %+v", m.SourceCounts, want)
	}
	var carriedUnavailable int
	for _, s := range m.Sources {
		if s.Status == SourceUnavailable {
			carriedUnavailable++
		}
	}
	if carriedUnavailable != m.SourceCounts.Unavailable {
		// Not a contradiction to fix: it is the reason the field exists. Left as a
		// guard so a future cap change that makes the two agree is noticed here.
		t.Logf("the carried slice shows %d unavailable of %d, the population %d of %d",
			carriedUnavailable, len(m.Sources), m.SourceCounts.Unavailable, m.SourceCounts.Total)
	}
}

// A status the product does not recognize is left OUT of the four buckets rather
// than swept into one, so Total stays above their sum and a consumer drawing a
// distribution shows the shortfall instead of a clean, wrong 100%.
func TestProductMeta_SourceCountsDoNotAbsorbAnUnknownStatus(t *testing.T) {
	snap := &FleetSnapshot{Sources: []SourceState{
		{ID: "a", Status: SourceAvailable},
		{ID: "b", Status: SourceStale},
		{ID: "c", Status: SourcePartial},
		{ID: "d", Status: SourceStatus("quantum")},
	}}
	c := NewQuery(snap).ProductMeta().SourceCounts
	if c.Total != 4 {
		t.Fatalf("total = %d, want every source counted", c.Total)
	}
	if sum := c.Available + c.Partial + c.Stale + c.Unavailable; sum != 3 {
		t.Errorf("buckets sum to %d, want the unrecognized status left unclassified", sum)
	}
}

// The breakdown is counted over the complete attributable population, so a source
// past the preview bound still reports its true totals rather than the 200 it
// managed to carry.
func TestSourceDetail_ContributionSurvivesThePreviewBound(t *testing.T) {
	revs := make([]inventoryRevision, 0, MaxDetailPreview+10)
	for i := 0; i < MaxDetailPreview+10; i++ {
		revs = append(revs, inventoryRevision{service: fmt.Sprintf("svc-%03d", i), distinct: "1"})
	}
	q := inventoryFleet(t, revs...)
	d, err := q.EntityDetail(KindSource, "local")
	if err != nil {
		t.Fatalf("EntityDetail(source, local): %v", err)
	}
	src := d.Source
	if !src.Entities.Truncated || src.Entities.Count != MaxDetailPreview {
		t.Fatalf("preview = %+v, want it bounded so this proves something", src.Entities)
	}
	want := SourceContribution{Services: len(revs), Revisions: len(revs)}
	if src.Contributed != want {
		t.Errorf("contributed = %+v, want the complete %+v rather than the bounded preview", src.Contributed, want)
	}
}
