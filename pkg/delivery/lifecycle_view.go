package delivery

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// LifecycleDigestLimit is how many of a service's artifacts one lifecycle
// answer reads to find the one a version or contract names: each is a
// registry read.
const LifecycleDigestLimit = 200

// lifecycleEventLimit is how many events one Stage of a lifecycle lists.
const lifecycleEventLimit = 50

// LifecycleQuery asks for one artifact of a service by exactly one of its
// digest, its contract's version or its contract (a manifest digest or a
// digest-pinned reference), on its Stages in Namespace ("" for every
// namespace the caller can list).
type LifecycleQuery struct{ Namespace, Service, Domain, Digest, Version, Contract string }

// Lifecycle is one artifact's way through delivery: whether it was
// published and, on each Stage of its service the caller can see, when it
// arrived, was verified, re-checked, rolled back or left.
type Lifecycle struct {
	Listing
	Service        string           `json:"service"`
	Domain         string           `json:"domain,omitempty"`
	Digest         string           `json:"digest" doc:"The artifact digest Stages point at"`
	Version        string           `json:"version,omitempty"`
	Contract       string           `json:"contract,omitempty" doc:"The digest-pinned contract the artifact names"`
	SourceRevision string           `json:"sourceRevision,omitempty" doc:"Stamped by pacto publish; an annotation, not signed"`
	Signature      string           `json:"signature" enum:"signed,unsigned,unknown" doc:"The Publish page's rule: signed when a Stage you can see names it in status.published or status.offered; unsigned only when an entry Stage fed by an OCIRepository, whose published list never reached its cap, holds it and none names it signed; else unknown"`
	SignedOn       []string         `json:"signedOn" doc:"The Stages (namespace/name) whose status.published or status.offered names this artifact: the evidence for signed"`
	UnsignedOn     []string         `json:"unsignedOn,omitempty" doc:"The entry Stages whose published list never reached its cap and does not name it: the evidence for unsigned"`
	Stages         []LifecycleStage `json:"stages"`
	Note           string           `json:"note"`
}

// LifecycleStage is the artifact on one Stage.
type LifecycleStage struct {
	Namespace       string           `json:"namespace"`
	Stage           string           `json:"stage"`
	Environment     string           `json:"environment"`
	Running         bool             `json:"running"`
	Verified        string           `json:"verified" enum:"yes,no,unknown" doc:"From the Stage's history (the verification outcome of the arrival) first, else status.verified; unknown when the digest is absent from a list at its cap or the Stage's history is partial"`
	Failed          string           `json:"failed" enum:"yes,no,unknown" doc:"From the Stage's history first, else status.failedDigests; unknown on the same terms as verified"`
	Offered         bool             `json:"offered" doc:"Offered downstream after it verified here"`
	LastCheck       *Recheck         `json:"lastCheck,omitempty" doc:"The newest re-check, when it judged this artifact"`
	Source          string           `json:"source" enum:"records,status"`
	RecordsVerified bool             `json:"recordsVerified"`
	Completeness    string           `json:"completeness" enum:"complete,partial"`
	Events          []LifecycleEvent `json:"events" doc:"Oldest first, at most the newest 50"`
}

// LifecycleEvent is one change of a Stage's history that touched the
// artifact.
type LifecycleEvent struct {
	Role string `json:"role" enum:"arrived,left,attempted" doc:"arrived: it moved the Stage to this artifact; left: it moved the Stage off it; attempted: it targeted it and moved nothing"`
	HistoryEntry
}

// The words of a lifecycle's tri-state answers.
const (
	lcYes      = "yes"
	lcNo       = "no"
	lcUnknown  = "unknown"
	lcComplete = "complete"
	lcPartial  = "partial"
)

