package fleet

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// An inventory answer ("12 services have no declared owner", "40% of revisions were
// never assessed") is only worth drawing if it covers the WHOLE matched population
// and if every bucket means exactly one thing. These tests pin both: the partitions,
// the ranking that is deliberately NOT a partition, and the filters that turn each
// bucket back into the list it counted.

// readinessSection returns a declared assessment with one claim in the given state,
// expiring on the given date. It is the smallest input that reaches each of
// readiness.Evaluate's outcomes.
func readinessSection(status, expires string) *contract.Readiness {
	return &contract.Readiness{
		Expires: expires,
		Claims:  []contract.ReadinessClaim{{ID: "dash", Type: "url", Status: status, Evidence: "https://x", Weight: 10}},
	}
}

// inventoryRevision is one contract in the inventory fixture: a service name, the
// owner its revision declares, the readiness it declares and, optionally, the
// scope of one operational target running it.
type inventoryRevision struct {
	service  string
	owner    contract.Owner
	ready    *contract.Readiness
	distinct string // content that makes two same-version revisions distinct
	target   string // scope of one target running this revision, if any
}

// inventoryFleet builds a snapshot from revision declarations, plus whatever
// targets those declarations asked for, from a single local collection. Ownership
// and readiness are properties of what was AUTHORED, so a fixture that declares
// little else keeps each assertion about the authored facts rather than about
// runtime.
func inventoryFleet(t *testing.T, revs ...inventoryRevision) *Query {
	t.Helper()
	raw := make([]RawRevision, 0, len(revs))
	var targets []RawTarget
	for i, r := range revs {
		c := &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: r.service, Version: "1.0.0", Owner: r.owner},
			Readiness:    r.ready,
		}
		fsys := fstest.MapFS{"note.txt": {Data: []byte(r.distinct)}}
		digest := fmt.Sprintf("sha256:rev%d", i)
		raw = append(raw, RawRevision{
			Bundle: &contract.Bundle{Contract: c, FS: fsys},
			Digest: digest,
		})
		if r.target != "" {
			targets = append(targets, RawTarget{
				Scope: r.target, Kind: "k8s", Name: r.service, Service: r.service,
				Digest: digest, Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow()),
			})
		}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{Revisions: raw, Targets: targets}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return NewQuery(snap)
}

// ownershipFleet is the fixture every ownership assertion reads: one service per
// state, including the contacts-only service that has a real owner but no label to
// rank it under.
func ownershipFleet(t *testing.T) *Query {
	t.Helper()
	return inventoryFleet(t,
		inventoryRevision{service: "agreed", owner: contract.Owner{Team: "team-a"}, distinct: "1"},
		inventoryRevision{service: "agreed", owner: contract.Owner{Team: "team-a"}, distinct: "2"},
		// The same team declared two ways: silence on one revision is not a conflict.
		inventoryRevision{service: "partly-silent", owner: contract.Owner{Team: "team-b"}, distinct: "1"},
		inventoryRevision{service: "partly-silent", distinct: "2"},
		inventoryRevision{service: "disputed", owner: contract.Owner{Team: "team-x"}, distinct: "1"},
		inventoryRevision{service: "disputed", owner: contract.Owner{Team: "team-y"}, distinct: "2"},
		inventoryRevision{service: "nobody", distinct: "1"},
		inventoryRevision{service: "contacts-only", owner: contract.Owner{
			Contacts: []contract.OwnerContact{{Type: "slack", Value: "#pager"}},
		}, distinct: "1"},
	)
}

// The three ownership buckets must be a partition of the SERVICE population, and
// they must not collapse into each other: "two teams claim this" and "nobody claims
// this" need opposite fixes, and a service whose revisions disagree is not owned.
func TestOwnershipTally_PartitionsServicesByWhatTheirRevisionsDeclare(t *testing.T) {
	agg := entityAggregateOf(t, ownershipFleet(t), EntityFilter{Kinds: []EntityKind{KindService}})

	// agreed, partly-silent and contacts-only are consistently owned; disputed is
	// conflicting; nobody is unowned.
	want := OwnershipTally{Consistent: 3, Conflicting: 1, Unowned: 1}
	if agg.Ownership != want {
		t.Errorf("ownership = %+v, want %+v", agg.Ownership, want)
	}
	if agg.Ownership.Total() != agg.Services {
		t.Errorf("ownership covers %d services, want the full population of %d", agg.Ownership.Total(), agg.Services)
	}
}

