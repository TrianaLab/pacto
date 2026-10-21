package delivery

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

func pubPEM(t *testing.T, pub any) string {
	t.Helper()
	s, err := PublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTrustScopesKeysToStreams(t *testing.T) {
	devPub, dev, _ := ed25519.GenerateKey(rand.Reader)
	prodPub, prod, _ := ed25519.GenerateKey(rand.Reader)
	oldPub, old, _ := ed25519.GenerateKey(rand.Reader)
	raw := "keys:\n" +
		"  - {name: dev, publicKey: " + quoteYAML(pubPEM(t, devPub)) + ", streams: ['*/dev/*']}\n" +
		"  - {name: prod, publicKey: " + quoteYAML(pubPEM(t, prodPub)) + ", streams: ['*/prod/*']}\n" +
		"  - {name: prod-old, publicKey: " + quoteYAML(pubPEM(t, oldPub)) + ", streams: ['*/prod/*']}\n"
	trust, err := ParseTrust([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := trust.Verifier()
	pae := PAE(PayloadType, []byte("{}"))
	sign := func(k ed25519.PrivateKey) ([]byte, VerificationMaterial) {
		return ed25519.Sign(k, pae), VerificationMaterial{PublicKey: &KeyHint{Hint: KeyHintOf(k.Public())}}
	}
	for _, c := range []struct {
		name   string
		key    ed25519.PrivateKey
		stream string
		ok     bool
	}{
		{"prod key, prod stream", prod, "c/prod/payments", true},
		{"a rotated-out key still listed", old, "c/prod/payments", true},
		{"dev key, prod stream", dev, "c/prod/payments", false},
		{"prod key, dev stream", prod, "c/dev/payments", false},
		{"a stream no key covers", prod, "c/test/payments", false},
	} {
		sig, m := sign(c.key)
		if err := v(c.stream, pae, sig, m); (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	if trust.Digest() == (TrustConfig{}).Digest() || !strings.HasPrefix(trust.Digest(), "sha256:") {
		t.Fatalf("digest = %s", trust.Digest())
	}

	bare, err := ParseTrust([]byte(pubPEM(t, prodPub)))
	if err != nil {
		t.Fatal(err)
	}
	bv, _ := bare.Verifier()
	if sig, m := sign(prod); bv("any/stream/at-all", pae, sig, m) != nil {
		t.Fatal("a bare public key does not cover every stream")
	}
}

func quoteYAML(s string) string { return `"` + strings.ReplaceAll(s, "\n", `\n`) + `"` }

func TestParseTrustRefuses(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pemKey := quoteYAML(pubPEM(t, pub))
	for name, raw := range map[string]string{
		"not yaml":            "keys: [",
		"unknown field":       "keys: [{publicKey: " + pemKey + ", colour: red}]",
		"no keys":             "keys: []",
		"bad pattern":         "keys: [{publicKey: " + pemKey + ", streams: ['[']}]",
		"key and identity":    "keys: [{publicKey: " + pemKey + ", identity: a, issuer: b}]",
		"identity, no issuer": "keys: [{identity: a}]",
		"not pem":             "keys: [{publicKey: nope}]",
		"bad key":             "keys: [{publicKey: \"-----BEGIN PUBLIC KEY-----\\nAAAA\\n-----END PUBLIC KEY-----\\n\"}]",
		"bad roots":           "keys: [{identity: a, issuer: b, roots: nope}]",
		"bare garbage pem":    "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n",
	} {
		if _, err := ParseTrust([]byte(raw)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := PublicKeyPEM(struct{}{}); err == nil {
		t.Fatal("an unsupported key marshalled")
	}
}

func TestKeylessTrustChecksTheChain(t *testing.T) {
	const id, iss = "https://kubernetes.io/namespaces/pacto/serviceaccounts/recorder", "https://issuer.example"
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fulcio"}, IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	v2, _ := asn1.Marshal(iss)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	u, _ := url.Parse(id)
	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(2), URIs: []*url.URL{u}, NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(9 * time.Minute), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtraExtensions: []pkix.Extension{{Id: oidIssuerV2, Value: v2}}}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	pae := PAE(PayloadType, []byte("{}"))
	digest := sha256.Sum256(pae)
	sig, _ := ecdsa.SignASN1(rand.Reader, leafKey, digest[:])
	m := VerificationMaterial{Certificate: &Certificate{RawBytes: leafDER}}

	roots := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	trust := TrustConfig{Keys: []TrustedKey{{Identity: id, Issuer: iss, Roots: roots}}}
	v, err := trust.Verifier()
	if err != nil {
		t.Fatal(err)
	}
	if err := v("s", pae, sig, m); err != nil {
		t.Fatalf("a certificate under the trusted root: %v", err)
	}
	// The same identity under another root does not count.
	_, otherDER := selfSigned(t, id, []pkix.Extension{{Id: oidIssuerV2, Value: v2}})
	other := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherDER}))
	ov, _ := TrustConfig{Keys: []TrustedKey{{Identity: id, Issuer: iss, Roots: other}}}.Verifier()
	if err := ov("s", pae, sig, m); err == nil || !strings.Contains(err.Error(), "does not chain") {
		t.Fatalf("a certificate under another root verified: %v", err)
	}
}
