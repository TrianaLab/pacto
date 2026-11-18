package signing

import (
	"net/http"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"

	"github.com/trianalab/pacto/v4/pkg/delivery"
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

// PublicGoodRootReadOnly is PublicGoodRoot for a process whose filesystem is
// read-only, as the promoter's is: it caches nothing on disk, so every call
// fetches the root from mirror through TUF.
func PublicGoodRootReadOnly(mirror string) (root.TrustedMaterial, error) {
	f := fetcher.NewDefaultFetcher()
	f.SetHTTPClient(&http.Client{Timeout: Timeout})
	return root.FetchTrustedRootWithOptions(tuf.DefaultOptions().WithRepositoryBaseURL(mirror).WithFetcher(f).WithDisableLocalCache())
}
