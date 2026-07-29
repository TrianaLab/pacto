// Package evidenceenvelope defines the transport-independent, versioned,
// Ed25519-signed envelope that carries a Pacto [evidence.EvidenceSet] from a
// remote or disconnected environment to a platform that ingests it. It is the
// wire contract of external evidence ingestion: a producer signs an envelope and
// reports it outbound; the platform verifies the signature against a trust store,
// checks freshness, and (at the ingestion layer) rejects replays before
// evaluating the evidence.
//
// The package is pure and framework independent — crypto, JSON and the evidence
// domain only, no HTTP, Kubernetes or persistence — so producers, the CLI and
// the ingestion host all share one definition and one canonicalization.
package evidenceenvelope

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// Protocol constants.
const (
	APIVersionV1     = "pacto.dev/evidence/v1"
	KindEnvelope     = "EvidenceEnvelope"
	AlgorithmEd25519 = "Ed25519"

	// MaxEnvelopeBytes bounds a decoded envelope so a producer cannot exhaust
	// memory at the ingestion boundary.
	MaxEnvelopeBytes = 1 << 20 // 1 MiB
	// MaxObservations bounds how many observations one envelope may carry.
	MaxObservations = 10000
)

// Producer identifies the environment that produced an envelope and the key that
// signed it. KeyID selects the public key in the verifier's trust store.
type Producer struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	KeyID   string `json:"keyId"`
}

// Signature carries the detached signature over the envelope's canonical bytes.
type Signature struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"` // base64 standard encoding
}

// Envelope is the signed wire form of an EvidenceSet.
type Envelope struct {
	APIVersion  string               `json:"apiVersion"`
	Kind        string               `json:"kind"`
	ID          string               `json:"id"`
	Producer    Producer             `json:"producer"`
	Sequence    uint64               `json:"sequence"`
	IssuedAt    time.Time            `json:"issuedAt"`
	ExpiresAt   time.Time            `json:"expiresAt"`
	EvidenceSet evidence.EvidenceSet `json:"evidenceSet"`
	Signature   *Signature           `json:"signature,omitempty"`
}

// canonicalError never echoes key material or raw signature bytes.
var (
	ErrUnsupportedVersion = errors.New("evidence envelope: unsupported apiVersion")
	ErrUnsupportedKind    = errors.New("evidence envelope: unsupported kind")
	ErrTooLarge           = errors.New("evidence envelope: payload exceeds the size limit")
	ErrTooManyObs         = errors.New("evidence envelope: too many observations")
	ErrMissingField       = errors.New("evidence envelope: required field is missing")
	ErrUnsignedEnvelope   = errors.New("evidence envelope: envelope is not signed")
	ErrUnknownKey         = errors.New("evidence envelope: producer key is not trusted")
	ErrBadAlgorithm       = errors.New("evidence envelope: unsupported signature algorithm")
	ErrBadSignature       = errors.New("evidence envelope: signature verification failed")
	ErrExpired            = errors.New("evidence envelope: envelope has expired")
	ErrNotYetValid        = errors.New("evidence envelope: envelope is not yet valid")
)

// signingBytes returns the canonical bytes signed and verified. It normalizes to
// sorted-key JSON with the signature omitted, so a decode/re-encode round trip
// (or any transport whitespace change) reproduces identical bytes on both sides.
func (e Envelope) signingBytes() ([]byte, error) {
	c := e
	c.Signature = nil
	return canonicalJSON(c)
}

// canonicalJSON re-encodes v through a generic decode so map keys are sorted and
// formatting is normalized, giving deterministic bytes independent of struct
// field order changes or received whitespace.
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return json.Marshal(generic)
}

// Sign returns a copy of e signed with key (Ed25519). It fills Producer.KeyID is
// the caller's responsibility; Sign only computes the signature.
func Sign(e Envelope, key ed25519.PrivateKey) (Envelope, error) {
	if e.APIVersion == "" {
		e.APIVersion = APIVersionV1
	}
	if e.Kind == "" {
		e.Kind = KindEnvelope
	}
	msg, err := e.signingBytes()
	if err != nil {
		return Envelope{}, err
	}
	sig := ed25519.Sign(key, msg)
	e.Signature = &Signature{Algorithm: AlgorithmEd25519, Value: base64.StdEncoding.EncodeToString(sig)}
	return e, nil
}

// TrustStore resolves a producer key id to its Ed25519 public key.
type TrustStore interface {
	PublicKey(keyID string) (ed25519.PublicKey, bool)
}

// MapTrustStore is an in-memory trust store keyed by key id.
type MapTrustStore map[string]ed25519.PublicKey

// PublicKey implements [TrustStore].
func (m MapTrustStore) PublicKey(keyID string) (ed25519.PublicKey, bool) {
	k, ok := m[keyID]
	return k, ok
}

// Verify checks structural validity, freshness and the Ed25519 signature against
// the trust store. now is the reference time for expiry (inject a fixed clock in
// tests). It returns a sentinel error (see the Err* vars) whose message never
// contains key or signature material.
func Verify(e Envelope, ts TrustStore, now time.Time) error {
	if err := e.validateStructure(); err != nil {
		return err
	}
	if e.Signature == nil {
		return ErrUnsignedEnvelope
	}
	if e.Signature.Algorithm != AlgorithmEd25519 {
		return ErrBadAlgorithm
	}
	pub, ok := ts.PublicKey(e.Producer.KeyID)
	if !ok {
		return ErrUnknownKey
	}
	sig, err := base64.StdEncoding.DecodeString(e.Signature.Value)
	if err != nil {
		return ErrBadSignature
	}
	msg, err := e.signingBytes()
	if err != nil {
		return ErrBadSignature
	}
	if !ed25519.Verify(pub, msg, sig) {
		return ErrBadSignature
	}
	if !e.ExpiresAt.IsZero() && now.After(e.ExpiresAt) {
		return ErrExpired
	}
	if !e.IssuedAt.IsZero() && now.Before(e.IssuedAt) {
		return ErrNotYetValid
	}
	return nil
}

// validateStructure enforces version, kind, required identity fields and bounds.
func (e Envelope) validateStructure() error {
	switch {
	case e.APIVersion != APIVersionV1:
		return ErrUnsupportedVersion
	case e.Kind != KindEnvelope:
		return ErrUnsupportedKind
	case e.ID == "" || e.Producer.ID == "" || e.Producer.KeyID == "":
		return ErrMissingField
	case len(e.EvidenceSet.Observations) > MaxObservations:
		return ErrTooManyObs
	}
	return nil
}

// Decode strictly decodes envelope bytes: it rejects payloads over the size
// limit, rejects unknown fields, and validates version, kind, identity and
// bounds. It does NOT verify the signature — call [Verify] after Decode.
func Decode(data []byte) (Envelope, error) {
	if len(data) > MaxEnvelopeBytes {
		return Envelope{}, ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var e Envelope
	if err := dec.Decode(&e); err != nil {
		return Envelope{}, fmt.Errorf("evidence envelope: decode: %w", err)
	}
	if err := e.validateStructure(); err != nil {
		return Envelope{}, err
	}
	return e, nil
}
