package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoMeshEdgeAndRow: the demo cluster's mesh source reports one Istio
// row, partial for one namespace without sidecars, and the one call Istio saw
// between two inferred workloads lands as an inferred relationship. The
// source is advisory: it adds no SOURCE_PARTIAL or SOURCE_STALE limitation
// and leaves the completeness of a consumer answer as it was.
func TestDemoMeshEdgeAndRow(t *testing.T) {
	snap := demoFleet(t)
	checkDemoMeshEdge(t, snap)
	checkDemoMeshRow(t, snap)
	checkDemoMeshAdvisory(t, snap)
}

// checkDemoMeshEdge: the Istio call is an inferred relationship the mesh
// source observed, never a declared or observed one.
func checkDemoMeshEdge(t *testing.T, snap *fleet.FleetSnapshot) {
	t.Helper()
	worker := fleet.NewServiceKey(fleet.K8sCaller(prodClusterID, "shop", "Deployment", "billing-worker"))
	billing := fleet.NewServiceKey(fleet.K8sCaller(prodClusterID, "shop", "Deployment", "legacy-billing"))
	i := slices.IndexFunc(snap.InferredRelationships, func(r fleet.Relationship) bool {
		return r.FromService == worker && r.ToService == billing
	})
	if i < 0 {
		t.Fatalf("no inferred edge %s -> %s in %+v", worker, billing, snap.InferredRelationships)
	}
	if rel := snap.InferredRelationships[i]; !slices.ContainsFunc(rel.ObservedSources, func(s fleet.ObservedSourceStat) bool { return s.Source == demoMeshID }) {
		t.Errorf("edge observed by %+v, want %s", rel.ObservedSources, demoMeshID)
	}
	for _, r := range snap.Relationships {
		// The external use its connection Secret joins is TestDemoManagedConsumersJoined's.
		if r.FromService == worker && r.ToExternal == "" {
			t.Errorf("a mesh edge of an inferred service is in Relationships: %+v", r)
		}
	}
}

// checkDemoMeshRow: the Istio row is reported, partial for one namespace
// without sidecars, and used for observed edges.
func checkDemoMeshRow(t *testing.T, snap *fleet.FleetSnapshot) {
	t.Helper()
	reported, _ := splitReporting(snap.Providers)
	j := slices.IndexFunc(reported, func(p fleet.Provider) bool { return p.Source == demoMeshID && p.ID == fleet.ProviderIstio })
	if j < 0 {
		t.Fatalf("no reported Istio row in %+v", snap.Providers)
	}
	row := reported[j]
	if row.Status != fleet.SourcePartial || len(row.Limitations) != 1 || row.Limitations[0].Code != fleet.LimitationMeshCoveragePartial ||
		row.Limitations[0].Message != "1 namespace without sidecars" || !slices.Equal(row.UsedFor, []string{fleet.UseObservedEdges}) {
		t.Errorf("Istio row = %+v", row)
	}
}

// checkDemoMeshAdvisory: the mesh source is advisory and partial, adds no
// SOURCE_* limitation and leaves consumer completeness as it was.
func checkDemoMeshAdvisory(t *testing.T, snap *fleet.FleetSnapshot) {
	t.Helper()
	k := slices.IndexFunc(snap.Sources, func(s fleet.SourceState) bool { return s.ID == demoMeshID })
	if k < 0 || !snap.Sources[k].Advisory || snap.Sources[k].Status != fleet.SourcePartial {
		t.Errorf("mesh source state = %+v", snap.Sources)
	}
	for _, l := range snap.Limitations {
		if l.Source == demoMeshID && (l.Code == fleet.LimitationSourcePartial || l.Code == fleet.LimitationSourceStale) {
			t.Errorf("advisory mesh source made the fleet partial: %+v", l)
		}
	}
	prod := &fleet.Cluster{ID: demoClusterID, Name: "production", NameSource: fleet.ClusterNameFromContext}
	workloads := func() fleet.Source {
		return fleet.NewMemorySource("k8s-production-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
			Cluster: prod, InferredServices: demoInferredServices(),
		})
	}
	build := func(sources ...fleet.Source) fleet.Completeness {
		s, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: demoNow, FreshnessWindow: 24 * time.Hour}, sources...)
		if err != nil {
			t.Fatal(err)
		}
		return s.ConsumerCompleteness()
	}
	if with, without := build(workloads(), demoMeshSource(prod, demoNow())), build(workloads()); with != without {
		t.Errorf("consumer completeness with the mesh = %q, without = %q", with, without)
	}
}
