package fleet

import (
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// AttestedOwner is the owner a revision's inferred record read from an owner
// file (CODEOWNERS or catalog-info.yaml), with its citation and the
// repository whose CI signed it. It holds only when the bundle that carries
// the record was signed by a CI identity an installation rule, a pin or the
// signed policy accepted (never an inferred rule), from a certificate that
// names a repository and the very commit the record says it was read at.
// The whole bundle, record included, is what that identity signed, so the
// record's own class marker raises nothing: a local, Kubernetes-only or
// unsigned revision is never attested. An attested owner routes reviews and
// never clears a gate.
func AttestedOwner(r *ContractRevision) (owner contract.Owner, citation, repository string, ok bool) {
	e, found := ownerFileEntry(r)
	if !found || !attestedSignature(r) {
		return contract.Owner{}, "", "", false
	}
	o, found := ownerFromRecord(e)
	if !found {
		return contract.Owner{}, "", "", false
	}
	sig := r.Provenance.Signature
	return o, attestedCitation(e, sig), sig.SignerRepository, true
}

// ownerFileEntry is the service.owner entry the revision's record read from
// an owner file.
func ownerFileEntry(r *ContractRevision) (contract.Inferred, bool) {
	if r == nil || r.Record == nil {
		return contract.Inferred{}, false
	}
	for _, e := range r.Record.Fields {
		if e.Field == "service.owner" && e.Class == contract.InferredClassOwnerFile {
			return e, true
		}
	}
	return contract.Inferred{}, false
}

// attestedSignature reports whether r's verified signature is an accepted CI
// identity's, issued for a repository and for the commit r's record names.
func attestedSignature(r *ContractRevision) bool {
	if r.Provenance == nil {
		return false
	}
	sig := r.Provenance.Signature
	return sig.Status == SignatureVerified && acceptedSignatureSource(sig.Source) && signerIsCI(r.Provenance) &&
		sig.SignerRepository != "" && sig.SignerCommit != "" && sig.SignerCommit == r.Record.Inputs.Commit
}

// acceptedSignatureSource reports whether source is a verified accept: an
// installation rule, a person's pin or the signed policy.
func acceptedSignatureSource(source string) bool {
	return source == "rule" || source == "pin" || source == "policy"
}

// signerIsCI reports whether the verified signer the signature names is
// classified as a CI identity. A signer missing from the list is not.
func signerIsCI(p *BundleProvenance) bool {
	for _, s := range p.Signers {
		if s.Subject == p.Signature.Signer && s.Issuer == p.Signature.Issuer {
			return s.Class == "ci"
		}
	}
	return false
}

// ownerFromRecord is the owner the entry's declaring YAML sets. An entry with
// no declaring YAML holds none.
// ponytail: discovery always writes the declaring YAML; an entry without it
// is not read through its display value.
func ownerFromRecord(e contract.Inferred) (contract.Owner, bool) {
	var doc struct {
		Service struct {
			Owner contract.Owner `yaml:"owner"`
		} `yaml:"service"`
	}
	if yaml.Unmarshal([]byte(e.Declare), &doc) != nil || doc.Service.Owner.IsEmpty() {
		return contract.Owner{}, false
	}
	return doc.Service.Owner, true
}

// attestedCitation is where an attested owner was read, as people read it:
// the owner file's base name and line, the short commit and the signer
// without its URL scheme.
func attestedCitation(e contract.Inferred, sig BundleSignature) string {
	commit := sig.SignerCommit
	if len(commit) > 7 {
		commit = commit[:7]
	}
	signer := sig.Signer
	if _, rest, ok := strings.Cut(signer, "://"); ok {
		signer = rest
	}
	return path.Base(strings.TrimPrefix(e.Source, "repository:")) + " at " + commit + ", signed by " + signer
}
