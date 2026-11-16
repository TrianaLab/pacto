package delivery

import (
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var atT = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// atStage is a Stage of payments in prod.
func atStage() StageObject {
	var st StageObject
	st.Metadata.Namespace, st.Metadata.Name, st.Spec.Environment = "prod", "payments-prod", "production"
	st.Spec.Service.Name, st.Spec.Service.Domain = "payments", "ghcr.io/acme"
	return st
}

// atHistory is a complete, verified records history of es.
func atHistory(es ...HistoryEntry) History {
	return History{Source: SourceRecords, Verified: true, Completeness: "complete", Entries: es}
}

func TestStateAtKnown(t *testing.T) {
	h := atHistory(recordedMove(move("sha256:old", "2026-11-01T00:00:00Z"), "r1"),
		recordedMove(move("sha256:mid", "2026-11-02T00:00:00Z"), "r2"),
		recordedMove(move("sha256:new", "2026-11-03T00:00:00Z"), "r3"))
	a := StateAt(atStage(), h, atT)
	if a.State != AtKnown || a.Digest != "sha256:mid" || a.Record != "r2" || a.Since != "2026-11-02T00:00:00Z" || a.Reason != "" {
		t.Fatalf("the newest signed move before T names what ran: %+v", a)
	}
	if a.Namespace != "prod" || a.Stage != "payments-prod" || a.Service != "payments" || a.Domain != "ghcr.io/acme" || a.Environment != "production" {
		t.Errorf("the Stage's own fields: %+v", a)
	}
}

func TestStateAtPartialHistoryNeverKnown(t *testing.T) {
	for name, h := range map[string]History{
		"partial":  {Source: SourceRecords, Verified: true, Completeness: "partial"},
		"problems": {Source: SourceRecords, Verified: true, Completeness: "complete", Problems: []string{"seq 3 is missing"}},
		"status":   {Source: SourceStatus, Verified: true, Completeness: "complete"},
	} {
		h.Entries = []HistoryEntry{recordedMove(move("sha256:a", "2026-11-01T00:00:00Z"), "r1")}
		if a := StateAt(atStage(), h, atT); a.State != AtPartial || !strings.Contains(a.Reason, "may be missing") {
			t.Errorf("%s: %+v", name, a)
		}
	}
}

func TestStateAtUntimedMoveAfterIsUnknown(t *testing.T) {
	h := atHistory(recordedMove(move("sha256:a", "2026-11-01T00:00:00Z"), "r1"), recordedMove(move("sha256:b", ""), "r2"))
	if a := StateAt(atStage(), h, atT); a.State == AtKnown || !strings.Contains(a.Reason, "no time") {
		t.Errorf("an untimed move after it leaves T in doubt: %+v", a)
	}
	// An untimed move alone names nothing.
	if a := StateAt(atStage(), atHistory(recordedMove(move("sha256:b", ""), "r2")), atT); a.State != AtUnknown {
		t.Errorf("no timed record: %+v", a)
	}
}

func TestStateAtNoRecordBeforeUnknown(t *testing.T) {
	h := atHistory(recordedMove(move("sha256:a", "2026-11-05T00:00:00Z"), "r1"), move("sha256:s", "2026-11-01T00:00:00Z"))
	if a := StateAt(atStage(), h, atT); a.State != AtUnknown || a.Digest != "" || a.Reason != "no record before this time" {
		t.Errorf("only a status move before T: unknown: %+v", a)
	}
}

func TestStateAtFailedMoveIgnored(t *testing.T) {
	failed := recordedMove(move("sha256:bad", "2026-11-02T00:00:00Z"), "r2")
	failed.Outcome = Failed
	h := atHistory(recordedMove(move("sha256:a", "2026-11-01T00:00:00Z"), "r1"), failed, HistoryEntry{Record: "r3", Kind: RecordOperation})
	if a := StateAt(atStage(), h, atT); a.State != AtKnown || a.Digest != "sha256:a" {
		t.Errorf("a failed move and a line that moved nothing are not moves: %+v", a)
	}
}

func TestStateAtStatusMoveAfterSignedIsPartial(t *testing.T) {
	unrecordable := move("sha256:u", "2026-11-02T00:00:00Z")
	unrecordable.Record, unrecordable.Kind = "r2", RecordUnrecordable
	for name, later := range map[string]HistoryEntry{"status": move("sha256:s", "2026-11-02T00:00:00Z"), "unrecordable": unrecordable} {
		h := atHistory(recordedMove(move("sha256:a", "2026-11-01T00:00:00Z"), "r1"), later)
		if a := StateAt(atStage(), h, atT); a.State != AtPartial || a.Digest != "sha256:a" || !strings.Contains(a.Reason, "not signed") {
			t.Errorf("%s: %+v", name, a)
		}
	}
	// After T it does not matter.
	h := atHistory(recordedMove(move("sha256:a", "2026-11-01T00:00:00Z"), "r1"), move("sha256:s", "2026-11-04T00:00:00Z"))
	if a := StateAt(atStage(), h, atT); a.State != AtKnown {
		t.Errorf("a status move after T: %+v", a)
	}
}

func TestStateAtUnverifiedNeverKnown(t *testing.T) {
	h := atHistory(recordedMove(move("sha256:a", "2026-11-01T00:00:00Z"), "r1"))
	h.Verified = false
	if a := StateAt(atStage(), h, atT); a.State != AtPartial || a.Digest != "sha256:a" || !strings.Contains(a.Reason, "signature not checked") {
		t.Errorf("an unverified history is never known: %+v", a)
	}
}

func TestStateAtStageFromHistory(t *testing.T) {
	h := atHistory()
	h.Stage = StageNow{Name: "payments-prod", Environment: "production"}
	a := StateAt(StageObject{Metadata: ObjectMeta{Namespace: "prod"}}, h, atT)
	if a.Stage != "payments-prod" || a.Environment != "production" || a.State != AtUnknown {
		t.Errorf("an unread Stage is named from its history: %+v", a)
	}
	if u := UnreadAt(atStage(), "forbidden"); u.State != AtUnknown || u.Reason != "forbidden" || u.Stage != "payments-prod" {
		t.Errorf("unread: %+v", u)
	}
}

func TestAtCompletePartialWhenUnreadable(t *testing.T) {
	known := AtAnswer{Stages: []StageAt{{State: AtKnown}, {State: AtKnown}}}
	known.Complete(true, false, nil)
	if known.Completeness != "complete" || known.Note != "2 Stages read, 2 of them known at this time" {
		t.Errorf("every Stage known: %+v", known)
	}
	for name, c := range map[string]struct {
		a          AtAnswer
		every, pgs bool
		unread     []string
	}{
		"unreadable": {AtAnswer{Stages: []StageAt{{State: AtKnown}}}, true, false, []string{"team-b: forbidden"}},
		"guess":      {AtAnswer{Stages: []StageAt{{State: AtKnown}}}, false, false, nil},
		"pages":      {AtAnswer{Stages: []StageAt{{State: AtKnown}}}, true, true, nil},
	} {
		c.a.Complete(c.every, c.pgs, c.unread)
		if c.a.Completeness != "partial" || !strings.HasSuffix(c.a.Note, "; others were not read") {
			t.Errorf("%s: %+v", name, c.a)
		}
	}
	unknown := AtAnswer{Stages: []StageAt{{State: AtKnown}, {State: AtUnknown}}}
	unknown.Complete(true, false, nil)
	if unknown.Completeness != "partial" || strings.Contains(unknown.Note, "others") {
		t.Errorf("an unknown Stage: %+v", unknown)
	}
}

func TestAtDescribe(t *testing.T) {
	digest := "sha256:" + strings.Repeat("c", 64)
	ref := "ghcr.io/acme/payments@" + digest
	a := AtAnswer{Stages: []StageAt{{Service: "payments", Digest: "sha256:a"}, {Service: "orders", Digest: "sha256:b"}, {Service: "payments"},
		{Service: "payments", Digest: "sha256:unread"}}}
	a.Describe(map[string]Described{
		"sha256:a": {Version: "2.1.0", Team: "Payments", OwnerKey: "team/payments", BusinessDomain: "money", Contract: ref, Service: "payments"},
		"sha256:b": {Version: "1.0.0", Contract: ref, Service: "payments"},
	})
	s := a.Stages[0]
	if s.Version != "2.1.0" || s.Owner != "Payments" || s.OwnerKey != "team/payments" || s.BusinessDomain != "money" ||
		s.Revision != string(fleet.NewRevisionKey(fleet.NewServiceKeyDomain("ghcr.io/acme", "payments"), digest)) {
		t.Errorf("described: %+v", s)
	}
	if a.Stages[1].Version != "1.0.0" || a.Stages[1].Revision != "" {
		t.Errorf("another service's contract gets no key: %+v", a.Stages[1])
	}
	if a.Stages[2] != (StageAt{Service: "payments"}) || a.Stages[3].Version != "" {
		t.Errorf("nothing to describe: %+v", a.Stages[2:])
	}
}