// A service whose only declared owner carries contacts but neither a team nor a DRI
// is OWNED: telling its reader to declare an owner would send them to add what is
// already there. It simply has no label to rank under, so it is consistent and
// absent from the ranking.
func TestOwnershipTally_ContactsOnlyOwnerIsOwnedButUnrankable(t *testing.T) {
	q := ownershipFleet(t)
	n, label := q.ownershipState(q.snap.Services["contacts-only"])
	if n != 1 || label != "" {
		t.Errorf("ownershipState = (%d, %q), want (1, \"\") — owned, but nothing to rank under", n, label)
	}
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	for _, c := range agg.ByOwner {
		if c.Owner == "" {
			t.Errorf("an empty owner label reached the ranking: %+v", agg.ByOwner)
		}
	}
}

// The ranking is not a partition and must be able to say so arithmetically: the
// ranked rows plus OtherOwners account for exactly the consistently owned services,
// and DistinctOwners reports how many owners the cut left out.
func TestByOwner_RankingIsBoundedAndAccountsForWhatItOmits(t *testing.T) {
	var revs []inventoryRevision
	// 12 owners, each owning a different number of services, so the cut at
	// maxOwnerRanking drops real rows rather than empty ones.
	for owner := 1; owner <= 12; owner++ {
		for svc := 0; svc < owner; svc++ {
			revs = append(revs, inventoryRevision{
				service: fmt.Sprintf("svc-%02d-%02d", owner, svc),
				owner:   contract.Owner{Team: fmt.Sprintf("team-%02d", owner)},
			})
		}
	}
	revs = append(revs, inventoryRevision{service: "orphan"})
	agg := entityAggregateOf(t, inventoryFleet(t, revs...), EntityFilter{Kinds: []EntityKind{KindService}})

	if len(agg.ByOwner) != maxOwnerRanking {
		t.Fatalf("ranking holds %d rows, want the bound of %d", len(agg.ByOwner), maxOwnerRanking)
	}
	if agg.DistinctOwners != 12 {
		t.Errorf("distinctOwners = %d, want 12 — the ranking must say how many owners it did not show", agg.DistinctOwners)
	}
	if agg.ByOwner[0].Owner != "team-12" || agg.ByOwner[0].Services != 12 {
		t.Errorf("ranking is not largest-first: %+v", agg.ByOwner[0])
	}
	ranked := 0
	for _, c := range agg.ByOwner {
		ranked += c.Services
	}
	// team-01 and team-02 fell past the bound: 1 + 2 services.
	if agg.OtherOwners != 3 {
		t.Errorf("otherOwners = %d, want 3", agg.OtherOwners)
	}
	if ranked+agg.OtherOwners != agg.Ownership.Consistent {
		t.Errorf("ranked %d + other %d != consistent %d — the ranking loses services",
			ranked, agg.OtherOwners, agg.Ownership.Consistent)
	}
	if agg.Ownership.Unowned != 1 {
		t.Errorf("the unowned service must stay out of the ranking and in its own bucket: %+v", agg.Ownership)
	}
}

// Ties break on the owner label so two owners with equal estates always rank in the
// same order: a ranking that reshuffles between two loads of the same page teaches
// its reader nothing.
func TestRankOwners_TiesAreDeterministic(t *testing.T) {
	got, other := rankOwners(map[string]*OwnerCount{
		"zed":   {Owner: "zed", Services: 2},
		"alpha": {Owner: "alpha", Services: 2},
		"mid":   {Owner: "mid", Services: 5},
	})
	if other != 0 {
		t.Errorf("otherOwners = %d, want 0 below the bound", other)
	}
	want := []string{"mid", "alpha", "zed"}
	for i, w := range want {
		if got[i].Owner != w {
			t.Fatalf("ranking = %+v, want order %v", got, want)
		}
	}
}

// readinessFleet declares one revision in each readiness state. The unit is always
// the revision: readiness is authored preparedness of one immutable contract.
func readinessFleet(t *testing.T) *Query {
	t.Helper()
	return inventoryFleet(t,
		inventoryRevision{service: "ready", owner: contract.Owner{Team: "team-a"}, ready: readinessSection(contract.StatusDone, "2099-12-31")},
		inventoryRevision{service: "under", owner: contract.Owner{Team: "team-a"}, ready: readinessSection(contract.StatusNotDone, "2099-12-31")},
		inventoryRevision{service: "lapsed", owner: contract.Owner{Team: "team-a"}, ready: readinessSection(contract.StatusDone, "2000-01-01")},
		inventoryRevision{service: "unassessed", owner: contract.Owner{Team: "team-a"}},
	)
}

