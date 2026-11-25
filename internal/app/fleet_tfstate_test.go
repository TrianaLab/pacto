package app

import (
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestFleetReadsTerraformStates(t *testing.T) {
	t.Parallel()
	state := "../../pkg/managedres/testdata/state-v4.json"
	snap, err := NewService(nil, nil).Fleet(t.Context(), FleetOptions{TerraformStates: []string{state, state}})
	if err != nil {
		t.Fatal(err)
	}
	rec := snap.Externals[fleet.ExternalKey("aws/rds/123456789012/eu-west-1/orders-db")]
	if rec == nil || len(rec.Resources) != 2 {
		t.Fatalf("external record %+v", rec)
	}
	if !slices.ContainsFunc(snap.Sources, func(s fleet.SourceState) bool { return s.ID == "tf-state-2" && s.Kind == "terraform" }) {
		t.Errorf("sources %+v", snap.Sources)
	}
}
