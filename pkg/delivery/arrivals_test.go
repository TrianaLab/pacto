package delivery

import (
	"reflect"
	"strings"
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
	return StageHistory{Namespace: ns, History: History{Stage: StageNow{Name: stage, Environment: "env-" + stage}, Source: "records", Completeness: "complete", Verified: verified, Entries: es}}
}

// contractOfArtifact maps artifact "a<N>" to contract "c<N>"; "e<N>" could
// not be read, any other artifact names no pinned contract.
func contractOfArtifact(d string) (string, bool) {
	if d == "" || d[0] != 'a' {
		return "", d == "" || d[0] != 'e'
	}
	return "c" + d[1:], true
}

// arrivals is Arrivals' reaches, failing t on any doubt.
func arrivals(t *testing.T, hs ...StageHistory) map[string][]fleet.ChangeReach {
	t.Helper()
	got, doubts := Arrivals(hs, contractOfArtifact)
	if len(doubts) > 0 {
		t.Fatalf("doubts %v", doubts)
	}
	return got
}

func TestArrivalsEarliestPerStage(t *testing.T) {
	got := arrivals(t,
		stageHistory("prod", "payments", true,
			recordedMove(move("a1", "2026-11-03T00:00:00Z"), "r3"),
			recordedMove(move("a1", "2026-11-01T00:00:00Z"), "r1"),
			recordedMove(move("a2", "2026-11-02T00:00:00Z"), "r2")),
		stageHistory("dev", "payments", true, recordedMove(move("a1", "2026-10-01T00:00:00Z"), "r0")))
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
	got := arrivals(t, stageHistory("prod", "p", true, move("a1", "2026-11-01T00:00:00Z")))["c1"]
	if len(got) != 1 || !got[0].FromStatus || got[0].Record != "" || got[0].Verified || got[0].Impact != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestArrivalsUntimedSkipped(t *testing.T) {
	failed := recordedMove(move("a3", "2026-11-01T00:00:00Z"), "r3")
	failed.Outcome = Failed
	finished := recordedMove(move("a4", ""), "r4")
	finished.Finished = "2026-11-04T00:00:00Z"
	got := arrivals(t, stageHistory("prod", "p", true,
		recordedMove(move("a1", ""), "r1"),          // no time
		recordedMove(move("a2", "yesterday"), "r2"), // unreadable time
		failed, // no move
		recordedMove(move("x5", "2026-11-05T00:00:00Z"), "r5"),                                                 // no contract
		HistoryEntry{Record: "r6", Kind: RecordOperation, Outcome: Succeeded, Applied: "2026-11-06T00:00:00Z"}, // no pointer
		finished,
	))
	if len(got) != 1 || got["c4"][0].At != "2026-11-04T00:00:00Z" {
		t.Fatalf("got %+v", got)
	}
}

func TestArrivalsCopiesImpact(t *testing.T) {
	e := recordedMove(move("a1", "2026-11-01T00:00:00Z"), "r1")
	e.Impact = &Verdict{Gate: GateImpact, Result: "Fail", Message: "breaks checkout"}
	got := arrivals(t, stageHistory("prod", "p", true, e))["c1"]
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
	got := arrivals(t, stageHistory("prod", "p", true, e))["c1"]
	if len(got) != 1 || !got[0].FromStatus || got[0].Record != "" || got[0].Impact != nil || got[0].Verified {
		t.Fatalf("got %+v (entry %+v)", got, e)
	}
}

func TestArrivalsUnverifiedMarked(t *testing.T) {
	got := arrivals(t, stageHistory("prod", "p", false, recordedMove(move("a1", "2026-11-01T00:00:00Z"), "r1")))["c1"]
	if len(got) != 1 || got[0].Verified || got[0].FromStatus || got[0].Record != "r1" {
		t.Fatalf("got %+v", got)
	}
}

func TestArrivalsNamesDoubts(t *testing.T) {
	gap := stageHistory("prod", "gap", true, recordedMove(move("a1", "2026-11-05T00:00:00Z"), "r5"))
	gap.Problems = []string{"seq 2 missing"}
	status := stageHistory("prod", "status", true, move("a1", "2026-11-01T00:00:00Z"))
	status.Source, status.Completeness = SourceStatus, "partial"
	unread := stageHistory("dev", "p", true, recordedMove(move("e1", "2026-11-01T00:00:00Z"), "r1"), recordedMove(move("e1", "2026-11-02T00:00:00Z"), "r2"))
	got, doubts := Arrivals([]StageHistory{gap, status, unread}, contractOfArtifact)
	want := []string{"prod/gap: earlier moves may be missing", "prod/status: earlier moves may be missing", "e1: its artifact could not be read"}
	if !reflect.DeepEqual(doubts, want) || len(got["c1"]) != 2 {
		t.Fatalf("got %+v doubts %v", got, doubts)
	}
}

func TestContractOf(t *testing.T) {
	pinned := "oci://ghcr.io/x/c@sha256:" + strings.Repeat("a", 64)
	of := ContractOf(map[string]Described{"a": {Contract: pinned}, "b": {Err: "503"}, "e": {Contract: pinned, Err: "bundle 503"}, "c": {Contract: "oci://ghcr.io/x/c:1"}, "n": {Err: "no annotation", NoContract: true}})
	for dg, want := range map[string]struct {
		c    string
		read bool
	}{"a": {"sha256:" + strings.Repeat("a", 64), true}, "e": {"sha256:" + strings.Repeat("a", 64), true}, "b": {"", false}, "c": {"", true}, "n": {"", true}, "d": {"", false}} {
		if c, read := of(dg); c != want.c || read != want.read {
			t.Errorf("%s: got %q %v", dg, c, read)
		}
	}
}
