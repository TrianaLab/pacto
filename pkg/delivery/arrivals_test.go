package delivery

import (
	"reflect"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// move is a succeeded move to artifact, applied at.
func move(artifact, applied string) HistoryEntry {
	return HistoryEntry{Pointer: &PointerChange{After: artifact}, Outcome: Succeeded, Applied: applied}
}

func recordedMove(e HistoryEntry, id string) HistoryEntry {
	e.Record, e.Kind = id, RecordOperation
	return e
}

func stageHistory(ns, stage string, verified bool, es ...HistoryEntry) StageHistory {
	return StageHistory{Namespace: ns, History: History{Stage: StageNow{Name: stage, Environment: "env-" + stage}, Source: "records", Verified: verified, Entries: es}}
}

// contractOfArtifact maps artifact "a<N>" to contract "c<N>".
func contractOfArtifact(d string) string {
	if d == "" || d[0] != 'a' {
		return ""
	}
	return "c" + d[1:]
}

func TestArrivalsEarliestPerStage(t *testing.T) {
	got := Arrivals([]StageHistory{
		stageHistory("prod", "payments", true,
			recordedMove(move("a1", "2026-11-03T00:00:00Z"), "r3"),
			recordedMove(move("a1", "2026-11-01T00:00:00Z"), "r1"),
			recordedMove(move("a2", "2026-11-02T00:00:00Z"), "r2")),
		stageHistory("dev", "payments", true, recordedMove(move("a1", "2026-10-01T00:00:00Z"), "r0")),
	}, contractOfArtifact)
	want := map[string][]fleet.ChangeReach{
		"c1": {
			{Namespace: "dev", Stage: "payments", Environment: "env-payments", At: "2026-10-01T00:00:00Z", Record: "r0", Verified: true},
			{Namespace: "prod", Stage: "payments", Environment: "env-payments", At: "2026-11-01T00:00:00Z", Record: "r1", Verified: true},
		},
		"c2": {{Namespace: "prod", Stage: "payments", Environment: "env-payments", At: "2026-11-02T00:00:00Z", Record: "r2", Verified: true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestArrivalsStatusLineMarked(t *testing.T) {
	got := Arrivals([]StageHistory{stageHistory("prod", "p", true, move("a1", "2026-11-01T00:00:00Z"))}, contractOfArtifact)["c1"]
	if len(got) != 1 || !got[0].FromStatus || got[0].Record != "" || got[0].Verified || got[0].Impact != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestArrivalsUntimedSkipped(t *testing.T) {
	failed := recordedMove(move("a3", "2026-11-01T00:00:00Z"), "r3")
	failed.Outcome = Failed
	finished := recordedMove(move("a4", ""), "r4")
	finished.Finished = "2026-11-04T00:00:00Z"
	got := Arrivals([]StageHistory{stageHistory("prod", "p", true,
		recordedMove(move("a1", ""), "r1"),          // no time
		recordedMove(move("a2", "yesterday"), "r2"), // unreadable time
		failed, // no move
		recordedMove(move("x5", "2026-11-05T00:00:00Z"), "r5"),                                                 // no contract
		HistoryEntry{Record: "r6", Kind: RecordOperation, Outcome: Succeeded, Applied: "2026-11-06T00:00:00Z"}, // no pointer
		finished,
	)}, contractOfArtifact)
	if len(got) != 1 || got["c4"][0].At != "2026-11-04T00:00:00Z" {
		t.Fatalf("got %+v", got)
	}
}

func TestArrivalsCopiesImpact(t *testing.T) {
	e := recordedMove(move("a1", "2026-11-01T00:00:00Z"), "r1")
	e.Impact = &Verdict{Gate: GateImpact, Result: "Fail", Message: "breaks checkout"}
	got := Arrivals([]StageHistory{stageHistory("prod", "p", true, e)}, contractOfArtifact)["c1"]
	want := &fleet.RecordedVerdict{Gate: GateImpact, Result: "Fail", Message: "breaks checkout"}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Impact, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestArrivalsUnrecordableIsStatus(t *testing.T) {
	e := HistoryEntry{Record: "r1", Kind: RecordUnrecordable, Impact: &Verdict{Gate: GateImpact, Result: "Pass"}}
	op := OperationObject{}
	op.Metadata.UID = "u1"
	op.Status.Outcome = Succeeded
	op.Status.Pointer = PointerChange{After: "a1"}
	op.Status.Times.Applied = "2026-11-01T00:00:00Z"
	e = WithUnsignedMove(e, []OperationObject{op}, "u1")
	got := Arrivals([]StageHistory{stageHistory("prod", "p", true, e)}, contractOfArtifact)["c1"]
	if len(got) != 1 || !got[0].FromStatus || got[0].Record != "" || got[0].Impact != nil || got[0].Verified {
		t.Fatalf("got %+v (entry %+v)", got, e)
	}
}

func TestArrivalsUnverifiedMarked(t *testing.T) {
	got := Arrivals([]StageHistory{stageHistory("prod", "p", false, recordedMove(move("a1", "2026-11-01T00:00:00Z"), "r1"))}, contractOfArtifact)["c1"]
	if len(got) != 1 || got[0].Verified || got[0].FromStatus || got[0].Record != "r1" {
		t.Fatalf("got %+v", got)
	}
}
