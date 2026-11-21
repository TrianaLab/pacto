package main

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoMeshID is the id of the demo cluster's mesh source.
const demoMeshID = "k8s-production-mesh"

// demoMeshSource is what a live read finds in a cluster that runs Istio: one
// Istio row, partial because the observability namespace runs without
// sidecars, and the one call Istio recorded in the last hour, named the way
// Istio names a workload (no kind), between two inferred Deployments. It is
// advisory, so its partial state never makes a fleet answer partial.
func demoMeshSource(cluster *fleet.Cluster, now time.Time) fleet.Source {
	row := fleet.Provider{
		ID: fleet.ProviderIstio, Name: "Istio", Evidence: "serves networking.istio.io; calls read from Prometheus",
		UsedFor: []string{fleet.UseObservedEdges}, Status: fleet.SourcePartial,
		Objects: []fleet.ProviderObject{
			{Kind: "Service", Namespace: "monitoring", Name: "prometheus-k8s"},
			{Kind: "Namespace", Namespace: "observability", Name: "observability"},
		},
		Limitations: []fleet.Limitation{{Code: fleet.LimitationMeshCoveragePartial, Source: demoMeshID, Message: "1 namespace without sidecars"}},
	}
	return fleet.NewMemorySource(demoMeshID, fleet.SourceKindMesh, &fleet.Collection{
		Advisory: true, ObservedUndeclaredOnly: true, Cluster: cluster, Providers: []fleet.Provider{row},
		Observed: []fleet.ObservedEdge{{
			From: fleet.K8sCaller("", "shop", "", "billing-worker"), To: fleet.K8sCaller("", "shop", "", "legacy-billing"),
			Count: 42, FirstSeen: now.Add(-time.Hour), LastSeen: now,
		}},
		State: &fleet.SourceState{Status: fleet.SourcePartial, ObservedAt: &now, LastSuccessfulSync: &now},
	})
}
