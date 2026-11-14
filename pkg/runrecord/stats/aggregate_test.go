package stats

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

var update = flag.Bool("update", false, "rewrite testdata/stats.golden.json")

var (
	aggNow    = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	aggWindow = Window{Since: aggNow.Add(-DefaultWindow), Until: aggNow}
)

// rec builds a CI record of one change on orders at the given time.
func rec(kind, newRev string, at time.Time) runrecord.Record {
	return runrecord.Record{
		Version: runrecord.Version, ID: runrecord.ID(kind, "orders", "sha256:old", newRev),
		Kind: kind, Service: "orders", OldRevision: "sha256:old", NewRevision: newRev,
		Outcome: runrecord.OutcomePassed, RecordedAt: at,
	}
}

func gated(r runrecord.Record, verdict string, blocking, unknown int) runrecord.Record {
	r.Gate = &runrecord.Gate{Verdict: verdict}
	r.Consumers = &runrecord.Consumers{Blocking: blocking, Unknown: unknown}
	return r
}

func breaking(r runrecord.Record) runrecord.Record {
	r.Classification = classificationBreaking
	return r
}

func set(recs ...runrecord.Record) RecordSet {
	var s RecordSet
	for i, r := range recs {
		s.Entries = append(s.Entries, Entry{Record: r, Digest: "sha256:" + strings.Repeat("0", 63) + string(rune('a'+i)), Anchor: runrecord.AnchorTag(r.Service)})
	}
	return s
}

func agg(s RecordSet) *Stats {
	return Aggregate(s, Query{Window: aggWindow}, Scope{}, aggNow)
}

func hoursAgo(h int) time.Time { return aggNow.Add(-time.Duration(h) * time.Hour) }

func TestAggregateEnvelopeAlwaysCarriesProvenanceAndAsOf(t *testing.T) {
	for _, s := range []RecordSet{{}, set(rec(runrecord.KindDiff, "sha256:new", hoursAgo(1)))} {
		st := Aggregate(s, Query{Window: aggWindow, By: "service"}, Scope{}, aggNow.In(time.FixedZone("x", 3600)))
		if st.Provenance != Provenance || !st.AsOf.Equal(aggNow) || st.AsOf.Location() != time.UTC || st.Window != aggWindow || st.By != "service" || st.Limitations == nil {
			t.Fatalf("envelope = %+v", st)
		}
	}
}

func TestAggregateDedupesRetriesByRecordID(t *testing.T) {
	r := rec(runrecord.KindImpact, "sha256:new", hoursAgo(1))
	st := agg(set(r, r, r))
	if st.RecordsRead != 3 || st.Runs != 1 || st.DistinctChanges != 1 {
		t.Fatalf("read %d runs %d changes %d", st.RecordsRead, st.Runs, st.DistinctChanges)
	}
}

func TestAggregateDedupeKeepsNewestSameID(t *testing.T) {
	blocked := gated(rec(runrecord.KindImpact, "sha256:new", hoursAgo(3)), runrecord.VerdictBlocked, 1, 0)
	clear := gated(rec(runrecord.KindImpact, "sha256:new", hoursAgo(2)), runrecord.VerdictClear, 0, 0)
	for _, s := range []RecordSet{set(blocked, clear), set(clear, blocked)} {
		if g := agg(s).Totals.Gates; g != (Gates{Clear: 1}) {
			t.Fatalf("gates = %+v", g)
		}
	}
	// Equal recordedAt: the larger digest wins whatever the listing order.
	clear.RecordedAt = blocked.RecordedAt
	s := set(blocked, clear) // clear has the larger digest
	rev := RecordSet{Entries: []Entry{s.Entries[1], s.Entries[0]}}
	for _, s := range []RecordSet{s, rev} {
		if g := agg(s).Totals.Gates; g != (Gates{Clear: 1}) {
			t.Fatalf("tie gates = %+v", g)
		}
	}
}

