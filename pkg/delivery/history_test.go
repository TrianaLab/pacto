package delivery

import (
	"strings"
	"testing"
)

// rec is a record at seq with parent, moving the Stage before → after (an
// empty after moved nothing).
func rec(id string, seq int64, parent, before, after string) StoredRecord {
	p := RecordPredicate{Kind: RecordOperation, Chain: Chain{Stream: "c/ns/s", Seq: seq, Parent: parent},
		Intent: RecordIntent{UID: "u-" + id, Name: "op-" + id, Params: &Params{From: before, To: after}},
		Type:   &RecordType{ID: TypePromote}, Actor: &RecordActor{User: "bot", Class: ClassAgent},
		Outcome: &RecordOutcome{Result: Succeeded, Verification: Verified, Pointer: PointerChange{Before: before, After: after}},
		Time:    RecordTime{Finished: "2026-10-21T10:00:0" + id + "Z"}}
	return StoredRecord{ID: id, Manifest: "m" + id, Statement: Statement{Predicate: p}}
}

func TestFoldStreamIntact(t *testing.T) {
	f := FoldStream([]StoredRecord{rec("2", 2, "1", "A", "B"), rec("1", 1, "", "", "A"), rec("3", 3, "2", "B", "C")})
	if len(f.Problems) != 0 || f.Head != "3" || f.Records[0].ID != "1" {
		t.Fatalf("fold = %+v", f)
	}
	// An unrecordable record hides what it moved, so the next move is not
	// called hidden for starting somewhere the one before did not leave.
	u := rec("2", 2, "1", "", "")
	u.Statement.Predicate.Kind, u.Statement.Predicate.Outcome = RecordUnrecordable, nil
	u.Statement.Predicate.Unrecordable = &Refusal{Check: CheckApproval}
	if f := FoldStream([]StoredRecord{rec("1", 1, "", "", "A"), u, rec("3", 3, "2", "B", "C")}); len(f.Problems) != 0 {
		t.Fatalf("problems after an unrecordable record = %v", f.Problems)
	}
	if FoldStream(nil).Head != "" {
		t.Fatal("an empty stream has a head")
	}
}

func TestFoldStreamReportsEverythingWrong(t *testing.T) {
	gap := rec("4", 4, "3", "", "")
	gap.Statement.Predicate.Gap, gap.Statement.Predicate.Outcome = &Gap{Seq: 9}, nil
	for name, c := range map[string]struct {
		recs []StoredRecord
		want string
	}{
		"missing first":  {[]StoredRecord{rec("2", 2, "1", "A", "B")}, "seq 1 is missing"},
		"missing middle": {[]StoredRecord{rec("1", 1, "", "", "A"), rec("4", 4, "3", "A", "B")}, "seqs 2 to 3 are missing"},
		"fork":           {[]StoredRecord{rec("1", 1, "", "", "A"), rec("2a", 2, "1", "A", "B"), rec("2b", 2, "1", "A", "C")}, "seq 2 has two records"},
		"broken chain":   {[]StoredRecord{rec("1", 1, "", "", "A"), rec("2", 2, "x", "A", "B")}, "does not chain from 1"},
		"older history":  {[]StoredRecord{rec("1", 1, "x", "", "A")}, "names a parent"},
		"gap record":     {[]StoredRecord{rec("1", 1, "", "", "A"), rec("2", 2, "1", "A", "B"), rec("3", 3, "2", "B", "C"), gap}, "seq 4 is a gap"},
		"hidden change":  {[]StoredRecord{rec("1", 1, "", "", "A"), rec("2", 2, "1", "Z", "B")}, "something moved it with no record"},
	} {
		f := FoldStream(c.recs)
		if !strings.Contains(strings.Join(f.Problems, "|"), c.want) {
			t.Errorf("%s: problems = %v, want %q", name, f.Problems, c.want)
		}
	}
}

func TestRecordEntry(t *testing.T) {
	e := RecordEntry(rec("1", 1, "", "", "A"))
	if e.Record != "1" || e.Operation != "op-1" || e.Type != TypePromote || e.To != "A" || e.Actor != "bot" ||
		e.Outcome != Succeeded || e.Pointer == nil || e.Pointer.After != "A" || e.Kind != RecordOperation {
		t.Fatalf("entry = %+v", e)
	}
	u := rec("2", 2, "1", "", "")
	u.Statement.Predicate = RecordPredicate{Kind: RecordUnrecordable, Chain: u.Statement.Predicate.Chain,
		Unrecordable: &Refusal{Check: CheckSensitive, Message: "looks like a secret"}}
	if e := RecordEntry(u); !strings.HasPrefix(e.Reason, "Unrecordable: sensitive-value") || e.Pointer != nil || e.Type != "" {
		t.Fatalf("unrecordable entry = %+v", e)
	}
	g := rec("3", 3, "2", "", "")
	g.Statement.Predicate = RecordPredicate{Kind: RecordGap, Chain: g.Statement.Predicate.Chain, Gap: &Gap{Seq: 7}}
	if e := RecordEntry(g); !strings.Contains(e.Reason, "local seq 7") {
		t.Fatalf("gap entry = %+v", e)
	}
}
