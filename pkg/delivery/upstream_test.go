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
	s, why := UpstreamFromRecords(recs, RecordMark{})
	if why != "" || !slices.Equal(s.Verified, []string{dC}) || !slices.Equal(s.Offered, []string{dA, c1}) || !slices.Equal(s.FailedDigests, []string{dA, c1, c2}) {
		t.Fatalf("got %+v %q", s, why)
	}
	if st := s.Stream; st.ID != "c2/test/payments" || st.Seq != 10 || st.Head != "r6" {
		t.Fatalf("the view names its newest record: %+v", s.Stream)
	}
	if s, why := UpstreamFromRecords(recs[1:], RecordMark{}); s != nil || !strings.Contains(why, "not whole") {
		t.Fatalf("a stream with a hole offers nothing: %+v %q", s, why)
	}
	if s, why := UpstreamFromRecords(recs, RecordMark{Stream: "c2/test/payments", Seq: 3, Record: "x3"}); s != nil || !strings.Contains(why, "rewritten") {
		t.Fatalf("a stream that no longer holds the record the Stage read offers nothing: %+v %q", s, why)
	}
	if s, why := UpstreamFromRecords(recs, RecordMark{Stream: "c2/test/payments", Seq: 3, Record: "r3"}); s == nil || why != "" {
		t.Fatalf("a stream that holds it reads: %+v %q", s, why)
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
	s, why := UpstreamFromRecords(recs, RecordMark{})
	if why != "" || !slices.Equal(s.Verified, []string{dA}) || !slices.Equal(s.FailedDigests, []string{dB}) {
		t.Fatalf("got %+v %q", s, why)
	}
	// The record the recorder writes for the promoter's freeze folds the same way.
	fz := OperationObject{Spec: OperationSpec{Type: TypeFreeze, Facet: FacetFreeze, Params: Params{From: dA},
		Cause: &Cause{Operation: "uid-p1", Verdict: &Verdict{Gate: "smoke", Result: Fail}}},
		Status: OperationStatus{Outcome: Succeeded, Verification: Unverified}}
	rec := NewOperationRecord(RecordInput{Operation: fz, Chain: Chain{Stream: "c2/test/payments", Seq: 5, Parent: "r4"}})
	s, why = UpstreamFromRecords(append(recs, StoredRecord{ID: "r5", Statement: rec}), RecordMark{})
	if why != "" || len(s.Verified) != 0 || !slices.Equal(s.FailedDigests, []string{dB, dA}) {
		t.Fatalf("a recorded freeze: %+v %q", s, why)
	}
}

// TestUpstreamTruncated: a read that does not hold what a Stage already read
// is refused, so removing or rewriting records of a stream never goes unseen,
// even once the stream grows past them again.
func TestUpstreamTruncated(t *testing.T) {
	seen := RecordMark{Stream: "c2/test/payments", Seq: 4, Record: "r4"}
	chain := func(ids ...string) map[int64]string {
		at := map[int64]string{}
		for i, id := range ids {
			at[int64(i+1)] = id
		}
		return at
	}
	cases := []struct {
		name string
		seen RecordMark
		got  Stream
		at   map[int64]string
		want string
	}{
		{"extends", seen, Stream{ID: seen.Stream, Seq: 5, Head: "r5"}, chain("r1", "r2", "r3", "r4", "r5"), ""},
		{"same", seen, Stream{ID: seen.Stream, Seq: 4, Head: "r4"}, chain("r1", "r2", "r3", "r4"), ""},
		{"nothing seen yet", RecordMark{}, Stream{ID: seen.Stream, Seq: 1, Head: "r1"}, chain("r1"), ""},
		{"another stream", seen, Stream{ID: "c2/test/other", Seq: 1, Head: "x1"}, chain("x1"), ""},
		{"cut", seen, Stream{ID: seen.Stream, Seq: 2, Head: "r2"}, chain("r1", "r2"), "ends at seq 2, but this Stage already read it up to seq 4"},
		{"emptied", seen, Stream{}, nil, "ends at seq 0"},
		{"rewritten", seen, Stream{ID: seen.Stream, Seq: 4, Head: "x4"}, chain("r1", "r2", "r3", "x4"), "the stream was rewritten"},
		{"rewritten then extended", seen, Stream{ID: seen.Stream, Seq: 5, Head: "r5"}, chain("r1", "r2", "r3", "x4", "r5"), "seq 4 is record x4"},
	}
	for _, c := range cases {
		got := UpstreamTruncated(c.seen, c.got, c.at)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}
