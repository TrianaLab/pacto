package main

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoObservedOnlyExternal builds a snapshot from the embedded bundles plus
// the demo's telemetry and cluster external uses, and checks what the dashboard
// shows: twilio is reached only through a trace (no contract declares it), and
// stripe is inferred from notification-worker's pod spec.
func TestDemoObservedOnlyExternal(t *testing.T) {
	src := bundlesFS(t)
	var revs []fleet.RawRevision
	for _, name := range src.names {
		for ver, e := range src.byName[name] {
			revs = append(revs, fleet.RawRevision{Bundle: e.bundle, RequestedRef: "oci://demo.pacto.local/" + name + ":" + ver,
				ResolvedRef: "oci://demo.pacto.local/" + name + "@sha256:" + e.hash})
		}
	}
	now := time.Now().UTC()
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{Now: func() time.Time { return now }, FreshnessWindow: 24 * time.Hour},
		fleet.NewMemorySource("local", "local", &fleet.Collection{Revisions: revs}),
		fleet.NewMemorySource("k8s-production", fleet.SourceKindKubernetes, &fleet.Collection{ExternalUses: demoExternalUses(now)}),
		fleet.NewMemorySource("otel-traces", "observation", &fleet.Collection{Observed: demoObservedEdges(now)}))
	if err != nil {
		t.Fatal(err)
	}
	observed, declared, inferred := workerExternalEdges(snap)
	if observed != 1 || declared != 0 || inferred != 1 {
		t.Fatalf("notification-worker edges: observed twilio %d, other twilio %d, inferred stripe %d; want 1, 0, 1", observed, declared, inferred)
	}
	rec := snap.Externals["twilio"]
	if rec == nil || len(rec.Sources) == 0 {
		t.Fatalf("twilio external missing or without sources: %+v", rec)
	}
	for _, s := range rec.Sources {
		if s.Kind != fleet.ExternalSourceObserved {
			t.Errorf("twilio source kind %q, want only observed", s.Kind)
		}
	}
}

// workerExternalEdges counts notification-worker's observed and other edges to
// twilio and its inferred edges to stripe.
func workerExternalEdges(snap *fleet.FleetSnapshot) (observed, declared, inferred int) {
	worker := fleet.NewServiceKey("notification-worker")
	for _, r := range snap.Relationships {
		switch {
		case r.FromService != worker:
		case r.ToExternal == "twilio" && r.Provenance == fleet.ProvenanceObserved:
			observed++
		case r.ToExternal == "twilio":
			declared++
		case r.ToExternal == "stripe" && r.Provenance == fleet.ProvenanceInferred:
			inferred++
		}
	}
	return observed, declared, inferred
}
