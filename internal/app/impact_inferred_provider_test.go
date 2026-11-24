package app

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

const stripeInferredRepo = "ghcr.io/acme/pacto-inferred/stripe"

// stripeBundle is stripe's inferred contract at version.
func stripeBundle(version string) *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.5",
		Service: contract.Service{Name: "stripe", Version: version, External: "stripe"}}, FS: fstest.MapFS{}}
}

// operatorOnly is the provenance of a revision only the operator signed.
func operatorOnly() *fleet.BundleProvenance {
	return &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified, Source: fleet.SignatureSourceOperator, Bundles: 1}}
}

// TestImpactInferredProviderFromRef: a change whose new ref is under
// pacto-inferred is analysed as the inferred contract it is: never added as
// a fleet service, its pinned consumers unknown and the gate unverified.
func TestImpactInferredProviderFromRef(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	checkout := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "checkout", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "stripe", Ref: "oci://" + stripeInferredRepo, Compatibility: "^0.0.1"}}}
	snap, err := fleet.Build(ctx, fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: stripeBundle("0.0.1"), ResolvedRef: stripeInferredRepo + ":0.0.1", Digest: "sha256:s1", Provenance: operatorOnly()},
		{Bundle: &contract.Bundle{Contract: checkout, FS: fstest.MapFS{}}, Digest: "sha256:checkout"},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	store := &mockBundleStore{PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
		return stripeBundle(ref[strings.LastIndex(ref, ":")+1:]), nil
	}}
	res, err := NewService(store, nil).ImpactOnSnapshot(ctx, ImpactOptions{OldPath: "oci://" + stripeInferredRepo + ":0.0.1",
		NewPath: "oci://" + stripeInferredRepo + ":0.0.2", Fleet: FleetOptions{Index: IndexNone}}, snap, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewRevisionAdded || res.Gate != impact.GateUnverified || res.GateCode() != impact.GateCodeInferredProvider ||
		len(res.Consumers) != 1 || res.Consumers[0].Service != "checkout" || res.Consumers[0].CompatibilityVerdict != impact.CompatibilityUnknown {
		t.Fatalf("added %v, gate %s %s, consumers %+v", res.NewRevisionAdded, res.Gate, res.GateCode(), res.Consumers)
	}
}

// TestInferredProviderOption: the location marks a new revision inferred, or
// failing that the snapshot's operator-only signature on that very version; a
// claimed contract elsewhere, another version or another signer is judged.
func TestInferredProviderOption(t *testing.T) {
	t.Parallel()
	signed := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments", Version: "1.1.0"}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: &contract.Bundle{Contract: signed, FS: fstest.MapFS{}}, Digest: "sha256:p", Provenance: operatorOnly()},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, path, key, version, want string
	}{
		{"inferred ref", "oci://" + stripeInferredRepo + ":0.0.2", "stripe", "0.0.2", "stripe"},
		{"claimed contract", "oci://ghcr.io/acme/stripe:2023.10.16", "stripe", "2023.10.16", ""},
		{"operator signed", "./payments", "payments", "1.1.0", "payments"},
		{"another version", "./payments", "payments", "1.2.0", ""},
	} {
		if got := inferredProvider(snap, c.path, fleet.ServiceKey(c.key), c.version); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	snap.Revisions[snap.Services["payments"].Revisions[0]].Provenance = nil
	if got := inferredProvider(snap, "./payments", "payments", "1.1.0"); got != "" {
		t.Errorf("unsigned: %q", got)
	}
}
