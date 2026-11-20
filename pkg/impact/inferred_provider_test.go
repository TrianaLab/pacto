package impact

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const stripeInferred = "ghcr.io/acme/pacto-inferred/stripe"

// operatorSigned is the provenance of a revision only this installation's
// operator signed.
func operatorSigned() *fleet.BundleProvenance {
	return &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified, Source: fleet.SignatureSourceOperator, Bundles: 1}, Source: "oci"}
}

// stripeContract is stripe's contract at version: inferred under
// pacto-inferred, or claimed elsewhere, naming external stripe either way.
func stripeContract(version string) *contract.Contract {
	return &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "stripe", Version: version, External: "stripe"}}
}

// withSurface is a bundle of c whose inferred record carries a stripe surface.
func withSurface(c *contract.Contract) *contract.Bundle {
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"}, Fields: []contract.Inferred{},
		Surface: &contract.InferredSurface{Identity: "stripe", Class: "vendor-api", AsOf: "2026-07-29T12:00:00Z", Completeness: &contract.SurfaceCompleteness{State: "complete"},
			Hosts: []contract.SurfaceValue{{Value: "api.stripe.com:443", Sources: []string{"k8s"}}}}}
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}}}
}

// inferredFleet is a fleet holding revs and a running checkout that pins
// dep on stripe.
func inferredFleet(t *testing.T, dep contract.Dependency, revs ...fleet.RawRevision) *fleet.FleetSnapshot {
	t.Helper()
	col := &fleet.Collection{
		Revisions: append(revs, fleet.RawRevision{Bundle: mkBundle(svcContract("checkout", "1.0.0", contract.Owner{Team: "shop"}, dep))}),
		Targets:   []fleet.RawTarget{{Scope: "prod", Kind: "deployment", Name: "checkout", Service: "checkout", Compliance: fleet.StatusCompliant, EvidenceAt: fresh()}},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// inferredStripe is the operator-signed stripe inferred contract at version.
func inferredStripe(version string) fleet.RawRevision {
	return fleet.RawRevision{Bundle: withSurface(stripeContract(version)), ResolvedRef: stripeInferred + ":" + version,
		Digest: "sha256:stripe-" + version, Provenance: operatorSigned()}
}

// pinned is checkout's dependency on stripe's inferred contract with rng.
func pinned(rng string) contract.Dependency {
	return contract.Dependency{Name: "stripe-pinned", Ref: "oci://" + stripeInferred, Compatibility: rng, Required: true}
}

// assertInferredUnknown checks res is an unverified INFERRED_PROVIDER answer
// whose only consumer, checkout, reads unknown.
func assertInferredUnknown(t *testing.T, res *Result) {
	t.Helper()
	if res.Gate != GateUnverified || res.GateCode() != GateCodeInferredProvider || !hasCode(res.Limitations, LimitationInferredProvider) {
		t.Errorf("gate %s %s %q, limitations %+v", res.Gate, res.GateCode(), res.GateReason, res.Limitations)
	}
	if len(res.Consumers) != 1 || res.Consumers[0].Service != "checkout" || res.Consumers[0].CompatibilityVerdict != CompatibilityUnknown ||
		res.Consumers[0].Note != inferredProviderNote || res.Blocking() != 0 {
		t.Errorf("consumers = %+v", res.Consumers)
	}
}

func TestImpactInferredProviderNeverClear(t *testing.T) {
	snap := inferredFleet(t, pinned("^2023.10"), inferredStripe("2023.10.15"), inferredStripe("2023.10.16"))
	res := Analyze(context.Background(), stripeContract("2023.10.15"), stripeContract("2023.10.16"), fstest.MapFS{}, fstest.MapFS{}, snap,
		Options{Domain: "ghcr.io/acme/pacto-inferred", InferredProvider: "stripe"})
	assertInferredUnknown(t, res)
	c := res.Consumers[0]
	if res.Completeness != fleet.CompletenessComplete || hasCode(res.Limitations, "SERVICE_NOT_IN_FLEET") ||
		c.Compatibility != "^2023.10" || !c.Direct || c.Provenance != fleet.ProvenanceDeclared || c.OwnerKey == "" || len(c.Targets) != 1 ||
		len(res.ActiveTargets) != 1 || !strings.Contains(res.GateReason, "pacto declare") {
		t.Errorf("answer = %+v", res)
	}
}

func TestImpactInferredProviderNeverBlocked(t *testing.T) {
	snap := inferredFleet(t, pinned("^2023.10"), inferredStripe("2023.10.16"))
	newC := stripeContract("2024.01.01")
	newC.Interfaces = nil
	old := stripeContract("2023.10.16")
	old.Interfaces = []contract.Interface{{Name: "api", Type: contract.InterfaceTypeGRPC, Ref: "api.proto"}}
	res := Analyze(context.Background(), old, newC, fstest.MapFS{}, fstest.MapFS{}, snap,
		Options{Domain: "ghcr.io/acme/pacto-inferred", InferredProvider: "stripe"})
	if res.Classification != "BREAKING" {
		t.Fatalf("classification = %s", res.Classification)
	}
	assertInferredUnknown(t, res)
}

func TestImpactOperatorSignedProviderUnknown(t *testing.T) {
	signed := fleet.RawRevision{Bundle: mkBundle(svcContract("payments", "1.1.0", contract.Owner{})), Provenance: operatorSigned()}
	snap := inferredFleet(t, contract.Dependency{Name: "payments", Compatibility: "^1.0.0"}, signed)
	res := Analyze(context.Background(), svcContract("payments", "1.0.0", contract.Owner{}), svcContract("payments", "1.1.0", contract.Owner{}),
		fstest.MapFS{}, fstest.MapFS{}, snap, Options{InferredProvider: "payments"})
	assertInferredUnknown(t, res)
}

func TestImpactClaimedExternalWithSurfaceIsJudged(t *testing.T) {
	claimed := fleet.RawRevision{Bundle: withSurface(stripeContract("2023.10.16")), ResolvedRef: "oci://ghcr.io/acme/stripe:2023.10.16", Digest: "sha256:claimed"}
	snap := inferredFleet(t, contract.Dependency{Name: "stripe", Ref: "oci://ghcr.io/acme/stripe", Compatibility: "^2023.10"}, claimed)
	newB := withSurface(stripeContract("2023.10.17"))
	res := Analyze(context.Background(), stripeContract("2023.10.16"), newB.Contract, fstest.MapFS{}, newB.FS, snap, Options{})
	if hasCode(res.Limitations, LimitationInferredProvider) || res.GateCode() == GateCodeInferredProvider ||
		len(res.Consumers) != 1 || res.Consumers[0].CompatibilityVerdict != CompatibilityCompatible {
		t.Errorf("a claimed contract is judged: %s %+v %+v", res.GateCode(), res.Consumers, res.Limitations)
	}
}

// TestInferredProviderConsumerStaleAndPolicy: a consumer that pins the
// inferred contract twice is listed once, stale target evidence makes its
// confidence unknown, and a policy referrer of an
// operator-signed provider never blocks.
func TestInferredProviderConsumerStaleAndPolicy(t *testing.T) {
	old := time.Now().Add(-72 * time.Hour)
	twice := pinned("^2023.11")
	twice.Name = "stripe-again"
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{inferredStripe("2023.10.16"), {Bundle: mkBundle(svcContract("checkout", "1.0.0", contract.Owner{}, pinned("^2023.10"), twice))}},
		Targets:   []fleet.RawTarget{{Scope: "prod", Kind: "deployment", Name: "checkout", Service: "checkout", Compliance: fleet.StatusCompliant, EvidenceAt: &old}},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	if got := inferredProviderConsumers(snap, "stripe"); len(got) != 1 || got[0].Confidence != ConfidenceUnknown {
		t.Errorf("stale consumer = %+v", got)
	}
	r := Result{Classification: "BREAKING", Consumers: []AffectedConsumer{{Service: "ref", CompatibilityVerdict: CompatibilityIncompatible,
		rangeVerdict: CompatibilityIncompatible, policyVerdict: CompatibilityIncompatible, Targets: []string{"prod/k8s/ref"}}}}
	r.forceUnknownVerdicts()
	if c := r.Consumers[0]; c.CompatibilityVerdict != CompatibilityUnknown || c.rangeVerdict != CompatibilityUnknown ||
		c.policyVerdict != CompatibilityUnknown || r.Blocking() != 0 {
		t.Errorf("forced = %+v", c)
	}
}
