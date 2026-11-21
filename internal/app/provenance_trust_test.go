package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// provRule is a policy rule that covers acme's release workflow of provRepo.
var provRule = delivery.PublisherRule{Name: "release", Issuer: provIssuer,
	Subject: `^https://github\.com/acme/app/\.github/workflows/release\.yml@refs/heads/main$`, Repository: `^https://github\.com/acme/app$`}

// provPin is a person's confirmation of acme's release workflow for app.
func provPin(svc delivery.ServiceRef) delivery.IdentityPin {
	return delivery.IdentityPin{Service: svc, Namespace: "prod", Issuer: provIssuer, Subject: provWorkflow, Repository: provRepo, Approver: "alice", At: "2026-11-17T00:00:00Z"}
}

// trusting makes pc read cfg and pins as the installation's trust, and
// counts the reads.
func trusting(pc *ProvenanceChecker, cfg delivery.PublisherConfig, pins PinsFunc, err error) *int {
	return trustingIn(pc, Installation{Config: cfg, Pins: pins}, err)
}

// trustingIn makes pc read in as the installation's trust, and counts the
// reads.
func trustingIn(pc *ProvenanceChecker, in Installation, err error) *int {
	n := new(int)
	pc.Trust = func(context.Context) (Installation, error) { *n++; return in, err }
	return n
}

// signPolicy pushes a delivery policy of rules signed by key to
// host/policy:v1.
func signPolicy(t *testing.T, reg *provRegistry, key ed25519.PrivateKey, rules ...delivery.PublisherRule) {
	t.Helper()
	signPolicyAt(t, reg, key, "v1", "2026-11-18T00:00:00Z", rules...)
}

