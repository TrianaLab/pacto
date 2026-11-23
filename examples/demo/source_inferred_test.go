package main

import (
	"io/fs"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/inferredcontract"
)

// TestDemoHasInferredServices: the demo reads four workloads no contract
// describes (beside acme-idp's contribution, TestDemoContributionIsInferred's), and the kernel-style caller legacy-billing stays an uncontracted
// caller of payments-service that links to its inferred entity.
func TestDemoHasInferredServices(t *testing.T) {
	snap := demoFleet(t)
	want := map[string]string{
		"k8s:" + prodClusterID + "/shop/Deployment/legacy-billing":      "shop",
		"k8s:" + prodClusterID + "/shop/Deployment/billing-worker":      "shop",
		"k8s:" + prodClusterID + "/shop/CronJob/nightly-export":         "shop",
		"k8s:" + prodClusterID + "/observability/DaemonSet/log-shipper": "observability",
	}
	inferred := 0
	for _, s := range snap.Services {
		if !s.Inferred() || s.Contribution != nil {
			continue
		}
		inferred++
		ns, ok := want[s.Name]
		if !ok || s.Workload == nil || s.Workload.Namespace != ns || s.Workload.Image == nil {
			t.Errorf("inferred service %s: %+v", s.Name, s.Workload)
		}
	}
	if inferred != len(want) {
		t.Errorf("inferred services = %d, want %d", inferred, len(want))
	}
	billing := fleet.NewServiceKey("k8s:" + prodClusterID + "/shop/Deployment/legacy-billing")
	found := false
	for _, c := range snap.UncontractedCallers {
		if c.Caller == "k8s:"+prodClusterID+"/shop/Deployment/legacy-billing" {
			found = c.ToService == fleet.NewServiceKey("payments-service") && c.Entity == billing
		}
	}
	if !found {
		t.Errorf("legacy-billing is not an uncontracted caller of payments-service with its entity: %+v", snap.UncontractedCallers)
	}
}

// TestDemoInferredStripeProvider: the registry mirror holds stripe's inferred
// contract, which shows as the provider of the stripe external with its
// surface and no signature, and never as a service.
func TestDemoInferredStripeProvider(t *testing.T) {
	snap := demoFleet(t)
	rec := snap.Externals["stripe"]
	if rec == nil || rec.Provider == nil {
		t.Fatalf("stripe external = %+v, want an inferred contract provider", rec)
	}
	p := rec.Provider
	if p.Repository != "registry.pacto.demo/demo/pacto-inferred/stripe" || len(p.Revisions) != 1 {
		t.Fatalf("provider = %+v", p)
	}
	r := p.Revisions[0]
	if r.Version != "0.0.1" || r.Signature != nil || r.Surface == nil || len(r.Surface.Hosts) == 0 || r.Surface.Hosts[0].Value != "api.stripe.com:443" {
		t.Errorf("revision = %+v, want unsigned 0.0.1 with the api.stripe.com:443 host", r)
	}
	for _, s := range snap.Services {
		if s.Name == "stripe" {
			t.Errorf("stripe's inferred contract became the service %s", s.Key)
		}
	}
}

// TestDemoInferredMatchesPublisher: the demo's hand-built inferred contract
// is byte for byte what inferredcontract.Bundle publishes for the same
// surface.
func TestDemoInferredMatchesPublisher(t *testing.T) {
	got := demoInferredRevision().Bundle
	want, err := inferredcontract.Bundle("stripe", demoInferredSurface(), "0.0.1",
		contract.InferredInputs{Pacto: "demo", Discovery: inferredcontract.Version, Sources: []contract.InferredSourceFile{}})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.RawYAML) != string(want.RawYAML) {
		t.Errorf("pacto.yaml = %q, want %q", got.RawYAML, want.RawYAML)
	}
	g, _ := fs.ReadFile(got.FS, contract.InferredRecordFile)
	w, _ := fs.ReadFile(want.FS, contract.InferredRecordFile)
	if string(g) != string(w) {
		t.Errorf("record = %s, want %s", g, w)
	}
}
