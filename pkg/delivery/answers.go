package delivery

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// The answers below (history, blame and undo) are pure: the CLI, MCP and the
// dashboard read the cluster and the registry, and the browser demo reads
// fixed data, then every one of them assembles the same answer here.

// historyNote is what every history answer says about where it came from.
const historyNote = "read from the Operation objects that still exist; an operation deleted from the cluster is missing " +
	"until signed records arrive"

// RecordsRead is what a reader got from a Stage's stream: the records folded,
// the referrers it could not verify and the digest of the trust config it
// verified them with, empty when it checked no signature.
type RecordsRead struct {
	Fold       Fold
	Unreadable []string
	Trust      string
}

// complete is the completeness of an answer read from records.
func (r *RecordsRead) complete() string {
	if r.Trust == "" || len(r.Fold.Problems) > 0 || len(r.Unreadable) > 0 {
		return "partial"
	}
	return "complete"
}

func (r *RecordsRead) note() string {
	switch {
	case r.Trust == "":
		return "read from the records in the registry, but no trust config was given, so no signature was checked; " +
			"pass --trust to verify them"
	case len(r.Fold.Problems) > 0 || len(r.Unreadable) > 0:
		return "read from the signed records in the registry, which show problems or hold records that could not be verified; " +
			"a change missing here may still have happened"
	}
	return "read from the signed records in the registry, verified with trust config " + r.Trust +
		", plus the operations not recorded yet"
}

// inFlight sorts operations without a stream seq (not concluded) last.
func inFlight(o OperationObject) int {
	if o.Status.Seq == 0 {
		return 1
	}
	return 0
}

// AssembleHistory is what happened on st: from its records when r holds them,
// plus the operations not recorded yet; otherwise from the operations still
// in the cluster, concluded ones in stream order, then the ones in flight. ops
// may hold other Stages' operations; they are left out. readErr is why the
// records could not be read. It returns, with the history, st's operations in
// that order.
func AssembleHistory(st *StageObject, ops []OperationObject, r *RecordsRead, readErr error, asOf string) (*History, []OperationObject) {
	ops = slices.DeleteFunc(slices.Clone(ops), func(o OperationObject) bool { return o.Spec.Stage != st.Metadata.Name })
	slices.SortStableFunc(ops, func(a, b OperationObject) int {
		return cmp.Or(cmp.Compare(inFlight(a), inFlight(b)), cmp.Compare(a.Status.Seq, b.Status.Seq),
			cmp.Compare(a.Metadata.CreationTimestamp, b.Metadata.CreationTimestamp))
	})
	h := &History{Stage: NowOf(st), AsOf: asOf, Completeness: "partial", Note: historyNote, Source: SourceStatus, Entries: []HistoryEntry{}}
	if readErr != nil {
		h.Unreadable, h.Note = []string{readErr.Error()}, "the records could not be read, so this is "+historyNote
	}
	recorded := map[string]bool{}
	// Without records the Operations are all the history there is, so one
	// deleted after it concluded is a gap to name, never a quiet omission.
	h.Problems = MissingSeqs(st, ops)
	if r != nil {
		r.Fold.Problems = append(r.Fold.Problems, h.Problems...)
		h.Source, h.Head, h.Problems, h.Unreadable = SourceRecords, r.Fold.Head, r.Fold.Problems, r.Unreadable
		h.Completeness, h.Note, h.Verified = r.complete(), r.note(), r.Trust != ""
		for _, rec := range r.Fold.Records {
			uid := rec.Statement.Predicate.Intent.UID
			recorded[uid] = true
			h.Entries = append(h.Entries, WithUnsignedMove(RecordEntry(rec), ops, uid))
		}
	}
	for _, o := range ops {
		if !recorded[o.Metadata.UID] {
			h.Entries = append(h.Entries, StatusEntry(o))
		}
	}
	return h, ops
}