// signPolicyAt pushes a delivery policy of rules signed by key at at to
// host/policy:tag and returns its digest.
func signPolicyAt(t *testing.T, reg *provRegistry, key ed25519.PrivateKey, tag, at string, rules ...delivery.PublisherRule) string {
	t.Helper()
	payload, _ := json.Marshal(delivery.PolicyStatement{Type: delivery.StatementType, Subject: []delivery.Subject{},
		PredicateType: delivery.PredicatePublisherPolicy, Predicate: delivery.PublisherPolicy{Publishers: rules, At: at}})
	bundle, err := edSigner(key).Sign(t.Context(), delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := mutate.AppendLayers(empty.Image, static.NewLayer(bundle, types.MediaType(delivery.BundleMediaType)))
	d, err := reg.client.Write(t.Context(), reg.host+"/policy", mutate.MediaType(img, types.OCIManifestSchema1))
	if err == nil {
		err = reg.client.Tag(t.Context(), reg.host+"/policy", d, tag)
	}
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// policySigners is a trust config whose one policy key is pub.
func policySigners(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	pem, err := delivery.PublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(delivery.TrustConfig{Keys: []delivery.TrustedKey{{Name: "security", Role: delivery.RolePolicy, PublicKey: pem}}})
	return string(b)
}

// TestProvenanceVerifiedByInstallationRule: the rule derived from the
// installation's configured registry, not from where the revision lives,
// verifies acme's CI.
func TestProvenanceVerifiedByInstallationRule(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme"}, nil, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureVerified, "Signed by")
	if p.Signature.Source != delivery.PublisherSourceRule || p.Signature.Signer != provWorkflow {
		t.Fatalf("signature %+v", p.Signature)
	}
	// Another organization's registry: acme's CI is a new signer, shown
	// against the installation's rule.
	pc = checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/other"}, nil, nil)
	p = pc.Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	wantStatus(t, p, fleet.SignatureUnverified, "a person confirms it")
	if !strings.Contains(p.Signature.ExpectedSigner, "other") {
		t.Fatalf("expected %+v", p.Signature)
	}
}

// TestProvenanceVerifiedByPin: with no rule, a pin a person wrote for the
// service verifies, whether its Stage names the revision's domain or none;
// what no rule covers says why.
func TestProvenanceVerifiedByPin(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pc := checkerFor(reg)
	var asked []delivery.ServiceRef
	trusting(pc, delivery.PublisherConfig{}, func(_ context.Context, svc delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
		asked = append(asked, svc)
		if svc.Domain != "" {
			return nil, "", "", nil
		}
		return []delivery.IdentityPin{provPin(svc)}, "", "", nil
	}, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureVerified, "an identity a person pinned for app in namespace prod")
	// Who confirmed it is the delivery trust view's, which only those who may
	// list the service's Stages read.
	if p.Signature.Source != delivery.PublisherSourcePin || !strings.Contains(p.Signature.Reason, "No publisher registry is configured") ||
		strings.Contains(p.Signature.Reason, "alice") || strings.Contains(p.Signature.Reason, "2026-11-17") {
		t.Fatalf("signature %+v", p.Signature)
	}
	if want := []delivery.ServiceRef{{Domain: reg.host, Name: "app"}, {Name: "app"}}; !slices.Equal(asked, want) {
		t.Fatalf("asked %+v, want %+v", asked, want)
	}
}

// TestProvenanceVerifiedByPolicy: a delivery policy signed by a policy key
// replaces the derived rule.
func TestProvenanceVerifiedByPolicy(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	signPolicy(t, reg, key, provRule)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/other", Policy: reg.host + "/policy:v1", PolicySigners: policySigners(t, pub)}, nil, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	wantStatus(t, p, fleet.SignatureVerified, "Signed by")
	if p.Signature.Source != delivery.PublisherSourcePolicy {
		t.Fatalf("signature %+v", p.Signature)
	}
}

// TestProvenancePolicyUnverifiedOnlyPins: a configured policy that cannot be
// read or verified gives no rule, and the reason says so; a pin still
// verifies.
func TestProvenancePolicyUnverifiedOnlyPins(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	signPolicy(t, reg, stranger, provRule)
	_, err := reg.client.Write(t.Context(), reg.host+"/empty", empty.Image)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := empty.Image.Digest()
	if err := reg.client.Tag(t.Context(), reg.host+"/empty", h.String(), "v1"); err != nil {
		t.Fatal(err)
	}
	ref := pushSigned(t, reg, s)
	signers := policySigners(t, pub)
	for policy, want := range map[string]string{
		reg.host + "/policy:v1": "no policy key signed it",
		reg.host + "/policy:v9": "it does not exist",
		reg.host + "/empty:v1":  "could not be verified (",
		"UPPER/policy:v1":       "could not be verified (",
	} {
		pc := checkerFor(reg)
		trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme", Policy: policy, PolicySigners: signers}, nil, nil)
		p := pc.Check(t.Context(), "oci", ref, nil)
		wantStatus(t, p, fleet.SignatureUnverified, want)
		if !strings.Contains(p.Signature.Reason, "so only pinned signers are accepted") {
			t.Errorf("%s: %+v", policy, p.Signature)
		}
	}
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Policy: reg.host + "/policy:v1", PolicySigners: "keys: nope"}, nil, nil)
	wantStatus(t, pc.Check(t.Context(), "oci", ref, nil), fleet.SignatureUnverified, "could not be verified")
	pc = checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Policy: reg.host + "/policy:v1", PolicySigners: signers},
		func(_ context.Context, svc delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
			return []delivery.IdentityPin{provPin(svc)}, "", "", nil
		}, nil)
	p := pc.Check(t.Context(), "oci", ref, nil)
	wantStatus(t, p, fleet.SignatureVerified, "only pinned signers are accepted")
	if p.Signature.Source != delivery.PublisherSourcePin {
		t.Fatalf("signature %+v", p.Signature)
	}
}

// TestProvenancePinReadFailureSaysSo: pins that cannot be read leave the
// rule in force and the reason names them.
func TestProvenancePinReadFailureSaysSo(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme"}, func(context.Context, delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
		return nil, "", "", errors.New("registry down")
	}, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureVerified, "The pins of app could not be read (registry down), so no pinned signer is accepted.")
}

// TestProvenancePinListingCutUnknown: when only part of a service's pins
// were read, a signer no rule or read pin covers is unknown, never
// unverified, and the reason says the listing was cut.
func TestProvenancePinListingCutUnknown(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/other"}, func(context.Context, delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
		return nil, "more than 20 pins of app are attached and only 20 were read", "", nil
	}, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	wantStatus(t, p, fleet.SignatureUnknown, "only 20 were read, so a signer no pin read covers is unknown")
}

// TestProvenanceDroppedPinNamed: a pin the reader dropped is named in the
// reason.
func TestProvenanceDroppedPinNamed(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/other"}, func(_ context.Context, svc delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
		if svc.Domain == "" {
			return nil, "", "", nil
		}
		return nil, "", "a pin of app is not valid and was dropped", nil
	}, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	wantStatus(t, p, fleet.SignatureUnverified, "covers: a person confirms it. A pin of app is not valid and was dropped.")
}

