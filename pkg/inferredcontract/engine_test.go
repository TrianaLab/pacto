package inferredcontract

import (
	"context"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// engineFleet is checkout calling stripe whose managed resources report the
// observed engines (empty: none).
func engineFleet(t *testing.T, engines ...string) *fleet.FleetSnapshot {
	t.Helper()
	snap := stripeFleet(t, []string{"api.stripe.com"})
	for _, e := range engines {
		snap.Externals["stripe"].Resources = append(snap.Externals["stripe"].Resources, fleet.ManagedResource{Engine: e})
	}
	return snap
}

// engineAt is the observed engines the tag ref records.
func engineAt(t *testing.T, reg publish.Registry, ref string) string {
	t.Helper()
	b, err := reg.Pull(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return engineOf(b)
}

func engineBundle(t *testing.T, version, openapi, engine string) []byte {
	t.Helper()
	snap := stripeFleet(t, []string{"api.stripe.com"})
	b, err := Bundle("stripe", Surface(snap, "stripe", now), version, inputs(Deps{Pacto: "4.7.0"}, snap), openapi, engine)
	if err != nil {
		t.Fatal(err)
	}
	return b.RawYAML
}

func TestBundleWritesEngine(t *testing.T) {
	head := "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.0.1\n  external: stripe\nmetadata:\n"
	if got, want := string(engineBundle(t, "0.0.1", "", "postgres 16")), head+"  run.pacto.external.engine: \"postgres 16\"\n"; got != want {
		t.Errorf("pacto.yaml = %q", got)
	}
	want := head + "  run.pacto.external.openapi: \"" + spec("a") + "\"\n  run.pacto.external.engine: \"mysql 8.0, postgres 16\"\n"
	if got := string(engineBundle(t, "0.0.1", spec("a"), "mysql 8.0, postgres 16")); got != want {
		t.Errorf("pacto.yaml with link = %q", got)
	}
}

func TestChangedOnEngine(t *testing.T) {
	ctx := context.Background()
	snap := stripeFleet(t, []string{"api.stripe.com"})
	b := func(version, engine string) *contract.Bundle {
		x, err := Bundle("stripe", Surface(snap, "stripe", now), version, inputs(Deps{}, snap), "", engine)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	if !Changed(ctx, b("0.0.1", "postgres 15"), b("0.0.2", "postgres 16")) || !Changed(ctx, b("0.0.1", ""), b("0.0.2", "postgres 16")) {
		t.Error("a moved engine is no change")
	}
	if Changed(ctx, b("0.0.1", "postgres 16"), b("0.0.2", "postgres 16")) {
		t.Error("the same engine is a change")
	}
}

func TestPublishOnEngineChange(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	publishOK(t, d, root, engineFleet(t, "postgres 15"))
	rep := publishOK(t, d, root, engineFleet(t, "postgres 16", "postgres 16"))
	if len(rep.Published) != 1 || rep.Published[0].Version != "0.0.2" || engineAt(t, reg, root+"/stripe:0.0.2") != "postgres 16" {
		t.Fatalf("report = %+v", rep)
	}
	// A desired-only engine is never published.
	snap := stripeFleet(t, []string{"api.stripe.com"})
	snap.Externals["stripe"].Resources = []fleet.ManagedResource{{Engine: "postgres 16"}, {EngineRequested: "postgres 17"}}
	if rep := publishOK(t, d, root, snap); len(rep.Published) != 0 || rep.Unchanged != 1 {
		t.Errorf("requested engine = %+v", rep)
	}
	// A complete run that reads no engine removes it.
	if rep := publishOK(t, d, root, engineFleet(t)); len(rep.Published) != 1 || engineAt(t, reg, root+"/stripe:0.0.3") != "" {
		t.Errorf("no engine = %+v", rep)
	}
}

func TestPartialRunKeepsEngine(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	publishOK(t, d, root, engineFleet(t, "postgres 16"))
	snap := engineFleet(t)
	snap.Completeness = fleet.CompletenessPartial
	if rep := publishOK(t, d, root, snap); len(rep.Published) != 0 || rep.Unchanged != 1 {
		t.Errorf("a partial run removed the engine: %+v", rep)
	}
	// A partial run that reads one still moves it.
	snap = engineFleet(t, "postgres 17")
	snap.Completeness = fleet.CompletenessPartial
	if rep := publishOK(t, d, root, snap); len(rep.Published) != 1 || engineAt(t, reg, root+"/stripe:0.0.2") != "postgres 17" {
		t.Errorf("partial run with an engine = %+v", rep)
	}
}

func TestRepairUsesComputedEngine(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	repo := root + "/stripe"
	// A foreign tag recording another engine is never signed.
	snap := engineFleet(t, "postgres 16")
	foreign, err := Bundle("stripe", Surface(snap, "stripe", now), "0.0.1", inputs(d, snap), "", "postgres 9")
	if err != nil {
		t.Fatal(err)
	}
	digest := pushUnsigned(t, reg, repo, "0.0.1", foreign)
	rep := publishOK(t, d, root, snap)
	if len(rep.Repaired) != 0 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, "never used") {
		t.Fatalf("report = %+v", rep)
	}
	if err := publish.VerifyDigest(context.Background(), reg, d.Verify, repo, digest); err == nil {
		t.Error("the installation key signed a foreign engine")
	}
	if engineAt(t, reg, repo+":0.0.2") != "postgres 16" {
		t.Error("the run did not publish its own engine")
	}
	// Its own crashed push is signed when the engine it computes matches.
	host, reg = testRegistry(t)
	d, root = deps(t, reg), host+"/demo/pacto-inferred"
	signer := d.Signer
	d.Signer = failSigner{}
	if rep := publishOK(t, d, root, snap); len(rep.Errors) != 1 {
		t.Fatalf("crashed push = %+v", rep)
	}
	d.Signer = signer
	if rep := publishOK(t, d, root, snap); len(rep.Repaired) != 1 || rep.Unchanged != 1 || len(rep.Published) != 0 {
		t.Errorf("repair = %+v", rep)
	}
}