// StatusEntry is an Operation's status as a history line.
func StatusEntry(o OperationObject) HistoryEntry {
	s := o.Status
	e := HistoryEntry{Seq: s.Seq, Operation: o.Metadata.Name, Type: o.Spec.Type, From: o.Spec.Params.From,
		To: o.Spec.Params.To, Actor: o.Spec.Actor.User, ActorClass: s.ActorClass, Phase: s.Phase, Outcome: s.Outcome,
		Verification: s.Verification, Reason: s.Reason, Finished: s.Times.Finished}
	if e.Finished == "" {
		e.Since = cmp.Or(s.Times.Held, s.Times.Admitted, o.Metadata.CreationTimestamp)
	}
	if s.Pointer.After != "" {
		ptr := s.Pointer
		e.Pointer = &ptr
	}
	if s.Record != nil {
		e.UnverifiedRecord = s.Record.Digest
	}
	for _, a := range s.Approvals {
		e.Approvals = append(e.Approvals, RecordApproval{By: a.By, Decision: a.Decision, Operation: o.Metadata.UID,
			AuthTime: a.AuthTime, Note: a.Note})
	}
	return e
}

// MissingSeqs names the local seqs the Stage concluded after the recorder's
// last that neither a listed Operation nor a conclusion still being written
// holds: their record is not written yet and their Operation is gone, so what
// they did cannot be read at all. An Operation an earlier Stage of the same
// name numbered holds nothing here.
func MissingSeqs(st *StageObject, ops []OperationObject) []string {
	held, live := map[int64]bool{}, map[string]bool{}
	for _, o := range ops {
		if o.Status.StageUID == "" || o.Status.StageUID == st.Metadata.UID {
			live[o.Metadata.UID], held[o.Status.Seq] = true, true
		}
	}
	for _, c := range st.Status.Stream.Concluded {
		held[c.Seq] = held[c.Seq] || live[c.UID]
	}
	var out []string
	for seq := st.Status.Stream.RecordedSeq + 1; seq <= st.Status.Stream.Seq; seq++ {
		if !held[seq] {
			out = append(out, fmt.Sprintf("local seq %d concluded, but neither its record nor its Operation can be read", seq))
		}
	}
	return out
}

// WithUnsignedMove fills an unrecordable record's line, which carries no
// params or outcome, from its Operation's status while the Operation is still
// in the cluster. Its reason still says Unrecordable: what it adds is not signed.
func WithUnsignedMove(e HistoryEntry, ops []OperationObject, uid string) HistoryEntry {
	i := slices.IndexFunc(ops, func(o OperationObject) bool { return o.Metadata.UID == uid })
	if e.Kind != RecordUnrecordable || i < 0 {
		return e
	}
	s := StatusEntry(ops[i])
	e.Type, e.From, e.To, e.Actor, e.ActorClass = s.Type, s.From, s.To, s.Actor, s.ActorClass
	e.Outcome, e.Verification, e.Pointer = s.Outcome, s.Verification, s.Pointer
	return e
}

// BlameOf says who put digest in place on h's Stage, by default the version it
// runs now: the newest change in h that moved the Stage to it. recs are the
// records h was read from and ops the Operations it was read from.
func BlameOf(h *History, recs []StoredRecord, ops []OperationObject, digest string) *Blame {
	b := &Blame{Stage: h.Stage, Digest: cmp.Or(digest, h.Stage.Pointer), AsOf: h.AsOf, Completeness: h.Completeness,
		Source: h.Source, Unreadable: h.Unreadable}
	if b.Digest == "" {
		b.Note = "the Stage runs nothing yet"
		return b
	}
	for i := len(h.Entries) - 1; i >= 0; i-- {
		e := h.Entries[i]
		if e.Pointer == nil || e.Pointer.After != b.Digest {
			continue
		}
		b.Change = &e
		b.Note = "the newest change that moved the Stage to " + b.Digest
		if e.Record != "" && e.Kind != RecordUnrecordable {
			p := recs[slices.IndexFunc(recs, func(x StoredRecord) bool { return x.ID == e.Record })].Statement.Predicate
			b.Approvals, b.Verdicts, b.IntentReason = p.Approvals, p.Verdicts, p.Intent.Reason
			if p.Actor != nil && p.Actor.OnBehalfOf != nil {
				b.OnBehalfOf = p.Actor.OnBehalfOf.User
			}
			return b
		}
		// Read from the Operation's status: no signed record vouches for it,
		// whatever the history's completeness.
		b.Completeness, b.Unsigned = "partial", []string{"change", "approvals", "verdicts", "intentReason", "onBehalfOf"}
		b.Note += ", read from its Operation's unsigned status"
		if e.Record != "" {
			b.Note += ": its record could not vouch for it (" + e.Reason + ")"
		}
		o := ops[slices.IndexFunc(ops, func(o OperationObject) bool { return o.Metadata.Name == e.Operation })]
		b.Verdicts, b.IntentReason, b.Approvals = o.Status.Verdicts, o.Spec.Reason, StatusEntry(o).Approvals
		if o.Spec.OnBehalfOf != nil {
			b.OnBehalfOf = o.Spec.OnBehalfOf.User
		}
		return b
	}
	noChange(b, h.Entries)
	return b
}