// TestProvenanceTrustMemo: the installation's trust is read once per
// provenanceTTL, and a config that cannot be read once per
// provenanceFailTTL, judged meanwhile with the inferred rule.
func TestProvenanceTrustMemo(t *testing.T) {
	_, s := ciSigstore(t)
	asGHCR(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	pc := checkerFor(reg)
	clock := time.Now()
	pc.now = func() time.Time { return clock }
	check := func() *fleet.BundleProvenance {
		pc.memo = map[string]provenanceEntry{} // forget the check, never the trust
		return pc.Check(t.Context(), "oci", ref, nil)
	}
	reads := trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme"}, nil, nil)
	check()
	clock = clock.Add(provenanceTTL - time.Second)
	if check(); *reads != 1 {
		t.Fatalf("read %d times within the TTL", *reads)
	}
	clock = clock.Add(2 * time.Second)
	if check(); *reads != 2 {
		t.Fatalf("read %d times after the TTL", *reads)
	}
	reads = trusting(pc, delivery.PublisherConfig{}, nil, errors.New("forbidden"))
	pc.trust = nil
	p := check()
	wantStatus(t, p, fleet.SignatureUnknown, "no installation rule was read, so it is not verified")
	if !strings.Contains(p.Signature.Reason, "publisher trust config could not be read (forbidden)") || p.Signature.Source != delivery.PublisherSourceInferred {
		t.Fatalf("signature %+v", p.Signature)
	}
	clock = clock.Add(provenanceFailTTL - time.Second)
	if check(); *reads != 1 {
		t.Fatalf("a failure read %d times within its TTL", *reads)
	}
	clock = clock.Add(2 * time.Second)
	if check(); *reads != 2 {
		t.Fatalf("a failure read %d times after its TTL", *reads)
	}
}

// TestInstallationTrust: the client reads the config and a pin reader as
// PublisherTrust does; no streams repository reads no pins, and a pin
// signer it cannot use answers why on every read.
func TestInstallationTrust(t *testing.T) {
	e := newAtEnv(t, testutil.ReferrersOptions{})
	streams := e.host + "/delivery/streams"
	e.publishing(&delivery.PublisherConfig{Registry: "ghcr.io/acme", StreamsRepository: streams, PinSigner: e.pinSigner()})
	e.pin(trustPayments, "alice", e.trustApproval("u1", ""), "u1", "2026-11-17T00:00:00Z")
	in, err := e.d.InstallationTrust(context.Background())
	pins := in.Pins
	if err != nil || in.Config.Registry != "ghcr.io/acme" || pins == nil || in.Unguarded != "" {
		t.Fatalf("%+v %v", in, err)
	}
	e.pin(trustPayments, "alice", e.trustApproval("u2", ""), "u2", "2026-11-18T00:00:00Z")
	// The newest pin of the namespace is the one in use, as the promoter reads it.
	if got, cut, dropped, err := pins(context.Background(), trustPayments); err != nil || cut+dropped != "" || len(got) != 1 || got[0].Operation != "u2" {
		t.Fatalf("pins %+v %q %q %v", got, cut, dropped, err)
	}
	e.publishing(&delivery.PublisherConfig{Registry: "ghcr.io/acme"})
	if in, err := e.d.InstallationTrust(context.Background()); err != nil || in.Pins != nil {
		t.Fatalf("no streams repository read pins: %v", err)
	}
	e.publishing(&delivery.PublisherConfig{StreamsRepository: streams})
	in, err = e.d.InstallationTrust(context.Background())
	if _, _, _, perr := in.Pins(context.Background(), trustPayments); err != nil || perr == nil || !strings.Contains(perr.Error(), "names no pin signer") {
		t.Fatalf("no pin signer: %v %v", err, perr)
	}
	e.d.PolicyNamespace = ""
	if _, err := e.d.InstallationTrust(context.Background()); err == nil {
		t.Fatal("no delivery namespace read a config")
	}
}

// TestProvenancePolicyRollbackKeepsNewer: a policy tag moved back to an
// older signed policy is judged as the promoter judges it, with the newer
// policy a Stage's watch already uses.
func TestProvenancePolicyRollbackKeepsNewer(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	other := provRule
	other.Subject = `^https://github\.com/acme/app/\.github/workflows/other\.yml@refs/heads/main$`
	newer := signPolicyAt(t, reg, key, "v2", "2026-11-18T00:00:00Z", other)
	signPolicyAt(t, reg, key, "v1", "2026-11-01T00:00:00Z", provRule)
	ref := pushSigned(t, reg, s)
	cfg := delivery.PublisherConfig{Policy: reg.host + "/policy:v1", PolicySigners: policySigners(t, pub)}
	pc := checkerFor(reg)
	trustingIn(pc, Installation{Config: cfg, PolicyDigest: newer, PolicyAt: "2026-11-18T00:00:00Z"}, nil)
	wantStatus(t, pc.Check(t.Context(), "oci", ref, nil), fleet.SignatureUnverified, "")
	// An older mark leaves the tag's policy in force.
	pc = checkerFor(reg)
	trustingIn(pc, Installation{Config: cfg, PolicyDigest: newer, PolicyAt: "2026-10-01T00:00:00Z"}, nil)
	wantStatus(t, pc.Check(t.Context(), "oci", ref, nil), fleet.SignatureVerified, "")
}

// TestProvenanceUnguardedCapsUnknown: when the Stages that hold the
// promoter's high-water marks cannot be listed, a policy or pin accept is
// unknown and the reason says why.
func TestProvenanceUnguardedCapsUnknown(t *testing.T) {
	_, s := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	signPolicy(t, reg, key, provRule)
	pc := checkerFor(reg)
	trustingIn(pc, Installation{Config: delivery.PublisherConfig{Policy: reg.host + "/policy:v1", PolicySigners: policySigners(t, pub)},
		Unguarded: "the Stages could not be listed (forbidden), so the promoter's no-rollback guards were not applied"}, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), nil)
	wantStatus(t, p, fleet.SignatureUnknown, "The Stages could not be listed (forbidden)")
}

