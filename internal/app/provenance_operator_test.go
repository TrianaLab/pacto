package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// operatorKey is an installation key and its advertisement (base64 DER).
func operatorKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key, base64.StdEncoding.EncodeToString(der)
}

// pushInferred pushes an image under the inferred contracts and signs it with
// each signer; it returns its oci:// ref.
func pushInferred(t *testing.T, reg *provRegistry, signers ...delivery.Signer) string {
	t.Helper()
	ref := pushImage(t, reg.client, reg.host+"/acme/"+contract.InferredRepository+"/stripe")
	repo, digest, _ := strings.Cut(ref, "@")
	for _, s := range signers {
		if err := publish.SignDigest(t.Context(), reg.client, s, repo, digest); err != nil {
			t.Fatal(err)
		}
	}
	return contract.OCIScheme + ref
}

func wantOperator(t *testing.T, p *fleet.BundleProvenance, key ed25519.PrivateKey) {
	t.Helper()
	wantStatus(t, p, fleet.SignatureUnverified, "this installation's operator")
	if p.Signature.Source != fleet.SignatureSourceOperator || p.Signature.Signer != signing.Hint(key.Public()) {
		t.Fatalf("signature %+v", p.Signature)
	}
}

// TestProvenanceOperatorSelfDeclaredIsUnverified: a bundle whose publish.sign
// names the operator's key and that the operator signed reads unverified,
// source operator.
func TestProvenanceOperatorSelfDeclaredIsUnverified(t *testing.T) {
	keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	key, adv := operatorKey(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme", InferredSigner: delivery.InferredSignerFromAdvertisement(adv)}, nil, nil)
	p := pc.Check(t.Context(), "oci", pushInferred(t, reg, edSigner(key)), signedBy(&contract.PublishSign{Key: "operator.pem"}))
	wantOperator(t, p, key)
}

// TestProvenanceOperatorKeySignedIsOperator: an Ed25519 operator signature
// is named by its key hint, and a cut pin listing never makes it unknown: no
// pin can cover the operator.
func TestProvenanceOperatorKeySignedIsOperator(t *testing.T) {
	keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	key, adv := operatorKey(t)
	pc := checkerFor(reg)
	cut := func(context.Context, delivery.ServiceRef) ([]delivery.IdentityPin, string, string, error) {
		return nil, "only the first pins were read", "", nil
	}
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme", InferredSigner: delivery.InferredSignerFromAdvertisement(adv)}, cut, nil)
	wantOperator(t, pc.Check(t.Context(), "oci", pushInferred(t, reg, edSigner(key)), nil), key)
}

// TestProvenanceDeclaredPlusOperatorSignatureJudgesPublisher: an operator
// signature beside an accepted publisher's never knocks it out.
func TestProvenanceDeclaredPlusOperatorSignatureJudgesPublisher(t *testing.T) {
	_, ci := ciSigstore(t)
	reg := provenanceTestRegistry(t)
	key, adv := operatorKey(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme", InferredSigner: delivery.InferredSignerFromAdvertisement(adv)}, nil, nil)
	p := pc.Check(t.Context(), "oci", pushInferred(t, reg, edSigner(key), ci), keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureVerified, "Signed by")
	if p.Signature.Source != delivery.PublisherSourceRule || p.Signature.Signer != provWorkflow {
		t.Fatalf("signature %+v", p.Signature)
	}
}

// TestProvenanceWithoutInferredSignerNeverVerified: with no advertised key,
// a malformed one or no installation config at all, the operator's
// signature is never verified.
func TestProvenanceWithoutInferredSignerNeverVerified(t *testing.T) {
	keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	key, _ := operatorKey(t)
	ref := pushInferred(t, reg, edSigner(key))
	for name, signer := range map[string]*delivery.TrustedKey{"none": nil, "malformed": {PublicKey: "not a key"}} {
		pc := checkerFor(reg)
		trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme", InferredSigner: signer}, nil, nil)
		if p := pc.Check(t.Context(), "oci", ref, nil); p.Signature.Status != fleet.SignatureUnknown || p.Signature.Source == fleet.SignatureSourceOperator {
			t.Fatalf("%s: %+v", name, p.Signature)
		}
	}
	// An advertised key that did not sign it: not the operator either.
	_, otherAdv := operatorKey(t)
	pc := checkerFor(reg)
	trusting(pc, delivery.PublisherConfig{Registry: "ghcr.io/acme", InferredSigner: delivery.InferredSignerFromAdvertisement(otherAdv)}, nil, nil)
	if p := pc.Check(t.Context(), "oci", ref, nil); p.Signature.Status != fleet.SignatureUnknown {
		t.Fatalf("another key: %+v", p.Signature)
	}
	if p := checkerFor(reg).Check(t.Context(), "oci", ref, nil); p.Signature.Status == fleet.SignatureVerified {
		t.Fatalf("no installation config: %+v", p.Signature)
	}
}

// TestInstallationTrustReadsInferredSigner: the operator's signer is the key
// the detection advertises; none without a readable advertisement.
func TestInstallationTrustReadsInferredSigner(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	_, adv := operatorKey(t)
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: now, Entries: []detect.Entry{},
		Installation: &detect.Installation{Registry: "ghcr.io/acme", SnapshotKey: adv}})
	path := filepath.Join(t.TempDir(), "detected.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Delivery{Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), Now: func() time.Time { return now }}
	for ref, want := range map[string]bool{"": false, "k8s:pacto-system/pacto-operator-detected": false, path: true} {
		d.Detected = ref
		if got := d.inferredSigner(t.Context()); (got != nil) != want || (want && got.PublicKey != delivery.InferredSignerFromAdvertisement(adv).PublicKey) {
			t.Errorf("%q: %+v", ref, got)
		}
	}
}

// TestOperatorSignerSurvivesUnreadableTrust: delivery off (no identities
// ConfigMap) or no cluster at all still recognises the operator's signature
// from the advertisement: never unknown with source none.
func TestOperatorSignerSurvivesUnreadableTrust(t *testing.T) {
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	key, adv := operatorKey(t)
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: now, Entries: []detect.Entry{},
		Installation: &detect.Installation{Registry: "ghcr.io/acme", SnapshotKey: adv}})
	path := filepath.Join(t.TempDir(), "detected.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Delivery{Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), Detected: path, Now: func() time.Time { return now }}
	in, err := d.InstallationTrust(t.Context())
	if err == nil || in.Config.InferredSigner == nil {
		t.Fatalf("delivery off: %+v %v", in.Config, err)
	}
	if in, err := d.DetectionTrust(t.Context()); !errors.Is(err, ErrNoInstallationTrust) || in.Config.InferredSigner == nil {
		t.Fatalf("no cluster: %+v %v", in.Config, err)
	}

	keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushInferred(t, reg, edSigner(key))
	signer := delivery.InferredSignerFromAdvertisement(adv)
	for name, err := range map[string]error{"unreadable": errors.New("configmaps \"pacto-delivery-identities\" not found"), "no cluster": ErrNoInstallationTrust} {
		pc := checkerFor(reg)
		trusting(pc, delivery.PublisherConfig{InferredSigner: signer}, nil, err)
		if p := pc.Check(t.Context(), "oci", ref, nil); p.Signature.Source != fleet.SignatureSourceOperator {
			t.Errorf("%s: %+v", name, p.Signature)
		}
	}
}
