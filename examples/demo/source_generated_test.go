package main

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestDemoFleetSizeReadsOnlyTheOfferedSizes(t *testing.T) {
	for param, want := range map[string]int{"": 0, "40": 40, "400": 400, "3000": 3000, "10000": 10000, "41": 0, "-40": 0, "big": 0, "<undefined>": 0} {
		if got := demoFleetSize(param); got != want {
			t.Errorf("demoFleetSize(%q) = %d, want %d", param, got, want)
		}
	}
}

func TestBuildGeneratedFleetHoldsTheAskedServices(t *testing.T) {
	snap, err := buildGeneratedFleet(400)
	if err != nil {
		t.Fatal(err)
	}
	m, err := fleet.NewQuery(snap).Map(fleet.MapQuery{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, g := range m.Groups {
		n += g.Services
	}
	if n < 350 || n > 400 {
		t.Fatalf("the generated fleet maps %d services, want about 400", n)
	}
}
