package stats

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// leadIn is a published digest promoted, verified, into env after lead.
func leadIn(env string, c byte, lead time.Duration) []runrecord.Record {
	p := verifiedTo(digest(c), hoursAgo(1))
	p.Environment = env
	return []runrecord.Record{published("orders", digest(c), "", hoursAgo(1).Add(-lead)), p}
}

func leadSet(parts ...[]runrecord.Record) RecordSet {
	var recs []runrecord.Record
	for _, p := range parts {
		recs = append(recs, p...)
	}
	return set(recs...)
}

func leadsIn(s RecordSet, sc Scope, groups map[string][]string) map[string]LeadTime {
	return LeadTimesIn(s, Query{Window: aggWindow}, sc, aggNow, groups)
}

// Percentiles do not average: the per-environment p50s are 1h, 10h and 10h,
// so a median of p50s reads 10h, while the nearest-rank p50 of the pooled
// promotions 1h, 1h, 1h, 10h, 10h is 1h.
func TestLeadTimesInPoolsDurations(t *testing.T) {
	h := time.Hour
	s := leadSet(leadIn("a", '1', h), leadIn("a", '2', h), leadIn("a", '3', h), leadIn("b", '4', 10*h), leadIn("c", '5', 10*h))
	got := leadsIn(s, Scope{}, map[string][]string{"g": {"a", "b", "c"}})["g"]
	if got.P50Seconds != nil && *got.P50Seconds == 36000 {
		t.Fatal("pooled p50 is the median of the p50s")
	}
	assertLead(t, got, 3600, 36000, 5, 0)
}

func TestLeadTimesInSameJoinAsAggregate(t *testing.T) {
	s := leadSet(leadIn("a", '1', time.Hour), leadIn("b", '2', 3*time.Hour), []runrecord.Record{verifiedTo("", hoursAgo(2))})
	for _, e := range Aggregate(s, Query{Window: aggWindow}, Scope{}, aggNow).Totals.Environments {
		if got := leadsIn(s, Scope{}, map[string][]string{"g": {e.Environment}})["g"]; !sameLead(got, e.LeadTime) {
			t.Fatalf("%s: LeadTimesIn %+v, Aggregate %+v", e.Environment, got, e.LeadTime)
		}
	}
}

func sameLead(a, b LeadTime) bool {
	eq := func(x, y *int64) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	return eq(a.P50Seconds, b.P50Seconds) && eq(a.P90Seconds, b.P90Seconds) && a.Joined == b.Joined && a.Unjoined == b.Unjoined && a.Unknown == b.Unknown
}

// A hidden service's publish never joins a visible promotion of the same
// digest, and a hidden service's own promotions add no sample.
func TestLeadTimesInHiddenNeverJoins(t *testing.T) {
	promo := of("shop/orders", verifiedTo(digest('d'), hoursAgo(1)))
	hiddenPub := published("billing", digest('d'), "", hoursAgo(5))
	billingPromo := of("billing", verifiedTo(digest('e'), hoursAgo(1)))
	s := set(hiddenPub, promo, published("billing", digest('e'), "", hoursAgo(4)), billingPromo)
	groups := map[string][]string{"g": {"production"}}
	if got := leadsIn(s, restrictedScope(t), groups)["g"]; got != (LeadTime{Unjoined: 1, Unknown: true}) {
		t.Fatalf("restricted lead time = %+v", got)
	}
	if got := leadsIn(s, unrestrictedScope(t), groups)["g"]; got.Joined != 1 || got.Unjoined != 1 {
		t.Fatalf("unrestricted lead time = %+v", got)
	}
}

func TestLeadTimesInUnknownWhenNoneJoined(t *testing.T) {
	s := set(verifiedTo(digest('d'), hoursAgo(1)))
	if got := leadsIn(s, Scope{}, map[string][]string{"g": {"production"}})["g"]; got != (LeadTime{Unjoined: 1, Unknown: true}) {
		t.Fatalf("lead time = %+v", got)
	}
}

func TestLeadTimesInEmptyGroup(t *testing.T) {
	s := leadSet(leadIn("a", '1', time.Hour))
	got := leadsIn(s, Scope{}, map[string][]string{"none": nil, "absent": {"z"}})
	if len(got) != 2 || got["none"] != (LeadTime{Unknown: true}) || got["absent"] != (LeadTime{Unknown: true}) {
		t.Fatalf("lead times = %+v", got)
	}
}

func TestLeadTimesInSeveralGroupsOnePass(t *testing.T) {
	s := leadSet(leadIn("a", '1', time.Hour), leadIn("b", '2', 2*time.Hour), leadIn("c", '3', 3*time.Hour))
	got := leadsIn(s, Scope{}, map[string][]string{"ab": {"a", "b"}, "bc": {"b", "c"}})
	assertLead(t, got["ab"], 3600, 7200, 2, 0)
	assertLead(t, got["bc"], 7200, 10800, 2, 0)
}

// A bucket window counts only its promotions and still joins publishes
// recorded before it.
func TestLeadTimesInWindow(t *testing.T) {
	s := leadSet(leadIn("a", '1', 48*time.Hour))
	w := Window{Since: hoursAgo(2), Until: aggNow}
	assertLead(t, LeadTimesIn(s, Query{Window: w}, Scope{}, aggNow, map[string][]string{"g": {"a"}})["g"], 172800, 172800, 1, 0)
	w = Window{Since: hoursAgo(10), Until: hoursAgo(5)}
	if got := LeadTimesIn(s, Query{Window: w}, Scope{}, aggNow, map[string][]string{"g": {"a"}})["g"]; got != (LeadTime{Unknown: true}) {
		t.Fatalf("lead time = %+v", got)
	}
}

// A promotion naming no environment and no Stage counts under no
// environment, as in Aggregate.
func TestLeadTimesInPromotionWithoutEnvironment(t *testing.T) {
	p := verifiedTo(digest('d'), hoursAgo(1))
	p.Environment, p.Stage = "", nil
	s := set(published("orders", digest('d'), "", hoursAgo(2)), p)
	if got := leadsIn(s, Scope{}, map[string][]string{"g": {""}})["g"]; got != (LeadTime{Unknown: true}) {
		t.Fatalf("lead time = %+v", got)
	}
}
