package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore/pkg/cryptoutils"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// pullEnv is a declare environment whose public-good root is its private
// Sigstore's, a published invoices bundle tagged 1.0.0 signed with s (nil:
// unsigned), and a service that pulls with a store recording what it pulls.
func pullEnv(t *testing.T, sign func(e *declareEnv) *signing.Signer) (*declareEnv, string, *string) {
	t.Helper()
	e := newDeclareEnv(t)
	old := publicGoodRoot
	t.Cleanup(func() { publicGoodRoot = old })
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return e.ss.Root, nil }
	digest := e.publishBundle(t, e.host+"/acme/invoices", "1.0.0", e.invoicesBaseline("1.0.0", ""), sign(e))
	pulled := new(string)
	e.svc.BundleStore = &mockBundleStore{PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
		*pulled = ref
		return testBundle(), nil
	}}
	return e, digest, pulled
}

func personSigned(t *testing.T) func(e *declareEnv) *signing.Signer {
	return func(e *declareEnv) *signing.Signer {
		s := e.signer(t, personToken("ana@acme.com"))
		return &s
	}
}

func (e *declareEnv) pull(t *testing.T, ref string, verify *contract.PublishSign) error {
	t.Helper()
	_, err := e.svc.Pull(t.Context(), PullOptions{Ref: ref, Output: filepath.Join(t.TempDir(), "out"), Verify: verify})
	return err
}

func TestPullVerifiesIdentity(t *testing.T) {
	e, digest, pulled := pullEnv(t, personSigned(t))
	repo := e.host + "/acme/invoices"
	if err := e.pull(t, "oci://"+repo+":1.0.0", &contract.PublishSign{Keyless: true, Identity: "ana@acme.com", Issuer: personIssuer}); err != nil || *pulled != repo+"@"+digest {
		t.Fatalf("%v %q", err, *pulled)
	}
	*pulled = ""
	err := e.pull(t, "oci://"+repo+":1.0.0", &contract.PublishSign{Keyless: true, Identity: "eve@acme.com", Issuer: personIssuer})
	if err == nil || !strings.Contains(err.Error(), "nothing was pulled") || *pulled != "" {
		t.Fatalf("%v %q", err, *pulled)
	}
	// A digest reference is verified as it is.
	if err := e.pull(t, "oci://"+repo+"@"+digest, &contract.PublishSign{Keyless: true, Identity: "ana@acme.com", Issuer: personIssuer}); err != nil || *pulled != repo+"@"+digest {
		t.Fatalf("%v %q", err, *pulled)
	}
}

func TestPullRefusesUnsignedWhenVerifying(t *testing.T) {
	e, _, pulled := pullEnv(t, func(*declareEnv) *signing.Signer { return nil })
	err := e.pull(t, "oci://"+e.host+"/acme/invoices:1.0.0", &contract.PublishSign{Keyless: true, Identity: "ana@acme.com", Issuer: personIssuer})
	if err == nil || !strings.Contains(err.Error(), "nothing was pulled") || *pulled != "" {
		t.Fatalf("%v %q", err, *pulled)
	}
}

func TestPullVerifiesKey(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(key)
	e, digest, pulled := pullEnv(t, func(*declareEnv) *signing.Signer { s := signing.Keyed(kp, ""); return &s })
	pem, _ := cryptoutils.MarshalPublicKeyToPEM(kp.GetPublicKey())
	dir := t.TempDir()
	pub, bad := filepath.Join(dir, "cosign.pub"), filepath.Join(dir, "bad.pub")
	_ = os.WriteFile(pub, pem, 0o600)
	_ = os.WriteFile(bad, []byte("not a key"), 0o600)
	ref := "oci://" + e.host + "/acme/invoices:1.0.0"
	if err := e.pull(t, ref, &contract.PublishSign{Key: pub}); err != nil || !strings.HasSuffix(*pulled, digest) {
		t.Fatalf("%v %q", err, *pulled)
	}
	for _, k := range []string{bad, filepath.Join(dir, "missing.pub")} {
		if err := e.pull(t, ref, &contract.PublishSign{Key: k}); err == nil || !strings.Contains(err.Error(), "nothing was pulled") {
			t.Fatalf("%s: %v", k, err)
		}
	}
}

func TestPullVerifyRefusals(t *testing.T) {
	e, _, pulled := pullEnv(t, personSigned(t))
	sign := &contract.PublishSign{Keyless: true, Identity: "ana@acme.com", Issuer: personIssuer}
	if err := e.pull(t, "oci://"+e.host+"/acme/invoices:9.9.9", sign); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatal(err)
	}
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return nil, errors.New("tuf down") }
	if err := e.pull(t, "oci://"+e.host+"/acme/invoices:1.0.0", sign); err == nil || !strings.Contains(err.Error(), "tuf down") {
		t.Fatal(err)
	}
	e.svc.sys.publishRegistry = func() publish.Registry { return headFails{e.reg} }
	if err := e.pull(t, "oci://"+e.host+"/acme/invoices:"+firstDeclaration, sign); err == nil || *pulled != "" {
		t.Fatalf("%v %q", err, *pulled)
	}
}

func TestPullWithoutVerifyUnchanged(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	svc.sys.publishRegistry = func() publish.Registry { t.Fatal("pull without --verify-* read the registry"); return nil }
	res, err := svc.Pull(t.Context(), PullOptions{Ref: "oci://ghcr.io/acme/svc:1.0.0", Output: filepath.Join(t.TempDir(), "out")})
	if err != nil || res.Ref != "ghcr.io/acme/svc:1.0.0" {
		t.Fatalf("%v %+v", err, res)
	}
}
