package delivery

import (
	"errors"
	"strings"
	"testing"
)

const (
	ansA = "sha256:aaaa"
	ansB = "sha256:bbbb"
	ansC = "sha256:cccc"
)

func ansStage() *StageObject {
	return &StageObject{Metadata: ObjectMeta{Name: "payments-prod", Namespace: "prod", UID: "s1"},
		Status: StageStatus{Pointer: ansB, LastOp: "u2", Stream: Stream{Seq: 4, RecordedSeq: 1,
			Concluded: []Concluded{{UID: "gone", Seq: 3}}}}}
}

func ansOp(name, uid string, seq int64, before, after string) OperationObject {
	o := OperationObject{Metadata: ObjectMeta{Name: name, Namespace: "prod", UID: uid, CreationTimestamp: name}}
	o.Spec = OperationSpec{Type: TypePromote, Stage: "payments-prod", Params: Params{From: before, To: after}, Actor: Actor{User: "bot"},
		Reason: "because"}
	o.Status = OperationStatus{Seq: seq, Pointer: PointerChange{Before: before, After: after}}
	if seq > 0 {
		o.Status.Phase = PhaseConcluded
	}
	return o
}

func ansRecord(id, uid, name string, seq int64, after string, edit func(*RecordPredicate)) StoredRecord {
	p := RecordPredicate{Kind: RecordOperation, Intent: RecordIntent{UID: uid, Name: name, Reason: "recorded reason"},
		Chain: Chain{Seq: seq}, Actor: &RecordActor{User: "bot", OnBehalfOf: &OnBehalfOf{User: "dev"}},
		Approvals: []RecordApproval{{By: "carol"}}, Outcome: &RecordOutcome{Pointer: PointerChange{After: after}}}
	if edit != nil {
		edit(&p)
	}
	return StoredRecord{ID: id, Statement: Statement{Predicate: p}}
}

func TestAssembleHistoryFromStatus(t *testing.T) {
	st := ansStage()
	other := ansOp("x", "ux", 1, "", ansA)
	other.Spec.Stage = "orders-prod"
	inflight := ansOp("c", "u3", 0, ansB, ansC)
	first := ansOp("a", "u1", 1, "", ansA)
	first.Status.Record = &RecordRef{Digest: "sha256:rec"}
	first.Status.Approvals = []ApprovalRecord{{Name: "ap", By: "carol", Decision: Approve, AuthTime: "1", Note: "ok"}}
	h, ops := AssembleHistory(st, []OperationObject{inflight, other, first}, nil, errors.New("registry down"), "now")
	if len(ops) != 2 || ops[0].Metadata.Name != "a" || h.Source != SourceStatus || h.Completeness != "partial" ||
		h.Unreadable[0] != "registry down" || !strings.HasPrefix(h.Note, "the records could not be read") {
		t.Fatalf("history = %+v ops %v", h, ops)
	}
	if e := h.Entries[0]; e.Pointer == nil || e.Pointer.After != ansA || e.UnverifiedRecord != "sha256:rec" ||
		len(e.Approvals) != 1 || e.Approvals[0] != (RecordApproval{By: "carol", Decision: Approve, Operation: "u1", AuthTime: "1", Note: "ok"}) {
		t.Fatalf("status entry = %+v", e)
	}
}

