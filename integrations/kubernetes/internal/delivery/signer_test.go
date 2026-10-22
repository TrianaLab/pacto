/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/sigstore/sigstore/pkg/signature/kms"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

func writeFile(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func writePEM(t *testing.T, typ string, der []byte) string {
	t.Helper()
	return writeFile(t, "key.pem", pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}))
}

// verifierOf is the verifier of a trust config holding only k.
func verifierOf(t *testing.T, k delivery.TrustedKey) delivery.Verifier {
	t.Helper()
	v, err := signing.TrustVerifier(delivery.TrustConfig{Keys: []delivery.TrustedKey{k}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// edSigner signs records with key through sigstore-go.
func edSigner(key ed25519.PrivateKey) delivery.Signer {
	kp, _ := signing.FromPrivateKey(key)
	return signing.Keyed(kp, "")
}

// edVerifier verifies records signed by pub, for any stream.
func edVerifier(pub crypto.PublicKey) delivery.Verifier {
	return func(_ string, b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
}

func gap() delivery.Statement {
	return delivery.NewGap("c", delivery.ObjectMeta{Name: "s"}, 1, delivery.Chain{Stream: "s", Seq: 1}, "", "t")
}

func TestRecorderTrust(t *testing.T) {
	ss := sigtest.New(t)
	own := delivery.TrustedKey{Name: "recorder", Identity: "id", Issuer: "iss", TrustedRoot: string(ss.RootJSON)}
	if tr, err := RecorderTrust(own, ""); err != nil || len(tr.Keys) != 1 {
		t.Fatalf("own key only: %+v %v", tr, err)
	}
	old, _, _ := ed25519.GenerateKey(rand.Reader)
	pemKey, _ := delivery.PublicKeyPEM(old)
	file := writeFile(t, "trust.pem", []byte(pemKey))
	tr, err := RecorderTrust(own, file)
	if err != nil || len(tr.Keys) != 2 || tr.Keys[1].PublicKey != pemKey {
		t.Fatalf("with an old key: %+v %v", tr, err)
	}
	if _, err := RecorderTrust(own, filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("read a missing trust config")
	}
	if _, err := RecorderTrust(own, writeFile(t, "t.yaml", []byte("keys: ["))); err == nil {
		t.Fatal("read a broken trust config")
	}
	if _, err := RecorderTrust(delivery.TrustedKey{}, ""); err == nil {
		t.Fatal("trusted an empty key")
	}
}

func TestNewSignerKeyFile(t *testing.T) {
	ctx := context.Background()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	s, v, err := NewSigner(ctx, SigningConfig{KeyFile: writePEM(t, "PRIVATE KEY", der)})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := delivery.SignRecord(ctx, s, gap())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenRecord(b, verifierOf(t, v)); err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]SigningConfig{
		"nothing":      {},
		"two":          {KeyFile: "k", Keyless: true},
		"missing file": {KeyFile: filepath.Join(t.TempDir(), "absent")},
		"not PEM":      {KeyFile: writeFile(t, "plain", []byte("not pem"))},
		"bad PKCS8":    {KeyFile: writePEM(t, "PRIVATE KEY", []byte("junk"))},
		"keyless bare": {Keyless: true},
		"no KMS":       {KMSKey: "nokms://k"},
	} {
		if _, _, err := NewSigner(ctx, cfg); err == nil {
			t.Errorf("%s: built a signer", name)
		}
	}
}

// fakeKMS is a KMS provider backed by a local key.
type fakeKMS struct{ signature.SignerVerifier }

func (fakeKMS) CreateKey(context.Context, string) (crypto.PublicKey, error) { return nil, nil }
func (fakeKMS) CryptoSigner(context.Context, func(error)) (crypto.Signer, crypto.SignerOpts, error) {
	return nil, nil, nil
}
func (fakeKMS) SupportedAlgorithms() []string { return nil }
func (fakeKMS) DefaultAlgorithm() string      { return "" }

func TestNewSignerKMS(t *testing.T) {
	ctx := context.Background()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sv, _ := signature.LoadSignerVerifier(key, crypto.SHA256)
	kms.AddProvider("recordertestkms://", func(context.Context, string, crypto.Hash, ...signature.RPCOption) (kms.SignerVerifier, error) {
		return fakeKMS{sv}, nil
	})
	s, own, err := NewSigner(ctx, SigningConfig{KMSKey: "recordertestkms://k"})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := delivery.SignRecord(ctx, s, gap())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenRecord(b, verifierOf(t, own)); err != nil {
		t.Fatalf("the KMS record does not verify under the trust entry: %v", err)
	}
}

// TestNewSignerKeyless: keyless records carry a certificate for the
// recorder's identity and a signed timestamp, and verify under the trusted
// root the recorder publishes in its trust entry, but not under another.
func TestNewSignerKeyless(t *testing.T) {
	ctx := context.Background()
	ss := sigtest.New(t)
	const id, iss = "recorder@pacto.example", "https://kubernetes.default.svc"
	cfg := SigningConfig{Keyless: true, Identity: id, Issuer: iss, FulcioURL: ss.Fulcio, TimestampURL: ss.TSA,
		TokenFile: writeFile(t, "token", []byte(sigtest.Token(id, iss))), TrustedRootFile: writeFile(t, "root.json", ss.RootJSON)}
	s, own, err := NewSigner(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if own.Identity != id || own.Issuer != iss || own.TrustedRoot != string(ss.RootJSON) {
		t.Fatalf("trust entry %+v", own)
	}
	b, _, err := delivery.SignRecord(ctx, s, gap())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenRecord(b, verifierOf(t, own)); err != nil {
		t.Fatal(err)
	}
	other := own
	other.TrustedRoot = string(sigtest.New(t).RootJSON)
	if _, _, err := delivery.OpenRecord(b, verifierOf(t, other)); err == nil {
		t.Fatal("a keyless record verified under an untrusted root")
	}
	wrong := own
	wrong.Identity = "mallory@pacto.example"
	if _, _, err := delivery.OpenRecord(b, verifierOf(t, wrong)); err == nil {
		t.Fatal("a keyless record verified for another identity")
	}

	noTSA := cfg
	noTSA.TimestampURL = ""
	badRoot := cfg
	badRoot.TrustedRootFile = writeFile(t, "bad.json", []byte("{}"))
	noRoot := cfg
	noRoot.TrustedRootFile = filepath.Join(t.TempDir(), "absent")
	for name, c := range map[string]SigningConfig{"no timestamp authority": noTSA, "bad root": badRoot, "missing root": noRoot} {
		if _, _, err := NewSigner(ctx, c); err == nil {
			t.Errorf("%s: built a signer", name)
		}
	}
}
