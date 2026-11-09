package delivery

import (
	"cmp"
	"math/bits"
	"slices"
	"time"
)

// TimelineBuckets is how many equal slices a timeline cuts its window into.
const TimelineBuckets = 12

// TimelineSpan is a timeline's window when no since bounds it: the last day.
const TimelineSpan = 24 * time.Hour

// TimelineCounts is how many history lines are of each kind. Each line counts
// in exactly one, the first that holds, worst first.
type TimelineCounts struct {
	Missing         int `json:"missing" doc:"Gap and unrecordable records: a change ran, but what it did is not on the record"`
	Failed          int `json:"failed" doc:"Changes that failed or errored"`
	Held            int `json:"held" doc:"Changes held for a person"`
	AcceptedUnknown int `json:"acceptedUnknown" doc:"Changes whose verification ended unknown and a person accepted it"`
	Unverified      int `json:"unverified" doc:"Changes that succeeded and moved what the Stage runs but were never verified: an override, or a promote a rollback or override preempted"`
	Released        int `json:"released" doc:"Changes that succeeded, moved what the Stage runs and verified: a promotion or rollback"`
	Other           int `json:"other" doc:"Every other line: in flight, cancelled, rejected, expired, superseded, or a change that moved nothing"`
}

// TimelineBucket is one slice of a timeline's window.
type TimelineBucket struct {
	Start string `json:"start"`
	End   string `json:"end"`
	TimelineCounts
}

// HistoryTimeline is how many history lines fell in each slice of a window,
// by kind (B7).
type HistoryTimeline struct {
	Start   string           `json:"start" doc:"The window's start: since, or one day before its end"`
	End     string           `json:"end" doc:"The window's end: until, or when the answer was made"`
	Buckets []TimelineBucket `json:"buckets" doc:"Equal slices of the window, oldest first"`
	Totals  TimelineCounts   `json:"totals" doc:"Every line in the window; the buckets sum to it"`
	Untimed int              `json:"untimed" doc:"Lines with no time, or one that does not parse: in no slice and no total"`
}

// TimelineWindow is the window a timeline covers: until (or now) back to
// since, or one day when since is not set.
func TimelineWindow(since, until, now time.Time) (time.Time, time.Time) {
	end := until
	if end.IsZero() {
		end = now
	}
	if since.IsZero() {
		return end.Add(-TimelineSpan), end
	}
	return since, end
}

// Timeline counts entries into TimelineBuckets equal slices of start..end by
// the time each line names: when it finished, else when it reached the Stage,
// else since when it waits. A line outside the window is in no slice.
func Timeline(entries []HistoryEntry, start, end time.Time) HistoryTimeline {
	t := HistoryTimeline{Start: stampOf(start), End: stampOf(end), Buckets: make([]TimelineBucket, TimelineBuckets)}
	span := max(end.Sub(start), time.Nanosecond)
	// bound is where slice i starts. It splits the window as quotient and
	// remainder so a window of centuries does not overflow time.Duration.
	bound := func(i int) time.Duration {
		n := time.Duration(i)
		return span/TimelineBuckets*n + span%TimelineBuckets*n/TimelineBuckets
	}
	for i := range t.Buckets {
		t.Buckets[i].Start = stampOf(start.Add(bound(i)))
		t.Buckets[i].End = stampOf(start.Add(bound(i + 1)))
	}
	for _, e := range entries {
		at, err := e.timed()
		if err != nil {
			t.Untimed++
			continue
		}
		if at.Before(start) || at.After(end) {
			continue
		}
		i := min(slice(at.Sub(start), span), TimelineBuckets-1)
		e.count(&t.Buckets[i].TimelineCounts)
		e.count(&t.Totals)
	}
	return t
}

// slice is the slice an offset 0 <= d <= span falls in: d*TimelineBuckets/span
// worked in 128 bits, so a window of centuries does not overflow int64.
func slice(d, span time.Duration) int {
	hi, lo := bits.Mul64(uint64(d), TimelineBuckets)
	q, _ := bits.Div64(hi, lo, uint64(span))
	return int(q)
}

// timed is the one time a timeline counts e at: when it finished, else when it
// reached the Stage, else since when it waits.
func (e HistoryEntry) timed() (time.Time, error) {
	return time.Parse(time.RFC3339, cmp.Or(e.Finished, e.Applied, e.Since))
}

// SliceEntries keeps the changes of h a timeline counts in since..until: those
// whose own time (see Timeline) is at or after since and before until; a zero
// bound is open. Unlike WindowEntries, a change with no time, or one that only
// overlaps the window, is dropped, so a timeline column's count is the length
// of its list. h is not changed.
// ponytail: the last column of a window with an until also counts a change at
// exactly until, which this half-open slice leaves out.
func SliceEntries(h History, since, until time.Time) History {
	h.Entries = slices.DeleteFunc(slices.Clone(h.Entries), func(e HistoryEntry) bool {
		at, err := e.timed()
		return err != nil || !since.IsZero() && at.Before(since) || !until.IsZero() && !at.Before(until)
	})
	h.Note += "; only changes with a time" + sliceWords(since, until) + " are listed"
	return h
}

// sliceWords says a half-open window in words, after "with a time".
func sliceWords(since, until time.Time) string {
	var w string
	if !since.IsZero() {
		w += " at or after " + since.UTC().Format(time.RFC3339)
	}
	if !since.IsZero() && !until.IsZero() {
		w += " and"
	}
	if !until.IsZero() {
		w += " before " + until.UTC().Format(time.RFC3339)
	}
	return w
}

func stampOf(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// count adds the line to the one kind it is counted in.
func (e HistoryEntry) count(c *TimelineCounts) {
	switch {
	case e.Kind == RecordGap || e.Kind == RecordUnrecordable:
		c.Missing++
	case e.Outcome == Failed || e.Outcome == Errored:
		c.Failed++
	case e.Phase == PhaseHeld:
		c.Held++
	case e.Verification == AcceptedUnknown:
		c.AcceptedUnknown++
	case e.Outcome == Succeeded && e.Pointer != nil && e.Verification == Verified:
		c.Released++
	case e.Outcome == Succeeded && e.Pointer != nil:
		c.Unverified++
	default:
		c.Other++
	}
}
