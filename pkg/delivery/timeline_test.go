package delivery

import (
	"fmt"
	"testing"
	"time"
)

func TestTimelineWindow(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s, e := TimelineWindow(time.Time{}, time.Time{}, now)
	if !e.Equal(now) || !s.Equal(now.Add(-24*time.Hour)) {
		t.Errorf("the last day: %v %v", s, e)
	}
	since, until := now.Add(-6*time.Hour), now.Add(-time.Hour)
	if s, e = TimelineWindow(since, until, now); !s.Equal(since) || !e.Equal(until) {
		t.Errorf("the asked window: %v %v", s, e)
	}
	if s, e = TimelineWindow(time.Time{}, until, now); !e.Equal(until) || !s.Equal(until.Add(-24*time.Hour)) {
		t.Errorf("a day back from until: %v %v", s, e)
	}
}

// Every line counts in one kind, worst first, in the slice its time names;
// the buckets sum to the totals and a line with no time is counted apart.
func TestTimeline(t *testing.T) {
	end := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	start := end.Add(-24 * time.Hour)
	at := func(h int) string { return end.Add(-time.Duration(h) * time.Hour).Format(time.RFC3339) }
	moved := &PointerChange{After: "sha256:b"}
	entries := []HistoryEntry{
		{Kind: RecordGap, Finished: at(23), Outcome: Failed},
		{Kind: RecordUnrecordable, Finished: at(23)},
		{Outcome: Errored, Finished: at(1)},
		{Outcome: Failed, Phase: PhaseHeld, Finished: at(1)},
		{Phase: PhaseHeld, Since: at(0)},
		{Verification: AcceptedUnknown, Outcome: Succeeded, Pointer: moved, Applied: at(5)},
		{Outcome: Succeeded, Pointer: moved, Finished: at(5)},
		{Outcome: Succeeded, Finished: at(5)},
		{Outcome: Cancelled, Finished: at(30)},
		{Outcome: Succeeded, Finished: "yesterday"},
		{Outcome: Succeeded},
	}
	tl := Timeline(entries, start, end)
	if tl.Untimed != 2 || len(tl.Buckets) != TimelineBuckets || tl.Start != start.Format(time.RFC3339) || tl.End != end.Format(time.RFC3339) {
		t.Fatalf("shape: %+v", tl)
	}
	if got := fmt.Sprintf("%+v", tl.Totals); got != "{Missing:2 Failed:2 Held:1 AcceptedUnknown:1 Released:1 Other:1}" {
		t.Errorf("totals: %s", got)
	}
	if sum := sumBuckets(tl.Buckets); sum != tl.Totals {
		t.Errorf("the buckets sum to the totals: %+v", sum)
	}
	checkSlices(t, tl, at)
}

// checkSlices checks where TestTimeline's lines fell.
func checkSlices(t *testing.T, tl HistoryTimeline, at func(int) string) {
	t.Helper()
	if b := tl.Buckets[0]; b.Missing != 2 || b.Start != tl.Start || b.End != at(22) {
		t.Errorf("the first slice holds the gap: %+v", b)
	}
	if b := tl.Buckets[TimelineBuckets-1]; b.Failed != 2 || b.Held != 1 || b.End != tl.End {
		t.Errorf("the last slice holds the end: %+v", b)
	}
	if b := tl.Buckets[9]; b.Released != 1 || b.AcceptedUnknown != 1 || b.Other != 1 {
		t.Errorf("five hours ago: %+v", b)
	}
}

// A window of no length still counts what is at its one instant.
func TestTimelineZeroWindow(t *testing.T) {
	end := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	if tl := Timeline([]HistoryEntry{{Finished: end.Format(time.RFC3339)}}, end, end); tl.Buckets[0].Other != 1 {
		t.Errorf("a zero window: %+v", tl.Buckets)
	}
}

func sumBuckets(bs []TimelineBucket) TimelineCounts {
	var sum TimelineCounts
	for _, b := range bs {
		sum.Missing += b.Missing
		sum.Failed += b.Failed
		sum.Held += b.Held
		sum.AcceptedUnknown += b.AcceptedUnknown
		sum.Released += b.Released
		sum.Other += b.Other
	}
	return sum
}

// A window of decades cuts its slices without overflowing, and one longer
// than time.Duration holds still counts every line in it.
func TestTimelineLongWindow(t *testing.T) {
	end := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	for _, start := range []time.Time{time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), {}} {
		entries := []HistoryEntry{{Finished: start.Format(time.RFC3339)}, {Finished: end.Format(time.RFC3339)}}
		tl := Timeline(entries, start, end)
		if tl.Buckets[0].Other != 1 || tl.Buckets[TimelineBuckets-1].Other != 1 || tl.Totals.Other != 2 {
			t.Errorf("from %s: %+v", start, tl.Buckets)
		}
	}
	tl := Timeline(nil, time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), end)
	if tl.Buckets[0].Start != tl.Start || tl.Buckets[TimelineBuckets-1].End != tl.End {
		t.Errorf("the slices cover the window: %+v", tl.Buckets)
	}
}

// A slice keeps what a timeline column counts, so the column's count is the
// length of its list: no change without a time and none that only overlaps.
func TestSliceEntries(t *testing.T) {
	tm := func(h int) time.Time { return time.Date(2026, 11, 5, h, 0, 0, 0, time.UTC) }
	at := func(h int) string { return tm(h).Format(time.RFC3339) }
	h := History{Note: "n", Entries: []HistoryEntry{
		{Operation: "overlaps", Applied: at(7), Finished: at(13)},
		{Operation: "finished-in", Applied: at(7), Finished: at(9)},
		{Operation: "applied-in", Applied: at(10)},
		{Operation: "waits-in", Since: at(8)},
		{Operation: "at-until", Finished: at(12)},
		{Operation: "timeless"},
		{Operation: "unparseable", Finished: "later"},
	}}
	for _, c := range []struct {
		since, until time.Time
		want, note   string
	}{
		{tm(8), tm(12), "[finished-in applied-in waits-in]", "n; only changes with a time at or after 2026-11-05T08:00:00Z and before 2026-11-05T12:00:00Z are listed"},
		{tm(10), time.Time{}, "[overlaps applied-in at-until]", "n; only changes with a time at or after 2026-11-05T10:00:00Z are listed"},
		{time.Time{}, tm(9), "[waits-in]", "n; only changes with a time before 2026-11-05T09:00:00Z are listed"},
		{time.Time{}, time.Time{}, "[overlaps finished-in applied-in waits-in at-until]", "n; only changes with a time are listed"},
	} {
		got := SliceEntries(h, c.since, c.until)
		var names []string
		for _, e := range got.Entries {
			names = append(names, e.Operation)
		}
		if fmt.Sprint(names) != c.want || got.Note != c.note {
			t.Errorf("%s..%s: %v %q", c.since, c.until, names, got.Note)
		}
	}
	if len(h.Entries) != 7 || h.Note != "n" {
		t.Fatalf("the input history was mutated: %+v", h)
	}
}
