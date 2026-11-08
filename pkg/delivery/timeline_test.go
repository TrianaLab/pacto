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
	var sum TimelineCounts
	for _, b := range tl.Buckets {
		sum.Missing += b.Missing
		sum.Failed += b.Failed
		sum.Held += b.Held
		sum.AcceptedUnknown += b.AcceptedUnknown
		sum.Released += b.Released
		sum.Other += b.Other
	}
	if sum != tl.Totals {
		t.Errorf("the buckets sum to the totals: %+v", sum)
	}
	if b := tl.Buckets[0]; b.Missing != 2 || b.Start != tl.Start || b.End != at(22) {
		t.Errorf("the first slice holds the gap: %+v", b)
	}
	if b := tl.Buckets[TimelineBuckets-1]; b.Failed != 2 || b.Held != 1 || b.End != tl.End {
		t.Errorf("the last slice holds the end: %+v", b)
	}
	if b := tl.Buckets[9]; b.Released != 1 || b.AcceptedUnknown != 1 || b.Other != 1 {
		t.Errorf("five hours ago: %+v", b)
	}
	// A window of no length still counts what is at its one instant.
	if tl = Timeline([]HistoryEntry{{Finished: at(0)}}, end, end); tl.Buckets[0].Other != 1 {
		t.Errorf("a zero window: %+v", tl.Buckets)
	}
}
