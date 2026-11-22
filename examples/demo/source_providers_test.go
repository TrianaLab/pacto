package main

import (
	"bytes"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
	"gopkg.in/yaml.v3"
)

// TestDemoProviderRows: the demo cluster reports the six rows a real one
// does, beside the reporting adapter rows of its provisioning reports, the
// Pacto CRD from the Kubernetes source and the workloads, Argo CD, Flux,
// Helm and Kargo promotion rows from the workload source (the promotion row
// is TestDemoKargoRowOffersTheTemplate's), and the workloads row counts
// what the demo itself holds: the targets the cluster reports plus the
// inferred workloads. The detected rows are TestDemoFleetHasDetectedRows's,
// the registry rows TestDemoRegistryRows's; the grouping rows' evidence is
// TestDemoGroups's.
func TestDemoProviderRows(t *testing.T) {
	snap := demoFleet(t)
	reported, reporting := splitReporting(snap.Providers)
	if len(reported) != 6 || len(reporting) != 5 {
		t.Fatalf("providers = %+v, reporting = %+v, want 6 and 5 rows", reported, reporting)
	}
	rows := map[string]fleet.Provider{}
	for _, p := range reported {
		if p.Cluster != "production" || p.Status != fleet.SourceAvailable {
			t.Errorf("row %s/%s: cluster %q status %q", p.Source, p.ID, p.Cluster, p.Status)
		}
		rows[p.Source+"/"+p.ID] = p
	}
	crd, ok := rows["k8s-production/"+fleet.ProviderPactoCRD]
	if !ok || crd.Evidence != "served (pacto.trianalab.io/v1alpha1)" {
		t.Errorf("crd row = %+v", crd)
	}
	for _, id := range []string{fleet.ProviderArgoCD, fleet.ProviderFlux, fleet.ProviderHelm} {
		row, ok := rows["k8s-production-workloads/"+id]
		if !ok {
			t.Errorf("no %s row", id)
		}
		// Only the Argo CD row offers a gate (516-S6); the demo copies no wording.
		if wantOffer := id == fleet.ProviderArgoCD; (len(row.Offers) == 1 && row.Offers[0].Doc == fleet.ArgoSyncGateDoc) != wantOffer {
			t.Errorf("row %s offers = %+v", id, row.Offers)
		}
	}
	work, ok := rows["k8s-production-workloads/"+fleet.ProviderKubernetesWorkloads]
	if want := fleet.WorkloadEvidence(demoWorkloadCounts(snap), nil); !ok || work.Evidence != want {
		t.Errorf("workloads row = %+v, want evidence %q", work, want)
	}
}

// TestDemoKargoRowOffersTheTemplate: the workload source reports the Kargo
// promotion row the chart's default fingerprints give when the cluster serves
// both Kargo CRDs, with one offer of the shared AnalysisTemplate after the
// merge by id.
func TestDemoKargoRowOffersTheTemplate(t *testing.T) {
	snap := demoFleet(t)
	i := slices.IndexFunc(snap.Providers, func(p fleet.Provider) bool { return p.ID == "promotion/Kargo" })
	if i < 0 {
		t.Fatalf("no Kargo promotion row in %+v", snap.Providers)
	}
	got := snap.Providers[i]
	want := []fleet.ProviderOffer{{ID: "kargo-analysis-template", Text: "Gate every Kargo promotion with one shared AnalysisTemplate",
		Doc: "https://pacto.run/latest/integrations/kubernetes/gitops-kargo/#kargo"}}
	if got.Source != "k8s-production-workloads" || got.Name != "Kargo" || got.Status != fleet.SourceAvailable ||
		got.Evidence != "promotion owned by Kargo (API group kargo.akuity.io served)" ||
		!slices.Equal(got.UsedFor, []string{fleet.UsePromotion}) || !reflect.DeepEqual(got.Offers, want) {
		t.Errorf("Kargo row = %+v", got)
	}
	objects := []fleet.ProviderObject{{Kind: "CustomResourceDefinition", Name: "stages.kargo.akuity.io"}, {Kind: "CustomResourceDefinition", Name: "warehouses.kargo.akuity.io"}}
	if !reflect.DeepEqual(got.Objects, objects) {
		t.Errorf("Kargo objects = %+v, want %+v", got.Objects, objects)
	}
	// Only the promoters with an offer are served in the demo.
	for _, p := range snap.Providers {
		if slices.Contains(p.UsedFor, fleet.UsePromotion) && p.ID != got.ID {
			t.Errorf("unexpected promotion row %+v", p)
		}
	}
}

