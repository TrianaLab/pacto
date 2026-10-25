package signing

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/sigstore/sigstore/pkg/signature/kms"

	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const statement = `{"_type":"https://in-toto.io/Statement/v1","subject":[{"digest":{"sha256":"` +
	"0000000000000000000000000000000000000000000000000000000000000000" + `"}}],"predicateType":"https://example.com/p","predicate":{}}`

// independentKeyVerify checks a bundle with sigstore-go alone, none of this
// package's code: what a third party holding the public key would run.
func independentKeyVerify(t *testing.T, b []byte, pub crypto.PublicKey) error {
	t.Helper()
	var bun bundle.Bundle
	if err := bun.UnmarshalJSON(b); err != nil {
		return err
	}
	v, _ := signature.LoadDefaultVerifier(pub)
	tm := root.NewTrustedPublicKeyMaterial(func(string) (root.TimeConstrainedVerifier, error) {
		return root.NewExpiringKey(v, time.Time{}, time.Time{}), nil
	})
	sv, err := verify.NewVerifier(tm, verify.WithNoObserverTimestamps())
	if err != nil {
		return err
	}
	_, err = sv.Verify(&bun, verify.NewPolicy(verify.WithoutArtifactUnsafe(), verify.WithKey()))
	return err
}

func keys(t *testing.T) map[string]crypto.Signer {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rs, _ := rsa.GenerateKey(rand.Reader, 2048)
	return map[string]crypto.Signer{"ED25519": ed, "ECDSA": ec, "RSA": rs}
}

// TestKeyedRoundTrip: every supported key type signs a bundle that this
// package and an independent sigstore-go verifier accept, and another key
// rejects.
func TestKeyedRoundTrip(t *testing.T) {
	ctx := t.Context()
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for alg, key := range keys(t) {
		kp, err := FromPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		if kp.GetKeyAlgorithm() != alg || kp.GetHashAlgorithm().String() != "SHA2_256" || kp.GetSigningAlgorithm() == 0 {
			t.Errorf("%s: %s %s %s", alg, kp.GetKeyAlgorithm(), kp.GetHashAlgorithm(), kp.GetSigningAlgorithm())
		}
		if pemKey, err := kp.GetPublicKeyPem(); err != nil || !strings.Contains(pemKey, "PUBLIC KEY") {
			t.Errorf("%s: %q %v", alg, pemKey, err)
		}
		b, err := Keyed(kp, "").Sign(ctx, "application/vnd.in-toto+json", []byte(statement))
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if !bytes.Contains(b, []byte(`"hint":"`+Hint(key.Public())+`"`)) {
			t.Errorf("%s: the bundle names the key by its hex hint: %s", alg, b)
		}
		if _, err := (Policy{Key: key.Public()}).Verify(b, nil); err != nil {
			t.Errorf("%s: %v", alg, err)
		}
		if err := independentKeyVerify(t, b, key.Public()); err != nil {
			t.Errorf("%s: an independent verifier rejects it: %v", alg, err)
		}
		if _, err := (Policy{Key: other.Public()}).Verify(b, nil); err == nil {
			t.Errorf("%s: another key verified it", alg)
		}
	}
}

func TestKeypairRefusals(t *testing.T) {
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if _, err := FromPrivateKey(p384); err == nil {
		t.Error("a P-384 key signs with SHA-384, which Pacto refuses")
	}
	if _, err := FromPrivateKey(struct{}{}); err == nil {
		t.Error("an unknown key type loaded")
	}
	if _, err := NewKeypair(nil, struct{}{}); err == nil {
		t.Error("an unknown public key type wrapped")
	}
	if Hint(struct{}{}) != "" {
		t.Error("a key x509 cannot marshal has no hint")
	}
}

func TestLoadKey(t *testing.T) {
	dir := t.TempDir()
	priv, pub, err := cryptoutils.GeneratePEMEncodedECDSAKeyPair(elliptic.P256(), cryptoutils.StaticPasswordFunc([]byte("pw")))
	if err != nil {
		t.Fatal(err)
	}
	enc := filepath.Join(dir, "cosign.key")
	_ = os.WriteFile(enc, priv, 0o600)
	t.Setenv("COSIGN_PASSWORD", "pw")
	kp, err := LoadKey(enc)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := cryptoutils.UnmarshalPEMToPublicKey(pub)
	if Hint(kp.GetPublicKey()) != Hint(want) {
		t.Error("the cosign key file loaded another key")
	}
	t.Setenv("COSIGN_PASSWORD", "wrong")
	if _, err := LoadKey(enc); err == nil {
		t.Error("a wrong password decrypted the key")
	}
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := cryptoutils.MarshalPrivateKeyToPEM(ed)
	plain := filepath.Join(dir, "ed.pem")
	_ = os.WriteFile(plain, der, 0o600)
	if _, err := LoadKey(plain); err != nil {
		t.Errorf("a PKCS#8 Ed25519 key: %v", err)
	}
	if _, err := LoadKey(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing key file loaded")
	}
}

