package evidenceoci

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
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

	// manifestSchemaVersion is the only OCI image-manifest schema version Pacto
	// writes and the only one it reads. image-spec exports no constant for it, so it
	// is declared once here and shared by the writer and the reader rather than
	// spelled twice.
	manifestSchemaVersion = 2
)

// ErrInvalidArtifact reports an evidence artifact that is not exactly one
// schema-valid Pacto record bound to the expected contract subject. On the read
// path it makes the containing subject partial; on the write path it fails closed.
var ErrInvalidArtifact = errors.New("evidence oci: invalid evidence artifact")

// canonicalConfigJSON is the serialized canonical OCI empty-JSON descriptor. It
// is taken from image-spec rather than spelled out, so the writer and the reader
// cannot drift apart, and it is compared whole: one comparison covers every
// field, including the inline data and any field a future image-spec adds.
var canonicalConfigJSON = descriptorJSON(ocispec.DescriptorEmptyJSON)

// descriptorJSON renders a descriptor for comparison and for error messages. Go
// marshals struct fields in declaration order and map keys in sorted order, so
// two descriptors are equal exactly when these bytes are.
func descriptorJSON(d ocispec.Descriptor) []byte {
	out, _ := json.Marshal(d) // strings, ints and byte slices; marshalling cannot fail
	return out
}

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
	// The canonical OCI empty-JSON descriptor, taken from image-spec rather than
	// rebuilt, so the writer and [ValidateManifest] cannot drift apart. Both the
	// blob and the descriptor's inline copy of it are cloned: the struct copy above
	// would otherwise leave Data aliasing image-spec's package-level slice, so a
	// caller writing through the returned Artifact could corrupt every later build.
	configDesc := ocispec.DescriptorEmptyJSON
	configDesc.Data = bytes.Clone(configDesc.Data)
	config := bytes.Clone(configDesc.Data)

	// Built by hand rather than with oras.PackManifest: that helper stamps an
	// org.opencontainers.image.created annotation, which would give the same record
	// a different digest on every publish.
	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: manifestSchemaVersion},
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
	// A manifest that does not declare OCI schema version 2 is a different
	// document (or none at all): its absence decodes to zero, so one comparison
	// covers both a Docker v1 manifest and a manifest with no version field.
	case m.SchemaVersion != manifestSchemaVersion:
		return ocispec.Descriptor{}, fmt.Errorf("%w: manifest schema version %d, want %d",
			ErrInvalidArtifact, m.SchemaVersion, manifestSchemaVersion)
	case m.MediaType != ocispec.MediaTypeImageManifest:
		return ocispec.Descriptor{}, fmt.Errorf("%w: manifest media type %q, want %q",
			ErrInvalidArtifact, m.MediaType, ocispec.MediaTypeImageManifest)
	// The config must be the canonical OCI empty JSON descriptor, whole: not three
	// of its fields, but every one of them, including the inline copy of its own
	// two bytes. Three matching fields still admit a descriptor that contradicts
	// itself — canonical media type, digest and size over other `data` — and admit
	// optional fields nothing here interprets, which would let a manifest assert
	// something Pacto neither reads nor means.
	case !bytes.Equal(descriptorJSON(m.Config), canonicalConfigJSON):
		return ocispec.Descriptor{}, fmt.Errorf("%w: config %s is not the canonical empty-JSON descriptor %s",
			ErrInvalidArtifact, descriptorJSON(m.Config), canonicalConfigJSON)
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

// validateRecord enforces what the STORE adds to an accepted record: a service
// and an acceptance instant, and three spellings of the contract revision — the
// signed ContractRef and the resolved domain and digest — that all agree with the
// OCI subject the record is attached to. Everything the envelope itself must
// satisfy is left to [validateEnvelope], which reuses the ingestion boundary's own
// validators rather than restating them here.
func validateRecord(rec evidenceingest.Record, subj Subject) error {
	switch {
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
	return validateEnvelope(rec.Envelope)
}

// validateEnvelope re-runs the canonical envelope rules on a stored record. The
// version, kind, identity and bounds an envelope must satisfy belong to the
// ingestion boundary, and a registry read must not have a second, weaker copy of
// them: the embedded envelope is serialized deterministically and handed to the
// same [evidenceenvelope.Decode] the producer's own bytes went through.
//
// Signatures are deliberately NOT reverified. They are verified once, at
// ingestion, and write authorization on the contract repository is the trust
// boundary for what the registry holds; reverifying on every read would put the
// trust store on the read path and would retroactively invalidate every record
// signed by a since-rotated key.
func validateEnvelope(env evidenceenvelope.Envelope) error {
	// The evidence set first. Once it is structurally valid every observation
	// marshals, which is what makes the encoding below unable to fail.
	if errs := evidence.ValidateEvidenceSet(env.EvidenceSet); len(errs) > 0 {
		return fmt.Errorf("%w: evidence set: %w", ErrInvalidArtifact, errors.Join(errs...))
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// No HTML escaping: expanding one '<' into six bytes would push an envelope
	// that is legitimately just under MaxEnvelopeBytes over the limit.
	enc.SetEscapeHTML(false)
	_ = enc.Encode(env) // the evidence set is valid, so every observation marshals
	if _, err := evidenceenvelope.Decode(bytes.TrimRight(buf.Bytes(), "\n")); err != nil {
		return fmt.Errorf("%w: envelope: %w", ErrInvalidArtifact, err)
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
