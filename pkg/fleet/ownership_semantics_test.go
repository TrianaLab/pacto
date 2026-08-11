package fleet

import (
	"fmt"
	"sort"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// ONE ownership question, asked the same way everywhere.
//
// Ownership is authored per contract revision. A service's Owner field is only a
// SUMMARY -- the owner declared by its lowest-keyed revision -- and deriveOwner
// records an OWNER_CONFLICT limitation when the revisions disagree. Anything that
// answers "who owns this" from that summary answers a different question from the
// ownership aggregate, which reads every revision. The two then contradict each
// other in public: the aggregate calls a service CONFLICTING and keeps it out of
// every owner's ranking, while an owner filter hands it back to whichever team
// won the summary -- and hides it from the other team entirely.
//
// The rule these tests fix: owner=x matches a service when AT LEAST ONE of its
// revisions declares an owner matching x. A count that means "consistently owned
// by x" is that filter plus ownership=consistent, so a bar and its own
// drill-down describe one population.

// disputedFleet is the counterexample fixture: two teams, one service they
// disagree about, one service each holds alone, one nobody claims. Every service
// runs one target, because a target's ownership is its service's.
func disputedFleet(t *testing.T) *Query {
	t.Helper()
	return inventoryFleet(t, disputedRevisions()...)
}

func disputedRevisions() []inventoryRevision {
	return []inventoryRevision{
		{service: "xsolo", owner: contract.Owner{Team: "team-x"}, distinct: "1", target: "prod"},
		{service: "disputed", owner: contract.Owner{Team: "team-x"}, distinct: "1", target: "prod"},
		{service: "disputed", owner: contract.Owner{Team: "team-y"}, distinct: "2"},
		{service: "ysolo", owner: contract.Owner{Team: "team-y"}, distinct: "1", target: "prod"},
		{service: "nobody", distinct: "1", target: "prod"},
	}
}

// serviceKeys lists the service keys an entity query matched, sorted.
func serviceKeys(t *testing.T, q *Query, f EntityFilter) []string {
	t.Helper()
	list, err := q.Entities(f)
	if err != nil {
		t.Fatalf("Entities(%+v): %v", f, err)
	}
	out := make([]string, 0, len(list.Entities))
	for _, e := range list.Entities {
		out = append(out, e.Key)
	}
	sort.Strings(out)
	if list.Total != len(out) {
		t.Fatalf("Entities(%+v) matched %d but returned %d rows", f, list.Total, len(out))
	}
	return out
}

// The aggregate's view of the disputed service: it belongs to neither team's
// ranking, because it has no single answer to "who do I page".
func TestOwnership_ConflictingContainsTheDisputedService(t *testing.T) {
	q := disputedFleet(t)
	got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipConflicting})
	if fmt.Sprint(got) != "[disputed]" {
		t.Errorf("ownership=conflicting listed %v, want [disputed]", got)
	}
}

// Both teams can find the service they disagree about. Filtering on the summary
// owner hides it from exactly one of them, and which one is decided by revision
// key order -- an implementation detail no reader can see.
func TestOwnerFilter_EitherClaimantCanDiscoverTheDisputedService(t *testing.T) {
	q := disputedFleet(t)
	for owner, want := range map[string]string{
		"team-x": "[disputed xsolo]",
		"team-y": "[disputed ysolo]",
	} {
		got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Owner: owner})
		if fmt.Sprint(got) != want {
			t.Errorf("owner=%s listed %v, want %s", owner, got, want)
		}
	}
}

// "Consistently owned by x" is the filter plus the state, and it excludes the
// service the two teams disagree about.
func TestOwnerFilter_ConsistentNarrowsToUncontestedOwnership(t *testing.T) {
	q := disputedFleet(t)
	got := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Owner: "team-x", Ownership: OwnershipConsistent})
	if fmt.Sprint(got) != "[xsolo]" {
		t.Errorf("owner=team-x + ownership=consistent listed %v, want [xsolo]", got)
	}
}

