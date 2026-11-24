package signing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
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

// openWith is a VerifyPersonRecord open func that verifies b and names named.
func openWith(b []byte, named string) func(func([]byte) error) (string, error) {
	return func(verify func([]byte) error) (string, error) { return named, verify(b) }
}

// TestVerifyPersonRecordRefusesUnlistedIssuer: a certificate from an issuer
// no signer lists never verifies and has no class; a signer with no subjects
// vouches for nobody; a subject outside the signer's is refused.
func TestVerifyPersonRecordRefusesUnlistedIssuer(t *testing.T) {
	ss := testutil.New(t)
	b := personBundle(t, ss, "alice@acme.com", "https://other.example", true)
	user, at, class, why := VerifyPersonRecord(signersFor(ss, personIssuer), "member request", openWith(b, "oidc:alice@acme.com"))
	if user != "" || !at.IsZero() || class != "" || !strings.Contains(why, "a member request was not counted") {
		t.Fatalf("%q %v %q %q", user, at, class, why)
	}
	good := personBundle(t, ss, "alice@acme.com", personIssuer, true)
	p := signersFor(ss, personIssuer)
	p.Signers[0].Subjects = ""
	if _, _, _, why := VerifyPersonRecord(p, "member request", openWith(good, "oidc:alice@acme.com")); !strings.Contains(why, "vouches for nobody") {
		t.Fatal(why)
	}
	p.Signers[0].Subjects = `.+@other\.com`
	if _, at, class, why := VerifyPersonRecord(p, "member request", openWith(good, "oidc:alice@acme.com")); at.IsZero() || class != delivery.ClassHuman ||
		!strings.Contains(why, "agents cannot approve") {
		t.Fatalf("%v %q %q", at, class, why)
	}
}

// TestVerifyPersonRecordClassifiesSigner: a person's email certificate is a
// person's and verifies as the user it names; a CI workflow's certificate
// is CI; a record naming another user than its certificate is refused.
func TestVerifyPersonRecordClassifiesSigner(t *testing.T) {
	ss := testutil.New(t)
	b := personBundle(t, ss, "alice@acme.com", personIssuer, true)
	user, at, class, why := VerifyPersonRecord(signersFor(ss, personIssuer), "member request", openWith(b, "oidc:alice@acme.com"))
	if user != "oidc:alice@acme.com" || at.IsZero() || class != delivery.ClassHuman || why != "" {
		t.Fatalf("%q %v %q %q", user, at, class, why)
	}
	if _, _, _, why := VerifyPersonRecord(signersFor(ss, personIssuer), "member request", openWith(b, "oidc:bob@acme.com")); !strings.Contains(why, "claiming approver oidc:bob@acme.com is signed as oidc:alice@acme.com") {
		t.Fatal(why)
	}
	const gha = "https://token.actions.githubusercontent.com"
	tok := testutil.CIToken("https://github.com/acme/app/.github/workflows/ship.yml@refs/heads/main", gha, "https://github.com/acme/app", "refs/heads/main")
	s, err := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := s.Sign(t.Context(), "application/vnd.in-toto+json", []byte(statement))
	if err != nil {
		t.Fatal(err)
	}
	if _, at, class, why := VerifyPersonRecord(signersFor(ss, gha), "member request", openWith(ci, "x")); at.IsZero() || class != delivery.ClassCI || why == "" {
		t.Fatalf("%v %q %q", at, class, why)
	}
}

// TestVerifyJoinRecord: a join record alice signed verifies as hers under
// her issuer's signer and not at all with no signer.
func TestVerifyJoinRecord(t *testing.T) {
	ss := testutil.New(t)
	s, err := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return testutil.Token("alice@acme.com", personIssuer), nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	j := delivery.ClusterJoin{Cluster: "c-1", Hub: "hub-1", KeyID: Hint(pub), PublicKey: base64.StdEncoding.EncodeToString(der),
		Decision: delivery.JoinApprove, Approver: "oidc:alice@acme.com", At: "2026-11-23T10:00:00Z"}
	b, err := delivery.SignClusterJoin(t.Context(), s, delivery.NewClusterJoinStatement(j))
	if err != nil {
		t.Fatal(err)
	}
	if v := VerifyJoinRecord(signersFor(ss, personIssuer), b); !v.Verified || v.User != "oidc:alice@acme.com" || v.Record.Hub != "hub-1" || v.SignedAt.IsZero() {
		t.Fatalf("%+v", v)
	}
	if v := VerifyJoinRecord(delivery.ClassPolicy{}, b); v.Verified || v.Why == "" {
		t.Fatalf("%+v", v)
	}
}