func TestAggregateFutureDatedRecordIsSkipped(t *testing.T) {
	ok := gated(rec(runrecord.KindImpact, "sha256:new", hoursAgo(1)), runrecord.VerdictBlocked, 1, 0)
	forged := gated(rec(runrecord.KindImpact, "sha256:new", aggNow.Add(6*time.Minute)), runrecord.VerdictClear, 0, 0)
	forged.ID = "sha256:forged"
	st := agg(set(ok, forged))
	if st.Skipped.Malformed != 1 || st.RecordsRead != 1 || st.Totals.Gates != (Gates{Blocked: 1}) || st.Completeness != fleet.CompletenessPartial {
		t.Fatalf("stats = %+v", st)
	}
	if !strings.Contains(st.Limitations[0].Message, "1 dated in the future") || st.Limitations[0].Code != LimitationSkipped {
		t.Fatalf("limitations = %+v", st.Limitations)
	}
	// An operation finishing in the future is skipped too.
	op := runrecord.Record{ID: "uid-1", Kind: runrecord.KindOperation, Service: "orders", RecordedAt: hoursAgo(1), FinishedAt: aggNow.Add(time.Hour).Format(time.RFC3339)}
	if st := agg(set(op)); st.Skipped.Malformed != 1 || st.Runs != 0 {
		t.Fatalf("future operation = %+v", st)
	}
	near := rec(runrecord.KindDiff, "sha256:new", aggNow.Add(4*time.Minute))
	if st := Aggregate(set(near), Query{Window: Window{Since: aggWindow.Since, Until: aggNow.Add(time.Hour)}}, Scope{}, aggNow); st.Skipped.Total() != 0 || st.Runs != 1 {
		t.Fatalf("within skew = %+v", st)
	}
}

func TestAggregateBreakingCaughtCountsDistinctChanges(t *testing.T) {
	diff := breaking(rec(runrecord.KindDiff, "sha256:new", hoursAgo(3)))
	impact := breaking(rec(runrecord.KindImpact, "sha256:new", hoursAgo(2)))
	rerun := impact
	rerun.ID, rerun.RecordedAt = "sha256:rerun", hoursAgo(1)
	other := rec(runrecord.KindDiff, "sha256:other", hoursAgo(1)) // not breaking
	st := agg(set(diff, impact, rerun, other))
	if st.Totals.BreakingCaught != (Distinct{Distinct: 1, Runs: 3}) || st.DistinctChanges != 2 || st.Runs != 4 {
		t.Fatalf("breaking %+v changes %d runs %d", st.Totals.BreakingCaught, st.DistinctChanges, st.Runs)
	}
}

func TestAggregateGatesUseNewestRecordPerChange(t *testing.T) {
	blocked := gated(rec(runrecord.KindImpact, "sha256:new", hoursAgo(3)), runrecord.VerdictBlocked, 2, 0)
	clear := gated(rec(runrecord.KindImpact, "sha256:new", hoursAgo(2)), runrecord.VerdictClear, 0, 0)
	clear.ID = "sha256:rerun"
	unverified := gated(rec(runrecord.KindImpact, "sha256:u", hoursAgo(1)), runrecord.VerdictUnverified, 0, 1)
	refused := rec(runrecord.KindPublish, "sha256:r", hoursAgo(1))
	refused.Gate = &runrecord.Gate{Verdict: runrecord.VerdictRefused}
	st := agg(set(blocked, clear, unverified, refused))
	if st.Totals.Gates != (Gates{Clear: 1, Unverified: 1}) || st.Totals.ConsumersProtected != (Protected{}) {
		t.Fatalf("totals = %+v", st.Totals)
	}
}

func TestAggregateConsumersProtectedSumsBlocking(t *testing.T) {
	a := gated(rec(runrecord.KindImpact, "sha256:a", hoursAgo(2)), runrecord.VerdictBlocked, 2, 1)
	b := gated(rec(runrecord.KindImpact, "sha256:b", hoursAgo(2)), runrecord.VerdictBlocked, 3, 0)
	c := gated(rec(runrecord.KindImpact, "sha256:c", hoursAgo(2)), runrecord.VerdictClear, 0, 5)
	d := rec(runrecord.KindImpact, "sha256:d", hoursAgo(2))
	d.Gate = &runrecord.Gate{Verdict: runrecord.VerdictBlocked} // no consumer counts
	st := agg(set(a, b, c, d))
	if st.Totals.ConsumersProtected != (Protected{Count: 5, Unknown: 1}) || st.Totals.Gates.Blocked != 3 {
		t.Fatalf("totals = %+v", st.Totals)
	}
}

func TestAggregateWindowFilters(t *testing.T) {
	w := Window{Since: hoursAgo(10), Until: hoursAgo(5)}
	atSince := rec(runrecord.KindDiff, "sha256:since", w.Since)
	atUntil := rec(runrecord.KindDiff, "sha256:until", w.Until)
	before := rec(runrecord.KindDiff, "sha256:before", hoursAgo(11))
	op := runrecord.Record{ID: "uid-1", Kind: runrecord.KindOperation, Service: "orders", RecordedAt: hoursAgo(1), FinishedAt: hoursAgo(6).Format(time.RFC3339)}
	opBadTime := runrecord.Record{ID: "uid-2", Kind: runrecord.KindOperation, Service: "orders", RecordedAt: hoursAgo(7), FinishedAt: "yesterday"}
	st := Aggregate(set(atSince, atUntil, before, op, opBadTime), Query{Window: w}, Scope{}, aggNow)
	if st.RecordsRead != 5 || st.Runs != 3 || st.DistinctChanges != 1 {
		t.Fatalf("read %d runs %d changes %d", st.RecordsRead, st.Runs, st.DistinctChanges)
	}
}

