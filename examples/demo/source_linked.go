package main

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// This file holds what the demo cluster's operator linked with no Pacto. It
// has no build tag, so host tests, the transcripts and the WASM demo read one
// snapshot.

// demoLinkedDigest is the image digest the recommender Deployment runs, which
// the signed images record of recommendations 1.1.0 holds.
const demoLinkedDigest = "sha256:7d3e9a1c5b2f8e4d6a0c9b7e3f1d5a8c2e6b4f0a9d7c3e1b5f8a2d6c4e0b9f71"

// demoLinkedAnnotation is the pacto.trianalab.io/contract value the campaigns
// Deployment's own chart sets.
const demoLinkedAnnotation = "oci://demo.pacto.local/campaign-service:1.1.0"

// demoLinked is what the operator's linker wrote for the growth namespace:
// two workloads it linked to a published contract with no Pacto CR, and the
// two workloads themselves as the workload reader lists them. recommender ran
// uncontracted for 20 minutes before its image digest linked it, so the fleet
// records that recommendations replaced it; campaigns carried the annotation
// from its first rollout, so it is handed over with no replacement.
func demoLinked(src *EmbedSource, now time.Time) ([]fleet.RawTarget, []fleet.RawInferredService) {
	seen := now.Add(-time.Minute)
	recSince, recCreated := now.Add(-12*time.Minute), now.Add(-32*time.Minute)
	campSince, campCreated := now.Add(-6*time.Minute), now.Add(-7*time.Minute)
	linked := func(name, service, version string, link fleet.ObservedLink) fleet.RawTarget {
		digest := demoDigest(src, service, version)
		return fleet.RawTarget{
			Scope: "growth", Kind: fleet.SourceKindKubernetes, Name: "Deployment/" + name, Service: service,
			ResolvedRef: demoRegistry + service + "@" + digest, Digest: digest, Compliance: fleet.StatusUnknown,
			EvidenceAt: &seen, ReconciledAt: &seen,
			Workload: &fleet.WorkloadRef{Kind: "Deployment", Name: name}, Link: &link,
		}
	}
	targets := []fleet.RawTarget{
		linked("recommender", "recommendations", "1.1.0", fleet.ObservedLink{Signal: fleet.LinkSignalImageDigest, Value: demoLinkedDigest, Since: &recSince}),
		linked("campaigns", "campaign-service", "1.1.0", fleet.ObservedLink{Signal: fleet.LinkSignalAnnotation, Value: demoLinkedAnnotation, Since: &campSince}),
	}
	pod := "spec.template.spec.containers[name="
	workloads := []fleet.RawInferredService{
		{Namespace: "growth", Kind: "Deployment", Name: "recommender", ObservedAt: &seen, CreatedAt: &recCreated,
			Image: &fleet.InferredValue{Value: "registry.example.com/growth/recommender@" + demoLinkedDigest, From: pod + "recommender].image"}},
		{Namespace: "growth", Kind: "Deployment", Name: "campaigns", ObservedAt: &seen, CreatedAt: &campCreated,
			Image: &fleet.InferredValue{Value: "registry.example.com/growth/campaigns:1.1.0", From: pod + "campaigns].image"}},
	}
	return targets, workloads
}
