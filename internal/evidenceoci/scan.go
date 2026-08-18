package evidenceoci

import (
	"context"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
)

// maxManifestBytes bounds one referrer manifest. A Pacto evidence manifest is a
// few hundred bytes; the bound exists so a registry cannot make the reader
// allocate whatever size a descriptor claims.
const maxManifestBytes = 1 << 20

// scanned is one evidence record and the untagged manifest that carries it. The
// descriptor is kept so a write can confirm its own publication came back from
// the Referrers API.
type scanned struct {
	Desc   ocispec.Descriptor
	Record evidenceingest.Record
}

// subjectScan is one subject's complete read. Enumeration either covers every
// page of the Referrers listing or fails: there is no truncated success, because
// replay protection and the operational graph both depend on having seen
// everything.
type subjectScan struct {
	Found []scanned
	// Invalid counts artifacts the listing named that could not be read as a
	// valid Pacto record. Any non-zero count means this subject's evidence is
	// incomplete: the read is partial and a write must fail closed.
	Invalid int
}

// scanSubject enumerates every referrer of subjectDesc and returns the evidence
// records among them.
//
// It lists referrers UNFILTERED and classifies each candidate by the artifactType
// the fetched manifest declares about itself, rather than passing the Pacto type
// to the registry. distribution-spec v1.1.1 requires the listing descriptor to
// carry the manifest's artifactType, but registries exist that report the config
// media type instead; against those, a server-side filter returns an empty index
// for a repository full of evidence, and an empty index is indistinguishable from
// an authoritative empty operational graph. Fetching each candidate costs a few
// extra small requests and can never turn present evidence into absent evidence.
func scanSubject(ctx context.Context, repo *remote.Repository, subj Subject, subjectDesc ocispec.Descriptor) (subjectScan, error) {
	var out subjectScan
	err := repo.Referrers(ctx, subjectDesc, "", func(page []ocispec.Descriptor) error {
		for _, desc := range page {
			switch rec, state := readReferrer(ctx, repo, subj, desc); state {
			case referrerRecord:
				out.Found = append(out.Found, scanned{Desc: desc, Record: rec})
			case referrerMalformed:
				out.Invalid++
			}
		}
		return nil
	})
	if err != nil {
		return subjectScan{}, fmt.Errorf("evidence oci: %s: referrers: %w", subj.Ref(), err)
	}
	return out, nil
}

// referrerState is how one referrer of a contract revision was classified.
type referrerState int

const (
	// referrerForeign is an artifact Pacto did not write. Another tool's
	// signature or SBOM on the same revision is irrelevant, not corrupt.
	referrerForeign referrerState = iota
	// referrerRecord is a valid Pacto evidence record.
	referrerRecord
	// referrerMalformed was named by the listing but cannot be read as a valid
	// Pacto record.
	referrerMalformed
)

// readReferrer classifies and, when it is Pacto's, decodes one referrer.
//
// An artifact the registry listed but then could not serve is malformed rather
// than a separate transport failure: either way this subject's evidence set is
// incomplete, which is exactly what a non-zero invalid count means. Only the
// enumeration itself can fail the whole subject, because only enumeration can
// leave the reader believing it saw everything.
func readReferrer(ctx context.Context, repo *remote.Repository, subj Subject, desc ocispec.Descriptor) (evidenceingest.Record, referrerState) {
	if desc.Size > maxManifestBytes {
		// Too large to be a Pacto manifest. If it claims to be one, that claim is
		// itself the malformation; otherwise it is simply not ours to read.
		if desc.ArtifactType == ArtifactType {
			return evidenceingest.Record{}, referrerMalformed
		}
		return evidenceingest.Record{}, referrerForeign
	}
	manifest, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		return evidenceingest.Record{}, referrerMalformed
	}
	if !claimsPactoType(desc, manifest) {
		return evidenceingest.Record{}, referrerForeign
	}
	payloadDesc, err := ValidateManifest(manifest, subj)
	if err != nil {
		return evidenceingest.Record{}, referrerMalformed
	}
	payload, err := content.FetchAll(ctx, repo, payloadDesc)
	if err != nil {
		return evidenceingest.Record{}, referrerMalformed
	}
	rec, err := DecodePayload(payload, subj)
	if err != nil {
		return evidenceingest.Record{}, referrerMalformed
	}
	return rec, referrerRecord
}

// claimsPactoType reports whether either the registry's listing descriptor or the
// manifest itself says this is a Pacto evidence record. Either claim is enough to
// make it Pacto's to validate, so neither a mislabelling registry nor a hostile
// manifest can hide a bad artifact behind the other's label.
func claimsPactoType(desc ocispec.Descriptor, manifest []byte) bool {
	if desc.ArtifactType == ArtifactType {
		return true
	}
	var declared struct {
		ArtifactType string `json:"artifactType"`
	}
	// Deliberately lenient, and the error deliberately dropped: this only decides
	// whose artifact it is, and bytes that will not parse claim nothing. The strict
	// decode that decides whether it is VALID runs next.
	_ = json.Unmarshal(manifest, &declared)
	return declared.ArtifactType == ArtifactType
}