// The four readiness buckets are exactly the states readiness.Evaluate computes, and
// they partition the REVISION population. "Nobody wrote an assessment" is its own
// bucket: it is not the same answer as "the assessment does not pass".
func TestReadinessTally_PartitionsRevisionsByTheirDeclaredAssessment(t *testing.T) {
	agg := entityAggregateOf(t, readinessFleet(t), EntityFilter{Kinds: []EntityKind{KindRevision}})
	want := ReadinessTally{Passing: 1, BelowThreshold: 1, Expired: 1, NotDeclared: 1}
	if agg.Readiness != want {
		t.Errorf("readiness = %+v, want %+v", agg.Readiness, want)
	}
	if agg.Readiness.Total() != agg.Revisions {
		t.Errorf("readiness covers %d revisions, want the full population of %d", agg.Readiness.Total(), agg.Revisions)
	}
}

// An expired assessment is not a below-threshold one even when every claim is done:
// it earns no weight at all and must not be read as a current failure to prepare.
func TestReadinessTally_ExpiredIsNotBelowThreshold(t *testing.T) {
	var tally ReadinessTally
	tally.add(&readiness.Result{Expired: true, Score: 100, MinScore: 100})
	if tally != (ReadinessTally{Expired: 1}) {
		t.Errorf("tally = %+v, want the expired bucket alone", tally)
	}
}

// Readiness and compliance are orthogonal axes, and the product must be able to
// express the case that proves it: a revision whose declared readiness passes,
// running on an operational target observed to violate its contract.
func TestReadinessAndCompliance_APassingRevisionCanRunOnANonCompliantTarget(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "ready-but-drifted", Version: "1.0.0", Owner: contract.Owner{Team: "team-a"}},
		Readiness:    readinessSection(contract.StatusDone, "2099-12-31"),
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:rd"}},
		Targets: []RawTarget{{
			Scope: "prod", Kind: "k8s", Name: "rd-app", Service: "ready-but-drifted",
			Digest: "sha256:rd", Compliance: StatusNonCompliant, EvidenceAt: ptrTime(fixedNow()),
		}},
	}))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	q := NewQuery(snap)
	ov := q.Overview()
	if ov.Summary.Readiness.Passing != 1 {
		t.Errorf("readiness = %+v, want the revision's assessment passing", ov.Summary.Readiness)
	}
	if ov.Summary.NonCompliantTargets != 1 {
		t.Errorf("nonCompliantTargets = %d, want 1", ov.Summary.NonCompliantTargets)
	}
}

// The overview tallies the COMPLETE fleet: both are authored-fact populations with a
// denominator of their own (services for ownership, revisions for readiness), and
// neither is a slice of a preview.
func TestOverviewSummary_OwnershipAndReadinessCoverTheWholeFleet(t *testing.T) {
	sum := readinessFleet(t).Overview().Summary
	if sum.Ownership.Total() != sum.Services {
		t.Errorf("ownership covers %d of %d services", sum.Ownership.Total(), sum.Services)
	}
	if sum.Readiness.Total() != sum.Revisions {
		t.Errorf("readiness covers %d of %d revisions", sum.Readiness.Total(), sum.Revisions)
	}
	if sum.Readiness != (ReadinessTally{Passing: 1, BelowThreshold: 1, Expired: 1, NotDeclared: 1}) {
		t.Errorf("fleet readiness = %+v", sum.Readiness)
	}
	if sum.Ownership != (OwnershipTally{Consistent: 4}) {
		t.Errorf("fleet ownership = %+v", sum.Ownership)
	}
}

// entityAggregateOf runs one query and returns the aggregate over everything it
// matched.
func entityAggregateOf(t *testing.T, q *Query, f EntityFilter) EntityAggregate {
	t.Helper()
	list, err := q.Entities(f)
	if err != nil {
		t.Fatalf("Entities(%+v): %v", f, err)
	}
	if list.Aggregate.Matched != list.Total {
		t.Fatalf("aggregate covers %d, list matched %d", list.Aggregate.Matched, list.Total)
	}
	return list.Aggregate
}

