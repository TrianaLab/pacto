package stats

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

func TestCoverageOfMatchesOverview(t *testing.T) {
	q := statsFleet(t)
	c, ov := CoverageOf(q), q.Overview()
	s := ov.Summary
	if c.Services != s.Services || c.Uncontracted != s.UncontractedCallers ||
		c.Links != (runrecord.CoverageLinks{Exact: s.ExactTargetLinks, Inferred: s.InferredTargetLinks, Ambiguous: s.AmbiguousTargetLinks, Unresolved: s.UnresolvedTargetLinks}) {
		t.Fatalf("coverage %+v does not match the overview %+v", c, s)
	}
	if !c.SnapshotAsOf.Equal(fleetNow) || c.Completeness != string(ov.Meta.Completeness) || c.Services == 0 || c.Links.Unresolved == 0 {
		t.Fatalf("coverage %+v", c)
	}
}

func TestCoverageOfCopiesKnowledgeTallyVerbatim(t *testing.T) {
	q := statsFleet(t)
	k := q.Overview().Summary.Knowledge
	got := CoverageOf(q).Knowledge
	want := runrecord.CoverageKnowledge{Services: k.Services, Described: k.Described, WithGaps: k.WithGaps, Unknown: k.Unknown, NotDeployed: k.Gaps.NotDeployed}
	if got == nil || *got != want || got.Services == 0 {
		t.Fatalf("knowledge %+v, want %+v", got, want)
	}
}

func TestCoverageOfUnreadableServiceIsUnknownNotObserved(t *testing.T) {
	// ghost runs in shop with no contract Pacto can read.
	q := statsFleet(t)
	k := CoverageOf(q).Knowledge
	if _, err := q.GetService("ghost"); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if k == nil || k.Unknown < 1 {
		t.Fatalf("an unreadable service is not unknown: %+v", k)
	}
	if observed := k.Services - k.Unknown - k.NotDeployed; observed != k.Described+k.WithGaps-k.NotDeployed {
		t.Fatalf("observed %d counts the unreadable service: %+v", observed, k)
	}
}

func TestCoverageOfEmptySnapshotIsPartial(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fleetNow }})
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	if q.ProductMeta().Completeness != fleet.CompletenessEmpty {
		t.Fatalf("fixture completeness %s", q.ProductMeta().Completeness)
	}
	c := CoverageOf(q)
	if c.Completeness != string(fleet.CompletenessPartial) {
		t.Fatalf("an empty snapshot is %q", c.Completeness)
	}
	if _, err := runrecord.Encode(runrecord.CoverageRecord(c, "4.6.0", fleetNow)); err != nil {
		t.Fatalf("an empty snapshot's record does not encode: %v", err)
	}
}

// coverageAt is a coverage record of services services, snapshot at asOf and
// recorded a minute later.
func coverageAt(services int, asOf time.Time) runrecord.Record {
	return runrecord.CoverageRecord(runrecord.Coverage{Services: services, SnapshotAsOf: asOf, Completeness: "complete"}, "4.6.0", asOf.Add(time.Minute))
}

func TestNewestCoverageIgnoresFutureSnapshot(t *testing.T) {
	until := hoursAgo(1)
	s := set(coverageAt(1, hoursAgo(5)), coverageAt(2, hoursAgo(3)), coverageAt(3, hoursAgo(0)))
	if c := newestCoverage(s.Entries, until); c == nil || c.Services != 2 {
		t.Fatalf("newest before until = %+v", c)
	}
	if c := newestCoverage(s.Entries[2:], until); c != nil {
		t.Fatalf("a snapshot after until won: %+v", c)
	}
	tie := set(coverageAt(1, hoursAgo(3)), coverageAt(2, hoursAgo(3)))
	tie.Entries[0].Record.ID, tie.Entries[1].Record.ID = "sha256:0", "sha256:f"
	if c := newestCoverage(tie.Entries, until); c == nil || c.Services != 2 {
		t.Fatalf("a tie is not broken by the larger id: %+v", c)
	}
}

