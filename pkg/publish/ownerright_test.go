package publish

import (
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const personIssuer = "https://accounts.example"

var (
	ownerNow      = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	billingClaim  = delivery.OwnerClaim{Owner: contract.Owner{Team: "billing"}, Provenance: "declared"}
	ownerApprover = &delivery.OwnerPolicy{DefaultApprovers: []delivery.SignerRule{{Name: "platform-admins", Issuer: personIssuer, Subject: `^admin@acme\.example$`}}}
)

func ownerInput(reg Registry, ss *testutil.Sigstore, repo, d string) OwnerRightInput {
	return OwnerRightInput{Registry: reg, Root: ss.Root, Repo: repo, Digest: d, Service: "payments",
		Proposed: contract.Owner{Team: "payments"}, Current: billingClaim, Policy: ownerApprover, Now: ownerNow}
}

func TestVerifyOwnerRightAllowedSigner(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, signerFor(t, ss, testutil.Token("admin@acme.example", personIssuer)))
	if r := VerifyOwnerRight(t.Context(), ownerInput(reg, ss, repo, d)); !r.Allowed || r.Grant != "default-approver:platform-admins" {
		t.Fatalf("%+v", r)
	}
}

// TestVerifyOwnerRightFirstAllowedWins: a refused signer beside an allowed
// one does not refuse the change.
func TestVerifyOwnerRightFirstAllowedWins(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, signerFor(t, ss, testutil.Token("eve@acme.example", personIssuer)),
		signerFor(t, ss, testutil.Token("admin@acme.example", personIssuer)))
	if r := VerifyOwnerRight(t.Context(), ownerInput(reg, ss, repo, d)); !r.Allowed {
		t.Fatalf("%+v", r)
	}
	host, reg = testRegistry(t, true)
	repo, d = signed(t, host, reg, signerFor(t, ss, testutil.Token("eve@acme.example", personIssuer)))
	if r := VerifyOwnerRight(t.Context(), ownerInput(reg, ss, repo, d)); r.Allowed || r.Code != delivery.OwnerSignerNotAllowed {
		t.Fatalf("%+v", r)
	}
}

func TestVerifyOwnerRightUnsignedIsUnverified(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg)
	r := VerifyOwnerRight(t.Context(), ownerInput(reg, ss, repo, d))
	if r.Allowed || r.Code != delivery.OwnerSignerUnverified || !strings.Contains(r.Unverified, d) {
		t.Fatalf("%+v", r)
	}
	host, reg = testRegistry(t, true)
	repo, d = signed(t, host, reg, ciSigner(t, ss, acmeSubject, acmeRepo))
	r = VerifyOwnerRight(t.Context(), ownerInput(failingReferrers{reg}, ss, repo, d))
	if r.Allowed || r.Code != delivery.OwnerSignerUnverified || !strings.Contains(r.Message, "registry down") {
		t.Fatalf("%+v", r)
	}
}

// TestVerifyOwnerRightNotPerson: a CI identity never sets an owner, and a
// class rule that names a person's subject an agent makes it no person.
func TestVerifyOwnerRightNotPerson(t *testing.T) {
	ss := testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, ciSigner(t, ss, acmeSubject, acmeRepo))
	if r := VerifyOwnerRight(t.Context(), ownerInput(reg, ss, repo, d)); r.Allowed || r.Code != delivery.OwnerSignerNotPerson {
		t.Fatalf("%+v", r)
	}
	host, reg = testRegistry(t, true)
	repo, d = signed(t, host, reg, signerFor(t, ss, testutil.Token("admin@acme.example", personIssuer)))
	in := ownerInput(reg, ss, repo, d)
	in.ClassRules = []delivery.SignerClassRule{{Issuer: personIssuer, Subject: `^admin@acme\.example$`, Class: delivery.ClassAgent}}
	if r := VerifyOwnerRight(t.Context(), in); r.Allowed || r.Code != delivery.OwnerSignerNotPerson {
		t.Fatalf("%+v", r)
	}
}

// TestSignaturesProveNothing: only signatures that were all read and none of
// which verifies under the root prove nothing; no signature, a verified one,
// an unreadable registry and a missing root do not.
func TestSignaturesProveNothing(t *testing.T) {
	ss, other := testutil.New(t), testutil.New(t)
	host, reg := testRegistry(t, true)
	repo, d := signed(t, host, reg, signerFor(t, ss, testutil.Token("eve@acme.example", personIssuer)))
	if !SignaturesProveNothing(t.Context(), reg, other.Root, false, repo, d) {
		t.Error("a signature the root does not chain to proves something")
	}
	if SignaturesProveNothing(t.Context(), reg, ss.Root, false, repo, d) {
		t.Error("a verified signature proves nothing")
	}
	if SignaturesProveNothing(t.Context(), reg, nil, false, repo, d) {
		t.Error("no root proves nothing")
	}
	if SignaturesProveNothing(t.Context(), failingReferrers{reg}, other.Root, false, repo, d) {
		t.Error("an unreadable registry proves nothing")
	}
	host, reg = testRegistry(t, true)
	repo, d = signed(t, host, reg)
	if SignaturesProveNothing(t.Context(), reg, other.Root, false, repo, d) {
		t.Error("an unsigned bundle proves nothing")
	}
}
