package main

import (
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoProviderRows: the demo cluster reports the five rows a real one
// does, beside the reporting adapter rows of its provisioning reports, the Pacto CRD from the Kubernetes source and the workloads, Argo CD,
// Flux and Helm rows from the workload source, and the workloads row counts
// what the demo itself holds: the targets the cluster reports plus the
// inferred workloads. The detected rows are TestDemoFleetHasDetectedRows's;
// the grouping rows' evidence is TestDemoGroups's.
func TestDemoProviderRows(t *testing.T) {
	snap := demoFleet(t)
	reported := slices.DeleteFunc(slices.Clone(snap.Providers), func(p fleet.Provider) bool { return p.Source == "cluster-detection" })
	// The provisioning reports the demo holds add one reporting adapter row
	// each, named by the source that delivered them.
	reporting := slices.DeleteFunc(slices.Clone(reported), func(p fleet.Provider) bool { return !slices.Equal(p.UsedFor, []string{fleet.UseReporting}) })
	reported = slices.DeleteFunc(reported, func(p fleet.Provider) bool { return slices.Equal(p.UsedFor, []string{fleet.UseReporting}) })
	if len(reported) != 5 || len(reporting) != 4 {
		t.Fatalf("providers = %+v, reporting = %+v, want 5 and 4 rows", reported, reporting)
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
	counts := map[string]int{}
	for _, tr := range snap.Targets {
		if slices.Contains(tr.Sources, "k8s-production") {
			counts[tr.Kind]++
		}
	}
	for _, s := range snap.Services {
		if s.Inferred() {
			counts[s.Workload.Kind]++
		}
	}
	for _, id := range []string{fleet.ProviderArgoCD, fleet.ProviderFlux, fleet.ProviderHelm} {
		if _, ok := rows["k8s-production-workloads/"+id]; !ok {
			t.Errorf("no %s row", id)
		}
	}
	work, ok := rows["k8s-production-workloads/"+fleet.ProviderKubernetesWorkloads]
	if want := fleet.WorkloadEvidence(counts, nil); !ok || work.Evidence != want {
		t.Errorf("workloads row = %+v, want evidence %q", work, want)
	}
}