// THE bar-and-its-drill-down invariant: a ranking row counts consistently owned
// services, so the destination it links to must total exactly that. Any owner
// ranked here also co-owns the disputed service, so a destination filtered on
// the owner alone would come back larger than the bar the reader clicked.
func TestByOwnerRanking_DrillsDownToExactlyWhatItCounted(t *testing.T) {
	q := disputedFleet(t)
	agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if len(agg.ByOwner) != 2 {
		t.Fatalf("ranking = %+v, want one row per consistently-owning team", agg.ByOwner)
	}
	for _, row := range agg.ByOwner {
		got := serviceKeys(t, q, EntityFilter{
			Kinds: []EntityKind{KindService}, OwnerKey: row.Key, Ownership: OwnershipConsistent})
		if len(got) != row.Services {
			t.Errorf("%s ranks %d services but its drill-down lists %d: %v", row.Key, row.Services, len(got), got)
		}
		// And the targets it claims are the targets of exactly those services.
		targets := 0
		for _, key := range got {
			targets += len(q.snap.Services[ServiceKey(key)].Targets)
		}
		if targets != row.Targets {
			t.Errorf("%s ranks %d targets but its %d services hold %d", row.Key, row.Targets, len(got), targets)
		}
	}
}

// Revisions and targets answer the same question as their service. A revision
// declares its own owner; a target inherits its service's, which is every owner
// any of that service's revisions declares.
func TestOwnerFilter_ReachesRevisionsAndTargetsOfADisputedService(t *testing.T) {
	q := disputedFleet(t)
	for _, owner := range []string{"team-x", "team-y"} {
		revs := serviceKeys(t, q, EntityFilter{Kinds: []EntityKind{KindRevision}, Owner: owner})
		if len(revs) != 2 {
			t.Errorf("owner=%s matched %d revisions, want its own two: %v", owner, len(revs), revs)
		}
		list, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindTarget}, Owner: owner})
		if err != nil {
			t.Fatalf("Entities: %v", err)
		}
		var parents []string
		for _, e := range list.Entities {
			parents = append(parents, e.ParentService)
		}
		sort.Strings(parents)
		if !containsStr(parents, "disputed") {
			t.Errorf("owner=%s reached targets %v, none of them the disputed service's", owner, parents)
		}
	}
}

// The owner ENTITY page is the same question again. Its services, revisions and
// targets are one estate: an owner shown one revision of a service and none of
// that service is being told two different things.
func TestOwnerDetail_EstateIncludesEveryServiceARevisionClaims(t *testing.T) {
	q := disputedFleet(t)
	for _, owner := range []string{"team:team-x", "team:team-y"} {
		d, err := q.EntityDetail(KindOwner, owner)
		if err != nil {
			t.Fatalf("EntityDetail(owner, %s): %v", owner, err)
		}
		sum := d.Owner.Summary
		if sum.Services != 2 || sum.Revisions != 2 {
			t.Errorf("%s owns %d services / %d revisions, want 2 / 2 (its own, plus the disputed one)",
				owner, sum.Services, sum.Revisions)
		}
		var keys []string
		for _, ref := range d.Owner.Services.Items {
			keys = append(keys, ref.Key)
		}
		if !containsStr(keys, "disputed") {
			t.Errorf("%s's estate omits the service it declares an owner for: %v", owner, keys)
		}
	}
}

