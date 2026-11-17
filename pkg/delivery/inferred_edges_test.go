package delivery

import (
	"context"
	"slices"
	"testing"
)

// The inferred legacy caller's runtime edge to payments is an inferred
// relationship, and it still leaves payments' consumer uncontracted.
func TestPlanMissingContractStillBlocksWithInferredEdge(t *testing.T) {
	p := paymentsPacto(complete(), legacy)
	from, to := minorSides()
	in := withInferredLegacy(t, planIn(t, from, to, p), p)
	if len(in.Snapshot.InferredRelationships) == 0 {
		t.Fatalf("no inferred relationship: %+v", in.Snapshot.UncontractedCallers)
	}
	plan := PlanFor(context.Background(), in)
	if got := ProposeLevel(StageSpec{}, StageStatus{Level: LevelAutoNonBreaking}, &plan); !slices.Contains(kinds(got), BlockerMissingContract) {
		t.Fatalf("an inferred edge lifted missing-contract: %+v", got)
	}
}
