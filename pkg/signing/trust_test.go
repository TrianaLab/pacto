package signing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
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

// TestRunnerSignsItsVerdict: an eval provider counts an answer only when a
// runner it trusts signed it, keyed or keyless, and the verdict names that
// runner and keeps the signed answer for the record.
func TestRunnerSignsItsVerdict(t *testing.T) {
	ctx := t.Context()
	ss := testutil.New(t)
	_, runnerKey, _ := ed25519.GenerateKey(rand.Reader)
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	subject := "sha256:" + strings.Repeat("a", 64)
	suite := "sha256:67fd4cc91da7c4d6870049981fed4d367327a4bb687e73666652796d80b533b8"
	payload, _ := json.Marshal(delivery.EvalStatement{Type: delivery.StatementType, PredicateType: delivery.PredicateEval,
		Subject:   []delivery.Subject{{Name: "pay-agent", Digest: map[string]string{"sha256": strings.Repeat("a", 64)}}},
		Predicate: delivery.EvalResult{Suite: suite, Run: "run-7", Passed: 2, Total: 2}})
	keyed := func(key ed25519.PrivateKey) []byte {
		kp, _ := FromPrivateKey(key)
		b, err := Keyed(kp, "").Sign(ctx, delivery.PayloadType, payload)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	tok := testutil.Token("evals@acme.example", "https://issuer.example")
	ks, _ := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, ss.Root)
	keyless, err := ks.Sign(ctx, delivery.PayloadType, payload)
	if err != nil {
		t.Fatal(err)
	}
	var answer []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(answer) }))
	defer srv.Close()
	gates, err := delivery.NewGates([]delivery.ProviderConfig{{Name: "evals", Type: delivery.ProviderEval, URL: srv.URL, Signers: []delivery.TrustedKey{
		{Name: "evals-ci", PublicKey: pemOf(t, runnerKey.Public())},
		{Name: "evals-keyless", Identity: "evals@acme.example", Issuer: "https://issuer.example", TrustedRoot: string(ss.RootJSON)},
	}}}, srv.Client(), OpenKey)
	if err != nil {
		t.Fatal(err)
	}
	e := gates["evals"].(delivery.Eval)
	e.Revision = func(context.Context, delivery.GateInput) delivery.Revision {
		return delivery.Revision{ContractRef: "oci://r/pay-agent@sha256:c0", Bundle: &contract.Bundle{
			Contract: &contract.Contract{Checks: []contract.Check{{Name: "evals", File: "evals/suite.yaml"}}},
			FS:       fstest.MapFS{"evals/suite.yaml": {Data: []byte("cases: []\n")}}}}
	}
	in := delivery.GateInput{Subjects: []string{subject}}
	for _, c := range []struct {
		name   string
		bundle []byte
		want   delivery.Result
		runner string
	}{
		{"a trusted key", keyed(runnerKey), delivery.Pass, "evals-ci"},
		{"a trusted identity", keyless, delivery.Pass, "evals-keyless (evals@acme.example from https://issuer.example)"},
		{"a stranger's key", keyed(stranger), delivery.Unknown, ""},
	} {
		answer = c.bundle
		v := e.Check(ctx, in, nil)
		if v.Result != c.want || v.Citation["runner"] != c.runner {
			t.Errorf("%s: %+v", c.name, v)
			continue
		}
		if c.want == delivery.Pass {
			verify, _ := delivery.RunnersOf([]delivery.TrustedKey{{Name: "audit", PublicKey: pemOf(t, runnerKey.Public())},
				{Name: "audit-keyless", Identity: "evals@acme.example", Issuer: "https://issuer.example", TrustedRoot: string(ss.RootJSON)}}, OpenKey)
			if who, err := delivery.CheckRunner(v, verify); err != nil || !strings.HasPrefix(who, "audit") {
				t.Errorf("%s: an auditor re-checks the kept answer: %q %v", c.name, who, err)
			}
		}
	}
}