func TestAssembleHistoryFromRecords(t *testing.T) {
	st := ansStage()
	unrec := ansOp("b", "u2", 2, ansA, ansB)
	leftover := ansOp("old", "u9", 4, "", ansA)
	leftover.Status.StageUID = "earlier"
	recs := []StoredRecord{ansRecord("r1", "u1", "a", 1, ansA, nil),
		ansRecord("r2", "u2", "b", 2, "", func(p *RecordPredicate) {
			p.Kind, p.Outcome, p.Unrecordable = RecordUnrecordable, nil, &Refusal{Check: CheckSchema}
		}),
		ansRecord("r3", "u7", "q", 3, "", func(p *RecordPredicate) { p.Kind, p.Outcome = RecordUnrecordable, nil })}
	for _, c := range []struct {
		r        RecordsRead
		complete string
		note     string
	}{
		{RecordsRead{Trust: "sha256:t"}, "partial", "show problems"}, // MissingSeqs adds problems
		{RecordsRead{}, "partial", "no trust config"},
	} {
		c.r.Fold = Fold{Records: recs, Head: "r3"}
		h, _ := AssembleHistory(st, []OperationObject{unrec, leftover}, &c.r, nil, "now")
		if h.Source != SourceRecords || h.Head != "r3" || h.Completeness != c.complete || !strings.Contains(h.Note, c.note) ||
			h.Verified != (c.r.Trust != "") || len(h.Entries) != 4 {
			t.Fatalf("history = %+v", h)
		}
		if strings.Join(h.Problems, "|") != "local seq 3 concluded, but neither its record nor its Operation can be read|"+
			"local seq 4 concluded, but neither its record nor its Operation can be read" {
			t.Fatalf("problems = %v", h.Problems)
		}
		if e := h.Entries[1]; e.Pointer == nil || e.Pointer.After != ansB || !strings.HasPrefix(e.Reason, "Unrecordable") {
			t.Fatalf("unsigned move = %+v", e)
		}
		if e := h.Entries[2]; e.Pointer != nil {
			t.Fatalf("an unrecordable record with its operation gone moves nothing known: %+v", e)
		}
	}
	clean := &RecordsRead{Trust: "sha256:t", Fold: Fold{Records: recs[:1]}}
	st.Status.Stream.Seq = 1
	if h, _ := AssembleHistory(st, nil, clean, nil, "now"); h.Completeness != "complete" || !strings.Contains(h.Note, "verified with trust config sha256:t") {
		t.Fatalf("clean = %+v", h)
	}
}

func TestBlameOf(t *testing.T) {
	st := ansStage()
	unrec := ansOp("b", "u2", 2, ansA, ansB)
	unrec.Spec.OnBehalfOf = &Principal{User: "dev"}
	unrec.Status.Approvals = []ApprovalRecord{{By: "carol"}}
	signed := ansRecord("r1", "u1", "a", 1, ansA, nil)
	recs := []StoredRecord{signed, ansRecord("r2", "u2", "b", 2, "", func(p *RecordPredicate) {
		p.Kind, p.Outcome, p.Unrecordable = RecordUnrecordable, nil, &Refusal{Check: CheckSchema}
	})}
	h, ops := AssembleHistory(st, []OperationObject{unrec}, &RecordsRead{Trust: "t", Fold: Fold{Records: recs}}, nil, "now")
	b := BlameOf(h, recs, ops, "")
	if b.Digest != ansB || b.Change == nil || b.Change.Operation != "b" || b.Completeness != "partial" || len(b.Unsigned) != 5 ||
		b.OnBehalfOf != "dev" || b.Approvals[0].By != "carol" || !strings.Contains(b.Note, "its record could not vouch for it") {
		t.Fatalf("unrecordable blame = %+v", b)
	}
	if b := BlameOf(h, recs, ops, ansA); b.Change == nil || b.OnBehalfOf != "dev" || b.IntentReason != "recorded reason" || len(b.Unsigned) != 0 {
		t.Fatalf("signed blame = %+v", b)
	}
	// From status only, with no record at all.
	h, ops = AssembleHistory(st, []OperationObject{unrec}, nil, nil, "now")
	if b := BlameOf(h, nil, ops, ""); b.Change == nil || strings.Contains(b.Note, "could not vouch") {
		t.Fatalf("status blame = %+v", b)
	}
}

// TestBlameOfNoChange: with no change to the digest, the answer says how sure
// that is; a partial history means unknown, never nobody.
func TestBlameOfNoChange(t *testing.T) {
	empty := &History{}
	if b := BlameOf(empty, nil, nil, ""); b.Note != "the Stage runs nothing yet" {
		t.Fatalf("empty = %+v", b)
	}
	for _, c := range []struct {
		h    *History
		want string
	}{
		{&History{Completeness: "complete", Entries: []HistoryEntry{{Kind: RecordUnrecordable}}}, "hides what its change moved"},
		{&History{Completeness: "complete"}, "no change in the stream moved"},
		{&History{Completeness: "partial"}, "unknown, not nobody"},
	} {
		b := BlameOf(c.h, nil, nil, ansC)
		wantComplete := c.want == "no change in the stream moved"
		if b.Change != nil || (b.Completeness == "complete") != wantComplete || !strings.Contains(b.Note, c.want) {
			t.Errorf("no change: %+v, want %q", b, c.want)
		}
	}
}