// TestInstallationTrustPinRollback: a namespace whose newest pin is older
// than the one its Stage already used gets no pin, and says so; the
// policy high-water mark comes from a watch that judges with the policy.
func TestInstallationTrustPinRollback(t *testing.T) {
	e := newAtEnv(t, testutil.ReferrersOptions{})
	streams := e.host + "/delivery/streams"
	stage := obj("delivery.pacto.trianalab.io/v1alpha1", "Stage", "prod", "payments-reg", map[string]any{
		"spec": map[string]any{"service": map[string]any{"name": "payments"}, "candidates": map[string]any{"registry": map[string]any{}}},
		"status": map[string]any{"publisher": map[string]any{"source": "policy", "policyDigest": "sha256:p", "policyAt": "2026-11-18T00:00:00Z",
			"pinAt": "2026-11-18T00:00:00Z"}}})
	e.publishing(&delivery.PublisherConfig{StreamsRepository: streams, PinSigner: e.pinSigner()}, stage)
	e.pin(trustPayments, "alice", e.trustApproval("u1", ""), "u1", "2026-11-17T00:00:00Z")
	in, err := e.d.InstallationTrust(context.Background())
	if err != nil || in.Unguarded != "" || in.PolicyDigest != "sha256:p" {
		t.Fatalf("%+v %v", in, err)
	}
	got, _, dropped, err := in.Pins(context.Background(), trustPayments)
	if err != nil || len(got) != 0 || !strings.Contains(dropped, "the newest pin of payments in prod is gone; no older pin is used") {
		t.Fatalf("pins %+v %q %v", got, dropped, err)
	}
	e.d.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "stages", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	if in, err := e.d.InstallationTrust(context.Background()); err != nil || !strings.Contains(in.Unguarded, "the Stages could not be listed (") || !strings.Contains(in.Unguarded, "forbidden") {
		t.Fatalf("%+v %v", in, err)
	}
}

// TestProvenancePinSurvivesReusableWorkflowBump: a pin taken at one
// pacto-actions commit verifies a bundle signed at another from the caller's
// default branch, and its reason names the pin's namespace instead of
// failing to find the pin by its exact subject.
func TestProvenancePinSurvivesReusableWorkflowBump(t *testing.T) {
	actions := "https://github.com/" + delivery.PactoActionsWorkflow
	_, s := keylessSigstoreFor(t, sigtest.CIToken(actions+"@2222", provIssuer, provRepo, "refs/heads/main"))
	reg := provenanceTestRegistry(t)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	onMain := provRule // covers only acme/app's own release.yml, so a pin decides
	onMain.Ref = "refs/heads/main"
	signPolicy(t, reg, key, onMain)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Policy: reg.host + "/policy:v1", PolicySigners: policySigners(t, pub)},
		func(_ context.Context, svc delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
			pin := provPin(svc)
			pin.Subject = actions + "@1111"
			return []delivery.IdentityPin{pin}, "", "", nil
		}, nil)
	p := pc.Check(t.Context(), "oci", pushSigned(t, reg, s), signedBy(nil))
	wantStatus(t, p, fleet.SignatureVerified, "an identity a person pinned for app in namespace prod")
	if p.Signature.Source != delivery.PublisherSourcePin {
		t.Fatalf("signature %+v", p.Signature)
	}
}
