package evidenceoci

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

// subjectDescOf is the descriptor a registry returns when the contract revision
// is resolved: the artifact's subject binding is that exact descriptor.
func subjectDescOf(s Subject) ocispec.Descriptor {
	return ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.Digest(s.Digest), Size: 1234}
}

func testSubject(t *testing.T) Subject {
	t.Helper()
	s, err := ParseSubject(refA)
	if err != nil {
		t.Fatalf("ParseSubject: %v", err)
	}
	return s
}

// testRecord is a minimal accepted record whose resolved identity matches s.
func testRecord(s Subject) evidenceingest.Record {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	return evidenceingest.Record{
		Envelope: evidenceenvelope.Envelope{
			APIVersion: evidenceenvelope.APIVersionV1,
			Kind:       evidenceenvelope.KindEnvelope,
			ID:         "env-1",
			Producer:   evidenceenvelope.Producer{ID: "remote-eu", KeyID: "key-1"},
			Sequence:   7,
			IssuedAt:   at,
			EvidenceSet: evidence.EvidenceSet{
				Subject:     evidence.SubjectRef{Kind: "service", Name: "orders"},
				ContractRef: s.Ref(),
				Source:      "collector",
				ObservedAt:  at,
			},
		},
		Compliance: "Compliant",
		Findings:   []finding.Finding{{Code: "X", Severity: finding.SeverityWarning, Message: "m"}},
		Coverage:   validation.Coverage{Evaluated: 1, Required: 1},
		AcceptedAt: at,
		Service:    "orders",
		Domain:     s.Domain(),
		Digest:     s.Digest,
	}
}

func mustBuild(t *testing.T, rec evidenceingest.Record, s Subject) Artifact {
	t.Helper()
	art, err := BuildArtifact(rec, s, subjectDescOf(s))
	if err != nil {
		t.Fatalf("BuildArtifact: %v", err)
	}
	return art
}

// The artifact is a pure function of the record and the resolved subject: the
// same accepted record always produces the same manifest digest, so a retry
// after a lost response cannot create a second, differently-addressed copy.
func TestBuildArtifact_IsDeterministic(t *testing.T) {
	s := testSubject(t)
	rec := testRecord(s)
	a, b := mustBuild(t, rec, s), mustBuild(t, rec, s)
	if !bytes.Equal(a.Manifest, b.Manifest) {
		t.Error("manifest bytes differ between two builds of the same record")
	}
	if a.ManifestDesc.Digest != b.ManifestDesc.Digest {
		t.Errorf("manifest digest differs: %s vs %s", a.ManifestDesc.Digest, b.ManifestDesc.Digest)
	}
	if !bytes.Equal(a.Payload, b.Payload) {
		t.Error("payload bytes differ between two builds of the same record")
	}
}

// The published shape is exactly what the design fixes: one payload layer, an
// empty config, the Pacto artifact type and one exact subject descriptor.
func TestBuildArtifact_Shape(t *testing.T) {
	s := testSubject(t)
	art := mustBuild(t, testRecord(s), s)

	var m ocispec.Manifest
	if err := json.Unmarshal(art.Manifest, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(m.Layers) != 1 {
		t.Fatalf("layers = %d, want exactly 1", len(m.Layers))
	}
	if m.Subject == nil {
		t.Fatal("manifest carries no subject: it would not be a referrer of anything")
	}
	subj := subjectDescOf(s)
	for _, c := range []struct {
		what      string
		got, want any
	}{
		{"manifest mediaType", m.MediaType, ocispec.MediaTypeImageManifest},
		{"schemaVersion", m.SchemaVersion, 2},
		{"artifactType", m.ArtifactType, ArtifactType},
		// The config is the canonical OCI empty-JSON descriptor, field for field,
		// not merely something that looks like it.
		{"config mediaType", m.Config.MediaType, ocispec.DescriptorEmptyJSON.MediaType},
		{"config digest", m.Config.Digest, ocispec.DescriptorEmptyJSON.Digest},
		{"config size", m.Config.Size, ocispec.DescriptorEmptyJSON.Size},
		{"config blob", string(art.Config), "{}"},
		{"layer mediaType", m.Layers[0].MediaType, PayloadMediaType},
		// The layer descriptor must address the payload bytes, not merely
		// resemble them.
		{"layer digest", m.Layers[0].Digest, art.PayloadDesc.Digest},
		{"layer size", m.Layers[0].Size, int64(len(art.Payload))},
		{"subject digest", m.Subject.Digest, subj.Digest},
		{"subject mediaType", m.Subject.MediaType, subj.MediaType},
		{"subject size", m.Subject.Size, subj.Size},
		{"manifest descriptor artifactType", art.ManifestDesc.ArtifactType, ArtifactType},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
		}
	}
}