// fakeKMS is a KMS provider backed by a local key.
type fakeKMS struct {
	signature.SignerVerifier
	pubErr, signErr error
}

func (f fakeKMS) SignMessage(r io.Reader, o ...signature.SignOption) ([]byte, error) {
	if f.signErr != nil {
		return nil, f.signErr
	}
	return f.SignerVerifier.SignMessage(r, o...)
}

func (f fakeKMS) PublicKey(...signature.PublicKeyOption) (crypto.PublicKey, error) {
	if f.pubErr != nil {
		return nil, f.pubErr
	}
	return f.SignerVerifier.PublicKey()
}
func (fakeKMS) CreateKey(context.Context, string) (crypto.PublicKey, error) { return nil, nil }
func (fakeKMS) CryptoSigner(context.Context, func(error)) (crypto.Signer, crypto.SignerOpts, error) {
	return nil, nil, nil
}
func (fakeKMS) SupportedAlgorithms() []string { return nil }
func (fakeKMS) DefaultAlgorithm() string      { return "" }

func TestKMS(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sv, _ := signature.LoadSignerVerifier(key, crypto.SHA256)
	// An RSA key that signs with PSS: its public key alone looks like
	// PKCS#1 v1.5, which every verifier would then check it as.
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	pss, _ := signature.LoadRSAPSSSignerVerifier(rk, crypto.SHA256, nil)
	// An RSA PKCS#1 v1.5 key that hashes with SHA-512: NewKeypair accepts its
	// public key, so only the probe refuses it.
	sha512, _ := signature.LoadRSAPKCS1v15SignerVerifier(rk, crypto.SHA512)
	// A P-384 key, which NewKeypair refuses before the probe runs.
	ek, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p384, _ := signature.LoadSignerVerifier(ek, crypto.SHA384)
	kms.AddProvider("testkms://", func(_ context.Context, uri string, _ crypto.Hash, _ ...signature.RPCOption) (kms.SignerVerifier, error) {
		switch uri {
		case "testkms://down":
			return nil, errors.New("kms down")
		case "testkms://nopub":
			return fakeKMS{SignerVerifier: sv, pubErr: errors.New("denied")}, nil
		case "testkms://nosign":
			return fakeKMS{SignerVerifier: sv, signErr: errors.New("denied")}, nil
		case "testkms://pss":
			return fakeKMS{SignerVerifier: pss}, nil
		case "testkms://sha512":
			return fakeKMS{SignerVerifier: sha512}, nil
		case "testkms://p384":
			return fakeKMS{SignerVerifier: p384}, nil
		}
		return fakeKMS{SignerVerifier: sv}, nil
	})
	kp, err := KMS(t.Context(), "testkms://key")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Keyed(kp, "").Sign(t.Context(), "application/vnd.in-toto+json", []byte(statement))
	if err != nil {
		t.Fatal(err)
	}
	if err := independentKeyVerify(t, b, key.Public()); err != nil {
		t.Errorf("a KMS signature does not verify: %v", err)
	}
	for _, uri := range []string{"testkms://down", "testkms://nopub", "testkms://nosign", "testkms://pss", "testkms://sha512", "testkms://p384", "nokms://key"} {
		if _, err := KMS(t.Context(), uri); err == nil {
			t.Errorf("%s loaded", uri)
		}
	}
	// A key no linked provider serves names the build to use.
	if _, err := KMS(t.Context(), "nokms://key"); err == nil || !strings.Contains(err.Error(), "-tags kms") {
		t.Errorf("no build named: %v", err)
	}
}