// The aggregate is the denominator the page is a slice OF, so it must not move when
// the page does. A distribution computed from the page would call the first two rows
// the fleet.
func TestEntityAggregate_IsIndependentOfPaging(t *testing.T) {
	q := ownershipFleet(t)
	full := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	page := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Limit: 2, Offset: 2})
	if full.Matched <= 2 {
		t.Fatalf("fixture does not exercise paging: %d services", full.Matched)
	}
	if fmt.Sprintf("%+v", full) != fmt.Sprintf("%+v", page) {
		t.Errorf("aggregate moved with the page:\n page: %+v\n full: %+v", page, full)
	}
}

// Every bucket must be reachable as a list, or the number is a dead end: the filter
// classifies by the same rule the tally counts by, so the two can never disagree.
func TestOwnershipFilter_TurnsEachBucketBackIntoItsList(t *testing.T) {
	q := ownershipFleet(t)
	for state, want := range map[string][]string{
		OwnershipConsistent:  {"agreed", "contacts-only", "partly-silent"},
		OwnershipConflicting: {"disputed"},
		OwnershipUnowned:     {"nobody"},
	} {
		list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindService}, Ownership: state})
		if err != nil {
			t.Fatalf("Entities(ownership=%s): %v", state, err)
		}
		var got []string
		for _, e := range list.Entities {
			got = append(got, e.Key)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("ownership=%s listed %v, want %v", state, got, want)
		}
		if list.Total != len(want) {
			t.Errorf("ownership=%s matched %d, want %d", state, list.Total, len(want))
		}
	}
}

func TestReadinessFilter_TurnsEachBucketBackIntoItsList(t *testing.T) {
	q := readinessFleet(t)
	for state, want := range map[string]string{
		ReadinessPassing:        "ready",
		ReadinessBelowThreshold: "under",
		ReadinessExpired:        "lapsed",
		ReadinessNotDeclared:    "unassessed",
	} {
		list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindRevision}, Readiness: state})
		if err != nil {
			t.Fatalf("Entities(readiness=%s): %v", state, err)
		}
		if list.Total != 1 {
			t.Fatalf("readiness=%s matched %d revisions, want 1", state, list.Total)
		}
		if list.Entities[0].ParentService != want {
			t.Errorf("readiness=%s listed %s, want a revision of %s", state, list.Entities[0].ParentService, want)
		}
	}
}

// Ownership classifies services and readiness classifies revisions. On a mixed
// query each filter must narrow to the kind it is about rather than letting the
// other kind through unfiltered.
func TestOwnershipAndReadinessFilters_NeverMatchAnotherKind(t *testing.T) {
	q := readinessFleet(t)
	list, err := q.Entities(EntityFilter{Ownership: OwnershipConsistent})
	if err != nil {
		t.Fatalf("Entities: %v", err)
	}
	for _, e := range list.Entities {
		if e.Kind != KindService {
			t.Errorf("an ownership filter matched a %s", e.Kind)
		}
	}
	rl, err := q.Entities(EntityFilter{Readiness: ReadinessPassing})
	if err != nil {
		t.Fatalf("Entities: %v", err)
	}
	for _, e := range rl.Entities {
		if e.Kind != KindRevision {
			t.Errorf("a readiness filter matched a %s", e.Kind)
		}
	}
}

// A filter value outside its enum, or applied to a kind it cannot describe, is a
// typed error rather than a silently empty page that reads as "nothing is wrong".
func TestOwnershipAndReadinessFilters_RejectNonsense(t *testing.T) {
	q := ownershipFleet(t)
	for name, tc := range map[string]struct {
		filter EntityFilter
		field  string
	}{
		"unknown ownership value": {EntityFilter{Ownership: "orphaned"}, "ownership"},
		"unknown readiness value": {EntityFilter{Readiness: "green"}, "readiness"},
		"ownership on revisions":  {EntityFilter{Kinds: []EntityKind{KindRevision}, Ownership: OwnershipUnowned}, "ownership"},
		"readiness on services":   {EntityFilter{Kinds: []EntityKind{KindService}, Readiness: ReadinessPassing}, "readiness"},
		"ownership on sources":    {EntityFilter{Kinds: []EntityKind{KindSource}, Ownership: OwnershipConsistent}, "ownership"},
	} {
		_, err := q.Entities(tc.filter)
		var iqe *InvalidQueryError
		if !errors.As(err, &iqe) || iqe.Field != tc.field {
			t.Errorf("%s: err = %v, want an InvalidQueryError on %q", name, err, tc.field)
		}
	}
}
