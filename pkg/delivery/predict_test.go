package delivery

import (
	"slices"
	"strings"
	"testing"
)

func frozenInput(t string) PlanInput {
	return PlanInput{Spec: OperationSpec{Type: t}, Status: StageStatus{Frozen: true, FrozenReason: "Errored"}}
}

func TestPredictFrozenHeldKeepsHeld(t *testing.T) {
	p := Plan{Floor: Floor{Human: true, Decision: Approve}}
	if got := predict(frozenInput(TypePromote), &p); got != PredictHeld || !slices.Contains(p.Conflicts, "StageFrozen: Errored") {
		t.Fatalf("a held promote on a frozen Stage stays Held and records the freeze: %s %v", got, p.Conflicts)
	}
}

func TestPredictFrozenWaits(t *testing.T) {
	var p Plan
	if got := predict(frozenInput(TypePromote), &p); got != PredictWaits || strings.Join(p.Conflicts, "") != "StageFrozen: Errored" {
		t.Fatalf("no floor on a frozen Stage waits: %s %v", got, p.Conflicts)
	}
}

func TestPredictRejectedSkipsFrozen(t *testing.T) {
	p := Plan{Verdicts: []Verdict{{Gate: GateDiff, Result: Fail}}, Floor: Floor{Human: true}}
	if got := predict(frozenInput(TypePromote), &p); got != PredictRejected || len(p.Conflicts) != 0 {
		t.Fatalf("a failing verdict rejects before the freeze is read: %s %v", got, p.Conflicts)
	}
}

func TestPredictRollbackOnFrozenProceeds(t *testing.T) {
	var p Plan
	if got := predict(frozenInput(TypeRollback), &p); got != PredictProceeds || len(p.Conflicts) != 0 {
		t.Fatalf("a rollback is exempt from the freeze: %s %v", got, p.Conflicts)
	}
}
