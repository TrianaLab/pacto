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
	who, at, err := VerifyPerson(personBundle(t, ss, "alice@acme.com", personIssuer, true), personIssuer, string(ss.RootJSON))
	if err != nil || who.Subject != "alice@acme.com" || who.Issuer != personIssuer || who.CI || at.Before(before) || at.After(time.Now().Add(time.Second)) || at.Location() != time.UTC {
		t.Fatalf("VerifyPerson = %+v %v %v", who, at, err)
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
	return approvalBundleOf(t, ss, testutil.Token(san, personIssuer), kind)
}

// approvalBundleOf is an approval record signed keyless with the OIDC token
// tok by kind.
func approvalBundleOf(t *testing.T, ss *testutil.Sigstore, tok, kind string) []byte {
	t.Helper()
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

// TestOpenPersonApprovalClassifiesSigner: a person's email certificate reads
// as a decision; the class and what classified it come with the record.
func TestOpenPersonApprovalClassifiesSigner(t *testing.T) {
	ss := testutil.New(t)
	a, err := OpenPersonApproval(approvalBundle(t, ss, "alice@acme.com", delivery.SignerPerson), signersFor(ss, personIssuer))
	if err != nil || a.Class != delivery.ClassHuman || !strings.HasPrefix(a.ClassifiedBy, "default:") || !delivery.ReadsAsDecision(a.Class) {
		t.Fatalf("%+v %v", a, err)
	}
}

// TestCISignedApprovalIsProposal: an approval a GitHub Actions workflow
// signed verifies, but its signer is a CI workload, so it is a proposal.
func TestCISignedApprovalIsProposal(t *testing.T) {
	ss := testutil.New(t)
	const gha = "https://token.actions.githubusercontent.com"
	tok := testutil.CIToken("https://github.com/acme/app/.github/workflows/ship.yml@refs/heads/main", gha, "https://github.com/acme/app", "refs/heads/main")
	a, err := OpenPersonApproval(approvalBundleOf(t, ss, tok, delivery.SignerPerson), signersFor(ss, gha))
	if err != nil || a.Class != delivery.ClassCI || delivery.ReadsAsDecision(a.Class) {
		t.Fatalf("%+v %v", a, err)
	}
}

// TestWorkloadIdentitySignedApprovalIsProposal: an approval a Kubernetes
// service account signed is an agent's, a proposal.
func TestWorkloadIdentitySignedApprovalIsProposal(t *testing.T) {
	ss := testutil.New(t)
	tok := testutil.CIToken("https://kubernetes.io/namespaces/agents/serviceaccounts/reviewer", personIssuer, "", "")
	a, err := OpenPersonApproval(approvalBundleOf(t, ss, tok, delivery.SignerPerson), signersFor(ss, personIssuer))
	if err != nil || a.Class != delivery.ClassAgent || a.SAN != "https://kubernetes.io/namespaces/agents/serviceaccounts/reviewer" || delivery.ReadsAsDecision(a.Class) {
		t.Fatalf("%+v %v", a, err)
	}
}

// signersFor accepts any subject issuer's certificates under ss.
func signersFor(ss *testutil.Sigstore, issuer string) delivery.ClassPolicy {
	return delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: issuer, TrustedRoot: string(ss.RootJSON), UsernamePrefix: "oidc:", Subjects: `.+`}}}
}

// TestVerifyPersonRecord: a record counts only when it verifies under an
// approver signer, maps to a person and names that person as its approver.
func TestVerifyPersonRecord(t *testing.T) {
	ss := testutil.New(t)
	p := delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: personIssuer, TrustedRoot: string(ss.RootJSON), UsernamePrefix: "oidc:", Subjects: `^[^@]+@acme\.com$`}}}
	open := func(b []byte, approver string) func(func([]byte) error) (string, error) {
		return func(verify func([]byte) error) (string, error) { return approver, verify(b) }
	}
	alice := personBundle(t, ss, "alice@acme.com", personIssuer, true)
	if user, at, why := VerifyPersonRecord(p, "join record", open(alice, "oidc:alice@acme.com")); user != "oidc:alice@acme.com" || at.IsZero() || why != "" {
		t.Fatalf("%q %v %q", user, at, why)
	}
	if _, _, why := VerifyPersonRecord(p, "join record", open(alice, "oidc:bob@acme.com")); !strings.Contains(why, "claiming approver oidc:bob@acme.com is signed as oidc:alice@acme.com") {
		t.Fatal(why)
	}
	if _, _, why := VerifyPersonRecord(p, "join record", open(personBundle(t, ss, "alice@acme.com", "https://other.example", true), "")); !strings.HasPrefix(why, "a join record was not counted: ") {
		t.Fatal(why)
	}
	if _, _, why := VerifyPersonRecord(p, "join record", open(personBundle(t, ss, "eve@evil.example", personIssuer, true), "")); !strings.Contains(why, "signed by eve@evil.example was not counted") {
		t.Fatal(why)
	}
	agent := approvalBundleOf(t, ss, testutil.CIToken("https://kubernetes.io/namespaces/agents/serviceaccounts/reviewer", personIssuer, "", ""), delivery.SignerPerson)
	if _, _, why := VerifyPersonRecord(signersFor(ss, personIssuer), "join record", func(verify func([]byte) error) (string, error) {
		_, _, err := delivery.OpenApproval(agent, verify)
		return "", err
	}); !strings.Contains(why, "agents cannot approve") {
		t.Fatal(why)
	}
}
