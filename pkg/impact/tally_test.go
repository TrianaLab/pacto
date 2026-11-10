package impact

import (
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestOutcomeOf(t *testing.T) {
	for _, tc := range []struct {
		verdict string
		refused bool
		want    string
	}{
		{CompatibilityIncompatible, false, OutcomeIncompatible},
		{CompatibilityCompatible, true, OutcomeIncompatible},
		{CompatibilityUnknown, true, OutcomeIncompatible},
		{CompatibilityUnknown, false, OutcomeUnknown},
		{CompatibilityCompatible, false, OutcomeCompatible},
		{"newer-engine-value", false, OutcomeUnclassified},
		{"", false, OutcomeUnclassified},
	} {
		if got := outcomeOf(tc.verdict, tc.refused, ""); got != tc.want {
			t.Errorf("outcomeOf(%q, %v) = %q, want %q", tc.verdict, tc.refused, got, tc.want)
		}
	}
}

func TestTallyFactsPartitionsAndRanks(t *testing.T) {
	var facts []consumerFact
	add := func(n int, verdict, owner string, refused bool) {
		for range n {
			facts = append(facts, consumerFact{verdict: verdict, owner: owner, refused: refused})
		}
	}
	add(3, CompatibilityIncompatible, "team-a", false)
	add(1, CompatibilityCompatible, "team-a", true) // refused: breaks whatever its range says
	add(9, CompatibilityCompatible, "team-b", false)
	add(2, CompatibilityUnknown, "team-c", false)
	add(1, CompatibilityIncompatible, "team-d", false)
	add(1, CompatibilityCompatible, "team-e", false)
	add(1, CompatibilityCompatible, "team-f", false)
	add(1, CompatibilityCompatible, "team-g", false)
	add(4, "mystery", "", false)

	got := tallyFacts(facts, false)
	wantOutcome := []TallyBucket{
		{Key: OutcomeIncompatible, Count: 5}, {Key: OutcomeUnknown, Count: 2},
		{Key: OutcomeCompatible, Count: 12}, {Key: OutcomeUnclassified, Count: 4},
	}
	if !reflect.DeepEqual(got.ByOutcome, wantOutcome) {
		t.Fatalf("ByOutcome = %+v, want %+v", got.ByOutcome, wantOutcome)
	}
	sum := 0
	for _, b := range got.ByOutcome {
		sum += b.Count
	}
	if sum != len(facts) {
		t.Fatalf("outcomes sum to %d, want %d", sum, len(facts))
	}
	// Ranked by consumers that break, then by size, then by name; capped.
	wantOwners := []OwnerTally{
		{Owner: "team-a", Consumers: 4, Incompatible: 4},
		{Owner: "team-d", Consumers: 1, Incompatible: 1},
		{Owner: "team-b", Consumers: 9},
		{Owner: "team-c", Consumers: 2},
		{Owner: "team-e", Consumers: 1},
	}
	if !reflect.DeepEqual(got.ByOwner, wantOwners) {
		t.Fatalf("ByOwner = %+v, want %+v", got.ByOwner, wantOwners)
	}
	if got.BeyondRanking != 2 || got.NoOwner != 4 || got.Owners != 7 {
		t.Fatalf("beyond %d, noOwner %d, owners %d; want 2, 4, 7", got.BeyondRanking, got.NoOwner, got.Owners)
	}
	ranked := got.BeyondRanking + got.NoOwner
	for _, o := range got.ByOwner {
		ranked += o.Consumers
	}
	if ranked != len(facts) {
		t.Fatalf("owner tally covers %d consumers, want %d", ranked, len(facts))
	}
}

func TestTallyFactsEmpty(t *testing.T) {
	got := tallyFacts(nil, false)
	if len(got.ByOutcome) != len(outcomeOrder) || got.ByOwner == nil || len(got.ByOwner) != 0 {
		t.Fatalf("empty tally = %+v; want every outcome bucket at 0 and an empty ranking", got)
	}
}

func TestResultAndProductCarryTheSameTallies(t *testing.T) {
	res := &Result{Consumers: []AffectedConsumer{
		{Service: "a", CompatibilityVerdict: CompatibilityIncompatible, Owner: "team-a", Direct: true},
		{Service: "b", CompatibilityVerdict: CompatibilityCompatible, Owner: "team-a", Unmet: []contract.Mismatch{{}}},
		{Service: "c", CompatibilityVerdict: CompatibilityUnknown},
	}}
	res.tally()
	pc := make([]ProductConsumer, 0, len(res.Consumers))
	for _, c := range res.Consumers {
		pc = append(pc, ProductConsumer{CompatibilityVerdict: c.CompatibilityVerdict, Owner: c.Owner, Unmet: c.Unmet})
	}
	page := pageConsumersOutcome(pc, 10, 0, 0, "", false)
	if !reflect.DeepEqual(page.ConsumerTallies, res.ConsumerTallies) {
		t.Fatalf("product %+v != result %+v", page.ConsumerTallies, res.ConsumerTallies)
	}
	if res.ConsumerTallies.ByOutcome[0].Count != 2 || res.ConsumerTallies.NoOwner != 1 {
		t.Fatalf("tallies = %+v", res.ConsumerTallies)
	}
}

func TestTallySummaries(t *testing.T) {
	tl := ConsumerTallies{
		ByOutcome: []TallyBucket{{Key: OutcomeIncompatible, Count: 2}, {Key: OutcomeUnknown}, {Key: OutcomeCompatible, Count: 9}, {Key: OutcomeUnclassified}},
		ByOwner:   []OwnerTally{{Owner: "team-a", Consumers: 4, Incompatible: 2}, {Owner: "team-b", Consumers: 7}},
	}
	if got := tl.OutcomeSummary(); got != "2 incompatible, 0 unknown, 9 compatible" {
		t.Errorf("OutcomeSummary = %q", got)
	}
	if got := tl.OwnerSummary(); got != "team-a 4 (2 incompatible), team-b 7" {
		t.Errorf("OwnerSummary = %q", got)
	}
	tl.ByOutcome[3].Count = 1
	tl.BeyondRanking, tl.NoOwner = 3, 1
	if got := tl.OutcomeSummary(); got != "2 incompatible, 0 unknown, 9 compatible, 1 unclassified" {
		t.Errorf("OutcomeSummary = %q", got)
	}
	if got := tl.OwnerSummary(); got != "team-a 4 (2 incompatible), team-b 7; 3 more of other owners; 1 with no identified owner" {
		t.Errorf("OwnerSummary = %q", got)
	}
	if got := (ConsumerTallies{NoOwner: 2}).OwnerSummary(); got != "2 with no identified owner" {
		t.Errorf("OwnerSummary = %q", got)
	}
	// In an event answer the count is of consumers that break.
	ev := ConsumerTallies{ByOutcome: []TallyBucket{{Key: OutcomeBreaks, Count: 2}}, ByOwner: []OwnerTally{{Owner: "team-a", Consumers: 2, Incompatible: 2}}}
	if got := ev.OwnerSummary(); got != "team-a 2 (2 break)" {
		t.Errorf("event OwnerSummary = %q", got)
	}
}
