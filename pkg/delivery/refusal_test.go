package delivery

import (
	"errors"
	"testing"
)

func TestSignAndOpenRefusal(t *testing.T) {
	r := PublishRefusal{Service: "payments", Version: "2.4.3", Baseline: "2.4.2", Classification: "BREAKING",
		RequiredBump: "major", Message: "BREAKING since 2.4.2 needs a major bump", At: "2026-10-22T10:00:00Z"}
	st := NewRefusalStatement("sha256:abc", r)
	if len(st.Subject) != 1 || st.Subject[0].Name != "contract" || st.Subject[0].Digest["sha256"] != "abc" {
		t.Fatalf("subject = %+v", st.Subject)
	}
	b, id, err := SignRefusal(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	got, gotID, err := OpenRefusal(b, func([]byte) error { return nil })
	if err != nil || gotID != id || got.Predicate != r {
		t.Fatalf("open = %+v %s %v", got, gotID, err)
	}
	if _, _, err := OpenRefusal(b, func([]byte) error { return errors.New("untrusted") }); err == nil {
		t.Fatal("an untrusted refusal opened")
	}
	// A record is not a refusal, and a refusal is not a record.
	rec, _, _ := SignRecord(t.Context(), plainSigner{}, Statement{Type: StatementType, PredicateType: PredicateOperation})
	if _, _, err := OpenRefusal(rec, func([]byte) error { return nil }); err == nil {
		t.Fatal("a record opened as a refusal")
	}
	if _, _, err := OpenRecord(b, accept); err == nil {
		t.Fatal("a refusal opened as a record")
	}
}
