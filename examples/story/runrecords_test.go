package story

import (
	"bytes"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

const goldenRunRecords = "testdata/runrecords.golden.jsonl"

func runRecords(t *testing.T, now time.Time) []runrecord.Record {
	t.Helper()
	recs, err := RunRecords(now, StatsServices())
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func encodeAll(t *testing.T, recs []runrecord.Record) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, r := range recs {
		line, err := runrecord.Encode(r)
		if err != nil {
			t.Fatalf("%+v: %v", r, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// TestRunRecordsGolden: the demo records at the story's clock.
// PACTO_UPDATE_RUN_RECORDS=1 (make -C examples/demo run-records) rewrites it.
func TestRunRecordsGolden(t *testing.T) {
	got := encodeAll(t, runRecords(t, Now))
	if os.Getenv("PACTO_UPDATE_RUN_RECORDS") == "1" {
		if err := os.WriteFile(goldenRunRecords, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenRunRecords)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/runrecords.golden.jsonl is stale; run make -C examples/demo run-records")
	}
}

func TestRunRecordsParse(t *testing.T) {
	for line := range bytes.Lines(encodeAll(t, runRecords(t, Now))) {
		line = bytes.TrimSuffix(line, []byte("\n"))
		r, err := runrecord.Parse(line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		again, err := runrecord.Encode(r)
		if err != nil || !bytes.Equal(again, line) {
			t.Fatalf("%s does not re-encode to itself: %s %v", line, again, err)
		}
	}
}

// shift moves every time in r by d.
func shift(r runrecord.Record, d time.Duration) runrecord.Record {
	r.RecordedAt = r.RecordedAt.Add(d)
	if r.AsOf != nil {
		at := r.AsOf.Add(d)
		r.AsOf = &at
	}
	for _, s := range []*string{&r.StartedAt, &r.FinishedAt} {
		if t, err := time.Parse(time.RFC3339, *s); err == nil {
			*s = t.Add(d).Format(time.RFC3339)
		}
	}
	return r
}

func TestRunRecordsShiftWithNow(t *testing.T) {
	later := 400 * 24 * time.Hour
	a, b := runRecords(t, Now), runRecords(t, Now.Add(later))
	if len(a) != len(b) {
		t.Fatalf("%d records at Now, %d 400 days later", len(a), len(b))
	}
	for i := range a {
		if back := shift(b[i], -later); !reflect.DeepEqual(back, a[i]) {
			t.Fatalf("record %d moved by more than its times:\n%+v\n%+v", i, back, a[i])
		}
	}
}

func TestRunRecordsWithinNinetyDays(t *testing.T) {
	for _, r := range runRecords(t, Now) {
		if r.RecordedAt.Before(Now.Add(-90*24*time.Hour)) || r.RecordedAt.After(Now) {
			t.Errorf("%s %s at %v is outside the 90 days before now", r.Kind, r.Service, r.RecordedAt)
		}
	}
}

func TestRunRecordsDeterministic(t *testing.T) {
	if !reflect.DeepEqual(runRecords(t, Now), runRecords(t, Now)) {
		t.Fatal("two calls differ")
	}
}

func TestRunRecordsHaveHonestUnknowns(t *testing.T) {
	recs := runRecords(t, Now)
	deploys := map[string]bool{}
	for _, r := range recs {
		if r.Kind == runrecord.KindPublish && r.DeployDigest != "" {
			deploys[r.Service+" "+r.DeployDigest] = true
		}
		if !slices.Contains([]string{runrecord.KindDiff, runrecord.KindImpact, runrecord.KindPublish, runrecord.KindOperation}, r.Kind) {
			t.Errorf("kind %q is outside the closed list", r.Kind)
		}
	}
	unjoined := false
	// weeks[env][w] counts promotes in week w of the last four.
	weeks := map[string]*[4]int{}
	for _, r := range recs {
		if r.Type != "promote" {
			continue
		}
		unjoined = unjoined || (r.To != "" && !deploys[r.Service+" "+r.To])
		if w := int(Now.Sub(r.RecordedAt) / (7 * 24 * time.Hour)); w < 4 {
			if weeks[r.Environment] == nil {
				weeks[r.Environment] = &[4]int{}
			}
			weeks[r.Environment][w]++
		}
	}
	if !unjoined {
		t.Error("every promote joins a publish record: no lead time is unknown")
	}
	gap := false
	for _, w := range weeks {
		gap = gap || slices.Contains(w[:], 0)
	}
	if !gap {
		t.Error("every environment promotes in each of the last four weeks: no rate is unknown")
	}
}

func TestRunRecordsCoverStagePaths(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range runRecords(t, Now) {
		if r.Stage != nil {
			seen[r.Stage.Namespace+"/"+r.Stage.Name] = true
		}
	}
	for _, s := range StatsServices() {
		for _, st := range s.Stages {
			if !seen[st.Namespace+"/"+st.Name] {
				t.Errorf("no operation record on Stage %s/%s", st.Namespace, st.Name)
			}
		}
	}
	for _, want := range []string{"prod-eu/checkout", "prod-us/checkout", "prod/audit-log", "prod/notification-worker"} {
		if !seen[want] {
			t.Errorf("no operation record on %s", want)
		}
	}
}

func TestRunRecordsRepeatRuns(t *testing.T) {
	runs := map[string]int{}
	for _, r := range runRecords(t, Now) {
		if r.Kind != runrecord.KindOperation {
			runs[r.ID]++
		}
	}
	repeated := 0
	for _, n := range runs {
		if n > 1 {
			repeated++
		}
	}
	if repeated < 2 {
		t.Fatalf("%d changes recorded more than once, want at least 2", repeated)
	}
}

func TestRunRecordsHoldNoPersonalData(t *testing.T) {
	out := string(encodeAll(t, runRecords(t, Now)))
	banned := []string{"@"}
	for _, p := range []Persona{PayAgent, Dana, Alice, Carol, Promoter} {
		banned = append(banned, p.User)
		banned = append(banned, p.Groups...)
	}
	for _, b := range banned {
		if strings.Contains(out, b) {
			t.Errorf("the records hold %q", b)
		}
	}
}

func TestRunRecordsOperationIDsAreUIDs(t *testing.T) {
	uid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	n := 0
	for _, r := range runRecords(t, Now) {
		if r.Kind != runrecord.KindOperation {
			continue
		}
		n++
		if !uid.MatchString(r.ID) {
			t.Errorf("operation id %q is not a UID", r.ID)
		}
		if _, err := runrecord.Encode(r); err != nil {
			t.Error(err)
		}
	}
	if n == 0 {
		t.Fatal("no operation record")
	}
}

func TestRecordsRejectsMissingRevisionOrStage(t *testing.T) {
	services := StatsServices()
	op := StatsEvent{At: time.Hour, Kind: runrecord.KindOperation, Type: delivery.TypePromote, Service: "payments", Stage: Test,
		From: -1, To: 0, Outcome: string(delivery.Succeeded), Actor: Promoter}
	if _, err := Records(Now, services, []StatsEvent{op}); err != nil {
		t.Fatalf("a valid event fails: %v", err)
	}
	pastEnd, noStage, noService, noKind := op, op, op, op
	pastEnd.To = 99
	noStage.Stage = "staging"
	noService.Service = "ledger"
	noKind.Kind = "deploy"
	for name, ev := range map[string]StatsEvent{"revision past the end": pastEnd, "absent Stage": noStage, "absent service": noService, "unknown kind": noKind} {
		if _, err := Records(Now, services, []StatsEvent{ev}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestHistoryRecordsUseOnlyHistoryDigests(t *testing.T) {
	story := map[string]bool{}
	for _, s := range StatsServices() {
		for _, r := range s.Revisions {
			story[r.Digest], story[r.Artifact] = true, true
		}
	}
	recs, err := HistoryRecords(Now, StatsServices())
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("no history")
	}
	for _, r := range recs {
		for _, d := range []string{r.OldRevision, r.NewRevision, r.BundleDigest, r.DeployDigest, r.To, r.From, r.Contract} {
			if story[d] {
				t.Errorf("history record %s %s names story digest %s", r.Kind, r.Service, d)
			}
		}
		if !r.RecordedAt.Before(Now.Add(-30 * 24 * time.Hour)) {
			t.Errorf("history record %s %s at %v is inside the story's 30 days", r.Kind, r.Service, r.RecordedAt)
		}
	}
}

// TestRunRecordsCoverageWeekly: one coverage record a week for 13 weeks, the
// newest the caller's snapshot unchanged, the older ones scaled down from it
// with the knowledge tally still adding up.
func TestRunRecordsCoverageWeekly(t *testing.T) {
	now := Now.Add(400 * day)
	newest := runrecord.Coverage{
		Services: 9, Uncontracted: 2,
		Knowledge:    &runrecord.CoverageKnowledge{Services: 9, Described: 5, WithGaps: 3, Unknown: 1, NotDeployed: 2},
		Links:        runrecord.CoverageLinks{Exact: 7, Inferred: 3, Ambiguous: 1, Unresolved: 1},
		SnapshotAsOf: now.Add(-time.Minute).Truncate(time.Second), Completeness: "complete",
	}
	recs := CoverageRecords(now, newest)
	if len(recs) != 13 {
		t.Fatalf("got %d coverage records, want 13", len(recs))
	}
	ids := map[string]bool{}
	prev := -1
	for i, r := range recs {
		checkCoveragePoint(t, now, i, r)
		ids[r.ID] = true
		if k := r.Coverage.Knowledge; k.Described < prev {
			t.Errorf("record %d described %d after %d", i, k.Described, prev)
		}
		prev = r.Coverage.Knowledge.Described
	}
	if len(ids) != 13 {
		t.Errorf("got %d distinct ids, want 13", len(ids))
	}
	if got := *recs[12].Coverage; !reflect.DeepEqual(got, newest) {
		t.Errorf("newest coverage = %+v, want %+v", got, newest)
	}
	if recs[0].Coverage.Knowledge.Described >= newest.Knowledge.Described {
		t.Errorf("oldest described %d, want fewer than %d", recs[0].Coverage.Knowledge.Described, newest.Knowledge.Described)
	}

	newest.Knowledge = nil
	for i, r := range CoverageRecords(now, newest) {
		if r.Coverage.Knowledge != nil {
			t.Errorf("record %d invents a knowledge tally: %+v", i, *r.Coverage.Knowledge)
		}
		if _, err := runrecord.Encode(r); err != nil {
			t.Errorf("record %d: %v", i, err)
		}
	}
}

// TestCoverageRecordsStampAnOutOfWeekSnapshotNow: a newest snapshot that is
// zero, older than the last weekly point or after now is stamped now, so the
// newest record stays the newest by snapshot time (what the totals read as the
// current value), keeps a distinct id and stays valid.
func TestCoverageRecordsStampAnOutOfWeekSnapshotNow(t *testing.T) {
	now := Now.Add(400 * day)
	for name, asOf := range map[string]time.Time{
		"zero": {}, "stale": now.Add(-30 * day), "on a weekly point": now.Add(-7 * day), "future": now.Add(time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			recs := CoverageRecords(now, runrecord.Coverage{Services: 9, SnapshotAsOf: asOf, Completeness: "complete",
				Knowledge: &runrecord.CoverageKnowledge{Services: 9, Described: 5, WithGaps: 3, Unknown: 1}})
			ids := map[string]bool{}
			for i, r := range recs {
				checkCoveragePoint(t, now, i, r)
				ids[r.ID] = true
				if i < 12 && !r.Coverage.SnapshotAsOf.Before(recs[12].Coverage.SnapshotAsOf) {
					t.Errorf("record %d snapshot %s is not before the newest %s", i, r.Coverage.SnapshotAsOf, recs[12].Coverage.SnapshotAsOf)
				}
			}
			if len(ids) != 13 {
				t.Errorf("got %d distinct ids, want 13", len(ids))
			}
			if got := recs[12].Coverage; !got.SnapshotAsOf.Equal(now) || got.Services != 9 {
				t.Errorf("newest = %+v, want 9 services snapshotted at %s", *got, now)
			}
		})
	}
}

// checkCoveragePoint checks weekly coverage point i: valid, fleet-wide, a
// week apart, its tally adding up and every older point partial.
func checkCoveragePoint(t *testing.T, now time.Time, i int, r runrecord.Record) {
	t.Helper()
	if _, err := runrecord.Encode(r); err != nil {
		t.Fatalf("record %d: %v", i, err)
	}
	if r.Service != runrecord.CoverageService || r.Kind != runrecord.KindCoverage {
		t.Fatalf("record %d names %s/%s", i, r.Kind, r.Service)
	}
	if want := now.Add(-time.Duration(12-i) * 7 * day).UTC().Truncate(time.Second); !r.RecordedAt.Equal(want) {
		t.Errorf("record %d recorded at %s, want %s", i, r.RecordedAt, want)
	}
	if k := r.Coverage.Knowledge; k.Described+k.WithGaps+k.Unknown != k.Services || k.Unknown < 0 || k.NotDeployed > k.Described+k.WithGaps {
		t.Errorf("record %d tally does not add up: %+v", i, *k)
	}
	if i < 12 && r.Coverage.Completeness != "partial" {
		t.Errorf("record %d completeness %q, want partial", i, r.Coverage.Completeness)
	}
}
