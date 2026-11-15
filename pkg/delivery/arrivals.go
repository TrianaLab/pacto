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
//
// doubts names what leaves a time in doubt, so the caller never reads a
// missing reach as "not recorded": each Stage whose history may lack earlier
// moves (read from statuses, partial or with a problem), since its earliest
// move read may not be its first, and each moved artifact contractOf could
// not read (read false).
func Arrivals(hs []StageHistory, contractOf func(digest string) (contract string, read bool)) (out map[string][]fleet.ChangeReach, doubts []string) {
	out = map[string][]fleet.ChangeReach{}
	unread := map[string]bool{}
	for _, h := range hs {
		if h.Source != SourceRecords || h.Completeness != string(fleet.CompletenessComplete) || len(h.Problems) > 0 {
			doubts = append(doubts, h.Namespace+"/"+h.Stage.Name+": earlier moves may be missing")
		}
		earliest := map[string]fleet.ChangeReach{}
		at := map[string]time.Time{}
		for _, e := range h.Entries {
			t, timed, ok := moveTime(e)
			if !ok || !timed {
				continue
			}
			c, read := contractOf(e.Pointer.After)
			if !read && !unread[e.Pointer.After] {
				unread[e.Pointer.After] = true
				doubts = append(doubts, e.Pointer.After+": its artifact could not be read")
			}
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
	return out, doubts
}

// ContractOf answers, from what each digest carries, the contract digest it
// names ("" when its contract is not digest-pinned) and whether that was
// read: a pinned contract was, even when the rest of the artifact was not,
// and so was an artifact that names none (NoContract).
func ContractOf(read map[string]Described) func(string) (string, bool) {
	return func(dg string) (string, bool) {
		d, ok := read[dg]
		c := ContractDigest(d.Contract)
		return c, ok && (c != "" || d.Err == "" || d.NoContract)
	}
}

// reachOf is e's move as a reach of h's Stage.
func reachOf(h StageHistory, e HistoryEntry) fleet.ChangeReach {
	r := fleet.ChangeReach{Namespace: h.Namespace, Stage: h.Stage.Name, Environment: h.Stage.Environment, At: cmp.Or(e.Applied, e.Finished)}
	if !recordMove(e) {
		r.FromStatus = true
		return r
	}
	r.Record, r.Verified = e.Record, SignedMove(h.History, e)
	if e.Impact != nil {
		r.Impact = &fleet.RecordedVerdict{Gate: e.Impact.Gate, Result: string(e.Impact.Result), Message: e.Impact.Message}
	}
	return r
}
