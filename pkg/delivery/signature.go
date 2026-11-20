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
// (Flux or the registry watch verified its signature) or status.offered (it
// passed pacto.published there). It is unsigned only when an entry Stage
// whose published list never reached its cap holds the digest, no Stage names
// it signed and, for a Stage fed by the registry watch, no publisher check
// accepted it (its verdict is unsigned or refused). Otherwise
// Pacto cannot tell: a downstream Stage, one fed by another cluster's records
// or a published list that may have dropped it says nothing.
type SignatureEvidence struct{ Signed, Unsigned bool }

// Add reads what st, a Stage that holds digest, says about its signature.
func (e *SignatureEvidence) Add(st StageObject, digest string) {
	ss := st.Status
	e.Signed = e.Signed || slices.Contains(ss.Published, digest) || slices.Contains(ss.Offered, digest)
	entry := st.Spec.Candidates.OCIRepositoryRef != nil || (st.Spec.Candidates.Registry != nil && refusedByWatch(ss.Publisher, digest))
	e.Unsigned = e.Unsigned || (entry && len(ss.Published) < MaxPublished)
}

// refusedByWatch reports whether the registry watch's verdict on digest is
// unsigned or refused: no publisher check accepted it.
func refusedByWatch(p *PublisherStatus, digest string) bool {
	if p == nil {
		return false
	}
	return slices.ContainsFunc(p.Verdicts, func(v PublisherVerdict) bool {
		return v.Digest == digest && (v.Verdict == VerdictUnsigned || v.Verdict == VerdictRefused)
	})
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
