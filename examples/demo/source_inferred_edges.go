package main

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoInferredEdges are calls the kernel observer saw from nightly-export, a
// CronJob no contract describes: to legacy-billing, another workload with no
// contract, to the declared search-indexer, which it feeds, and to event-bus,
// a provider of the messaging offering, whose page then lists it as an
// uncontracted caller linked to its inferred service. Each end with no
// contract is named by its workload, so the graph shows the calls as inferred
// edges.
func demoInferredEdges(now time.Time) []fleet.ObservedEdge {
	export := fleet.K8sCaller("", "shop", "CronJob", "nightly-export")
	return []fleet.ObservedEdge{
		{From: export, To: fleet.K8sCaller("", "shop", "Deployment", "legacy-billing"), Count: 24, FirstSeen: now.Add(-26 * time.Hour), LastSeen: now.Add(-2 * time.Hour)},
		{From: export, To: "search-indexer", Count: 6, FirstSeen: now.Add(-26 * time.Hour), LastSeen: now.Add(-2 * time.Hour)},
		{From: export, To: "event-bus", Count: 12, FirstSeen: now.Add(-26 * time.Hour), LastSeen: now.Add(-2 * time.Hour)},
	}
}

// demoTakeover is a contract taking over a running workload: the experiments
// Deployment ran with no contract for days, then experiment-service's Pacto
// was created minutes before the demo loaded with a target that runs as it.
// The workload stops being an inferred service and the fleet records that
// experiment-service replaced it.
func demoTakeover(now time.Time) (fleet.RawTarget, fleet.RawInferredService) {
	created, pacto, seen := now.Add(-9*24*time.Hour), now.Add(-4*time.Minute), now.Add(-2*time.Minute)
	target := fleet.RawTarget{
		Scope: "prod", Kind: "Deployment", Name: "experiment-service", Service: "experiment-service",
		ResolvedRef: demoRegistry + "experiment-service:1.0.0", Compliance: fleet.StatusCompliant,
		Coverage: &fleet.Coverage{Evaluated: 3, Required: 3}, EvidenceAt: &seen, ReconciledAt: &seen,
		Labels:   map[string]string{"env": "production", "region": "eu-west-1", "managed-by": "platform", "app.kubernetes.io/name": "experiment-service"},
		Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: "experiments"}, CreatedAt: &pacto,
	}
	workload := fleet.RawInferredService{
		Namespace: "prod", Kind: "Deployment", Name: "experiments", ObservedAt: &seen, CreatedAt: &created,
		Image: &fleet.InferredValue{Value: "registry.example.com/growth/experiments:0.9.4", From: "spec.template.spec.containers[name=experiments].image"},
	}
	return target, workload
}
