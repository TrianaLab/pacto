package stats

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

func behind(t *testing.T, s RecordSet, sc Scope, metric, env string) []runrecord.Record {
	t.Helper()
	recs, err := Behind(s, Query{Window: aggWindow}, sc, aggNow, metric, env)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func ids(recs []runrecord.Record) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}

func held(r runrecord.Record) runrecord.Record {
	r.HeldFor = "10m"
	return r
}

// behindFixture holds every metric's records: two blocked changes (one also
// breaking), a clear and an unverified change, operations in production and
// staging, a joined promotion and two coverage records.
func behindFixture() RecordSet {
	staging := held(promote(delivery.Failed, delivery.Unverified, hoursAgo(4)))
	staging.Environment = "staging"
	rolled := op(opRollback, delivery.Succeeded, "", hoursAgo(3))
	return set(
		breaking(gated(rec(runrecord.KindImpact, "sha256:a", hoursAgo(9)), runrecord.VerdictBlocked, 3, 1)),
		breaking(rec(runrecord.KindDiff, "sha256:a", hoursAgo(10))),
		gated(rec(runrecord.KindImpact, "sha256:b", hoursAgo(8)), runrecord.VerdictBlocked, 2, 0),
		gated(rec(runrecord.KindImpact, "sha256:c", hoursAgo(7)), runrecord.VerdictClear, 0, 0),
		gated(rec(runrecord.KindImpact, "sha256:d", hoursAgo(6)), runrecord.VerdictUnverified, 0, 0),
		published("orders", digest('d'), "", hoursAgo(5)),
		verifiedTo(digest('d'), hoursAgo(2)),
		verifiedTo(digest('e'), hoursAgo(1)),
		held(promote(delivery.Errored, delivery.Verified, hoursAgo(1))),
		promote(delivery.Succeeded, delivery.Unverified, hoursAgo(2)),
		staging, rolled,
		coverageAt(3, hoursAgo(12)), coverageAt(4, hoursAgo(11)),
	)
}

func TestBehindMatchesAggregateCounts(t *testing.T) {
	s := behindFixture()
	tot := agg(s).Totals
	env := func(name string) EnvironmentMetrics {
		for _, e := range tot.Environments {
			if e.Environment == name {
				return e
			}
		}
		t.Fatalf("no environment %s in %+v", name, tot.Environments)
		return EnvironmentMetrics{}
	}
	prod := env("production")
	for metric, want := range map[string]int{
		"breaking":           tot.BreakingCaught.Distinct,
		"blocked":            tot.Gates.Blocked,
		"clear":              tot.Gates.Clear,
		"unverified":         tot.Gates.Unverified,
		"protected":          tot.Gates.Blocked,
		"held":               prod.Held.Count,
		"failed":             prod.Failed.Count,
		"rolledBack":         prod.RolledBack.Count,
		"verificationFailed": prod.VerificationFailed.Count,
	} {
		e := ""
		if !slices.Contains([]string{"breaking", "blocked", "clear", "unverified", "protected"}, metric) {
			e = "production"
		}
		if got := len(behind(t, s, Scope{}, metric, e)); got != want || want == 0 {
			t.Errorf("%s: %d records, aggregate says %d", metric, got, want)
		}
	}
	consumers := 0
	for _, r := range behind(t, s, Scope{}, "protected", "") {
		consumers += r.Consumers.Blocking
	}
	if consumers != tot.ConsumersProtected.Count {
		t.Errorf("protected consumers %d, aggregate %d", consumers, tot.ConsumersProtected.Count)
	}
	var promotes int
	for _, r := range behind(t, s, Scope{}, "leadTime", "production") {
		if r.Kind == runrecord.KindOperation {
			promotes++
		}
	}
	if promotes != prod.LeadTime.Joined || promotes != 1 {
		t.Errorf("leadTime promotes %d, aggregate joined %d", promotes, prod.LeadTime.Joined)
	}
	if got := behind(t, s, Scope{}, "coverage", ""); len(got) != 2 {
		t.Errorf("coverage records = %d", len(got))
	}
	if got := behind(t, s, Scope{}, "held", "staging"); len(got) != env("staging").Held.Count {
		t.Errorf("staging held = %d", len(got))
	}
}

func TestBehindOperationsOfEveryEnvironment(t *testing.T) {
	if got := behind(t, behindFixture(), Scope{}, "failed", ""); len(got) != 2 {
		t.Fatalf("failed in every environment = %v", ids(got))
	}
}

func TestBehindDedupesByIDLikeAggregate(t *testing.T) {
	r := gated(rec(runrecord.KindImpact, "sha256:new", hoursAgo(1)), runrecord.VerdictBlocked, 1, 0)
	if got := behind(t, set(r, r, r), Scope{}, "blocked", ""); len(got) != 1 {
		t.Fatalf("records = %v", ids(got))
	}
}

func TestBehindBlockedUsesNewestPerChange(t *testing.T) {
	blocked := gated(rec(runrecord.KindDiff, "sha256:new", hoursAgo(3)), runrecord.VerdictBlocked, 1, 0)
	clear := gated(rec(runrecord.KindImpact, "sha256:new", hoursAgo(2)), runrecord.VerdictClear, 0, 0)
	s := set(blocked, clear)
	if got := behind(t, s, Scope{}, "blocked", ""); len(got) != 0 {
		t.Fatalf("blocked = %v", ids(got))
	}
	if got := behind(t, s, Scope{}, "clear", ""); len(got) != 1 || got[0].ID != clear.ID {
		t.Fatalf("clear = %v", ids(got))
	}
}

