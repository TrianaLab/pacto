package delivery

import (
	"cmp"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Arrivals is, per contract digest, when its artifact first reached each
// Stage of hs: the earliest timed move (applied, else finished) of each
// Stage to an artifact whose contract digest contractOf names. A signed move
// (SignedMove) carries its record and verdict; a record on an unverified
// history carries its record and says it was not checked; a line read from
// an Operation's status or an unrecordable record says it was not signed.
// A move with no time is left out: no time is invented.
func Arrivals(hs []StageHistory, contractOf func(digest string) string) map[string][]fleet.ChangeReach {
	out := map[string][]fleet.ChangeReach{}
	for _, h := range hs {
		earliest := map[string]fleet.ChangeReach{}
		at := map[string]time.Time{}
		for _, e := range h.Entries {
			t, ok := arrivalTime(e)
			if !ok {
				continue
			}
			c := contractOf(e.Pointer.After)
			if prev, seen := at[c]; c == "" || seen && !t.Before(prev) {
				continue
			}
			at[c], earliest[c] = t, reachOf(h, e)
		}
		for c, r := range earliest {
			out[c] = append(out[c], r)
		}
	}
	for _, rs := range out {
		slices.SortFunc(rs, func(a, b fleet.ChangeReach) int {
			return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Stage, b.Stage))
		})
	}
	return out
}

// arrivalTime is when e moved its Stage, when it is a succeeded move with a
// readable time.
func arrivalTime(e HistoryEntry) (time.Time, bool) {
	if e.Pointer == nil || e.Pointer.After == "" || e.Outcome != Succeeded {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, cmp.Or(e.Applied, e.Finished))
	return t, err == nil
}

// reachOf is e's move as a reach of h's Stage.
func reachOf(h StageHistory, e HistoryEntry) fleet.ChangeReach {
	r := fleet.ChangeReach{Namespace: h.Namespace, Stage: h.Stage.Name, Environment: h.Stage.Environment, At: cmp.Or(e.Applied, e.Finished)}
	if e.Record == "" || e.Kind != RecordOperation {
		r.FromStatus = true
		return r
	}
	r.Record, r.Verified = e.Record, SignedMove(h.History, e)
	if e.Impact != nil {
		r.Impact = &fleet.RecordedVerdict{Gate: e.Impact.Gate, Result: string(e.Impact.Result), Message: e.Impact.Message}
	}
	return r
}
