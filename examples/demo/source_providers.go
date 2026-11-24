package main

import (
	"bytes"
	_ "embed"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// demoFingerprints are the chart's default fingerprints, pinned equal by
// TestDemoFingerprintsMatchChartDefaults, so no demo Go file names a
// promoter.
//
//go:embed fingerprints.yaml
var demoFingerprints []byte

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
		ID: fleet.ProviderPactoCRD, Name: "Pacto CRD", Evidence: "served (pacto.trianalab.io/v1)",
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

// demoPromoterRows are the promotion rows the workload source reports for a
// cluster that serves the CRD of every promoter fingerprint carrying an offer,
// shaped as internal/fleetsrc's promoterRows shapes them (that package links
// client-go, which the WASM bundle must not), with each CRD as an object.
// ponytail: the evidence names the first CRD's API group; a product whose CRDs
// span groups would list only that one.
func demoPromoterRows() ([]fleet.Provider, error) {
	fp, err := workloadmanager.ParseFingerprints(bytes.NewReader(demoFingerprints))
	if err != nil {
		return nil, err
	}
	var rows []fleet.Provider
	for _, f := range fp.Promoters {
		if f.Offer == nil || f.CRD == "" {
			continue
		}
		i := slices.IndexFunc(rows, func(p fleet.Provider) bool { return p.Name == f.Product })
		if i < 0 {
			_, group, _ := strings.Cut(f.CRD, ".")
			i, rows = len(rows), append(rows, fleet.Provider{ID: "promotion/" + f.Product, Name: f.Product,
				UsedFor: []string{fleet.UsePromotion}, Evidence: fleet.PromotionOwnedBy + f.Product + " (API group " + group + " served)"})
		}
		rows[i].Objects = append(rows[i].Objects, fleet.ProviderObject{Kind: "CustomResourceDefinition", Name: f.CRD})
		rows[i].Offers = append(rows[i].Offers, fleet.ProviderOffer(*f.Offer))
	}
	return rows, nil
}

// demoKernelEgressRow is the kernel-egress row the cluster source reports,
// shaped as internal/fleetsrc's KernelEgressRow shapes it (that package links
// client-go, which the WASM bundle must not): one of the cluster's four
// nodes runs pods without the node agent, so the calls fulfillment-service
// makes outside the cluster cannot be ruled out there.
func demoKernelEgressRow() fleet.Provider {
	return fleet.Provider{
		ID: fleet.ProviderKernelEgress, Name: "Kernel egress", Evidence: "node agent on 3/4 nodes",
		UsedFor: []string{fleet.UseExternalEgress}, Status: fleet.SourcePartial, Scope: fleet.ScopeCluster,
		Services: []fleet.ServiceKey{fleet.NewServiceKey("fulfillment-service")},
		Limitations: []fleet.Limitation{{Code: fleet.LimitationExternalsUnreported, Source: "k8s-production",
			Message: "the node agent cannot rule out an external call: a node runs pods but has no node agent (coverage: partial, node-without-agent)"}},
	}
}

// demoKernelEgressLimitation is the limitation delivery.EgressLimitations puts
// on fulfillment-service's target for the same partial coverage, so every
// external answer says its consumers may be incomplete, as on a real cluster.
func demoKernelEgressLimitation() fleet.Limitation {
	return fleet.Limitation{Code: fleet.LimitationExternalsUnreported, Source: "delivery",
		Message: "the external calls fulfillment-service makes cannot be ruled out: a node runs pods but has no node agent (coverage: partial, node-without-agent)"}
}
