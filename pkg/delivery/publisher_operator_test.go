package delivery

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"testing"
)

// TestJudgeOperatorIdentityRefusedFirst: the operator's keyless identity is
// refused with source operator even when a rule covers it, and it never
// outweighs another signer of the same digest.
func TestJudgeOperatorIdentityRefusedFirst(t *testing.T) {
	rules := []PublisherRule{acmeRule(t)}
	op := &TrustedKey{Identity: ciAcme.Subject, Issuer: ciAcme.Issuer}
	v := JudgePublisher(payments, ciAcme, "", rules, PublisherSourceRule, nil, op)
	if v.Verdict != VerdictRefused || v.Source != PublisherSourceOperator || v.Identity.Subject != ciAcme.Subject {
		t.Fatalf("the operator was judged a publisher: %+v", v)
	}
	if v := JudgeSigners(payments, []PublisherIdentity{ciAcme}, "", rules, PublisherSourceRule, nil, op); v.Source != PublisherSourceOperator || v.Verdict != VerdictRefused {
		t.Fatalf("operator alone: %+v", v)
	}
	// With another signer the operator is left out: the person is judged.
	if v := JudgeSigners(payments, []PublisherIdentity{ciAcme, person}, "", rules, PublisherSourceRule, nil, op); v.Source != PublisherSourceNone || v.Identity.Subject != person.Subject {
		t.Fatalf("operator beside a person: %+v", v)
	}
	// An accepted publisher keeps its verdict beside an operator signature.
	asPerson := &TrustedKey{Identity: person.Subject, Issuer: person.Issuer}
	if v := JudgeSigners(payments, []PublisherIdentity{person, ciAcme}, "", rules, PublisherSourceRule, nil, asPerson); v.Verdict != VerdictAccepted {
		t.Fatalf("operator beside an accepted publisher: %+v", v)
	}
}

// TestJudgeNilInferredUnchanged: with no operator signer, or a key one that
// names no keyless identity, the verdict is the one the rules give.
func TestJudgeNilInferredUnchanged(t *testing.T) {
	rules := []PublisherRule{acmeRule(t)}
	for _, op := range []*TrustedKey{nil, {PublicKey: "pem"}} {
		if v := JudgePublisher(payments, ciAcme, "", rules, PublisherSourceRule, nil, op); v.Verdict != VerdictAccepted {
			t.Fatalf("%+v: %+v", op, v)
		}
	}
}

func TestInferredSignerFromAdvertisement(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	k := InferredSignerFromAdvertisement(base64.StdEncoding.EncodeToString(der))
	if k == nil || len(k.Streams) != 1 || k.Streams[0] != "pacto-inferred/*" || k.Identity != "" {
		t.Fatalf("signer %+v", k)
	}
	if b, _ := pem.Decode([]byte(k.PublicKey)); b == nil || string(b.Bytes) != string(der) {
		t.Fatalf("the key is not the advertised one: %q", k.PublicKey)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("not a key"))} {
		if k := InferredSignerFromAdvertisement(bad); k != nil {
			t.Errorf("%q: %+v", bad, k)
		}
	}
}
