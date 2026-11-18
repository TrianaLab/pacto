package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// TestDemoProviderRows: the demo cluster reports the two rows a real one
// does, the Pacto CRD from the Kubernetes source and the workloads from the
// workload source, and the workloads row counts what the demo itself holds:
// the targets the cluster reports plus the inferred workloads. The detected
// rows are TestDemoFleetHasDetectedRows's, the registry rows
// TestDemoRegistryRows's.
func TestDemoProviderRows(t *testing.T) {
	snap := demoFleet(t)
	reported := slices.DeleteFunc(slices.Clone(snap.Providers), func(p fleet.Provider) bool {
		return p.Source == "cluster-detection" || strings.HasPrefix(p.ID, fleet.ProviderRegistryPrefix)
	})
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
