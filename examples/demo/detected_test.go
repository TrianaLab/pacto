package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoFleetHasDetectedRows: the demo cluster's detection shows as thirteen
// current inferred rows (the Pull Secret proposal, the Datadog Agent, the Backstage catalog, the
// three egress readers and the running Crossplane among them) plus the one declared Installation row, from an available advisory
// source that adds no snapshot limitation, so the demo's completeness is what
// its other sources make it. The three trace backends a live edge source
// reads are that source's rows, never the detection source's. The producer
// row of the inferred offerings the installation advertises is apart.
func TestDemoFleetHasDetectedRows(t *testing.T) {
	snap := demoFleet(t)
	var rows []fleet.Provider
	declared := 0
	// source_offering.go's producer row is apart.
	offerings := fleet.ProducerRowPrefix + demoOfferingProducer
	for _, p := range slices.DeleteFunc(slices.Clone(snap.Providers), func(p fleet.Provider) bool { return p.ID == offerings }) {
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
	if len(rows) != 13 || declared != 1 {
		t.Fatalf("detected rows = %+v (%d declared), want thirteen inferred and one declared", rows, declared)
	}
	if slices.ContainsFunc(rows, func(p fleet.Provider) bool { return slices.Contains(demoTraceKinds, p.ID) }) {
		t.Errorf("the detection source lists a trace backend an edge source reads: %+v", rows)
	}
	for _, id := range []string{demoServiceGraphID, demoJaegerID, demoReceiverID} {
		if !slices.ContainsFunc(snap.Providers, func(p fleet.Provider) bool { return p.Source == id }) {
			t.Errorf("no row from %s", id)
		}
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

// TestDemoProvisioningSuggestion: the demo cluster runs Crossplane, so an
// offering use nothing reported on suggests accepting its inferred offerings
// in the production cluster.
func TestDemoProvisioningSuggestion(t *testing.T) {
	q := fleet.NewQuery(demoFleet(t))
	const want = "Crossplane detected in production: accept its inferred offerings"
	found := false
	for _, s := range q.Snapshot().Services {
		d, err := q.EntityDetail(fleet.KindService, s.Name)
		if err != nil || d.Service == nil {
			continue
		}
		for _, u := range d.Service.ConsumedOfferings.Items {
			for _, st := range u.Provisioning {
				if st.Suggestion != "" && st.Suggestion != want {
					t.Errorf("%s %s suggestion %q, want %q", s.Name, u.Provider.Key, st.Suggestion, want)
				}
				found = found || st.Suggestion == want
			}
		}
	}
	if !found {
		t.Error("no offering use carries the Crossplane suggestion")
	}
}
