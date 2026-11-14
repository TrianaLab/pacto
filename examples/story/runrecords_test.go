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