// TestKeylessRoundTrip: a keyless bundle verifies for the pinned identity
// and issuer under the root that issued it, and fails for another identity,
// another issuer, another root or no root.
func TestKeylessRoundTrip(t *testing.T) {
	ss := testutil.New(t)
	tok := testutil.Token("ci@acme.example", "https://issuer.example")
	s, err := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Sign(t.Context(), "application/vnd.in-toto+json", []byte(statement))
	if err != nil {
		t.Fatal(err)
	}
	ok := Policy{Identity: "ci@acme.example", Issuer: "https://issuer.example", Root: ss.Root}
	if _, err := ok.Verify(b, nil); err != nil {
		t.Fatal(err)
	}
	// An independent check: sigstore-go's own verifier over the trusted root
	// JSON, as `cosign verify-blob-attestation --trusted-root` reads it.
	tr, _ := root.NewTrustedRootFromJSON(ss.RootJSON)
	var bun bundle.Bundle
	_ = bun.UnmarshalJSON(b)
	sv, _ := verify.NewVerifier(tr, verify.WithSignedTimestamps(1))
	id, _ := verify.NewShortCertificateIdentity("https://issuer.example", "", "ci@acme.example", "")
	if _, err := sv.Verify(&bun, verify.NewPolicy(verify.WithoutArtifactUnsafe(), verify.WithCertificateIdentity(id))); err != nil {
		t.Errorf("an independent verifier rejects it: %v", err)
	}
	other := testutil.New(t)
	for name, p := range map[string]Policy{
		"identity": {Identity: "mallory@acme.example", Issuer: ok.Issuer, Root: ss.Root},
		"issuer":   {Identity: ok.Identity, Issuer: "https://evil.example", Root: ss.Root},
		"root":     {Identity: ok.Identity, Issuer: ok.Issuer, Root: other.Root},
		"no root":  {Identity: ok.Identity, Issuer: ok.Issuer},
		"tlog":     {Identity: ok.Identity, Issuer: ok.Issuer, Root: ss.Root, Tlog: true},
		"none":     {Root: ss.Root},
	} {
		if _, err := p.Verify(b, nil); err == nil {
			t.Errorf("wrong %s verified", name)
		}
	}
	// A root that lists a CT log requires the certificate's SCT.
	vs, _ := ca.NewVirtualSigstore()
	withCT, _ := root.NewTrustedRoot(root.TrustedRootMediaType01, ss.Root.FulcioCertificateAuthorities(), vs.CTLogs(),
		ss.Root.TimestampingAuthorities(), nil)
	if _, err := (Policy{Identity: ok.Identity, Issuer: ok.Issuer, Root: withCT}).Verify(b, nil); err == nil {
		t.Error("a certificate without an SCT verified under a root with a CT log")
	}
	// cosign must be told to skip the SCT exactly when the root lists no CT log.
	ctJSON, _ := withCT.MarshalJSON()
	if !WithoutSCT(string(ss.RootJSON)) || WithoutSCT(string(ctJSON)) || WithoutSCT("{") {
		t.Error("WithoutSCT misreads which roots list a CT log")
	}
	// The statement's subject is checked when a digest is given.
	zero := make([]byte, sha256.Size)
	if _, err := ok.Verify(b, zero); err != nil {
		t.Errorf("the subject digest: %v", err)
	}
	if _, err := ok.Verify(b, bytes.Repeat([]byte{1}, sha256.Size)); err == nil {
		t.Error("another digest verified")
	}
	if _, err := ok.Verify([]byte("{"), nil); err == nil {
		t.Error("a broken bundle verified")
	}
	if _, err := (Policy{Key: struct{}{}}).Verify(b, nil); err == nil {
		t.Error("an unknown key type verified")
	}
}

func TestKeylessFailures(t *testing.T) {
	ss := testutil.New(t)
	if _, err := Keyless(ss.Fulcio, "", "", nil, nil); err == nil {
		t.Error("keyless without a timestamp authority or a log")
	}
	s, _ := Keyless(ss.Fulcio, "", "https://rekor.invalid", nil, nil)
	if len(s.Rekor) != 1 {
		t.Error("the Rekor URL adds a log")
	}
	noTok, _ := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return "", errors.New("no token") }, ss.Root)
	if _, err := noTok.Sign(t.Context(), "t", nil); err == nil || !strings.Contains(err.Error(), "OIDC token") {
		t.Errorf("a missing token: %v", err)
	}
	badTok, _ := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return "garbage", nil }, ss.Root)
	if _, err := badTok.Sign(t.Context(), "t", nil); err == nil {
		t.Error("Fulcio accepted a token with no subject")
	}
}

func TestIDToken(t *testing.T) {
	ctx := t.Context()
	file := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(file, []byte("from-file\n"), 0o600)
	if tok, err := IDToken(file)(ctx); err != nil || tok != "from-file" {
		t.Errorf("file: %q %v", tok, err)
	}
	t.Setenv("SIGSTORE_ID_TOKEN", "from-env")
	if tok, err := IDToken("")(ctx); err != nil || tok != "from-env" {
		t.Errorf("env: %q %v", tok, err)
	}
	t.Setenv("SIGSTORE_ID_TOKEN", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	if _, err := IDToken("")(ctx); err == nil {
		t.Error("no token source")
	}
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audience") != "sigstore" || r.Header.Get("Authorization") != "Bearer req-token" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"value":"from-github"}`))
	}))
	defer gh.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", gh.URL+"?x=1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "req-token")
	if tok, err := IDToken("")(ctx); err != nil || tok != "from-github" {
		t.Errorf("github: %q %v", tok, err)
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "wrong")
	if _, err := IDToken("")(ctx); err == nil {
		t.Error("a refused GitHub token request")
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "http://127.0.0.1:1/?x=1")
	if _, err := IDToken("")(ctx); err == nil {
		t.Error("an unreachable GitHub token endpoint")
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "://bad")
	if _, err := IDToken("")(ctx); err == nil {
		t.Error("a malformed GitHub token URL")
	}
}

func TestPublicGoodRootFailsClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := PublicGoodRoot(srv.URL); err == nil {
		t.Error("a TUF mirror that answers nothing gave a trusted root")
	}
}
