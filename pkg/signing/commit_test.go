package signing

import (
	"testing"

	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/verify"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const acmeCommit = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

func resultWith(ext certificate.Extensions) *verify.VerificationResult {
	return &verify.VerificationResult{Signature: &verify.SignatureVerificationResult{
		Certificate: &certificate.Summary{Extensions: ext}}}
}

// TestCommitOfCI: a CI certificate names the commit its token was issued for.
func TestCommitOfCI(t *testing.T) {
	r := resultWith(certificate.Extensions{SourceRepositoryURI: acmeRepo, SourceRepositoryDigest: acmeCommit})
	if got := CommitOf(r); got != acmeCommit {
		t.Fatalf("commit %q", got)
	}
}

// TestCommitOfPersonIsEmpty: without a source repository there is no commit,
// whatever the digest extension says; nothing verified, nothing read.
func TestCommitOfPersonIsEmpty(t *testing.T) {
	for name, r := range map[string]*verify.VerificationResult{
		"no repository": resultWith(certificate.Extensions{SourceRepositoryDigest: acmeCommit}),
		"nil result":    nil,
		"nil signature": {},
		"nil cert":      {Signature: &verify.SignatureVerificationResult{}},
	} {
		if got := CommitOf(r); got != "" {
			t.Errorf("%s: commit %q", name, got)
		}
	}
}

// TestBundleSignatureForTable: only an accepted, verified CI verdict carries
// the signer repository and commit.
func TestBundleSignatureForTable(t *testing.T) {
	ci := &delivery.PublisherIdentity{Issuer: ghIssuer, Subject: acmeSubject, Repository: acmeRepo, CI: true}
	person := &delivery.PublisherIdentity{Issuer: ghIssuer, Subject: "ana@acme.example"}
	for _, tc := range []struct {
		name         string
		v            delivery.PublisherVerdict
		status       string
		repo, commit string
	}{
		{"accepted ci", delivery.PublisherVerdict{Verdict: delivery.VerdictAccepted, Source: delivery.PublisherSourceRule, Identity: ci}, fleet.SignatureVerified, acmeRepo, acmeCommit},
		{"accepted ci inferred", delivery.PublisherVerdict{Verdict: delivery.VerdictAccepted, Source: delivery.PublisherSourceInferred, Identity: ci}, fleet.SignatureUnknown, "", ""},
		{"accepted person", delivery.PublisherVerdict{Verdict: delivery.VerdictAccepted, Source: delivery.PublisherSourcePin, Identity: person}, fleet.SignatureVerified, "", ""},
		{"new signer", delivery.PublisherVerdict{Verdict: delivery.VerdictNewSigner, Source: delivery.PublisherSourceNone, Identity: ci}, fleet.SignatureUnverified, "", ""},
		{"refused", delivery.PublisherVerdict{Verdict: delivery.VerdictRefused, Source: delivery.PublisherSourceNone, Identity: ci}, fleet.SignatureUnverified, "", ""},
		{"unsigned", delivery.PublisherVerdict{Verdict: delivery.VerdictUnsigned, Source: delivery.PublisherSourceNone}, fleet.SignatureUnsigned, "", ""},
		{"unknown", delivery.PublisherVerdict{Verdict: delivery.VerdictUnknown, Source: delivery.PublisherSourceNone}, fleet.SignatureUnknown, "", ""},
	} {
		sig := BundleSignatureFor(tc.v, acmeCommit)
		if sig.Status != tc.status || sig.Source != tc.v.Source || sig.SignerRepository != tc.repo || sig.SignerCommit != tc.commit {
			t.Errorf("%s: %+v", tc.name, sig)
		}
		if tc.v.Identity != nil && (sig.Signer != tc.v.Identity.Subject || sig.Issuer != tc.v.Identity.Issuer) {
			t.Errorf("%s: signer %+v", tc.name, sig)
		}
	}
}
