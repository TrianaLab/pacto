package delivery

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

const inferredStripeRepo = "reg.example/acme/pacto-inferred/stripe"

// inferredStripe is stripe's inferred contract at version: no configuration,
// no range anyone can check.
func inferredStripe(version string) *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.5",
		Service: contract.Service{Name: "stripe", Version: version, External: "stripe"}}, FS: fstest.MapFS{}}
}

// pinStripe is a dependency on stripe's inferred contract with rng.
func pinStripe(rng string) contract.Dependency {
	return contract.Dependency{Name: "stripe", Ref: "oci://" + inferredStripeRepo, Compatibility: rng, Required: true}
}

// inferredStripeFleet is a fleet holding the operator-signed stripe inferred
// contract at 2023.10.16 and checkout, which pins ^2023.10 on it.
func inferredStripeFleet(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	col := &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: inferredStripe("2023.10.16"), ResolvedRef: inferredStripeRepo + ":2023.10.16", Digest: "sha256:stripe",
			Provenance: &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified, Source: fleet.SignatureSourceOperator, Bundles: 1}}},
		{Bundle: bundle("checkout", "1.0.0", pinStripe("^2023.10")), Digest: "sha256:checkout"},
	}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// TestInferredProviderUnverifiedOnEveryGate: a ^2023.10 range on stripe's
// inferred contract, whose 2023.10.16 satisfies it, is never compatible,
// incompatible or clear: the fleet reads it unknown, impact unverified, the
// move-first check an unknown error and the environment gate Unknown.
func TestInferredProviderUnverifiedOnEveryGate(t *testing.T) {
	snap := inferredStripeFleet(t)
	for _, gate := range []struct {
		name  string
		check func(*testing.T, *fleet.FleetSnapshot)
	}{
		{"fleet range", fleetRangeUnknown},
		{"impact", impactUnverified},
		{"move first", moveFirstUnknown},
		{"environment", environmentUnknown},
	} {
		t.Run(gate.name, func(t *testing.T) { gate.check(t, snap) })
	}
}

// fleetRangeUnknown: the one edge from checkout reads unknown, INFERRED_PROVIDER.
func fleetRangeUnknown(t *testing.T, snap *fleet.FleetSnapshot) {
	seen := 0
	for _, rel := range snap.Relationships {
		if rel.FromService != "checkout" || rel.To != "stripe" {
			continue
		}
		seen++
		if rel.RangeStatus != fleet.RangeUnknown || rel.RangeReason != fleet.RangeReasonInferredProvider || rel.ToExternal != "stripe" {
			t.Errorf("edge = %+v", rel)
		}
	}
	if seen != 1 {
		t.Errorf("%d edges from checkout to stripe", seen)
	}
}

// impactUnverified: a new stripe revision gates unverified, checkout unknown.
func impactUnverified(t *testing.T, snap *fleet.FleetSnapshot) {
	res := impact.Analyze(context.Background(), inferredStripe("2023.10.15").Contract, inferredStripe("2023.10.16").Contract,
		fstest.MapFS{}, fstest.MapFS{}, snap, impact.Options{Domain: "reg.example/acme/pacto-inferred", InferredProvider: "stripe"})
	if res.Gate != impact.GateUnverified || res.GateCode() != impact.GateCodeInferredProvider ||
		len(res.Consumers) != 1 || res.Consumers[0].CompatibilityVerdict != impact.CompatibilityUnknown {
		t.Errorf("impact = %s %s %+v", res.Gate, res.GateCode(), res.Consumers)
	}
}

// moveFirstUnknown: a checkout candidate that pins the range is an unknown error.
func moveFirstUnknown(t *testing.T, snap *fleet.FleetSnapshot) {
	why, err := acceptsWhatRunsHere(snap, bundle("checkout", "1.1.0", pinStripe("^2023.10")).Contract, "payments")
	if why != "" || err == nil || !strings.Contains(err.Error(), "stripe is an inferred contract the operator published") {
		t.Errorf("acceptsWhatRunsHere = %q %v", why, err)
	}
}

// environmentUnknown: values for a configuration whose ref names the
// inferred contract, which declares none, are Unknown for a person to accept.
func environmentUnknown(t *testing.T, _ *fleet.FleetSnapshot) {
	b := envBundle(map[string]string{"prod": "prod.yaml"}, map[string]string{"prod.yaml": "app:\n  replicas: 3\n"}, false)
	b.Contract.Configurations[0].Ref = "oci://" + inferredStripeRepo + ":2023.10.16"
	to := ResolveRevision(t.Context(), mapRegistry{"r/consumer@" + c1: b, inferredStripeRepo + ":2023.10.16": inferredStripe("2023.10.16")}, "r/a", dB)
	to.ContractRef = rev(dB, c2, b).ContractRef
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, rev(dA, c1, bundle("payments", "1.0.0")), to)
	if v := verdict(t, p, GateEnvironment); v.Result != Unknown || p.Floor.Decision != AcceptUnknown {
		t.Errorf("environment = %+v, floor %+v", v, p.Floor)
	}
}

// inferredHere is a delivery snapshot where a stripe at version runs.
func inferredHere(version string) *fleet.FleetSnapshot {
	return &fleet.FleetSnapshot{Completeness: fleet.CompletenessComplete, Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{
		"stripe@sha256:s": {Service: "stripe", Version: version, ResolvedRef: inferredStripeRepo + ":" + version},
	}}
}

// TestAcceptsWhatRunsHereInferredRefUnknown: whether the stripe running here
// satisfies the range or fails it, a range on its inferred contract is
// unknown, never "accepts X but Y runs here" and never a pass.
func TestAcceptsWhatRunsHereInferredRefUnknown(t *testing.T) {
	for _, running := range []string{"2023.10.16", "2024.01.01"} {
		why, err := acceptsWhatRunsHere(inferredHere(running), bundle("checkout", "1.1.0", pinStripe("^2023.10")).Contract, "payments")
		if why != "" || err == nil || !strings.Contains(err.Error(), "Pacto never checks a range against it") {
			t.Errorf("%s runs here: %q %v", running, why, err)
		}
	}
}

// TestAcceptsBothInferredRefUnknown: a range on the provider's inferred
// contract accepts neither side, with the unknown error.
func TestAcceptsBothInferredRefUnknown(t *testing.T) {
	rng, ok, err := acceptsBoth(bundle("checkout", "1.1.0", pinStripe("^2023.10")).Contract, "stripe", "2023.10.17", "2023.10.16")
	if rng != "^2023.10" || ok || err == nil || !strings.Contains(err.Error(), "stripe is an inferred contract") {
		t.Errorf("acceptsBoth = %q %v %v", rng, ok, err)
	}
}
