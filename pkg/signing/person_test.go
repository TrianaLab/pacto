package signing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const personIssuer = "https://login.acme.example"

func personBundle(t *testing.T, ss *testutil.Sigstore, san, issuer string, timestamps bool) []byte {
	t.Helper()
	tok := testutil.Token(san, issuer)
	s, err := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !timestamps {
		s.Timestamps = nil
	}
	b, err := s.Sign(t.Context(), "application/vnd.in-toto+json", []byte(statement))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestVerifyPersonReturnsSANAndTime: any subject the issuer vouches for under
// the trusted root verifies, and the answer names it and the time the
// timestamp authority observed.
func TestVerifyPersonReturnsSANAndTime(t *testing.T) {
	ss := testutil.New(t)
	before := time.Now().Add(-time.Second)
	san, at, err := VerifyPerson(personBundle(t, ss, "alice@acme.com", personIssuer, true), personIssuer, string(ss.RootJSON))
	if err != nil || san != "alice@acme.com" || at.Before(before) || at.After(time.Now().Add(time.Second)) || at.Location() != time.UTC {
		t.Fatalf("VerifyPerson = %q %v %v", san, at, err)
	}
}

// TestVerifyPersonRefusesOtherIssuer: a certificate from another issuer, one
// under another root, a key-signed bundle and a broken root never verify.
func TestVerifyPersonRefusesOtherIssuer(t *testing.T) {
	ss := testutil.New(t)
	b := personBundle(t, ss, "alice@acme.com", "https://other.example", true)
	if _, _, err := VerifyPerson(b, personIssuer, string(ss.RootJSON)); err == nil || !strings.Contains(err.Error(), personIssuer) {
		t.Fatalf("another issuer verified: %v", err)
	}
	other := testutil.New(t)
	if _, _, err := VerifyPerson(personBundle(t, ss, "alice@acme.com", personIssuer, true), personIssuer, string(other.RootJSON)); err == nil {
		t.Fatal("a certificate under another root verified")
	}
	if _, _, err := VerifyPerson(b, personIssuer, "{"); err == nil || !strings.Contains(err.Error(), "trustedRoot") {
		t.Fatalf("a broken root: %v", err)
	}
}

// TestVerifyPersonNeedsObserverTime: a certificate lives minutes, so a
// bundle no timestamp authority or log observed proves no signing time.
func TestVerifyPersonNeedsObserverTime(t *testing.T) {
	ss := testutil.New(t)
	if _, _, err := VerifyPerson(personBundle(t, ss, "alice@acme.com", personIssuer, false), personIssuer, string(ss.RootJSON)); err == nil {
		t.Fatal("a bundle with no observed time verified")
	}
}

// approvalBundle is an approval record signed keyless as san by kind.
func approvalBundle(t *testing.T, ss *testutil.Sigstore, san, kind string) []byte {
	t.Helper()
	tok := testutil.Token(san, personIssuer)
	s, err := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := delivery.ApprovalStatement{Type: delivery.StatementType, PredicateType: delivery.PredicateApproval,
		Predicate: delivery.ApprovalPredicate{Operation: delivery.ApprovedOperation{Name: "p1", UID: "u1"}, Decision: delivery.Approve,
			Signer: delivery.ApprovalSigner{Kind: kind}, Time: "2026-01-01T00:00:00Z"}}
	b, _, err := delivery.SignApproval(t.Context(), s, st)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestOpenPersonApproval: a person-signed approval verifies under the signer
// of its issuer and names who it speaks for; no signer, another root or a
// record that does not say a person signed it never does.
func TestOpenPersonApproval(t *testing.T) {
	ss := testutil.New(t)
	other := testutil.New(t)
	p := delivery.ClassPolicy{Signers: []delivery.PersonSigner{
		{Issuer: personIssuer, TrustedRoot: string(other.RootJSON), UsernamePrefix: "oidc:", Subjects: `^[^@]+@acme\.com$`},
		{Issuer: personIssuer, TrustedRoot: string(ss.RootJSON), UsernamePrefix: "oidc:", Subjects: `^[^@]+@acme\.com$`},
	}}
	a, err := OpenPersonApproval(approvalBundle(t, ss, "alice@acme.com", delivery.SignerPerson), p)
	if err != nil || a.Issuer != personIssuer || a.SAN != "alice@acme.com" || a.Identity.User != "oidc:alice@acme.com" ||
		a.Statement.Predicate.Operation.UID != "u1" || !strings.HasPrefix(a.ID, "sha256:") || a.At.IsZero() {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := OpenPersonApproval(approvalBundle(t, ss, "alice@acme.com", delivery.SignerPerson), delivery.ClassPolicy{}); err == nil ||
		!strings.Contains(err.Error(), "no approver signer") {
		t.Fatal(err)
	}
	if _, err := OpenPersonApproval(approvalBundle(t, ss, "alice@acme.com", delivery.SignerRecorder), p); err == nil ||
		!strings.Contains(err.Error(), "does not say a person signed it") {
		t.Fatal(err)
	}
	if _, err := OpenPersonApproval(approvalBundle(t, ss, "alice@acme.com", delivery.SignerPerson), delivery.ClassPolicy{Signers: p.Signers[:1]}); err == nil {
		t.Fatal("a certificate under another root verified")
	}
}
