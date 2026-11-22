package main

import (
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoInferredEdges: the calls the demo cluster saw between workloads with
// no contract, and from one of them to a declared service, are inferred
// relationships, and none of them is a declared or observed relationship.
func TestDemoInferredEdges(t *testing.T) {
	snap := demoFleet(t)
	export := fleet.NewServiceKey(fleet.K8sCaller(prodClusterID, "shop", "CronJob", "nightly-export"))
	billing := fleet.NewServiceKey(fleet.K8sCaller(prodClusterID, "shop", "Deployment", "legacy-billing"))
	want := map[[2]fleet.ServiceKey]bool{
		{export, billing}: true,
		{export, fleet.NewServiceKey("search-indexer")}:    true,
		{export, fleet.NewServiceKey("event-bus")}:         true,
		{billing, fleet.NewServiceKey("payments-service")}: true,
	}
	for _, r := range snap.InferredRelationships {
		k := [2]fleet.ServiceKey{r.FromService, r.ToService}
		if !want[k] {
			continue
		}
		if r.Provenance != fleet.ProvenanceInferred || r.ObservedCount == 0 || len(r.ObservedSources) == 0 {
			t.Errorf("inferred edge %s -> %s: %+v", r.FromService, r.ToService, r)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("missing inferred edges %v in %+v", want, snap.InferredRelationships)
	}
	for _, r := range snap.Relationships {
		if r.FromService == export || r.FromService == billing || r.ToService == billing {
			t.Errorf("an edge of an inferred service is in Relationships: %+v", r)
		}
	}
}

// TestDemoReplacement: experiment-service's Pacto target runs as the
// experiments Deployment, which ran with no contract for days before the
// Pacto was created, so the demo records a pacto-target replacement and attention reports
// it at the demo clock.
func TestDemoReplacement(t *testing.T) {
	snap := demoFleet(t)
	from := fleet.NewServiceKey(fleet.K8sCaller(prodClusterID, "prod", "Deployment", "experiments"))
	to := fleet.NewServiceKey("experiment-service")
	i := slices.IndexFunc(snap.Replacements, func(r fleet.Replacement) bool { return r.Link == "pacto-target" })
	if i < 0 {
		t.Fatalf("replacements = %+v, want a pacto-target one", snap.Replacements)
	}
	r := snap.Replacements[i]
	if r.From != from || r.To != to || r.Link != "pacto-target" || r.At == nil || r.Target == "" {
		t.Errorf("replacement = %+v", r)
	}
	if _, ok := snap.Services[from]; ok {
		t.Errorf("the replaced workload is still an inferred service")
	}
	l, err := fleet.NewQuery(snap).Attention(fleet.AttentionFilter{Category: "knowledge", Service: string(to)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range l.Items {
		found = found || it.Code == "INFERRED_REPLACED"
	}
	if !found {
		t.Errorf("no INFERRED_REPLACED item for %s: %+v", to, l.Items)
	}
}