// TestDemoFingerprintsMatchChartDefaults: the demo's fingerprints are the
// chart's default fingerprints value, so the demo row is what an installation
// at chart defaults reports.
func TestDemoFingerprintsMatchChartDefaults(t *testing.T) {
	raw, err := os.ReadFile("../../integrations/kubernetes/charts/pacto-operator/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Fingerprints yaml.Node `yaml:"fingerprints"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	chartYAML, err := yaml.Marshal(&values.Fingerprints)
	if err != nil {
		t.Fatal(err)
	}
	chart, err := workloadmanager.ParseFingerprints(bytes.NewReader(chartYAML))
	if err != nil {
		t.Fatal(err)
	}
	demo, err := workloadmanager.ParseFingerprints(bytes.NewReader(demoFingerprints))
	if err != nil {
		t.Fatal(err)
	}
	if len(chart.Promoters) == 0 || !reflect.DeepEqual(demo, chart) {
		t.Errorf("demo fingerprints = %+v, chart defaults = %+v", demo, chart)
	}
}

// splitReporting drops the detected and registry rows and splits the rest into the
// reported rows and the reporting adapter rows: the provisioning reports the
// demo holds add one reporting adapter row each, named by the source that
// delivered them.
func splitReporting(providers []fleet.Provider) (reported, reporting []fleet.Provider) {
	for _, p := range providers {
		switch {
		case p.Source == "cluster-detection", strings.HasPrefix(p.ID, fleet.ProviderRegistryPrefix):
		case slices.Equal(p.UsedFor, []string{fleet.UseReporting}):
			reporting = append(reporting, p)
		default:
			reported = append(reported, p)
		}
	}
	return reported, reporting
}

// demoWorkloadCounts counts what the demo holds per kind: the targets the
// cluster reports plus the inferred workloads.
func demoWorkloadCounts(snap *fleet.FleetSnapshot) map[string]int {
	counts := map[string]int{}
	for _, tr := range snap.Targets {
		if slices.Contains(tr.Sources, "k8s-production") {
			counts[tr.Kind]++
		}
		if kind, _, ok := strings.Cut(tr.Name, "/"); ok && tr.Link != nil {
			counts[kind]++ // a linked target is named <Kind>/<name> after its workload
		}
	}
	for _, s := range snap.Services {
		if s.Inferred() {
			counts[s.Workload.Kind]++
		}
	}
	// A workload a declaration replaced still runs; a Pacto target's or a
	// linked target's is counted as its target above.
	for _, r := range snap.Replacements {
		if r.Link != "pacto-target" && r.Link != "observed" && r.Workload != nil {
			counts[r.Workload.Kind]++
		}
	}
	return counts
}

// TestDemoRegistryRows: each demo registry reports the row its probe would,
// one native with _catalog and one without Referrers or _catalog (the GHCR
// shape), from the source that reads that registry and naming the services
// read from it.
func TestDemoRegistryRows(t *testing.T) {
	snap := demoFleet(t)
	cases := []struct {
		id, source, evidence string
		caps                 capabilities.Capabilities
		usedFor              []string
	}{
		{fleet.ProviderRegistryPrefix + "demo.pacto.local", "local", "Referrers, _catalog", demoNativeRegistry,
			[]string{fleet.UseNativeReferrers, fleet.UseIndexListedByCatalog}},
		{fleet.ProviderRegistryPrefix + "partners.demo.pacto.local", "partner-registry", "no Referrers, no _catalog", demoTagsRegistry,
			[]string{fleet.UseWriteOnceTags, fleet.UseIndexListedByTag}},
	}
	for _, tc := range cases {
		i := slices.IndexFunc(snap.Providers, func(p fleet.Provider) bool { return p.ID == tc.id })
		if i < 0 {
			t.Fatalf("no row %s in %+v", tc.id, snap.Providers)
		}
		got := snap.Providers[i]
		if got.Source != tc.source || got.Evidence != tc.evidence || got.Status != fleet.SourceAvailable || !slices.Equal(got.UsedFor, tc.usedFor) {
			t.Errorf("row %s = %+v", tc.id, got)
		}
		var services []fleet.ServiceKey
		for _, r := range snap.Revisions {
			if strings.HasPrefix(r.ResolvedRef, "oci://"+tc.caps.Host+"/") && !slices.Contains(services, r.ServiceKey) {
				services = append(services, r.ServiceKey)
			}
		}
		want := fleetsrc.RegistryRow(tc.caps, services)
		want.Source, want.Status = tc.source, fleet.SourceAvailable
		slices.Sort(got.Services)
		slices.Sort(want.Services)
		if len(services) == 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("row %s\n got %+v\nwant %+v", tc.id, got, want)
		}
	}
}
