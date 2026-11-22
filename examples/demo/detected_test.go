package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoFleetHasDetectedRows: the demo cluster's detection shows as seven
// current inferred rows (the Pull Secret proposal among them) plus the one
// declared Installation row, from an available advisory source that adds no
// snapshot limitation, so the demo's completeness is what its other sources
// make it.
func TestDemoFleetHasDetectedRows(t *testing.T) {
	snap := demoFleet(t)
	var rows []fleet.Provider
	declared := 0
	for _, p := range snap.Providers {
		if p.Source != "cluster-detection" {
			continue
		}
		if p.Provenance == fleet.ProvenanceDeclared {
			declared++
			checkDeclaredDetectedRow(t, p)
		} else {
			rows = append(rows, p)
		}
	}
	if len(rows) != 7 || declared != 1 {
		t.Fatalf("detected rows = %+v (%d declared), want seven inferred and one declared", rows, declared)
	}
	for _, p := range rows {
		checkInferredDetectedRow(t, p)
	}
	i := slices.IndexFunc(snap.Sources, func(s fleet.SourceState) bool { return s.ID == "cluster-detection" })
	if i < 0 || snap.Sources[i].Status != fleet.SourceAvailable || !snap.Sources[i].Advisory || snap.Sources[i].Kind != fleet.SourceKindDetection {
		t.Fatalf("sources = %+v", snap.Sources)
	}
	if slices.ContainsFunc(snap.Limitations, func(l fleet.Limitation) bool { return l.Source == "cluster-detection" }) {
		t.Errorf("the advisory source added a snapshot limitation: %+v", snap.Limitations)
	}
}

// checkDeclaredDetectedRow: the one declared row is the available
// Installation row whose evidence names the demo registry.
func checkDeclaredDetectedRow(t *testing.T, p fleet.Provider) {
	t.Helper()
	if p.ID != "installation" || p.Status != fleet.SourceAvailable || !strings.HasPrefix(p.Evidence, "profile default; registry ghcr.io/acme") {
		t.Errorf("declared row %+v", p)
	}
}

// checkInferredDetectedRow: every other detected row is inferred, current,
// on the production cluster and free of a cluster-mismatch limitation.
func checkInferredDetectedRow(t *testing.T, p fleet.Provider) {
	t.Helper()
	if p.Provenance != fleet.ProvenanceInferred || p.AsOf == nil || !p.AsOf.Equal(demoClock) || p.Cluster != "production" ||
		slices.ContainsFunc(p.Limitations, func(l fleet.Limitation) bool { return l.Code == fleet.LimitationDetectionClusterMismatch }) {
		t.Errorf("row %+v", p)
	}
}
