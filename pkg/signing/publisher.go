package signing

import (
	"net/http"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// IdentityOf is who signed, read from the verified certificate: its issuer,
// its subject (the URI SAN, else the email SAN) and, for a CI workload
// identity, the source repository and ref it attests.
func IdentityOf(r *verify.VerificationResult) delivery.PublisherIdentity {
	if r == nil || r.Signature == nil || r.Signature.Certificate == nil {
		return delivery.PublisherIdentity{}
	}
	c := r.Signature.Certificate
	return delivery.PublisherIdentity{Issuer: c.Issuer, Subject: c.SubjectAlternativeName,
		Repository: c.SourceRepositoryURI, Ref: c.SourceRepositoryRef, CI: c.SourceRepositoryURI != ""}
}

// CommitOf is the commit a CI certificate was issued for (Fulcio's
// SourceRepositoryDigest), read only when the certificate attests a source
// repository, so a person's certificate never names one.
func CommitOf(r *verify.VerificationResult) string {
	if r == nil || r.Signature == nil || r.Signature.Certificate == nil || r.Signature.Certificate.SourceRepositoryURI == "" {
		return ""
	}
	return r.Signature.Certificate.SourceRepositoryDigest
}

// BundleSignatureFor is the fleet's signature for a publisher verdict: who
// signed, what judged it and its status. Only a verified accept (never an
// inferred one) of a CI identity names the signer repository and commit, the
// facts an attested owner rests on.
func BundleSignatureFor(v delivery.PublisherVerdict, commit string) fleet.BundleSignature {
	sig := fleet.BundleSignature{Status: fleet.SignatureUnknown, Source: v.Source}
	if v.Identity != nil {
		sig.Signer, sig.Issuer = v.Identity.Subject, v.Identity.Issuer
	}
	switch v.Verdict {
	case delivery.VerdictAccepted:
		if v.Source == delivery.PublisherSourceInferred {
			return sig
		}
		sig.Status = fleet.SignatureVerified
		if v.Identity != nil && v.Identity.CI {
			sig.SignerRepository, sig.SignerCommit = v.Identity.Repository, commit
		}
	case delivery.VerdictNewSigner, delivery.VerdictRefused:
		sig.Status = fleet.SignatureUnverified
	case delivery.VerdictUnsigned:
		sig.Status = fleet.SignatureUnsigned
	}
	return sig
}

// PublicGoodRootReadOnly is PublicGoodRoot for a process whose filesystem is
// read-only, as the promoter's is: it caches nothing on disk, so every call
// fetches the root from mirror through TUF.
func PublicGoodRootReadOnly(mirror string) (root.TrustedMaterial, error) {
	f := fetcher.NewDefaultFetcher()
	f.SetHTTPClient(&http.Client{Timeout: Timeout})
	return root.FetchTrustedRootWithOptions(tuf.DefaultOptions().WithRepositoryBaseURL(mirror).WithFetcher(f).WithDisableLocalCache())
}
