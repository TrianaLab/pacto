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
	s, why := UpstreamFromRecords(recs, nil)
	if why != "" || !slices.Equal(s.Verified, []string{dC}) || !slices.Equal(s.Offered, []string{dA, c1}) || !slices.Equal(s.FailedDigests, []string{dA, c1, c2}) {
		t.Fatalf("got %+v %q", s, why)
	}
	if s, why := UpstreamFromRecords(recs, []string{"m: bad signature"}); s != nil || !strings.Contains(why, "bad signature") {
		t.Fatalf("an unverifiable record offers nothing: %+v %q", s, why)
	}
	if s, why := UpstreamFromRecords(recs[1:], nil); s != nil || !strings.Contains(why, "not whole") {
		t.Fatalf("a stream with a hole offers nothing: %+v %q", s, why)
	}
}
