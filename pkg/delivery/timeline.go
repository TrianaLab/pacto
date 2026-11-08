package delivery

import (
	"cmp"
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
	Released        int `json:"released" doc:"Changes that succeeded and moved what the Stage runs: a promotion, rollback or override"`
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
	for i := range t.Buckets {
		t.Buckets[i].Start = stampOf(start.Add(span * time.Duration(i) / TimelineBuckets))
		t.Buckets[i].End = stampOf(start.Add(span * time.Duration(i+1) / TimelineBuckets))
	}
	for _, e := range entries {
		at, err := time.Parse(time.RFC3339, cmp.Or(e.Finished, e.Applied, e.Since))
		if err != nil {
			t.Untimed++
			continue
		}
		if at.Before(start) || at.After(end) {
			continue
		}
		i := min(int(at.Sub(start)*TimelineBuckets/span), TimelineBuckets-1)
		e.count(&t.Buckets[i].TimelineCounts)
		e.count(&t.Totals)
	}
	return t
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
	case e.Outcome == Succeeded && e.Pointer != nil:
		c.Released++
	default:
		c.Other++
	}
}