func TestBehindEnvironmentFilter(t *testing.T) {
	s := behindFixture()
	for _, r := range behind(t, s, Scope{}, "held", "staging") {
		if r.Environment != "staging" {
			t.Fatalf("staging held = %+v", r)
		}
	}
	if got := behind(t, s, Scope{}, "breaking", "production"); len(got) != 0 {
		t.Fatalf("breaking in production = %v", ids(got))
	}
	if got := behind(t, s, Scope{}, "coverage", "production"); len(got) != 2 {
		t.Fatalf("coverage ignores the environment: %v", ids(got))
	}
}

func TestBehindRestrictedNeverReturnsHidden(t *testing.T) {
	hidden := gated(of("billing", rec(runrecord.KindImpact, "sha256:new", hoursAgo(2))), runrecord.VerdictBlocked, 3, 0)
	visible := gated(of("shop/orders", rec(runrecord.KindImpact, "sha256:new", hoursAgo(2))), runrecord.VerdictBlocked, 1, 0)
	hiddenOp := of("billing", held(promote(delivery.Failed, delivery.Verified, hoursAgo(1))))
	s := set(hidden, visible, hiddenOp, coverageAt(3, hoursAgo(1)))
	sc := restrictedScope(t)
	for _, m := range MetricNames {
		for _, r := range behind(t, s, sc, m, "") {
			if r.Service != "shop/orders" {
				t.Fatalf("%s returned %s", m, r.Service)
			}
		}
	}
	if got := behind(t, s, sc, "blocked", ""); len(got) != 1 {
		t.Fatalf("blocked = %v", ids(got))
	}
}

func TestBehindLeadTimeNeverReturnsHiddenPublish(t *testing.T) {
	promo := of("shop/orders", verifiedTo(digest('d'), hoursAgo(1)))
	s := set(published("billing", digest('d'), "", hoursAgo(5)), promo)
	sc := restrictedScope(t)
	if got := behind(t, s, sc, "leadTime", ""); len(got) != 0 {
		t.Fatalf("lead time records = %+v", got)
	}
	if lt := aggBy(s, "", sc).Totals.Environments[0].LeadTime; lt.Joined != 0 || lt.Unjoined != 1 {
		t.Fatalf("lead time = %+v", lt)
	}
}

func TestLeadTimeJoinIsPerService(t *testing.T) {
	ordersPub := published("orders", digest('d'), "", hoursAgo(5))
	catalogPub := published("catalog", digest('d'), "", hoursAgo(3))
	catalogPromo := of("catalog", verifiedTo(digest('d'), hoursAgo(1)))
	catalogPromo.Stage = &runrecord.StageRef{Namespace: "shop", Name: "prod", Cluster: "c2"}
	s := set(ordersPub, catalogPub, verifiedTo(digest('d'), hoursAgo(2)), catalogPromo)
	got := behind(t, s, Scope{}, "leadTime", "")
	if len(got) != 4 {
		t.Fatalf("records = %v", ids(got))
	}
	pairs := map[string]string{}
	for _, r := range got {
		if r.Kind == runrecord.KindPublish {
			pairs[r.Service] += "publish"
		} else {
			pairs[r.Service] += "promote"
		}
	}
	if pairs["orders"] != "promotepublish" && pairs["orders"] != "publishpromote" || len(pairs["catalog"]) != len("promotepublish") {
		t.Fatalf("pairs = %v", pairs)
	}
	if lt := agg(s).Totals.Environments[0].LeadTime; lt.Joined != 2 || *lt.P50Seconds != 7200 || *lt.P90Seconds != 10800 {
		t.Fatalf("lead time = %+v", lt)
	}
}

func TestBehindUnknownMetricErrors(t *testing.T) {
	_, err := Behind(set(), Query{Window: aggWindow}, Scope{}, aggNow, "usage", "")
	if err == nil || !strings.Contains(err.Error(), `"usage"`) || !strings.Contains(err.Error(), strings.Join(MetricNames, ", ")) {
		t.Fatalf("err = %v", err)
	}
}

func TestBehindSortedNewestFirst(t *testing.T) {
	a := gated(rec(runrecord.KindImpact, "sha256:a", hoursAgo(3)), runrecord.VerdictClear, 0, 0)
	b := gated(rec(runrecord.KindImpact, "sha256:b", hoursAgo(1)), runrecord.VerdictClear, 0, 0)
	c := gated(rec(runrecord.KindImpact, "sha256:c", hoursAgo(1)), runrecord.VerdictClear, 0, 0)
	got := ids(behind(t, set(a, b, c), Scope{}, "clear", ""))
	tied := []string{b.ID, c.ID}
	slices.Sort(tied)
	if want := append(tied, a.ID); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBehindIgnoresFutureAndOutOfWindow(t *testing.T) {
	future := gated(rec(runrecord.KindImpact, "sha256:f", aggNow.Add(time.Hour)), runrecord.VerdictClear, 0, 0)
	old := gated(rec(runrecord.KindImpact, "sha256:o", aggWindow.Since.Add(-time.Hour)), runrecord.VerdictClear, 0, 0)
	if got := behind(t, set(future, old), Scope{}, "clear", ""); len(got) != 0 {
		t.Fatalf("records = %v", ids(got))
	}
}

func TestMetricNamesHaveOneDefinitionEach(t *testing.T) {
	if len(behindMetrics) != len(MetricNames) {
		t.Fatalf("%d definitions for %d names", len(behindMetrics), len(MetricNames))
	}
	for _, m := range MetricNames {
		if behindMetrics[m] == nil {
			t.Fatalf("no definition for %s", m)
		}
	}
}
