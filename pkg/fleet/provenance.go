package fleet

import (
	"sort"
	"time"
)

// Signature statuses of a revision's bundle digest. Only a signature that was
// read and failed verification is unverified; anything Pacto could not read or
// check is unknown, never unsigned.
const (
	SignatureVerified   = "verified"
	SignatureUnverified = "unverified"
	SignatureUnsigned   = "unsigned"
	SignatureUnknown    = "unknown"
	// SignatureNotChecked is a golden path list entry whose newest revision no
	// source that read it checks signatures, the bucket SignatureTally counts as
	// NotChecked.
	SignatureNotChecked = "not-checked"
)

// BundleSignature is the outcome of checking the Sigstore bundles attached to a
// revision's bundle digest.
type BundleSignature struct {
	Status string `json:"status" enum:"verified,unverified,unsigned,unknown"`
	// Signer and Issuer are the identity the revision's own contract declares in
	// publish.sign (self-declared), set only when Status is verified.
	Signer string `json:"signer,omitempty"`
	Issuer string `json:"issuer,omitempty"`
	// ExpectedSigner and ExpectedIssuer are that declared identity when Status is
	// unverified: what the signature was checked against, not who signed it.
	ExpectedSigner string `json:"expectedSigner,omitempty"`
	ExpectedIssuer string `json:"expectedIssuer,omitempty"`
	// Bundles is how many Sigstore bundle referrers are attached to the digest.
	Bundles int    `json:"bundles"`
	Reason  string `json:"reason,omitempty"`
}

// OCIReferrer is one artifact the registry lists as a referrer of the digest.
type OCIReferrer struct {
	ArtifactType string `json:"artifactType"`
	Digest       string `json:"digest"`
	// Created is the org.opencontainers.image.created annotation; usually absent.
	Created *time.Time `json:"created,omitempty"`
}

// ReferrersPreview is a bounded preview of a digest's referrers.
type ReferrersPreview struct {
	Total     int           `json:"total"`
	Count     int           `json:"count"`
	Truncated bool          `json:"truncated"`
	Items     []OCIReferrer `json:"items"`
}

// BundleProvenance is what a registry source found attached to a revision's
// bundle digest. Nil on a revision means no source that read it checks signatures.
type BundleProvenance struct {
	Signature BundleSignature  `json:"signature"`
	Referrers ReferrersPreview `json:"referrers"`
	// ReferrersUnavailable is why the referrers could not be listed, or "".
	ReferrersUnavailable string    `json:"referrersUnavailable,omitempty"`
	Source               string    `json:"source"`
	CheckedAt            time.Time `json:"checkedAt"`
}

// SignatureTally buckets the in-use revisions of a service by signature status.
// NotChecked counts revisions no source that read them checks signatures.
type SignatureTally struct {
	Verified   int `json:"verified"`
	Unverified int `json:"unverified"`
	Unsigned   int `json:"unsigned"`
	Unknown    int `json:"unknown"`
	NotChecked int `json:"notChecked"`
}

// add buckets one revision's provenance. An unrecognised status counts as
// unknown so it is never read as a positive answer.
func (t *SignatureTally) add(p *BundleProvenance) {
	if p == nil {
		t.NotChecked++
		return
	}
	switch p.Signature.Status {
	case SignatureVerified:
		t.Verified++
	case SignatureUnverified:
		t.Unverified++
	case SignatureUnsigned:
		t.Unsigned++
	default:
		t.Unknown++
	}
}

// RevisionSignature is one in-use revision's check, without its referrer list
// (the revision page has it), so the service list stays bounded.
type RevisionSignature struct {
	Revision  EntityRef        `json:"revision"`
	Checked   bool             `json:"checked"`
	Signature *BundleSignature `json:"signature,omitempty"`
	// Referrers is Referrers.Total of the revision's provenance, a count that
	// means nothing when ReferrersUnavailable says why they could not be listed.
	Referrers            int        `json:"referrers"`
	ReferrersUnavailable string     `json:"referrersUnavailable,omitempty"`
	Source               string     `json:"source,omitempty"`
	CheckedAt            *time.Time `json:"checkedAt,omitempty"`
}

// RevisionSignaturesPreview is a bounded preview of per-revision signatures.
type RevisionSignaturesPreview struct {
	Total     int                 `json:"total"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated"`
	Items     []RevisionSignature `json:"items"`
}

// NewReferrersPreview sorts referrers by digest, bounds them at
// MaxDetailPreview and caps registry strings.
func NewReferrersPreview(items []OCIReferrer) ReferrersPreview {
	sorted := append([]OCIReferrer{}, items...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Digest < sorted[j].Digest })
	src, total, trunc := boundSlice(sorted, MaxDetailPreview)
	for i := range src {
		src[i] = OCIReferrer{
			ArtifactType: capLen(src[i].ArtifactType, maxRuntimeValueLen),
			Digest:       capLen(src[i].Digest, maxRuntimeValueLen),
			Created:      copyTime(src[i].Created),
		}
	}
	return ReferrersPreview{Total: total, Count: len(src), Truncated: trunc, Items: src}
}

// cloneProvenance deep-copies p so the snapshot never aliases source memory.
func cloneProvenance(p *BundleProvenance) *BundleProvenance {
	if p == nil {
		return nil
	}
	c := *p
	c.Referrers.Items = make([]OCIReferrer, len(p.Referrers.Items))
	for i, r := range p.Referrers.Items {
		r.Created = copyTime(r.Created)
		c.Referrers.Items[i] = r
	}
	return &c
}

// mergeProvenance folds a second contribution's provenance into existing. A
// source that checked wins over one that did not; two that both checked keep
// the one whose Source sorts first, so the snapshot is order-independent.
func mergeProvenance(existing, add *ContractRevision) {
	if add.Provenance == nil {
		return
	}
	if existing.Provenance == nil || add.Provenance.Source < existing.Provenance.Source {
		existing.Provenance = cloneProvenance(add.Provenance)
	}
}

// revisionSignatures lists the signature check of each revision, newest first.
// revs may be clones: Provenance is exported and survives them.
func revisionSignatures(revs []*ContractRevision) RevisionSignaturesPreview {
	ordered := append([]*ContractRevision{}, revs...)
	sort.Slice(ordered, func(i, j int) bool { return lessRevisionChrono(ordered[j], ordered[i]) })
	src, total, trunc := boundSlice(ordered, MaxDetailPreview)
	items := make([]RevisionSignature, len(src))
	for i, r := range src {
		items[i] = RevisionSignature{Revision: revisionEntityRef(r)}
		if p := r.Provenance; p != nil {
			sig := p.Signature
			checked := p.CheckedAt
			items[i].Checked, items[i].Signature, items[i].Referrers, items[i].Source, items[i].CheckedAt = true, &sig, p.Referrers.Total, p.Source, &checked
			items[i].ReferrersUnavailable = p.ReferrersUnavailable
		}
	}
	return RevisionSignaturesPreview{Total: total, Count: len(items), Truncated: trunc, Items: items}
}
