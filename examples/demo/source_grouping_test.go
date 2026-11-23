package main

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoGroups: the demo groups its inferred services as a cluster with
// Argo CD, Flux and Helm would. legacy-billing and billing-worker share one
// Argo CD Application and its repository, the group filter returns exactly
// those two, log-shipper names the Flux GitRepository its HelmRelease reads,
// nightly-export is a plain Helm release with no repository, and the workload
// source reports the three provider rows (testDemoGroupRows).
func TestDemoGroups(t *testing.T) {
	snap := demoFleet(t)
	byName := map[string]*fleet.InferredWorkload{}
	for _, s := range snap.Services {
		if s.Inferred() {
			byName[s.Workload.Name] = s.Workload
		}
	}
	testDemoArgoGroup(t, snap, byName)
	if w := byName["log-shipper"]; w == nil || len(w.Repositories) != 1 || w.Repositories[0].From != "GitRepository flux-system/platform spec.url" {
		t.Errorf("log-shipper: %+v", w)
	}
	if w := byName["nightly-export"]; w == nil || w.Group.Label != "Helm release shop/exporter" || len(w.Repositories) != 0 {
		t.Errorf("nightly-export: %+v", w)
	}
	testDemoGroupRows(t, snap)
}

// testDemoArgoGroup: legacy-billing and billing-worker share the Argo CD
// Application shop/shop and its repository, and the group filter returns
// exactly those two.
func testDemoArgoGroup(t *testing.T, snap *fleet.FleetSnapshot, byName map[string]*fleet.InferredWorkload) {
	t.Helper()
	for _, name := range []string{"legacy-billing", "billing-worker"} {
		w := byName[name]
		if w == nil || w.Group == nil || w.Group.Key != prodClusterID+"/argocd/shop/shop" || w.Group.Label != "Argo CD Application shop/shop" {
			t.Fatalf("%s: %+v", name, w)
		}
		if len(w.Repositories) != 1 || w.Repositories[0].URL != "https://github.com/example/shop" || w.Repositories[0].Path != "deploy/legacy" {
			t.Errorf("%s repositories = %+v", name, w.Repositories)
		}
	}
	l, err := fleet.NewQuery(snap).Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindService}, Group: prodClusterID + "/argocd/shop/shop"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range l.Entities {
		got[e.Label] = true
	}
	if l.Total != 2 || !got["k8s:"+prodClusterID+"/shop/Deployment/legacy-billing"] || !got["k8s:"+prodClusterID+"/shop/Deployment/billing-worker"] {
		t.Errorf("group filter = %+v", l.Entities)
	}
}

// testDemoGroupRows: the workload source reports the Argo CD, Flux and Helm
// rows, available, with evidence pkg/fleet words from their counts.
func testDemoGroupRows(t *testing.T, snap *fleet.FleetSnapshot) {
	t.Helper()
	want := map[string]string{fleet.ProviderArgoCD: "1 Application", fleet.ProviderFlux: "1 GitRepository, 1 HelmRelease", fleet.ProviderHelm: "1 Helm release"}
	for _, p := range snap.Providers {
		ev, ok := want[p.ID]
		if !ok || p.Source != "k8s-production-workloads" {
			continue
		}
		delete(want, p.ID)
		if p.Status != fleet.SourceAvailable || p.Evidence != ev {
			t.Errorf("row %s: status %q evidence %q, want %q", p.ID, p.Status, p.Evidence, ev)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing rows %v", want)
	}
}
