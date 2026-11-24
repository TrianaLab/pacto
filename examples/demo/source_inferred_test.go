package main

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
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

// TestDemoInferredStripeProvider: the registry mirror holds stripe's two
// inferred contracts, which show as the provider of the stripe external with
// 0.0.2 first, its surface, no signature and an unchecked change of the
// linked OpenAPI digest, and never as a service.
func TestDemoInferredStripeProvider(t *testing.T) {
	snap := demoFleet(t)
	rec := snap.Externals["stripe"]
	if rec == nil || rec.Provider == nil {
		t.Fatalf("stripe external = %+v, want an inferred contract provider", rec)
	}
	p := rec.Provider
	if p.Repository != "registry.pacto.demo/demo/pacto-inferred/stripe" || len(p.Revisions) != 2 {
		t.Fatalf("provider = %+v", p)
	}
	r := p.Revisions[0]
	if r.Version != "0.0.2" || r.Signature != nil || r.Surface == nil || len(r.Surface.Hosts) == 0 || r.Surface.Hosts[0].Value != "api.stripe.com:443" {
		t.Errorf("revision = %+v, want unsigned 0.0.2 with the api.stripe.com:443 host", r)
	}
	checkDemoStripeChange(t, p)
	for _, s := range snap.Services {
		if s.Name == "stripe" {
			t.Errorf("stripe's inferred contract became the service %s", s.Key)
		}
	}
}

// checkDemoStripeChange: the older revision is unsigned 0.0.1 and the change
// is an unchecked move of the linked OpenAPI digest only.
func checkDemoStripeChange(t *testing.T, p *fleet.ExternalProvider) {
	t.Helper()
	if o := p.Revisions[1]; o.Version != "0.0.1" || o.Signature != nil {
		t.Errorf("older revision = %+v, want unsigned 0.0.1", o)
	}
	c := p.Change
	if c == nil || c.Checked || c.From != "0.0.1" || c.To != "0.0.2" ||
		c.OpenAPIFrom != demoStripeOpenAPI["0.0.1"] || c.OpenAPITo != demoStripeOpenAPI["0.0.2"] || len(c.Added)+len(c.Removed) != 0 {
		t.Errorf("change = %+v, want an unchecked 0.0.1 -> 0.0.2 OpenAPI digest move", c)
	}
}

// TestDemoInferredMatchesPublisher: each hand-built inferred contract of the
// demo is byte for byte what inferredcontract.Bundle publishes for the same
// surface and OpenAPI link.
func TestDemoInferredMatchesPublisher(t *testing.T) {
	for _, v := range []string{"0.0.1", "0.0.2"} {
		got := demoInferredRevision(v).Bundle
		want, err := inferredcontract.Bundle("stripe", demoInferredSurface(v), v,
			contract.InferredInputs{Pacto: "demo", Discovery: inferredcontract.Version, Sources: []contract.InferredSourceFile{}}, demoStripeOpenAPI[v])
		if err != nil {
			t.Fatal(err)
		}
		if string(got.RawYAML) != string(want.RawYAML) {
			t.Errorf("%s pacto.yaml = %q, want %q", v, got.RawYAML, want.RawYAML)
		}
		g, _ := fs.ReadFile(got.FS, contract.InferredRecordFile)
		w, _ := fs.ReadFile(want.FS, contract.InferredRecordFile)
		if string(g) != string(w) {
			t.Errorf("%s record = %s, want %s", v, g, w)
		}
	}
}

// TestDemoUncheckedChangeRaisesNoAttention: no source checked the demo
// revisions' signatures, so their change raises no EXTERNAL_CHANGED item.
func TestDemoUncheckedChangeRaisesNoAttention(t *testing.T) {
	l, err := fleet.NewQuery(demoFleet(t)).Attention(fleet.AttentionFilter{Limit: fleet.MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range l.Items {
		if it.Code == fleet.AttentionExternalChanged {
			t.Errorf("an unchecked change raised %+v", it)
		}
	}
}

// TestDemoImpactChangedExternalStripe: a change of stripe leaves the gate
// unverified and puts every service that uses it in the Attention lane.
func TestDemoImpactChangedExternalStripe(t *testing.T) {
	snap := demoFleet(t)
	res := impact.Analyze(context.Background(), nil, nil, nil, nil, snap,
		impact.Options{Event: &impact.Event{Kind: impact.EventChanged, External: "stripe"}})
	if res.Gate != impact.GateUnverified || res.External == nil || res.External.Change == nil || res.External.Change.Checked {
		t.Fatalf("gate = %s, external = %+v, want unverified with an unchecked change", res.Gate, res.External)
	}
	// A demo consumer pins no range, so the answer offers the ref form over the inferred contract.
	if !strings.Contains(res.Pin, "ref: oci://"+demoInferredRepository+"\n") {
		t.Errorf("pin = %q, want the ref form over %s", res.Pin, demoInferredRepository)
	}
	want := map[string]bool{}
	for _, s := range snap.Externals["stripe"].Sources {
		if s.Service != "" {
			want[string(s.Service)] = true
		}
	}
	if len(want) == 0 || len(res.Consumers) != len(want) {
		t.Fatalf("consumers = %+v, want one per stripe user %v", res.Consumers, want)
	}
	for _, c := range res.Consumers {
		if c.Effect != impact.EffectAttention || !want[c.Service] {
			t.Errorf("consumer %s effect = %s, want attention", c.Service, c.Effect)
		}
	}
}
