package delivery

import "slices"

// The three signature states of a published version.
const (
	SignatureSigned   = "signed"
	SignatureUnsigned = "unsigned"
	SignatureUnknown  = "unknown"
)

// SignatureEvidence is what the Stages that name one digest say about its
// signature. It is signed when a Stage names the digest in status.published
// (Flux verified its signature on the candidate OCIRepository) or
// status.offered (it passed pacto.published there). It is unsigned only when
// an entry Stage, fed by an OCIRepository and whose published list never
// reached its cap, holds the digest and no Stage names it signed. Otherwise
// Pacto cannot tell: a downstream Stage, one fed by another cluster's records
// or a published list that may have dropped it says nothing.
type SignatureEvidence struct{ Signed, Unsigned bool }

// Add reads what st, a Stage that holds digest, says about its signature.
func (e *SignatureEvidence) Add(st StageObject, digest string) {
	ss := st.Status
	e.Signed = e.Signed || slices.Contains(ss.Published, digest) || slices.Contains(ss.Offered, digest)
	e.Unsigned = e.Unsigned || (st.Spec.Candidates.OCIRepositoryRef != nil && len(ss.Published) < MaxPublished)
}

// Signature is signed, unsigned or unknown.
func (e SignatureEvidence) Signature() string {
	switch {
	case e.Signed:
		return SignatureSigned
	case e.Unsigned:
		return SignatureUnsigned
	}
	return SignatureUnknown
}
