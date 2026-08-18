package evidenceoci

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
	"github.com/trianalab/pacto/v3/pkg/strictjson"
)

const (
	// ArtifactType is the OCI artifactType every Pacto evidence record carries. It
	// is what distinguishes Pacto's referrers from every other artifact attached to
	// the same contract revision (signatures, SBOMs, attestations).
	ArtifactType = "application/vnd.pacto.evidence.record.v1+json"

	// PayloadMediaType is the single payload layer's media type. It matches
	// [ArtifactType] so the layer is self-describing when fetched on its own.
	PayloadMediaType = ArtifactType

	// RecordSchemaVersion is the payload schema. It is checked before the record is
	// read, so a future version is a recognised incompatibility rather than a field
	// silently missing.
	RecordSchemaVersion = "pacto.dev/evidence-record/v1"

	// maxPayloadBytes bounds one evidence payload: the signed envelope cap plus room
	// for the findings the server evaluated from it. A larger blob is malformed, and
	// is rejected from the manifest descriptor before any byte is fetched.
	maxPayloadBytes = 8 << 20
)

// ErrInvalidArtifact reports an evidence artifact that is not exactly one
// schema-valid Pacto record bound to the expected contract subject. On the read
// path it makes the containing subject partial; on the write path it fails closed.
var ErrInvalidArtifact = errors.New("evidence oci: invalid evidence artifact")

// Artifact is one publishable evidence record: the payload blob, the empty
// config blob every OCI image manifest requires, and the manifest binding them
// to the contract subject. Every field is a deterministic function of the record
// and the subject, so republishing an accepted record yields the same digest
// instead of a second copy.
type Artifact struct {
	Payload      []byte
	PayloadDesc  ocispec.Descriptor
	Config       []byte
	ConfigDesc   ocispec.Descriptor
	Manifest     []byte
	ManifestDesc ocispec.Descriptor
}

// payloadDoc is the wire shape of the payload layer.
type payloadDoc struct {
	SchemaVersion string                `json:"schemaVersion"`
	Record        evidenceingest.Record `json:"record"`
}

// BuildArtifact renders an accepted record as an OCI 1.1 referrer of subj.
// subjectDesc is the descriptor the registry resolved for the contract revision;
// it must address subj's digest, because that descriptor is the only thing that
// decides what the evidence is evidence OF.
func BuildArtifact(rec evidenceingest.Record, subj Subject, subjectDesc ocispec.Descriptor) (Artifact, error) {
	if err := validateRecord(rec, subj); err != nil {
		return Artifact{}, err
	}
	if subjectDesc.Digest.String() != subj.Digest {
		return Artifact{}, fmt.Errorf("%w: subject descriptor %s is not the configured revision %s",
			ErrInvalidArtifact, subjectDesc.Digest, subj.Digest)
	}
	payload, _ := json.Marshal(payloadDoc{SchemaVersion: RecordSchemaVersion, Record: rec}) // plain structs; marshalling cannot fail
	if len(payload) > maxPayloadBytes {
		return Artifact{}, fmt.Errorf("%w: payload is %d bytes, over the %d byte limit",
			ErrInvalidArtifact, len(payload), maxPayloadBytes)
	}

	payloadDesc := blobDescriptor(PayloadMediaType, payload)
	config := []byte("{}")
	configDesc := blobDescriptor(ocispec.MediaTypeEmptyJSON, config)
	configDesc.Data = config

	// Built by hand rather than with oras.PackManifest: that helper stamps an
	// org.opencontainers.image.created annotation, which would give the same record
	// a different digest on every publish.
	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config:       configDesc,
		Layers:       []ocispec.Descriptor{payloadDesc},
		Subject:      &subjectDesc,
	}
	manifestBytes, _ := json.Marshal(manifest) // descriptors and strings; marshalling cannot fail
	manifestDesc := blobDescriptor(ocispec.MediaTypeImageManifest, manifestBytes)
	manifestDesc.ArtifactType = ArtifactType

	return Artifact{
		Payload:      payload,
		PayloadDesc:  payloadDesc,
		Config:       config,
		ConfigDesc:   configDesc,
		Manifest:     manifestBytes,
		ManifestDesc: manifestDesc,
	}, nil
}