func TestBuildArtifact_PayloadRoundTrips(t *testing.T) {
	s := testSubject(t)
	rec := testRecord(s)
	art := mustBuild(t, rec, s)

	got, err := DecodePayload(art.Payload, s)
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if got.Envelope.ID != rec.Envelope.ID || got.Service != rec.Service || got.Digest != rec.Digest {
		t.Errorf("round-tripped record identity differs: %+v", got)
	}
	if len(got.Findings) != 1 || got.Findings[0].Code != "X" {
		t.Errorf("findings did not round-trip: %+v", got.Findings)
	}
	if !got.AcceptedAt.Equal(rec.AcceptedAt) {
		t.Errorf("acceptedAt = %v, want %v", got.AcceptedAt, rec.AcceptedAt)
	}
	var envelope struct {
		SchemaVersion string          `json:"schemaVersion"`
		Record        json.RawMessage `json:"record"`
	}
	if err := json.Unmarshal(art.Payload, &envelope); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if envelope.SchemaVersion != RecordSchemaVersion {
		t.Errorf("payload schemaVersion = %q, want %q", envelope.SchemaVersion, RecordSchemaVersion)
	}
}

// A record whose identity does not match the subject it would be attached to
// must never be publishable: the subject binding is the record's identity.
func TestBuildArtifact_RejectsMismatchedRecord(t *testing.T) {
	s := testSubject(t)
	other, err := ParseSubject(refB)
	if err != nil {
		t.Fatalf("ParseSubject: %v", err)
	}
	cases := map[string]func(*evidenceingest.Record){
		"contract ref of another subject": func(r *evidenceingest.Record) { r.Envelope.EvidenceSet.ContractRef = other.Ref() },
		"digest of another revision":      func(r *evidenceingest.Record) { r.Digest = digestB },
		"domain of another registry":      func(r *evidenceingest.Record) { r.Domain = "other.example/team" },
		"no envelope id":                  func(r *evidenceingest.Record) { r.Envelope.ID = "" },
		"no producer":                     func(r *evidenceingest.Record) { r.Envelope.Producer.ID = "" },
		"no service":                      func(r *evidenceingest.Record) { r.Service = "" },
		"no accepted-at":                  func(r *evidenceingest.Record) { r.AcceptedAt = time.Time{} },
		"no target subject":               func(r *evidenceingest.Record) { r.Envelope.EvidenceSet.Subject.Name = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rec := testRecord(s)
			mutate(&rec)
			if _, err := BuildArtifact(rec, s, subjectDescOf(s)); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("error = %v, want ErrInvalidArtifact", err)
			}
		})
	}
}

// A producer cannot make the registry store an unbounded blob by sending an
// envelope that evaluates into enormous findings.
func TestBuildArtifact_RejectsOversizedPayload(t *testing.T) {
	s := testSubject(t)
	rec := testRecord(s)
	rec.Findings[0].Message = strings.Repeat("x", maxPayloadBytes+1)
	if _, err := BuildArtifact(rec, s, subjectDescOf(s)); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("error = %v, want ErrInvalidArtifact", err)
	}
}

// A subject descriptor that does not address the configured revision would
// attach the record to something else entirely.
func TestBuildArtifact_RejectsForeignSubjectDescriptor(t *testing.T) {
	s := testSubject(t)
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.Digest(digestB), Size: 10}
	if _, err := BuildArtifact(testRecord(s), s, desc); !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("error = %v, want ErrInvalidArtifact", err)
	}
}

