package main

import "github.com/trianalab/pacto/v4/pkg/fleet"

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