func TestAggregateKindAndServiceFilters(t *testing.T) {
	diff := rec(runrecord.KindDiff, "sha256:new", hoursAgo(1))
	impact := rec(runrecord.KindImpact, "sha256:new", hoursAgo(1))
	payments := rec(runrecord.KindImpact, "sha256:new", hoursAgo(1))
	payments.Service, payments.ID = "payments", "sha256:payments"
	s := set(diff, impact, payments)
	if st := Aggregate(s, Query{Window: aggWindow, Kinds: []string{runrecord.KindImpact}}, Scope{}, aggNow); st.Runs != 2 {
		t.Fatalf("kind runs = %d", st.Runs)
	}
	if st := Aggregate(s, Query{Window: aggWindow, Service: "payments"}, Scope{}, aggNow); st.Runs != 1 || st.DistinctChanges != 1 {
		t.Fatalf("service runs = %d", st.Runs)
	}
}

func TestAggregateZeroDenominatorIsUnknown(t *testing.T) {
	got, _ := json.Marshal(ratio(0, 0))
	if string(got) != `{"count":0,"of":0,"unknown":true}` {
		t.Fatalf("ratio(0, 0) = %s", got)
	}
	if got, _ := json.Marshal(ratio(1, 4)); string(got) != `{"count":1,"of":4}` {
		t.Fatalf("ratio(1, 4) = %s", got)
	}
}

func TestAggregateSkippedOrTruncatedIsPartial(t *testing.T) {
	r := rec(runrecord.KindDiff, "sha256:new", hoursAgo(1))
	if st := agg(set(r)); st.Completeness != fleet.CompletenessComplete || len(st.Limitations) != 0 {
		t.Fatalf("whole read = %+v", st)
	}
	skipped := set(r)
	skipped.Skipped = map[string]Skipped{"pacto-stats-a": {Oversized: 1}, "pacto-stats-b": {Unreadable: 2}}
	st := agg(skipped)
	if st.Completeness != fleet.CompletenessPartial || st.Skipped != (Skipped{Oversized: 1, Unreadable: 2}) ||
		st.Limitations[0].Message != "3 records could not be read: 0 malformed, 1 oversized, 2 unreadable; they are left out of every number" {
		t.Fatalf("skipped = %+v", st)
	}
	truncated := set(r)
	truncated.Truncated = "more than 3 run records"
	st = agg(truncated)
	if st.Completeness != fleet.CompletenessPartial || st.Truncated != truncated.Truncated ||
		st.Limitations[0].Code != LimitationTruncated || !strings.Contains(st.Limitations[0].Message, "more than 3 run records") {
		t.Fatalf("truncated = %+v", st)
	}
	// No records and a cut read is partial, never empty.
	if st := agg(RecordSet{Truncated: "more than 3 run records"}); st.Completeness != fleet.CompletenessPartial {
		t.Fatalf("empty truncated = %+v", st)
	}
}

func TestAggregateNoRecordsHasNoTotals(t *testing.T) {
	old := rec(runrecord.KindDiff, "sha256:new", aggWindow.Since.Add(-time.Hour))
	for _, s := range []RecordSet{{}, set(old)} {
		st := agg(s)
		data, _ := json.Marshal(st)
		if bytes.Contains(data, []byte(`"totals"`)) || st.Completeness != fleet.CompletenessEmpty ||
			len(st.Limitations) != 1 || st.Limitations[0].Code != LimitationNoRecords {
			t.Fatalf("no records = %s", data)
		}
	}
}

func TestAggregateGoldenJSON(t *testing.T) {
	impact := breaking(gated(rec(runrecord.KindImpact, "sha256:new", hoursAgo(2)), runrecord.VerdictBlocked, 2, 1))
	diff := breaking(rec(runrecord.KindDiff, "sha256:new", hoursAgo(3)))
	publish := gated(rec(runrecord.KindPublish, "sha256:next", hoursAgo(2)), runrecord.VerdictClear, 0, 0)
	publish.DeployDigest = digest('d')
	held := verifiedTo(digest('d'), hoursAgo(1))
	held.HeldFor = "30m0s"
	s := set(impact, diff, publish, impact, held, promote(delivery.Failed, "", hoursAgo(1)))
	s.Skipped = map[string]Skipped{runrecord.AnchorTag("orders"): {Malformed: 1}}
	st := Aggregate(s, Query{Window: aggWindow, By: "service"}, Scope{}, aggNow)
	st.Registry = "registry.example.com/acme/pacto-stats"
	got, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const path = "testdata/stats.golden.json"
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch (run with -update to rewrite):\n%s", got)
	}
}