// noChange says why no change in entries moved the Stage to b's digest, and
// how sure that is. An unrecordable record whose move is unknown may be the
// change, so it makes the answer partial.
func noChange(b *Blame, entries []HistoryEntry) {
	hidden := slices.ContainsFunc(entries, func(e HistoryEntry) bool {
		return e.Kind == RecordUnrecordable && e.Pointer == nil
	})
	switch {
	case b.Completeness == "complete" && hidden:
		b.Completeness = "partial"
		b.Note = "no readable change moved the Stage to " + b.Digest + ", but an unrecordable record hides what its " +
			"change moved, and that change may have"
	case b.Completeness == "complete":
		b.Note = "no change in the stream moved the Stage to " + b.Digest
	default:
		b.Note = "no change Pacto could read moved the Stage to " + b.Digest + ", and the history is partial, " +
			"so who did it is unknown, not nobody"
	}
}

// UndoOf decides how to undo op on st, as a plan nobody proposes: the request
// to plan, or nil when there is nothing to undo. gone says op is no longer in
// the cluster (it was rebuilt from its record), so a rollback cannot name it.
// st may be nil when op moved nothing.
func UndoOf(op *OperationObject, gone bool, st *StageObject) (*Undo, *Request) {
	ns, name := op.Metadata.Namespace, op.Metadata.Name
	u := &Undo{Operation: ns + "/" + name, Kind: "none"}
	ptr := op.Status.Pointer
	if ptr.After == "" {
		u.Note = "it moved nothing, so there is nothing to undo"
		return u, nil
	}
	req := &Request{Namespace: ns, Stage: op.Spec.Stage, Reason: "undo " + name}
	runs := st.Status.LastOp == op.Metadata.UID && st.Status.Pointer == ptr.After
	switch {
	case !gone && runs:
		u.Kind, req.Type, req.Undoes = "rollback", TypeRollback, name
		u.Note = "it is the change the Stage runs now, so a rollback bound to it undoes it"
	case ptr.Before == "":
		u.Note = "it brought the Stage its first version; there is no earlier version to go back to"
		return u, nil
	case runs:
		u.Kind, req.Type, req.To = "promote", TypePromote, ptr.Before
		u.Note = "the Stage still runs this change, but its Operation is gone, so a rollback cannot name it; going back to " +
			ptr.Before + " is a promote, and every gate runs"
	case st.Status.Pointer == ptr.Before:
		u.Note = "the Stage already runs " + ptr.Before + ", the version it replaced"
		return u, nil
	default:
		u.Kind, req.Type, req.To = "promote", TypePromote, ptr.Before
		u.Note = "the Stage changed since (it runs " + orNone(st.Status.Pointer) + "), and only the change it runs now can be " +
			"rolled back, so going back to " + ptr.Before + " is a promote, and every gate runs"
	}
	return u, req
}

// Planned completes u with the plan of its request and the command that
// proposes it.
func (u *Undo) Planned(req Request, plan Plan) {
	u.Plan = &plan
	u.Command = fmt.Sprintf("pacto delivery propose -n %s --stage %s --type %s", req.Namespace, req.Stage, strings.TrimPrefix(req.Type, "delivery.pacto.trianalab.io/"))
	if req.Undoes != "" {
		u.Command += " --undoes " + req.Undoes
	} else {
		u.Command += " --to " + req.To
	}
	u.Command += fmt.Sprintf(" --reason %q --intent-digest %s", req.Reason, plan.IntentDigest)
}
