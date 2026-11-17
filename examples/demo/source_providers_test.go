package main

import (
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoProviderRows: the demo cluster reports the two rows a real one
// does, the Pacto CRD from the Kubernetes source and the workloads from the
// workload source, and the workloads row counts what the demo itself holds:
// the targets the cluster reports plus the inferred workloads. The detected
// rows are TestDemoFleetHasDetectedRows's.
func TestDemoProviderRows(t *testing.T) {
	snap := demoFleet(t)
	reported := slices.DeleteFunc(slices.Clone(snap.Providers), func(p fleet.Provider) bool { return p.Source == "cluster-detection" })
	if len(reported) != 2 {
		t.Fatalf("providers = %+v, want 2 rows", reported)
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
	work, ok := rows["k8s-production-workloads/"+fleet.ProviderKubernetesWorkloads]
	if want := fleet.WorkloadEvidence(counts, nil); !ok || work.Evidence != want {
		t.Errorf("workloads row = %+v, want evidence %q", work, want)
	}
}