func TestCoverageInAggregateIsNewestNeverSummed(t *testing.T) {
	st := agg(set(coverageAt(10, hoursAgo(30)), coverageAt(12, hoursAgo(5)), rec(runrecord.KindDiff, "sha256:new", hoursAgo(2))))
	if st.Coverage == nil || st.Coverage.Services != 12 {
		t.Fatalf("coverage = %+v, want the newest (12)", st.Coverage)
	}
	if st.Totals == nil || st.RecordsRead != 3 {
		t.Fatalf("recordsRead %d totals %+v", st.RecordsRead, st.Totals)
	}
	old := agg(set(coverageAt(10, aggWindow.Since.Add(-time.Hour))))
	if old.Coverage != nil {
		t.Fatalf("a record before the window is the coverage: %+v", old.Coverage)
	}
	only := agg(set(coverageAt(10, hoursAgo(5))))
	if only.Coverage == nil || only.Totals != nil || only.Runs != 0 || only.Completeness != fleet.CompletenessComplete {
		t.Fatalf("coverage alone = %+v", only)
	}
	// Coverage alone is no empty window: only the CI and delivery records are missing.
	if codes := limitationCodes(only.Limitations); !slices.Equal(codes, []string{LimitationOnlyCoverage}) {
		t.Fatalf("coverage alone limitations = %q", codes)
	}
}

func limitationCodes(ls []fleet.Limitation) []string {
	var codes []string
	for _, l := range ls {
		codes = append(codes, l.Code)
	}
	return codes
}

func TestCoverageExcludedFromRunsAndGroups(t *testing.T) {
	cov := coverageAt(10, hoursAgo(5))
	st := Aggregate(set(cov, cov, diffOf("billing")), Query{Window: aggWindow, By: "service"}, unrestrictedScope(t), aggNow)
	if st.Runs != 1 || st.DistinctChanges != 1 {
		t.Fatalf("runs %d distinct %d count coverage", st.Runs, st.DistinctChanges)
	}
	if got := labels(st.Groups); got != "billing" {
		t.Fatalf("groups %q", got)
	}
	kinds := Aggregate(set(cov, diffOf("billing")), Query{Window: aggWindow, Kinds: []string{runrecord.KindDiff}}, Scope{}, aggNow)
	if kinds.Coverage != nil {
		t.Fatal("a kind filter without coverage kept coverage")
	}
}

func TestRestrictedHidesCoverageRecords(t *testing.T) {
	sc := restrictedScope(t)
	base := Aggregate(set(diffOf("catalog")), Query{Window: aggWindow}, sc, aggNow)
	st := Aggregate(set(diffOf("catalog"), coverageAt(10, hoursAgo(5))), Query{Window: aggWindow}, sc, aggNow)
	if st.Coverage != nil || st.RecordsRead != base.RecordsRead || st.Runs != base.Runs {
		t.Fatalf("a restricted caller sees coverage: %+v", st)
	}
	// Coverage names no service, so it is said apart from records that do.
	got := limitationCodes(st.Limitations)
	if st.Hidden.Present || st.Completeness != fleet.CompletenessPartial || !slices.Contains(got, LimitationCoverageOutsideView) ||
		slices.Contains(got, LimitationOutsideView) || slices.Contains(limitationCodes(base.Limitations), LimitationCoverageOutsideView) {
		t.Fatalf("hidden coverage is not said on its own: base %+v, with coverage %+v %+v", base.Limitations, st.Hidden, st.Limitations)
	}
	if open := Aggregate(set(coverageAt(10, hoursAgo(5))), Query{Window: aggWindow}, Scope{Restricted: true, Place: func(string) (Placement, bool) { return Placement{}, true }}, aggNow); open.Coverage != nil {
		t.Fatal("a scope that places every name shows coverage history")
	}
}

// A snapshot taken within the second a record is written must not land after
// its recordedAt, which is whole seconds, or Parse rejects the record.
func TestCoverageOfSnapshotIsWholeSeconds(t *testing.T) {
	at := fleetNow.Add(700 * time.Millisecond)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	c := CoverageOf(fleet.NewQuery(snap))
	if !c.SnapshotAsOf.Equal(fleetNow) {
		t.Fatalf("snapshotAsOf %s, want %s", c.SnapshotAsOf, fleetNow)
	}
	if _, err := runrecord.Encode(runrecord.CoverageRecord(c, "dev", at)); err != nil {
		t.Fatal(err)
	}
}

// A read that only found the coverage anchor withholds coverage from a
// restricted caller as surely as one that read it.
func TestRestrictedCoverageAnchorWithheld(t *testing.T) {
	s := set(diffOf("catalog"))
	s.CoverageAnchor = true
	st := Aggregate(s, Query{Window: aggWindow}, restrictedScope(t), aggNow)
	if !slices.Contains(limitationCodes(st.Limitations), LimitationCoverageOutsideView) || st.Completeness != fleet.CompletenessPartial {
		t.Fatalf("limitations %+v, %s", st.Limitations, st.Completeness)
	}
	if open := Aggregate(s, Query{Window: aggWindow}, Scope{}, aggNow); slices.Contains(limitationCodes(open.Limitations), LimitationCoverageOutsideView) {
		t.Fatal("an unrestricted caller is told coverage is withheld")
	}
}
