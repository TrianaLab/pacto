package delivery

import (
	"cmp"
	"fmt"
	"strings"
	"time"
)

// States of a Stage at a time.
const (
	AtKnown   = "known"
	AtPartial = "partial"
	AtUnknown = "unknown"
)

// StageAt is which artifact one Stage ran at a time, as its records say.
type StageAt struct {
	Namespace   string `json:"namespace" doc:"The Stage's namespace"`
	Stage       string `json:"stage" doc:"The Stage's name"`
	Environment string `json:"environment,omitempty" doc:"The environment the Stage delivers to"`
	Service     string `json:"service,omitempty" doc:"The service the Stage delivers"`
	Domain      string `json:"domain,omitempty" doc:"The registry domain the Stage names for its service, empty for the default"`
	State       string `json:"state" enum:"known,partial,unknown" doc:"known: a signed record names what ran and nothing after it is missing or unsigned; partial: a record names it but a later move may be missing, is not signed or the signatures were not checked; unknown: no record before this time, or the Stage could not be read"`
	Digest      string `json:"digest,omitempty" doc:"The artifact digest the Stage ran, from the newest recorded move at or before the time"`
	Since       string `json:"since,omitempty" doc:"When that move reached the Stage"`
	Record      string `json:"record,omitempty" doc:"The record of that move"`
	Version     string `json:"version,omitempty" doc:"The version the artifact's contract declares, when it could be read"`
	Revision    string `json:"revision,omitempty" doc:"The fleet revision key of the artifact. On the CLI and MCP it is derived from the contract's digest and name, not checked against a fleet; on the dashboard it is the revision this dashboard's fleet holds"`
	Owner       string `json:"owner,omitempty" doc:"The owner the artifact's contract declares"`
	OwnerKey    string `json:"ownerKey,omitempty" doc:"The key of that owner"`
	// BusinessDomain is the contract's service.domain; Domain is the
	// registry scope.
	BusinessDomain string `json:"businessDomain,omitempty" doc:"The business domain the artifact's contract declares"`
	Reason         string `json:"reason,omitempty" doc:"Why the state is not known"`
}

// AtAnswer is which artifact each Stage read ran at one time.
type AtAnswer struct {
	At           string    `json:"at" doc:"The time asked about, RFC 3339"`
	AsOf         string    `json:"asOf" doc:"When the records were read"`
	Completeness string    `json:"completeness" enum:"complete,partial" doc:"complete only when every Stage is known, every namespace was read and no other page of Stages remains"`
	Note         string    `json:"note" doc:"How many Stages were read and what was not"`
	Unreadable   []string  `json:"unreadable,omitempty" doc:"The namespaces or Stages that could not be read, and why"`
	Stages       []StageAt `json:"stages" doc:"Each Stage read, with what it ran at the time"`
}

// UnreadAt is st at a time its history could not be read: unknown, and why.
func UnreadAt(st StageObject, reason string) StageAt {
	a := stageAtOf(st)
	a.State, a.Reason = AtUnknown, reason
	return a
}

// stageAtOf is st's own fields.
func stageAtOf(st StageObject) StageAt {
	return StageAt{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Environment: st.Spec.Environment,
		Service: st.Spec.Service.Name, Domain: st.Spec.Service.Domain}
}

// recordMove says e is a line read from an operation record, signed or not.
func recordMove(e HistoryEntry) bool { return e.Record != "" && e.Kind == RecordOperation }

// moveTime is when e moved its Stage, when it is a succeeded move; ok is
// false for any other line, timed says whether its time could be read.
func moveTime(e HistoryEntry) (t time.Time, timed, ok bool) {
	if e.Pointer == nil || e.Pointer.After == "" || e.Outcome != Succeeded {
		return t, false, false
	}
	t, err := time.Parse(time.RFC3339, cmp.Or(e.Applied, e.Finished))
	return t, err == nil, true
}

// StateAt is which artifact st ran at t, read from its history h: the newest
// recorded, timed move at or before t names it. It is known only on a
// complete, verified history of records with no problem, when no move after
// it lacks a time and no move after it, at or before t, is unsigned. A
// time is never guessed: no recorded move before t is unknown.
func StateAt(st StageObject, h History, t time.Time) StageAt {
	a := stageAtOf(st)
	a.Stage, a.Environment = cmp.Or(a.Stage, h.Stage.Name), cmp.Or(a.Environment, h.Stage.Environment)
	found := -1
	for i, e := range h.Entries {
		if when, timed, ok := moveTime(e); ok && timed && recordMove(e) && !when.After(t) {
			found = i
		}
	}
	if found < 0 {
		a.State, a.Reason = AtUnknown, "no record before this time"
		return a
	}
	e := h.Entries[found]
	a.Digest, a.Since, a.Record = e.Pointer.After, cmp.Or(e.Applied, e.Finished), e.Record
	reasons := historyDoubts(h)
	reasons = append(reasons, laterDoubts(h, found, t)...)
	a.State = AtKnown
	if len(reasons) > 0 {
		a.State, a.Reason = AtPartial, strings.Join(reasons, "; ")
	}
	return a
}

// historyDoubts are why h as a whole cannot vouch for a move.
func historyDoubts(h History) []string {
	var out []string
	if !h.Verified {
		out = append(out, "recorded, signature not checked")
	}
	if h.Source != SourceRecords || h.Completeness != "complete" || len(h.Problems) > 0 {
		out = append(out, "a later move may be missing")
	}
	return out
}

// laterDoubts are why the moves after h.Entries[from] leave what ran at t
// in doubt: one with no time, or one at or before t that is not signed.
func laterDoubts(h History, from int, t time.Time) []string {
	var untimed, unsigned bool
	for _, e := range h.Entries[from+1:] {
		when, timed, ok := moveTime(e)
		switch {
		case !ok:
		case !timed:
			untimed = true
		case !when.After(t) && !SignedMove(h, e):
			unsigned = true
		}
	}
	var out []string
	if untimed {
		out = append(out, "a move after it has no time")
	}
	if unsigned {
		out = append(out, "a move after it is not signed")
	}
	return out
}

// Complete says how complete a is: complete only when every Stage is known,
// every namespace was read (everyNamespace, and nothing unreadable) and no
// other page of Stages remains.
func (a *AtAnswer) Complete(everyNamespace, morePages bool, unreadable []string) {
	known := 0
	for _, s := range a.Stages {
		if s.State == AtKnown {
			known++
		}
	}
	a.Unreadable, a.Completeness = unreadable, "complete"
	a.Note = fmt.Sprintf("%d Stages read, %d of them known at this time", len(a.Stages), known)
	if !everyNamespace || morePages || len(unreadable) > 0 {
		a.Note += "; others were not read"
	}
	if known < len(a.Stages) || !everyNamespace || morePages || len(unreadable) > 0 {
		a.Completeness = "partial"
	}
}

// Describe fills each Stage's version, owner, business domain and derived
// revision key (RevisionKeyOf) from what its digest carries.
func (a *AtAnswer) Describe(read map[string]Described) {
	for i, s := range a.Stages {
		d, ok := read[s.Digest]
		if s.Digest == "" || !ok {
			continue
		}
		var st StageObject
		st.Spec.Service.Name = s.Service
		a.Stages[i].Version, a.Stages[i].Owner, a.Stages[i].OwnerKey, a.Stages[i].BusinessDomain = d.Version, d.Team, d.OwnerKey, d.BusinessDomain
		a.Stages[i].Revision = string(RevisionKeyOf(st, d))
	}
}
