/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func writePEM(t *testing.T, typ string, der []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewSignerEd25519(t *testing.T) {
	ctx := context.Background()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	s, v, err := NewSigner(ctx, SigningConfig{Ed25519KeyFile: writePEM(t, "PRIVATE KEY", der)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := delivery.NewGap("c", delivery.ObjectMeta{Name: "s"}, 1, delivery.Chain{Stream: "s", Seq: 1}, "", "t")
	b, _, err := delivery.SignRecord(ctx, s, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenRecord(b, v); err != nil {
		t.Fatal(err)
	}

	notPEM := filepath.Join(t.TempDir(), "plain")
	_ = os.WriteFile(notPEM, []byte("not pem"), 0o600)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecDer, _ := x509.MarshalPKCS8PrivateKey(ec)
	for name, cfg := range map[string]SigningConfig{
		"nothing":       {},
		"two":           {Ed25519KeyFile: "k", Keyless: true},
		"missing file":  {Ed25519KeyFile: filepath.Join(t.TempDir(), "absent")},
		"not PEM":       {Ed25519KeyFile: notPEM},
		"bad PKCS8":     {Ed25519KeyFile: writePEM(t, "PRIVATE KEY", []byte("junk"))},
		"wrong type":    {Ed25519KeyFile: writePEM(t, "PRIVATE KEY", ecDer)},
		"keyless bare":  {Keyless: true},
		"kms cosign no": {KMSKey: "awskms:///k", Cosign: filepath.Join(t.TempDir(), "no-cosign")},
	} {
		if _, _, err := NewSigner(ctx, cfg, execRun); err == nil {
			t.Errorf("%s: built a signer", name)
		}
	}
}

// fakeCosign plays cosign: public-key prints the ECDSA key, sign-blob signs
// the blob and, keyless, writes a certificate for it.
type fakeCosign struct {
	key      *ecdsa.PrivateKey
	certDER  []byte
	calls    [][]string
	fail     string // the subcommand that fails
	skipCert bool
	badPEM   bool
}

func (f *fakeCosign) run(_ context.Context, stdin []byte, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if args[0] == f.fail {
		return nil, errors.New("cosign " + args[0] + " failed")
	}
	if args[0] == "public-key" {
		if f.badPEM {
			return []byte("nope"), nil
		}
		der, _ := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
		return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
	}
	out := func(flag string) string { return args[slices.Index(args, flag)+1] }
	digest := sha256.Sum256(stdin)
	sig, _ := ecdsa.SignASN1(rand.Reader, f.key, digest[:])
	_ = os.WriteFile(out("--output-signature"), sig, 0o600)
	if slices.Contains(args, "--output-certificate") && !f.skipCert {
		body := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.certDER})
		if f.badPEM {
			body = []byte("nope")
		}
		_ = os.WriteFile(out("--output-certificate"), body, 0o600)
	}
	return nil, nil
}

func TestCosignKMS(t *testing.T) {
	ctx := context.Background()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f := &fakeCosign{key: key}
	s, v, err := NewSigner(ctx, SigningConfig{KMSKey: "awskms:///alias/pacto"}, f.run)
	if err != nil {
		t.Fatal(err)
	}
	st := delivery.NewGap("c", delivery.ObjectMeta{Name: "s"}, 1, delivery.Chain{Stream: "s", Seq: 1}, "", "t")
	b, _, err := delivery.SignRecord(ctx, s, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenRecord(b, v); err != nil {
		t.Fatal(err)
	}
	if got := f.calls[1]; !slices.Contains(got, "--tlog-upload=false") || !slices.Contains(got, "awskms:///alias/pacto") {
		t.Fatalf("sign-blob args = %v", got)
	}
	for _, edit := range []func(*fakeCosign){
		func(f *fakeCosign) { f.fail = "public-key" },
		func(f *fakeCosign) { f.badPEM = true },
	} {
		g := &fakeCosign{key: key}
		edit(g)
		if _, _, err := NewSigner(ctx, SigningConfig{KMSKey: "k"}, g.run); err == nil {
			t.Error("built a signer from a broken public-key")
		}
	}
	f.fail = "sign-blob"
	if _, _, err := delivery.SignRecord(ctx, s, st); err == nil {
		t.Fatal("signed with a failing cosign")
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "absent"))
	if _, _, err := delivery.SignRecord(ctx, s, st); err == nil {
		t.Fatal("signed with no temporary directory")
	}
}

func TestCosignKeyless(t *testing.T) {
	ctx := context.Background()
	const id, iss = "https://kubernetes.io/namespaces/pacto/serviceaccounts/recorder", "https://issuer.example"
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	u, _ := url.Parse(id)
	v2, _ := asn1.Marshal(iss)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), URIs: []*url.URL{u},
		ExtraExtensions: []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}, Value: v2}}}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	f := &fakeCosign{key: key, certDER: der}
	s, v, err := NewSigner(ctx, SigningConfig{Keyless: true, Identity: id, Issuer: iss, Cosign: "/usr/local/bin/cosign"}, f.run)
	if err != nil {
		t.Fatal(err)
	}
	st := delivery.NewGap("c", delivery.ObjectMeta{Name: "s"}, 1, delivery.Chain{Stream: "s", Seq: 1}, "", "t")
	b, _, err := delivery.SignRecord(ctx, s, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenRecord(b, v); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*fakeCosign){
		func(f *fakeCosign) { f.skipCert = true },
		func(f *fakeCosign) { f.badPEM = true },
	} {
		g := &fakeCosign{key: key, certDER: der}
		edit(g)
		gs, _, _ := NewSigner(ctx, SigningConfig{Keyless: true, Identity: id, Issuer: iss}, g.run)
		if _, _, err := delivery.SignRecord(ctx, gs, st); err == nil {
			t.Error("signed without a certificate")
		}
	}
	// sign-blob that writes no signature at all.
	silent := cosignSigner{path: "cosign", run: func(context.Context, []byte, string, ...string) ([]byte, error) { return nil, nil }}
	if _, _, err := silent.Sign(ctx, []byte("x")); err == nil {
		t.Fatal("signed with no signature written")
	}
}

func TestExecRun(t *testing.T) {
	ctx := context.Background()
	if out, err := execRun(ctx, []byte("ok"), "sh", "-c", "cat"); err != nil || string(out) != "ok" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := execRun(ctx, nil, "sh", "-c", "echo boom >&2; exit 3"); err == nil {
		t.Fatal("a failing program succeeded")
	}
}
