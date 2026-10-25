package signing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

func pemOf(t *testing.T, pub any) string {
	t.Helper()
	s, err := delivery.PublicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestTrustVerifier: a trust config verifies real bundles per stream, keyed
// and keyless, and refuses what its keys did not sign.
func TestTrustVerifier(t *testing.T) {
	ctx := t.Context()
	ss := testutil.New(t)
	_, prod, _ := ed25519.GenerateKey(rand.Reader)
	_, dev, _ := ed25519.GenerateKey(rand.Reader)
	trust := delivery.TrustConfig{Keys: []delivery.TrustedKey{
		{Name: "prod", PublicKey: pemOf(t, prod.Public()), Streams: []string{"*/prod/*"}},
		{Name: "dev", PublicKey: pemOf(t, dev.Public()), Streams: []string{"*/dev/*"}},
		{Name: "recorder", Identity: "rec@acme.example", Issuer: "https://issuer.example", TrustedRoot: string(ss.RootJSON),
			Streams: []string{"*/ci/*"}},
	}}
	v, err := TrustVerifier(trust)
	if err != nil {
		t.Fatal(err)
	}
	signWith := func(key ed25519.PrivateKey) []byte {
		kp, _ := FromPrivateKey(key)
		b, err := Keyed(kp, "").Sign(ctx, delivery.PayloadType, []byte(statement))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	tok := testutil.Token("rec@acme.example", "https://issuer.example")
	ks, _ := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, ss.Root)
	keyless, err := ks.Sign(ctx, delivery.PayloadType, []byte(statement))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, stream string
		bundle       []byte
		ok           bool
	}{
		{"prod key, prod stream", "c/prod/p", signWith(prod), true},
		{"dev key, prod stream", "c/prod/p", signWith(dev), false},
		{"keyless recorder, its stream", "c/ci/p", keyless, true},
		{"keyless recorder, prod stream", "c/prod/p", keyless, false},
	} {
		if err := v(c.stream, 1, c.bundle); (err == nil) != c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	for name, k := range map[string]delivery.TrustedKey{
		"bad key":          {PublicKey: "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"},
		"no trusted root":  {Identity: "a", Issuer: "b"},
		"bad trusted root": {Identity: "a", Issuer: "b", TrustedRoot: "{}"},
		"both":             {PublicKey: "x", Identity: "a", Issuer: "b"},
	} {
		if _, err := TrustVerifier(delivery.TrustConfig{Keys: []delivery.TrustedKey{k}}); err == nil {
			t.Errorf("%s: built a verifier", name)
		} else if !strings.Contains(err.Error(), "trust config key 0") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
