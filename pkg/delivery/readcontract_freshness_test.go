package delivery

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDeliveryReadContractFreshnessUnchanged pins that the delivery planner's
// zero-window snapshot never carries the product path's freshness disclosure:
// impact applies its own 24h rule, and its completeness gate must not move.
func TestDeliveryReadContractFreshnessUnchanged(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	snap, err := Snapshot(context.Background(), []PactoView{checkoutPacto("^1.0.0")}, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Targets) == 0 {
		t.Fatal("the fixture must hold a target, or the disclosure could never apply")
	}
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationFreshnessNotEvaluated {
			t.Fatalf("the delivery snapshot must not disclose freshness: %+v", l)
		}
	}
	if snap.Completeness != fleet.CompletenessComplete {
		t.Errorf("completeness = %q, want complete", snap.Completeness)
	}
}
