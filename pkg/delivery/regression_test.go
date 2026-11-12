package delivery

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/impact"
)

func TestRegressionEvidence(t *testing.T) {
	const bad, good = "sha256:bad", "sha256:good"
	stage := func(ns, name string, s StageStatus) StageObject {
		return StageObject{Metadata: ObjectMeta{Namespace: ns, Name: name}, Status: s}
	}
	full := make([]string, MaxFailedDigests)
	for i := range full {
		full[i] = "sha256:old" + strconv.Itoa(i)
	}
	stages := []StageObject{
		stage("shop", "prod", StageStatus{FailedDigests: []string{good, bad}, LastCheck: &Recheck{Operation: "op-1", Digest: bad, At: "2026-11-10T10:00:00Z", Verdicts: []Verdict{
			{Gate: "slo", Result: Fail, Message: "error rate 5%"}, {Gate: "smoke", Result: Pass},
		}}}),
		stage("shop", "staging", StageStatus{LastCheck: &Recheck{Digest: good, Verdicts: []Verdict{{Gate: "slo", Result: Fail}}}}),
		stage("shop", "canary", StageStatus{FailedDigests: full}),
		// A Stage of another service is the caller's filter: reported as given.
		stage("other", "prod", StageStatus{FailedDigests: []string{bad}}),
	}
	histories := []StageHistory{
		{Namespace: "shop", History: History{Stage: StageNow{Name: "prod"}, Entries: []HistoryEntry{
			{Operation: "rb-1", Type: TypeRollback, Finished: "2026-11-10T11:00:00Z", Record: "rec-1",
				Pointer: &PointerChange{Before: bad, After: good}, Cause: &Cause{Verdict: &Verdict{Gate: "slo", Result: Fail, Message: "error rate 5%"}}},
			// From status: no record id.
			{Operation: "rb-2", Type: TypeRollback, Pointer: &PointerChange{Before: bad, After: good}, Cause: &Cause{Reason: "pages"}},
			// Rejected: From names the digest but the pointer never moved.
			{Operation: "rb-3", Type: TypeRollback, From: bad},
			{Operation: "rb-4", Type: TypeRollback, Pointer: &PointerChange{Before: bad, After: bad}},
			{Operation: "p-1", Type: TypePromote, Pointer: &PointerChange{Before: bad, After: good}},
		}}},
		// A history of a Stage not passed in is not read.
		{Namespace: "shop", History: History{Stage: StageNow{Name: "gone"}, Entries: []HistoryEntry{
			{Operation: "rb-9", Type: TypeRollback, Pointer: &PointerChange{Before: bad, After: good}},
		}}},
	}
	ev, unknown := RegressionEvidence(stages, histories, bad)
	want := []impact.RegressionEvidence{
		{Namespace: "other", Stage: "prod", Digest: bad, Kind: impact.RegressionFailed, Message: "the Stage lists " + bad + " among the versions that failed there"},
		{Namespace: "shop", Stage: "prod", Digest: bad, Kind: impact.RegressionCheck, Gate: "slo", Result: "Fail", Message: "error rate 5%", At: "2026-11-10T10:00:00Z", Operation: "op-1"},
		{Namespace: "shop", Stage: "prod", Digest: bad, Kind: impact.RegressionFailed, Message: "the Stage lists " + bad + " among the versions that failed there"},
		{Namespace: "shop", Stage: "prod", Digest: bad, Kind: impact.RegressionRollback, Gate: "slo", Result: "Fail", Message: "error rate 5%", At: "2026-11-10T11:00:00Z", Operation: "rb-1", Record: "rec-1"},
		{Namespace: "shop", Stage: "prod", Digest: bad, Kind: impact.RegressionRollback, Message: "pages", Operation: "rb-2"},
	}
	if !reflect.DeepEqual(ev, want) {
		t.Errorf("evidence =\n%+v\nwant\n%+v", ev, want)
	}
	if len(unknown) != 1 || unknown[0] != "shop/canary: its failed list is full (50 entries), so an older failure of "+bad+" may have aged out" {
		t.Errorf("unknown = %q", unknown)
	}
}
