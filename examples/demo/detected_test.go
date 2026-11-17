package main

import (
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoFleetHasDetectedRows: the demo cluster's detection shows as six
// current inferred rows from an available advisory source that adds no
// snapshot limitation, so the demo's completeness is what its other sources
// make it.
func TestDemoFleetHasDetectedRows(t *testing.T) {
	snap := demoFleet(t)
	var rows []fleet.Provider
	for _, p := range snap.Providers {
		if p.Source == "cluster-detection" {
			rows = append(rows, p)
		}
	}
	if len(rows) != 6 {
		t.Fatalf("detected rows = %+v, want six", rows)
	}
	for _, p := range rows {
		if p.Provenance != fleet.ProvenanceInferred || p.AsOf == nil || !p.AsOf.Equal(demoClock) || p.Cluster != "production" ||
			slices.ContainsFunc(p.Limitations, func(l fleet.Limitation) bool { return l.Code == fleet.LimitationDetectionClusterMismatch }) {
			t.Errorf("row %+v", p)
		}
	}
	i := slices.IndexFunc(snap.Sources, func(s fleet.SourceState) bool { return s.ID == "cluster-detection" })
	if i < 0 || snap.Sources[i].Status != fleet.SourceAvailable || !snap.Sources[i].Advisory || snap.Sources[i].Kind != fleet.SourceKindDetection {
		t.Fatalf("sources = %+v", snap.Sources)
	}
	if slices.ContainsFunc(snap.Limitations, func(l fleet.Limitation) bool { return l.Source == "cluster-detection" }) {
		t.Errorf("the advisory source added a snapshot limitation: %+v", snap.Limitations)
	}
}