func TestUndoOf(t *testing.T) {
	st := ansStage()
	runs := ansOp("b", "u2", 2, ansA, ansB)
	for _, c := range []struct {
		name string
		op   OperationObject
		gone bool
		st   StageStatus
		kind string
		note string
	}{
		{"moved nothing", ansOp("n", "un", 1, "", ""), false, st.Status, "none", "moved nothing"},
		{"rollback", runs, false, st.Status, "rollback", "a rollback bound to it"},
		{"first version", ansOp("a", "u1", 1, "", ansA), false, st.Status, "none", "first version"},
		{"gone but runs", runs, true, st.Status, "promote", "its Operation is gone"},
		{"already back", ansOp("a2", "u5", 1, ansB, ansC), false, st.Status, "none", "already runs"},
		{"changed since", ansOp("a3", "u6", 1, ansA, ansC), false, st.Status, "promote", "changed since (it runs " + ansB + ")"},
	} {
		s := *st
		s.Status = c.st
		u, req := UndoOf(&c.op, c.gone, &s)
		if u.Kind != c.kind || !strings.Contains(u.Note, c.note) || (req == nil) != (c.kind == "none") || u.Operation != "prod/"+c.op.Metadata.Name {
			t.Errorf("%s: %+v %+v", c.name, u, req)
		}
	}
	u, req := UndoOf(&runs, false, st)
	u.Planned(*req, Plan{IntentDigest: "sha256:i"})
	if u.Plan == nil || u.Command != `pacto delivery propose -n prod --stage payments-prod --type rollback --undoes b --reason "undo b" --intent-digest sha256:i` {
		t.Fatalf("rollback command = %q", u.Command)
	}
	u, req = UndoOf(&runs, true, st)
	u.Planned(*req, Plan{IntentDigest: "sha256:i"})
	if !strings.Contains(u.Command, "--type promote --to "+ansA) {
		t.Fatalf("promote command = %q", u.Command)
	}
}

func TestExplainOperation(t *testing.T) {
	op := ansOp("p1", "u1", 0, ansA, ansB)
	op.Status.Phase, op.Status.Question = PhaseHeld, "Approve?"
	op.Status.Plan = &Plan{Order: &Order{}}
	op.Status.Verdicts = []Verdict{{Gate: GateDiff}}
	e := ExplainOperation(&op, nil)
	if e.Operation != "prod/p1" || e.Stage.Name != "payments-prod" || e.Order == nil || len(e.Verdicts) != 1 ||
		e.Summary != "delivery.pacto.trianalab.io/promote on Stage payments-prod, "+ansA+" -> "+ansB+" waits for a person: Approve?" {
		t.Fatalf("explanation = %+v", e)
	}
	if e := ExplainOperation(&op, ansStage()); e.Stage.Pointer != ansB {
		t.Fatalf("stage = %+v", e.Stage)
	}
	for _, c := range []struct {
		status OperationStatus
		want   string
	}{
		{OperationStatus{Phase: PhaseConcluded, Outcome: Succeeded, Verification: Verified, Reason: "Verified", Message: "ok"}, "concluded Succeeded/Verified: Verified: ok"},
		{OperationStatus{Phase: PhaseConcluded, Outcome: Rejected, Reason: "R", Message: "m"}, "concluded Rejected: R: m"},
		{OperationStatus{Phase: PhaseConcluded, Outcome: Failed, Verification: Unverified}, "concluded Failed/Unverified"},
		{OperationStatus{Phase: PhaseConcluded, Outcome: Failed, Message: "m"}, "concluded Failed: m"},
		{OperationStatus{}, "(none) -> " + ansB + " is not admitted yet"},
		{OperationStatus{Phase: PhaseRunning}, "is Running: in progress"},
		{OperationStatus{Phase: PhaseVerifying, Message: "waiting"}, "is Verifying: waiting"},
	} {
		o := ansOp("p", "u", 0, "", ansB)
		o.Status = c.status
		if got := Summarize(&o); !strings.HasSuffix(got, c.want) {
			t.Errorf("summary %q, want suffix %q", got, c.want)
		}
	}
	for v, want := range map[*Verdict]string{
		{Gate: GateImpact}: "pacto delivery plan",
		{Gate: "h", Citation: map[string]string{"query": "q", "provider": "mira"}}: "/api/v1/query of the mira",
		{Gate: GateContract}: "read contractStatus",
		{Gate: "x", Citation: map[string]string{"provider": GateRuntime}}: "read contractStatus",
		{Gate: "x", Citation: map[string]string{"provider": "http"}}:      "to the http provider",
		{Gate: "x"}: "",
	} {
		if got := RerunHint(*v); !strings.Contains(got, want) || (want == "") != (got == "") {
			t.Errorf("rerun %+v = %q, want %q", v, got, want)
		}
	}
}