// ValidateManifest checks a fetched manifest is a well-formed Pacto evidence
// artifact for subj and returns the descriptor of its payload layer. It runs
// before the payload is fetched, so a malformed artifact is rejected without
// trusting the blob it points at.
func ValidateManifest(data []byte, subj Subject) (ocispec.Descriptor, error) {
	var m ocispec.Manifest
	if err := strictjson.Unmarshal(data, &m); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("%w: manifest: %w", ErrInvalidArtifact, err)
	}
	switch {
	case m.MediaType != ocispec.MediaTypeImageManifest:
		return ocispec.Descriptor{}, fmt.Errorf("%w: manifest media type %q, want %q",
			ErrInvalidArtifact, m.MediaType, ocispec.MediaTypeImageManifest)
	case m.ArtifactType != ArtifactType:
		return ocispec.Descriptor{}, fmt.Errorf("%w: artifact type %q, want %q",
			ErrInvalidArtifact, m.ArtifactType, ArtifactType)
	case len(m.Layers) != 1:
		return ocispec.Descriptor{}, fmt.Errorf("%w: %d layers, want exactly 1", ErrInvalidArtifact, len(m.Layers))
	case m.Layers[0].MediaType != PayloadMediaType:
		return ocispec.Descriptor{}, fmt.Errorf("%w: layer media type %q, want %q",
			ErrInvalidArtifact, m.Layers[0].MediaType, PayloadMediaType)
	case m.Layers[0].Size < 0 || m.Layers[0].Size > maxPayloadBytes:
		return ocispec.Descriptor{}, fmt.Errorf("%w: layer is %d bytes, outside the 0..%d byte range",
			ErrInvalidArtifact, m.Layers[0].Size, maxPayloadBytes)
	case m.Subject == nil:
		return ocispec.Descriptor{}, fmt.Errorf("%w: manifest has no subject", ErrInvalidArtifact)
	case m.Subject.Digest.String() != subj.Digest:
		return ocispec.Descriptor{}, fmt.Errorf("%w: subject %s is not the configured revision %s",
			ErrInvalidArtifact, m.Subject.Digest, subj.Digest)
	}
	return m.Layers[0], nil
}

// DecodePayload strictly decodes one evidence payload and checks the record it
// carries is bound to subj. Unknown fields, trailing JSON, an unknown schema
// version, an oversized blob, a missing identity and any disagreement with the
// immutable contract ref are all malformed.
func DecodePayload(data []byte, subj Subject) (evidenceingest.Record, error) {
	if len(data) > maxPayloadBytes {
		return evidenceingest.Record{}, fmt.Errorf("%w: payload is %d bytes, over the %d byte limit",
			ErrInvalidArtifact, len(data), maxPayloadBytes)
	}
	var doc payloadDoc
	if err := strictjson.Unmarshal(data, &doc); err != nil {
		return evidenceingest.Record{}, fmt.Errorf("%w: payload: %w", ErrInvalidArtifact, err)
	}
	if doc.SchemaVersion != RecordSchemaVersion {
		return evidenceingest.Record{}, fmt.Errorf("%w: payload schema %q, want %q",
			ErrInvalidArtifact, doc.SchemaVersion, RecordSchemaVersion)
	}
	if err := validateRecord(doc.Record, subj); err != nil {
		return evidenceingest.Record{}, err
	}
	return doc.Record, nil
}

// validateRecord enforces the identity every stored record must carry: it names a
// producer, an envelope, an operational subject and an acceptance instant, and
// its three spellings of the contract revision — the signed ContractRef and the
// resolved domain and digest — all agree with the OCI subject it is attached to.
func validateRecord(rec evidenceingest.Record, subj Subject) error {
	switch {
	case rec.Envelope.ID == "":
		return fmt.Errorf("%w: record has no envelope id", ErrInvalidArtifact)
	case rec.Envelope.Producer.ID == "":
		return fmt.Errorf("%w: record has no producer", ErrInvalidArtifact)
	case rec.Envelope.EvidenceSet.Subject.Name == "":
		return fmt.Errorf("%w: record has no operational subject", ErrInvalidArtifact)
	case rec.Service == "":
		return fmt.Errorf("%w: record has no service", ErrInvalidArtifact)
	case rec.AcceptedAt.IsZero():
		return fmt.Errorf("%w: record has no acceptance instant", ErrInvalidArtifact)
	case rec.Envelope.EvidenceSet.ContractRef != subj.Ref():
		return fmt.Errorf("%w: contract ref %q is not the subject %q",
			ErrInvalidArtifact, rec.Envelope.EvidenceSet.ContractRef, subj.Ref())
	case rec.Digest != subj.Digest:
		return fmt.Errorf("%w: record digest %q is not the subject digest %q",
			ErrInvalidArtifact, rec.Digest, subj.Digest)
	case rec.Domain != subj.Domain():
		return fmt.Errorf("%w: record domain %q is not the subject domain %q",
			ErrInvalidArtifact, rec.Domain, subj.Domain())
	}
	return nil
}

// blobDescriptor is the content-addressed descriptor of exactly these bytes.
func blobDescriptor(mediaType string, data []byte) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
}
