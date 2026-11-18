package signing

import (
	"github.com/sigstore/sigstore-go/pkg/verify"

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
