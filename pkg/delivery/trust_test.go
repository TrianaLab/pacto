package delivery

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

func pubPEM(t *testing.T, pub any) string {
	t.Helper()
	s, err := PublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func quoteYAML(s string) string { return `"` + strings.ReplaceAll(s, "\n", `\n`) + `"` }

// byName opens each key as a check that accepts the bundle naming it, so the
// stream scoping is tested without crypto; pkg/signing tests the crypto.
func byName(k TrustedKey) (func([]byte) error, error) {
	return func(b []byte) error {
		if string(b) != k.Name {
			return errors.New("signed by " + string(b))
		}
		return nil
	}, nil
}

func TestTrustScopesKeysToStreams(t *testing.T) {
	devPub, _, _ := ed25519.GenerateKey(rand.Reader)
	raw := "keys:\n" +
		"  - {name: dev, publicKey: " + quoteYAML(pubPEM(t, devPub)) + ", streams: ['*/dev/*']}\n" +
		"  - {name: prod, publicKey: " + quoteYAML(pubPEM(t, devPub)) + ", streams: ['*/prod/*']}\n" +
		"  - {name: prod-old, publicKey: " + quoteYAML(pubPEM(t, devPub)) + ", streams: ['*/prod/*']}\n" +
		"  - {name: ci, identity: ci@acme, issuer: https://issuer, trustedRoot: '{}', streams: ['*/ci/*']}\n"
	trust, err := ParseTrust([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := trust.Verifier(byName)
	for _, c := range []struct {
		name, key, stream string
		ok                bool
	}{
		{"prod key, prod stream", "prod", "c/prod/payments", true},
		{"a rotated-out key still listed", "prod-old", "c/prod/payments", true},
		{"dev key, prod stream", "dev", "c/prod/payments", false},
		{"prod key, dev stream", "prod", "c/dev/payments", false},
		{"a stream no key covers", "prod", "c/test/payments", false},
		{"keyless", "ci", "c/ci/payments", true},
	} {
		if err := v(c.stream, 1, []byte(c.key)); (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
	if k, ok := trust.For("c/ci/payments"); !ok || k.Name != "ci" {
		t.Errorf("For(ci) = %v %v", k, ok)
	}
	if _, ok := trust.For("c/test/payments"); ok {
		t.Error("For found a key for a stream no key covers")
	}
	if trust.Digest() == (TrustConfig{}).Digest() || !strings.HasPrefix(trust.Digest(), "sha256:") {
		t.Fatalf("digest = %s", trust.Digest())
	}
	if _, err := trust.Verifier(func(TrustedKey) (func([]byte) error, error) { return nil, errors.New("bad key") }); err == nil {
		t.Fatal("a key open refused still built a verifier")
	}

	bare, err := ParseTrust([]byte(pubPEM(t, devPub)))
	if err != nil {
		t.Fatal(err)
	}
	bv, _ := bare.Verifier(byName)
	if bv("any/stream/at-all", 1, []byte("")) != nil {
		t.Fatal("a bare public key does not cover every stream")
	}
}

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
		"bad trusted root":    "keys: [{identity: a, issuer: b, trustedRoot: nope}]",
		"keyless, no root":    "keys: [{identity: a, issuer: b}]",
		"the old roots field": "keys: [{identity: a, issuer: b, roots: x}]",
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

// TestTrustRevokesAKeyAfterASeq: a key with untilSeq verifies the history it
// signed and nothing after it, on its own stream only, while the key that
// replaced it signs on.
func TestTrustRevokesAKeyAfterASeq(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	raw := "keys:\n" +
		"  - {name: old, publicKey: " + quoteYAML(pubPEM(t, pub)) + ", streams: ['c/prod/payments'], untilSeq: 41}\n" +
		"  - {name: new, publicKey: " + quoteYAML(pubPEM(t, pub)) + ", streams: ['*/prod/*']}\n"
	trust, err := ParseTrust([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	v, _ := trust.Verifier(byName)
	for _, c := range []struct {
		key, stream string
		seq         int64
		ok          bool
	}{
		{"old", "c/prod/payments", 41, true},
		{"old", "c/prod/payments", 42, false},
		{"old", "c/prod/orders", 1, false},
		{"new", "c/prod/payments", 42, true},
	} {
		err := v(c.stream, c.seq, []byte(c.key))
		if (err == nil) != c.ok {
			t.Errorf("%s at %s seq %d: err = %v", c.key, c.stream, c.seq, err)
		}
	}
	only := TrustConfig{Keys: trust.Keys[:1]}
	ov, _ := only.Verifier(byName)
	if err := ov("c/prod/payments", 42, []byte("old")); err == nil || !strings.Contains(err.Error(), "revoked after seq 41") {
		t.Errorf("a revoked key's refusal = %v", err)
	}
	// The revocation is named even when a later key fails to verify too.
	if err := v("c/prod/payments", 42, []byte("old")); err == nil || !strings.Contains(err.Error(), "revoked after seq 41") {
		t.Errorf("refusal = %v", err)
	}
	if _, err := ParseTrust([]byte("keys:\n  - {publicKey: " + quoteYAML(pubPEM(t, pub)) + ", untilSeq: -1}\n")); err == nil {
		t.Error("a negative untilSeq parsed")
	}
}