// KnownDigests are the digests st remembers: published, offered, verified,
// failed and running, distinct, in that order.
func KnownDigests(st StageObject) []string {
	ss := st.Status
	var out []string
	for _, d := range slices.Concat(ss.Published, ss.Offered, ss.Verified, ss.FailedDigests, []string{ss.Pointer}) {
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// LifecycleDigests are the artifacts to read to answer q over stages, each
// with the Stage to read it from: the digest q names (from a Stage that
// knows it, else the first), or every digest the Stages know, at most
// LifecycleDigestLimit, with a note when some were left out.
func LifecycleDigests(q LifecycleQuery, stages []StageObject) (ds []string, from []*StageObject, note string) {
	if len(stages) == 0 {
		return nil, nil, ""
	}
	if q.Digest != "" {
		at := slices.IndexFunc(stages, func(st StageObject) bool { return slices.Contains(KnownDigests(st), q.Digest) })
		return []string{q.Digest}, []*StageObject{&stages[max(at, 0)]}, ""
	}
	for i := range stages {
		for _, d := range KnownDigests(stages[i]) {
			if !slices.Contains(ds, d) {
				ds, from = append(ds, d), append(from, &stages[i])
			}
		}
	}
	if n := len(ds); n > LifecycleDigestLimit {
		note = fmt.Sprintf("; the Stages know %d artifacts and only the first %d were read to find the one asked for", n, LifecycleDigestLimit)
		ds, from = ds[:LifecycleDigestLimit], from[:LifecycleDigestLimit]
	}
	return ds, from, note
}

// ResolveLifecycleDigest is the artifact q names among revs, what the
// service's Stages know. A digest no Stage knows is taken as given.
func ResolveLifecycleDigest(q LifecycleQuery, revs []Revision) (Revision, error) {
	if n := len(slices.DeleteFunc([]string{q.Digest, q.Version, q.Contract}, func(s string) bool { return s == "" })); n != 1 {
		return Revision{}, errors.New("pass exactly one of digest, version or contract")
	}
	if q.Digest != "" {
		if i := slices.IndexFunc(revs, func(r Revision) bool { return r.Digest == q.Digest }); i >= 0 {
			return revs[i], nil
		}
		return Revision{Digest: q.Digest}, nil
	}
	found, at, unread := matchRevisions(q, revs)
	if len(found) == 1 {
		return at, nil
	}
	return Revision{}, unresolved(q, found, unread)
}

// matchRevisions are the digests of revs whose contract has q's version or
// is q's contract, the last of them and how many of revs could not be read.
func matchRevisions(q LifecycleQuery, revs []Revision) (found []string, at Revision, unread int) {
	want := ContractDigest(q.Contract)
	if want == "" {
		want = q.Contract
	}
	for _, r := range revs {
		switch {
		case r.Err != "" && !r.NoContract:
			unread++
		case q.Version != "" && r.Bundle != nil && r.Bundle.Contract != nil && r.Bundle.Contract.Service.Version == q.Version,
			q.Contract != "" && r.ContractRef != "" && ContractDigest(r.ContractRef) == want:
			found, at = append(found, r.Digest), r
		}
	}
	return found, at, unread
}

// unresolved says why q names no artifact, or several.
func unresolved(q LifecycleQuery, found []string, unread int) error {
	what := "version " + q.Version
	msg := fmt.Sprintf("no version %s of %s is known to its Stages", q.Version, q.Service)
	if q.Version == "" {
		what = "contract " + q.Contract
		msg = fmt.Sprintf("no artifact of %s that names contract %s is known to its Stages", q.Service, q.Contract)
	}
	if len(found) > 1 {
		return fmt.Errorf("%s names %d artifacts (%s): pass the digest", what, len(found), strings.Join(found, ", "))
	}
	if unread > 0 {
		msg += fmt.Sprintf("; %d of the artifacts its Stages know could not be read", unread)
	}
	return errors.New(msg)
}

// LifecycleOf is r on stages, the Stages of its service the caller can see,
// each read with its history in hs. hs is matched by namespace and Stage
// name: a Stage whose history was not read is partial and has no events.
// The caller sets Service, Domain, AsOf and what it could not read.
func LifecycleOf(r Revision, stages []StageObject, hs []StageHistory) Lifecycle {
	l := Lifecycle{Listing: Listing{Completeness: lcComplete}, Digest: r.Digest, Contract: r.ContractRef, SourceRevision: r.SourceRevision,
		SignedOn: []string{}, Stages: []LifecycleStage{}}
	if r.Bundle != nil && r.Bundle.Contract != nil {
		l.Version = r.Bundle.Contract.Service.Version
	}
	byStage := map[string]*History{}
	for i := range hs {
		byStage[hs[i].Namespace+"/"+hs[i].Stage.Name] = &hs[i].History
	}
	var sig SignatureEvidence
	var partials, capped, unrecorded []string
	known := false
	for _, st := range stages {
		name := st.Metadata.Namespace + "/" + st.Metadata.Name
		s, total := lifecycleStage(st, byStage[name], r.Digest)
		l.Stages = append(l.Stages, s)
		if s.Completeness != lcComplete {
			l.Completeness, partials = lcPartial, append(partials, name)
		}
		if total > lifecycleEventLimit {
			capped = append(capped, fmt.Sprintf("only the newest %d of %d events are listed on %s", lifecycleEventLimit, total, name))
		}
		if s.Running && s.Completeness == lcComplete && total <= lifecycleEventLimit &&
			!slices.ContainsFunc(s.Events, func(e LifecycleEvent) bool { return e.Role == "arrived" }) {
			unrecorded = append(unrecorded, name)
		}
		if !slices.Contains(KnownDigests(st), r.Digest) && total == 0 {
			continue
		}
		known = true
		l.addEvidence(&sig, st, name)
	}
	l.Signature = sig.Signature()
	l.Note = lifecycleNote(r, known, partials, capped)
	if len(unrecorded) > 0 {
		l.Note += "; no recorded change brought it to " + strings.Join(unrecorded, ", ") + ": it was placed there before the records began or moved outside Pacto"
	}
	return l
}

// addEvidence adds what st, named name, says about l's signature to sig and
// names it on the side it supports.
func (l *Lifecycle) addEvidence(sig *SignatureEvidence, st StageObject, name string) {
	var e SignatureEvidence
	e.Add(st, l.Digest)
	sig.Signed, sig.Unsigned = sig.Signed || e.Signed, sig.Unsigned || e.Unsigned
	switch {
	case e.Signed:
		l.SignedOn = append(l.SignedOn, name)
	case e.Unsigned:
		l.UnsignedOn = append(l.UnsignedOn, name)
	}
}

// lifecycleNote says what a lifecycle of r was read from and what it lacks.
func lifecycleNote(r Revision, known bool, partials, capped []string) string {
	note := "each Stage of the service you can see, from its history and its status"
	if r.Err != "" && !r.NoContract {
		note += "; its artifact could not be read: " + r.Err
	}
	if !known {
		note += "; no Stage you can see knows " + r.Digest
	}
	if len(partials) > 0 {
		note += "; the history of " + strings.Join(partials, ", ") + " is partial or was not read, so a change missing there may still have happened"
	}
	for _, c := range capped {
		note += "; " + c
	}
	return note
}

// lifecycleStage is digest d on st, read with its history h (nil when it was
// not read), and how many events h holds for d before the cap.
func lifecycleStage(st StageObject, h *History, d string) (LifecycleStage, int) {
	ss := st.Status
	s := LifecycleStage{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Environment: st.Spec.Environment, Running: ss.Pointer == d,
		Offered: slices.Contains(ss.Offered, d), Source: SourceStatus, Completeness: lcPartial, Events: []LifecycleEvent{}}
	if ss.LastCheck != nil && ss.LastCheck.Digest == d {
		s.LastCheck = ss.LastCheck
	}
	var verified, failed string
	if h != nil {
		s.Source, s.RecordsVerified = h.Source, h.Verified
		if h.Completeness == lcComplete {
			s.Completeness = lcComplete
		}
		for _, e := range h.Entries {
			if role := lifecycleRole(e, d); role != "" {
				s.Events = append(s.Events, LifecycleEvent{Role: role, HistoryEntry: e})
			}
			verified, failed = historyOutcome(e, d, verified, failed)
		}
	}
	total := len(s.Events)
	s.Events = s.Events[max(total-lifecycleEventLimit, 0):]
	whole := s.Completeness == lcComplete
	s.Verified = triState(slices.Contains(ss.Verified, d), verified, len(ss.Verified) < MaxVerified && whole)
	s.Failed = triState(slices.Contains(ss.FailedDigests, d), failed, len(ss.FailedDigests) < MaxFailedDigests && whole)
	return s, total
}

// triState is yes when the Stage's list holds the digest, else what its
// history says, else no when the list never dropped anything and the history
// is complete, else unknown: a capped list or a partial history is no proof.
func triState(inList bool, history string, provable bool) string {
	switch {
	case inList:
		return lcYes
	case history != "":
		return history
	case provable:
		return lcNo
	}
	return lcUnknown
}

// historyOutcome applies e to what the history says about d so far, by the
// rules ApplyEffects and the records' upstream view apply: a verified
// arrival verifies it, a failure fails it, and a rollback or freeze raised
// on a Fail verdict fails the version it moves from.
func historyOutcome(e HistoryEntry, d, verified, failed string) (string, string) {
	c := e.Cause
	switch {
	case e.To == d && e.Outcome == Failed,
		(e.Type == TypeRollback || e.Type == TypeFreeze) && e.From == d && c != nil && c.Verdict != nil && c.Verdict.Result == Fail:
		return lcNo, lcYes
	case e.To == d && e.Outcome == Succeeded && e.Verification == Verified:
		return lcYes, failed
	}
	return verified, failed
}

// lifecycleRole is what e did to digest d: arrived when it moved the Stage
// to d, left when it moved the Stage off d, attempted when it targeted d and
// moved nothing, "" otherwise.
func lifecycleRole(e HistoryEntry, d string) string {
	p := e.Pointer
	switch {
	case p != nil && p.After == d:
		return "arrived"
	case p != nil && p.Before == d:
		return "left"
	case e.To == d && (p == nil || p.Before == p.After):
		return "attempted"
	}
	return ""
}
