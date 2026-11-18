package main

import (
	"slices"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// demoNativeRegistry and demoTagsRegistry are what a probe of the demo's two
// registries would find: demo.pacto.local serves the Referrers API and
// _catalog; partners.demo.pacto.local serves neither, the GHCR shape, so
// records there are write-once tags. The demo probes nothing; the leaf
// capabilities package keeps oras-go out of the WASM bundle.
var (
	demoNativeRegistry = capabilities.Capabilities{Host: "demo.pacto.local", Referrers: capabilities.ReferrersNative,
		Listing: capabilities.ListingCatalog, ArtifactType: capabilities.ArtifactTypeUnknown, Auth: "bearer", CheckedAt: demoNow()}
	demoTagsRegistry = capabilities.Capabilities{Host: "partners.demo.pacto.local", Referrers: capabilities.ReferrersWriteOnceTags,
		Listing: capabilities.ListingTags, ArtifactType: capabilities.ArtifactTypeUnknown, Auth: "bearer", CheckedAt: demoNow()}
)

// demoRegistryRow is the Registry row an OCI source reports for caps, naming
// the services of revs as it names the services it read from that host.
// internal/fleetsrc links client-go, which the WASM bundle must not, so the
// row is built here; TestDemoRegistryRows pins it equal to
// fleetsrc.RegistryRow, so the wording cannot drift.
func demoRegistryRow(caps capabilities.Capabilities, revs []fleet.RawRevision) fleet.Provider {
	var services []fleet.ServiceKey
	for _, r := range revs {
		if key := fleet.NewServiceKeyDomain(r.Domain, r.Bundle.Contract.Service.Name); !slices.Contains(services, key) {
			services = append(services, key)
		}
	}
	used := []string{fleet.UseNativeReferrers, fleet.UseIndexListedByCatalog}
	if caps.Referrers != capabilities.ReferrersNative {
		used = []string{fleet.UseWriteOnceTags, fleet.UseIndexListedByTag}
	}
	at := caps.CheckedAt
	return fleet.Provider{
		ID: fleet.ProviderRegistryPrefix + caps.Host, Name: "Registry " + caps.Host,
		Evidence: caps.Evidence(), UsedFor: used, AsOf: &at,
		Details:  []string{"mode: " + caps.Mode(), "auth: " + caps.Auth, "artifactType: not checked (read-only probe)", "cosign signatures: not checked"},
		Services: services,
	}
}

// demoProviders are the rows the demo cluster's two sources detect, as the
// live ones do: the Kubernetes source's Pacto CRD row and the workload
// source's Kubernetes workloads row. The workloads row counts every workload
// the demo cluster runs, the targets plus the inferred workloads, per kind and
// namespace; pkg/fleet words its evidence, so the demo copies no wording.
func demoProviders(targets []fleet.RawTarget, inferred []fleet.RawInferredService) (crd, workloads fleet.Provider) {
	crd = fleet.Provider{
		ID: fleet.ProviderPactoCRD, Name: "Pacto CRD", Evidence: "served (pacto.trianalab.io/v1alpha1)",
		UsedFor: []string{fleet.UseDeclaredTargets},
		Objects: []fleet.ProviderObject{{Kind: "CustomResourceDefinition", Name: "pactos.pacto.trianalab.io"}},
	}
	workloads = fleet.Provider{
		ID: fleet.ProviderKubernetesWorkloads, Name: "Kubernetes workloads",
		UsedFor: []string{fleet.UseInferredServices}, Counts: []fleet.ProviderCount{},
	}
	add := func(kind, ns string) {
		for i, c := range workloads.Counts {
			if c.Kind == kind && c.Namespace == ns {
				workloads.Counts[i].N++
				return
			}
		}
		workloads.Counts = append(workloads.Counts, fleet.ProviderCount{Kind: kind, Namespace: ns, N: 1})
	}
	for _, t := range targets {
		add(t.Kind, t.Scope)
	}
	for _, in := range inferred {
		add(in.Kind, in.Namespace)
	}
	return crd, workloads
}
