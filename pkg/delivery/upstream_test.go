package delivery

import (
	"slices"
	"strings"
	"testing"
)

func upstreamRecord(seq int64, parent, typ string, to string, out Outcome, ver Verification, published bool) StoredRecord {
	p := RecordPredicate{Kind: RecordOperation, Type: &RecordType{ID: typ}, Intent: RecordIntent{Params: &Params{To: to}},
		Outcome: &RecordOutcome{Result: out, Verification: ver}, Chain: Chain{Stream: "c2/test/payments", Seq: seq, Parent: parent}}
	if published {
		p.Verdicts = []Verdict{{Gate: GatePublished, Result: Pass}}
	}
	return StoredRecord{ID: "r" + string(rune('0'+seq)), Statement: Statement{Predicate: p}}
}

func TestUpstreamFromRecords(t *testing.T) {
	recs := []StoredRecord{
		upstreamRecord(1, "", TypePromote, dA, Succeeded, Verified, true),
		upstreamRecord(2, "r1", TypePromote, dB, Succeeded, AcceptedUnknown, true), // never verified
		upstreamRecord(3, "r2", TypePromote, dC, Succeeded, Verified, false),       // verified, never offered
		upstreamRecord(4, "r3", TypePromote, dA, Failed, "", true),                 // failed later: leaves Verified
		upstreamRecord(5, "r4", TypeRollback, dC, Succeeded, Verified, true),       // verified, but only a promote offers
		upstreamRecord(6, "r5", TypePromote, c1, Succeeded, Verified, true),
		upstreamRecord(7, "r6", TypeRollback, dC, Succeeded, Verified, false), // a re-check failed c1: its rollback leaves it
		upstreamRecord(8, "r7", TypeOverride, c2, Failed, "", false),          // a Failed override fails its version too
		upstreamRecord(9, "r8", TypeUnfreeze, "", Succeeded, Verified, false), // moves no version
	}
	recs[6].Statement.Predicate.Intent.Params.From = c1
	recs[6].Statement.Predicate.Intent.Cause = &Cause{Verdict: &Verdict{Gate: "smoke", Result: Fail}}
	recs = append(recs, StoredRecord{ID: "r6", Statement: Statement{Predicate: RecordPredicate{Kind: RecordOperation,
		Chain: Chain{Stream: "c2/test/payments", Seq: 10, Parent: "r9"}}}}) // no outcome
	s, why := UpstreamFromRecords(recs)
	if why != "" || !slices.Equal(s.Verified, []string{dC}) || !slices.Equal(s.Offered, []string{dA, c1}) || !slices.Equal(s.FailedDigests, []string{dA, c1, c2}) {
		t.Fatalf("got %+v %q", s, why)
	}
	if s, why := UpstreamFromRecords(recs[1:]); s != nil || !strings.Contains(why, "not whole") {
		t.Fatalf("a stream with a hole offers nothing: %+v %q", s, why)
	}
}

// A re-check that fails and cannot roll back freezes the Stage through a
// freeze record naming the failed version: downstream sees it failed. A
// person's freeze with no failed check behind it fails nothing.
func TestUpstreamFoldsAFailedFreeze(t *testing.T) {
	recs := []StoredRecord{
		upstreamRecord(1, "", TypePromote, dA, Succeeded, Verified, true),
		upstreamRecord(2, "r1", TypePromote, dB, Succeeded, Verified, true),
		upstreamRecord(3, "r2", TypeFreeze, "", Succeeded, Unverified, false),
		upstreamRecord(4, "r3", TypeFreeze, "", Succeeded, Unverified, false),
	}
	recs[2].Statement.Predicate.Intent.Params.From = dA // a person paused on dA, no failed check
	recs[3].Statement.Predicate.Intent.Params.From = dB
	recs[3].Statement.Predicate.Intent.Cause = &Cause{Verdict: &Verdict{Gate: "smoke", Result: Fail}}
	s, why := UpstreamFromRecords(recs)
	if why != "" || !slices.Equal(s.Verified, []string{dA}) || !slices.Equal(s.FailedDigests, []string{dB}) {
		t.Fatalf("got %+v %q", s, why)
	}
	// The record the recorder writes for the promoter's freeze folds the same way.
	fz := OperationObject{Spec: OperationSpec{Type: TypeFreeze, Facet: FacetFreeze, Params: Params{From: dA},
		Cause: &Cause{Operation: "uid-p1", Verdict: &Verdict{Gate: "smoke", Result: Fail}}},
		Status: OperationStatus{Outcome: Succeeded, Verification: Unverified}}
	rec := NewOperationRecord(RecordInput{Operation: fz, Chain: Chain{Stream: "c2/test/payments", Seq: 5, Parent: "r4"}})
	s, why = UpstreamFromRecords(append(recs, StoredRecord{ID: "r5", Statement: rec}))
	if why != "" || len(s.Verified) != 0 || !slices.Equal(s.FailedDigests, []string{dB, dA}) {
		t.Fatalf("a recorded freeze: %+v %q", s, why)
	}
}
