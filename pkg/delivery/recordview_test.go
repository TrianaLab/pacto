package delivery

import (
	"fmt"
	"strings"
	"testing"
)

func TestRecordViewOf(t *testing.T) {
	rec := StoredRecord{ID: "sha256:r2", Statement: Statement{Predicate: RecordPredicate{Kind: RecordOperation,
		Intent:   RecordIntent{Name: "ship", Reason: "ship it", Params: &Params{From: "sha256:a", To: "sha256:b"}},
		Actor:    &RecordActor{User: "bot", Class: ClassAgent, OnBehalfOf: &OnBehalfOf{User: "carol", Asserted: true}},
		Verdicts: []Verdict{{Gate: GateDiff, Result: Pass}, {Gate: "health", Result: Pass}},
		Chain:    Chain{Seq: 2}}}}
	v := RecordViewOf("prod", "payments", rec, "sha256:trust", "2026-11-10T10:00:00Z")
	got := fmt.Sprint(v.Namespace, v.Stage, v.Record, v.Verified, v.Trust, v.Entry.Operation, v.Entry.Seq, v.IntentReason, v.OnBehalfOf, v.AsOf,
		len(v.Verdicts), v.Verdicts[0].Rerun != "", v.Verdicts[1].Rerun, v.Note)
	want := fmt.Sprint("prod", "payments", "sha256:r2", true, "sha256:trust", "ship", 2, "ship it", "carol", "2026-11-10T10:00:00Z",
		2, true, "", "read from the signed record in the registry, verified with trust config sha256:trust")
	if got != want {
		t.Fatalf("verified view = %s\nwant %s", got, want)
	}

	// No trust config: nothing was checked, and an empty record still sends [].
	v = RecordViewOf("prod", "payments", StoredRecord{ID: "sha256:r1"}, "", "")
	if v.Verified || v.Verdicts == nil || v.OnBehalfOf != "" || !strings.Contains(v.Note, "its signature was not checked") {
		t.Fatalf("unchecked view = %+v", v)
	}
}
