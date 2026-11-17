package main

import (
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// demoGroupingProviders are the Argo CD, Flux and Helm rows the demo's
// workload source reports, counted from the demo's own groups and
// repositories as the live source counts what it listed: each distinct
// object once, per kind and namespace, with a zero count per served kind so
// a kind with no object reads as read. pkg/fleet words the evidence.
func demoGroupingProviders(inferred []fleet.RawInferredService) []fleet.Provider {
	gitops := []fleet.ProviderCount{{Kind: "Application"}, {Kind: "Kustomization"}, {Kind: "HelmRelease"}, {Kind: "GitRepository"}}
	helm := []fleet.ProviderCount{}
	seen := map[[3]string]bool{}
	add := func(counts []fleet.ProviderCount, kind, ns, name string) []fleet.ProviderCount {
		if seen[[3]string{kind, ns, name}] {
			return counts
		}
		seen[[3]string{kind, ns, name}] = true
		for i, c := range counts {
			if c.Kind == kind && c.Namespace == ns && c.N > 0 {
				counts[i].N++
				return counts
			}
		}
		return append(counts, fleet.ProviderCount{Kind: kind, Namespace: ns, N: 1})
	}
	for _, in := range inferred {
		if g := in.Group; g != nil {
			switch g.Rule {
			case workloadmanager.ArgoCD:
				gitops = add(gitops, "Application", g.Namespace, g.Name)
			case workloadmanager.FluxKustomization:
				gitops = add(gitops, "Kustomization", g.Namespace, g.Name)
			case workloadmanager.FluxHelmRelease:
				gitops = add(gitops, "HelmRelease", g.Namespace, g.Name)
			case workloadmanager.Helm:
				helm = add(helm, "Helm release", g.Namespace, g.Name)
			}
		}
		for _, r := range in.Repositories {
			if r.Kind == "GitRepository" {
				// The demo knows a GitRepository by its namespace and URL.
				gitops = add(gitops, r.Kind, r.Namespace, r.URL)
			}
		}
	}
	crd := func(name string) fleet.ProviderObject {
		return fleet.ProviderObject{Kind: "CustomResourceDefinition", Name: name}
	}
	both := []string{fleet.UseServiceGrouping, fleet.UseRepositoryProvenance}
	return []fleet.Provider{
		{ID: fleet.ProviderArgoCD, Name: "Argo CD", UsedFor: both, Counts: kinds(gitops, "Application"),
			Objects: []fleet.ProviderObject{crd("applications.argoproj.io")}},
		{ID: fleet.ProviderFlux, Name: "Flux", UsedFor: both, Counts: kinds(gitops, "Kustomization", "HelmRelease", "GitRepository"),
			Objects: []fleet.ProviderObject{crd("kustomizations.kustomize.toolkit.fluxcd.io"), crd("helmreleases.helm.toolkit.fluxcd.io"), crd("gitrepositories.source.toolkit.fluxcd.io")}},
		{ID: fleet.ProviderHelm, Name: "Helm", UsedFor: []string{fleet.UseServiceGrouping}, Counts: helm},
	}
}

// kinds is the counts of the named kinds.
func kinds(counts []fleet.ProviderCount, want ...string) []fleet.ProviderCount {
	var out []fleet.ProviderCount
	for _, c := range counts {
		for _, k := range want {
			if c.Kind == k {
				out = append(out, c)
			}
		}
	}
	return out
}