// Strict decoding is the read-side trust boundary: anything that is not exactly
// one schema-valid Pacto record is a malformed artifact, never a silently
// dropped field.
func TestDecodePayload_Rejects(t *testing.T) {
	s := testSubject(t)
	valid := string(mustBuild(t, testRecord(s), s).Payload)

	cases := map[string][]byte{
		"empty":          []byte(""),
		"not json":       []byte("not json"),
		"trailing json":  []byte(valid + `{"extra":1}`),
		"trailing brace": []byte(valid + "}"),
		"unknown top-level field": []byte(`{"schemaVersion":"` + RecordSchemaVersion +
			`","record":{},"extra":1}`),
		"unknown record field":   []byte(strings.Replace(valid, `"record":{`, `"record":{"extra":1,`, 1)),
		"unknown schema version": []byte(`{"schemaVersion":"pacto.dev/evidence-record/v2","record":{}}`),
		"missing schema version": []byte(`{"record":{}}`),
		"oversized":              make([]byte, maxPayloadBytes+1),
		"contract ref of another subject": []byte(strings.ReplaceAll(valid,
			refA, refB)),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePayload(data, s); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("error = %v, want ErrInvalidArtifact", err)
			}
		})
	}
}

// The manifest is validated before its payload is fetched, so a malformed Pacto
// artifact is detected without trusting the blob it points at.
func TestValidateManifest_Rejects(t *testing.T) {
	s := testSubject(t)
	good := mustBuild(t, testRecord(s), s)

	remarshal := func(mutate func(*ocispec.Manifest)) []byte {
		var m ocispec.Manifest
		if err := json.Unmarshal(good.Manifest, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		mutate(&m)
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return out
	}
	// withoutField drops a top-level key entirely, which is how a manifest arrives
	// with no schemaVersion at all rather than with an explicit zero.
	withoutField := func(field string) []byte {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(good.Manifest, &raw); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		delete(raw, field)
		out, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return out
	}

	cases := map[string][]byte{
		"not json":      []byte("{"),
		"trailing data": append(append([]byte{}, good.Manifest...), '}'),
		// A Docker v1 manifest is a different document with different rules; reading
		// it as an OCI image manifest would trust fields it never promised.
		"schema version 1":       remarshal(func(m *ocispec.Manifest) { m.SchemaVersion = 1 }),
		"missing schema version": withoutField("schemaVersion"),
		// The config must be the canonical OCI empty JSON. Anything else is a blob
		// the reader would have to fetch and interpret to know what it is.
		"wrong config media type": remarshal(func(m *ocispec.Manifest) {
			m.Config.MediaType = ocispec.MediaTypeImageConfig
		}),
		"wrong config digest": remarshal(func(m *ocispec.Manifest) {
			m.Config.Digest = digest.FromString(`{"not":"empty"}`)
		}),
		"wrong config size": remarshal(func(m *ocispec.Manifest) { m.Config.Size = 3 }),
		"two layers": remarshal(func(m *ocispec.Manifest) {
			m.Layers = append(m.Layers, m.Layers[0])
		}),
		"no layer":           remarshal(func(m *ocispec.Manifest) { m.Layers = nil }),
		"wrong artifactType": remarshal(func(m *ocispec.Manifest) { m.ArtifactType = "application/vnd.other" }),
		"wrong layer media type": remarshal(func(m *ocispec.Manifest) {
			m.Layers[0].MediaType = "application/json"
		}),
		"no subject": remarshal(func(m *ocispec.Manifest) { m.Subject = nil }),
		"foreign subject": remarshal(func(m *ocispec.Manifest) {
			m.Subject.Digest = digest.Digest(digestB)
		}),
		"oversized layer": remarshal(func(m *ocispec.Manifest) {
			m.Layers[0].Size = maxPayloadBytes + 1
		}),
		"negative layer size": remarshal(func(m *ocispec.Manifest) { m.Layers[0].Size = -1 }),
		"wrong manifest media type": remarshal(func(m *ocispec.Manifest) {
			m.MediaType = ocispec.MediaTypeImageIndex
		}),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateManifest(data, s); !errors.Is(err, ErrInvalidArtifact) {
				t.Fatalf("error = %v, want ErrInvalidArtifact", err)
			}
		})
	}
}

func TestValidateManifest_AcceptsPublishedArtifact(t *testing.T) {
	s := testSubject(t)
	art := mustBuild(t, testRecord(s), s)
	desc, err := ValidateManifest(art.Manifest, s)
	if err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	if desc.Digest != art.PayloadDesc.Digest {
		t.Errorf("payload descriptor = %s, want %s", desc.Digest, art.PayloadDesc.Digest)
	}
}