// Attention is filtered by owner too, and a team that cannot see the attention
// items of a service it co-owns is being told that service is somebody else's
// problem.
func TestAttentionOwnerFilter_ReachesACoOwnedService(t *testing.T) {
	q := disputedFleet(t)
	for _, owner := range []string{"team-x", "team-y"} {
		list, err := q.Attention(AttentionFilter{Owner: owner})
		if err != nil {
			t.Fatalf("Attention(owner=%s): %v", owner, err)
		}
		unfiltered, err := q.Attention(AttentionFilter{})
		if err != nil {
			t.Fatalf("Attention: %v", err)
		}
		want := 0
		for _, it := range unfiltered.Items {
			if it.Service == "disputed" {
				want++
			}
		}
		if want == 0 {
			t.Skip("the fixture raises no attention item for the disputed service")
		}
		got := 0
		for _, it := range list.Items {
			if it.Service == "disputed" {
				got++
			}
		}
		if got != want {
			t.Errorf("owner=%s sees %d of the disputed service's %d attention items", owner, got, want)
		}
	}
}

// Owner DISCOVERY must offer both claimants, or a filter that would work is one
// the reader never learns exists.
func TestOwnerDiscovery_ListsBothClaimants(t *testing.T) {
	got := serviceKeys(t, disputedFleet(t), EntityFilter{Kinds: []EntityKind{KindOwner}})
	if fmt.Sprint(got) != "[team:team-x team:team-y]" {
		t.Errorf("owner discovery listed %v, want both claimants", got)
	}
}

// Free-text search folds owner in, and must fold in the same owners the owner
// filter does.
func TestSearchText_FindsAServiceByAnyDeclaredOwner(t *testing.T) {
	q := disputedFleet(t)
	for _, owner := range []string{"team-x", "team-y"} {
		res, err := q.Search(SearchFilter{Text: owner})
		if err != nil {
			t.Fatalf("Search(text=%s): %v", owner, err)
		}
		var names []string
		for _, s := range res.Services {
			names = append(names, s.Name)
		}
		if !containsStr(names, "disputed") {
			t.Errorf("searching %q found %v, not the service that declares it", owner, names)
		}
	}
}

// The legacy service search filter is the same question once more.
func TestSearchOwnerFilter_MatchesAnyDeclaredOwner(t *testing.T) {
	q := disputedFleet(t)
	res, err := q.Search(SearchFilter{Owner: "team-y"})
	if err != nil {
		t.Fatalf("Search(owner=team-y): %v", err)
	}
	var names []string
	for _, s := range res.Services {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	if fmt.Sprint(names) != "[disputed ysolo]" {
		t.Errorf("owner=team-y found %v, want [disputed ysolo]", names)
	}
}

// None of the above may depend on the order the revisions arrived in. The summary
// owner does -- it is the lowest-keyed revision's -- so an answer that changes
// when the fixture is declared backwards is an answer still being read off the
// summary.
func TestOwnershipAnswers_AreIndependentOfDeclarationOrder(t *testing.T) {
	forward := disputedRevisions()
	reversed := make([]inventoryRevision, 0, len(forward))
	for i := len(forward) - 1; i >= 0; i-- {
		reversed = append(reversed, forward[i])
	}
	ask := func(q *Query) string {
		var b []string
		for _, f := range []EntityFilter{
			{Kinds: []EntityKind{KindService}, Owner: "team-x"},
			{Kinds: []EntityKind{KindService}, Owner: "team-y"},
			{Kinds: []EntityKind{KindService}, Owner: "team-x", Ownership: OwnershipConsistent},
			{Kinds: []EntityKind{KindService}, Owner: "team-y", Ownership: OwnershipConsistent},
			{Kinds: []EntityKind{KindService}, Ownership: OwnershipConflicting},
			{Kinds: []EntityKind{KindOwner}},
		} {
			b = append(b, fmt.Sprint(serviceKeys(t, q, f)))
		}
		agg := entityAggregateOf(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
		b = append(b, fmt.Sprintf("%+v %+v", agg.Ownership, agg.ByOwner))
		return fmt.Sprint(b)
	}
	if a, z := ask(inventoryFleet(t, forward...)), ask(inventoryFleet(t, reversed...)); a != z {
		t.Errorf("declaration order changed the answers:\n forward: %s\nreversed: %s", a, z)
	}
}
