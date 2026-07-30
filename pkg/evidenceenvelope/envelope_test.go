package evidenceenvelope

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// Fixed clock points around the envelope's validity window.
var (
	issued      = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expires     = time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	validNow    = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	afterExpiry = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	beforeIssue = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
)

func prov() evidence.Provenance {
	return evidence.Provenance{Collector: "collector", DetectedAt: issued}
}

// keyPair derives a deterministic Ed25519 key pair from a fixed 32-byte seed
// pattern, so signatures are reproducible across runs.
func keyPair(pattern byte) (ed25519.PrivateKey, ed25519.PublicKey) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = pattern
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv, priv.Public().(ed25519.PublicKey)
}

// baseEnvelope is a structurally valid, unsigned envelope with one observation.
func baseEnvelope() Envelope {
	return Envelope{
		APIVersion: APIVersionV1,
		Kind:       KindEnvelope,
		ID:         "env-1",
		Producer:   Producer{ID: "prod-1", Version: "1.0", KeyID: "key-1"},
		Sequence:   7,
		IssuedAt:   issued,
		ExpiresAt:  expires,
		EvidenceSet: evidence.EvidenceSet{
			Subject:     evidence.SubjectRef{Kind: "service", Name: "orders"},
			ContractRef: "oci://example/orders:1.0",
			Source:      "collector",
			ObservedAt:  issued,
			Observations: []evidence.Observation{
				evidence.NewCapabilityObserved(
					evidence.SubjectRef{Kind: "capability", Name: "health"}, true, prov()),
			},
		},
	}
}

// badObsEnvelope carries an observation that fails validate() on marshal, so
// signingBytes (and thus json.Marshal) errors out.
func badObsEnvelope() Envelope {
	e := baseEnvelope()
	e.EvidenceSet.Observations = []evidence.Observation{{
		Kind:    "BadKind",
		Subject: evidence.SubjectRef{Kind: "x", Name: "y"},
		Outcome: evidence.Observed,
		Value:   json.RawMessage(`{}`),
	}}
	return e
}

func mustSign(t *testing.T, e Envelope, key ed25519.PrivateKey) Envelope {
	t.Helper()
	signed, err := Sign(e, key)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return signed
}

func TestSignVerifyHappyPath(t *testing.T) {
	priv, pub := keyPair(1)
	ts := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1"}}

	signed := mustSign(t, baseEnvelope(), priv)
	if signed.Signature == nil {
		t.Fatal("Sign left Signature nil")
	}
	if signed.Signature.Algorithm != AlgorithmEd25519 {
		t.Errorf("algorithm = %q, want %q", signed.Signature.Algorithm, AlgorithmEd25519)
	}
	// Pre-set version/kind survive (false branch of the default fill-ins).
	if signed.APIVersion != APIVersionV1 || signed.Kind != KindEnvelope {
		t.Errorf("version/kind changed: %q/%q", signed.APIVersion, signed.Kind)
	}
	if err := Verify(signed, ts, validNow); err != nil {
		t.Fatalf("Verify happy path: %v", err)
	}
}

func TestSignDefaultsVersionKind(t *testing.T) {
	priv, _ := keyPair(1)
	e := baseEnvelope()
	e.APIVersion = ""
	e.Kind = ""

	signed := mustSign(t, e, priv)
	if signed.APIVersion != APIVersionV1 {
		t.Errorf("APIVersion default = %q, want %q", signed.APIVersion, APIVersionV1)
	}
	if signed.Kind != KindEnvelope {
		t.Errorf("Kind default = %q, want %q", signed.Kind, KindEnvelope)
	}
}

func TestSignMarshalError(t *testing.T) {
	priv, _ := keyPair(1)
	if _, err := Sign(badObsEnvelope(), priv); err == nil {
		t.Fatal("Sign should fail when an observation cannot marshal")
	}
}

// Round trip through Marshal -> Decode -> Verify proves canonicalJSON produces
// stable signing bytes regardless of transport re-encoding.
func TestRoundTripStability(t *testing.T) {
	priv, pub := keyPair(1)
	ts := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1"}}

	signed := mustSign(t, baseEnvelope(), priv)
	data, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := Verify(decoded, ts, validNow); err != nil {
		t.Fatalf("Verify after round trip: %v", err)
	}
}

func TestVerifyFailures(t *testing.T) {
	priv, pub := keyPair(1)
	ts := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1"}}

	cases := []struct {
		name   string
		mutate func(e *Envelope)
		now    time.Time
		want   error
	}{
		{"unsigned", func(e *Envelope) { e.Signature = nil }, validNow, ErrUnsignedEnvelope},
		{"bad-algorithm", func(e *Envelope) { e.Signature.Algorithm = "RSA" }, validNow, ErrBadAlgorithm},
		{"unknown-key", func(e *Envelope) { e.Producer.KeyID = "nope" }, validNow, ErrUnknownKey},
		{"not-base64", func(e *Envelope) { e.Signature.Value = "@@@not-base64@@@" }, validNow, ErrBadSignature},
		{"tampered-field", func(e *Envelope) { e.ID = "tampered" }, validNow, ErrBadSignature},
		{"tampered-signature", flipSignatureByte, validNow, ErrBadSignature},
		{"expired", func(e *Envelope) {}, afterExpiry, ErrExpired},
		{"not-yet-valid", func(e *Envelope) {}, beforeIssue, ErrNotYetValid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := mustSign(t, baseEnvelope(), priv)
			tc.mutate(&e)
			if err := Verify(e, ts, tc.now); !errors.Is(err, tc.want) {
				t.Fatalf("Verify = %v, want %v", err, tc.want)
			}
		})
	}
}

func flipSignatureByte(e *Envelope) {
	raw, err := base64.StdEncoding.DecodeString(e.Signature.Value)
	if err != nil {
		panic(err)
	}
	raw[0] ^= 0xff
	e.Signature.Value = base64.StdEncoding.EncodeToString(raw)
}

// A syntactically valid, trusted, base64 signature over an envelope whose
// evidence cannot marshal makes signingBytes fail inside Verify.
func TestVerifySigningBytesError(t *testing.T) {
	_, pub := keyPair(1)
	ts := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1"}}

	e := badObsEnvelope()
	e.Signature = &Signature{
		Algorithm: AlgorithmEd25519,
		Value:     base64.StdEncoding.EncodeToString([]byte("dummy")),
	}
	if err := Verify(e, ts, validNow); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("Verify = %v, want %v", err, ErrBadSignature)
	}
}

// validateStructure is exercised through both Verify and Decode.
func TestValidateStructure(t *testing.T) {
	_, pub := keyPair(1)
	ts := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1"}}

	cases := []struct {
		name       string
		mutate     func(e *Envelope)
		want       error
		skipDecode bool // too-many-obs cannot marshal / stays under the size limit
	}{
		{"bad-version", func(e *Envelope) { e.APIVersion = "bogus" }, ErrUnsupportedVersion, false},
		{"bad-kind", func(e *Envelope) { e.Kind = "bogus" }, ErrUnsupportedKind, false},
		{"missing-id", func(e *Envelope) { e.ID = "" }, ErrMissingField, false},
		{"missing-producer-id", func(e *Envelope) { e.Producer.ID = "" }, ErrMissingField, false},
		{"missing-producer-keyid", func(e *Envelope) { e.Producer.KeyID = "" }, ErrMissingField, false},
		{"too-many-obs", func(e *Envelope) {
			e.EvidenceSet.Observations = make([]evidence.Observation, MaxObservations+1)
		}, ErrTooManyObs, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := baseEnvelope()
			tc.mutate(&e)
			if err := Verify(e, ts, validNow); !errors.Is(err, tc.want) {
				t.Fatalf("Verify = %v, want %v", err, tc.want)
			}
			if tc.skipDecode {
				return
			}
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if _, err := Decode(data); !errors.Is(err, tc.want) {
				t.Fatalf("Decode = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeStrict(t *testing.T) {
	t.Run("unknown-field", func(t *testing.T) {
		data, err := json.Marshal(baseEnvelope())
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		m["bogusField"] = 1
		withExtra, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("re-Marshal: %v", err)
		}
		if _, err := Decode(withExtra); err == nil {
			t.Fatal("Decode should reject unknown fields")
		}
	})

	t.Run("too-large", func(t *testing.T) {
		if _, err := Decode(make([]byte, MaxEnvelopeBytes+1)); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Decode = %v, want %v", err, ErrTooLarge)
		}
	})

	t.Run("malformed-json", func(t *testing.T) {
		if _, err := Decode([]byte("{not json")); err == nil {
			t.Fatal("Decode should reject malformed JSON")
		}
	})
}

func TestMapTrustStore(t *testing.T) {
	_, pub := keyPair(1)
	ts := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1"}}

	got, ok := ts.Entry("key-1")
	if !ok || !got.PublicKey.Equal(pub) || got.ProducerID != "prod-1" {
		t.Fatalf("Entry(hit) = %+v, %v", got, ok)
	}
	if _, ok := ts.Entry("missing"); ok {
		t.Fatal("Entry(miss) reported found")
	}
}

func TestVerifyAuthorization(t *testing.T) {
	priv, pub := keyPair(1)
	signed := mustSign(t, baseEnvelope(), priv) // producer prod-1, subject "orders"

	// A key bound to a DIFFERENT producer cannot be used to sign as prod-1.
	wrongProducer := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-2"}}
	if err := Verify(signed, wrongProducer, validNow); !errors.Is(err, ErrProducerMismatch) {
		t.Errorf("cross-producer = %v, want ErrProducerMismatch", err)
	}

	// A subject outside the key's allowlist is rejected; one inside (incl. a glob) is allowed.
	outOfScope := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1", Subjects: []string{"payments"}}}
	if err := Verify(signed, outOfScope, validNow); !errors.Is(err, ErrSubjectNotAllowed) {
		t.Errorf("out-of-scope subject = %v, want ErrSubjectNotAllowed", err)
	}
	inScope := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1", Subjects: []string{"ord*"}}}
	if err := Verify(signed, inScope, validNow); err != nil {
		t.Errorf("in-scope subject (glob) = %v, want nil", err)
	}

	// An unbound entry (empty ProducerID) permits any producer — single-tenant use.
	unbound := MapTrustStore{"key-1": TrustEntry{PublicKey: pub}}
	if err := Verify(signed, unbound, validNow); err != nil {
		t.Errorf("unbound entry = %v, want nil", err)
	}
}

func TestCanonicalPreservesLargeSequence(t *testing.T) {
	priv, pub := keyPair(1)
	ts := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1"}}

	// Two sequences above 2^53 must produce DIFFERENT signing bytes (they both
	// round to the same float64, so this fails without exact-integer canonicaliza-
	// tion), proving the uint64 sequence is authenticated exactly.
	a := baseEnvelope()
	a.Sequence = 1<<53 + 1
	b := baseEnvelope()
	b.Sequence = 1<<53 + 2
	ab, _ := a.signingBytes()
	bb, _ := b.signingBytes()
	if string(ab) == string(bb) {
		t.Fatal("distinct large sequences produced identical signing bytes (float64 rounding)")
	}

	// Max uint64 signs, verifies, and is preserved exactly in the signing bytes.
	m := baseEnvelope()
	m.Sequence = ^uint64(0)
	signed := mustSign(t, m, priv)
	if err := Verify(signed, ts, validNow); err != nil {
		t.Fatalf("max-uint64 sequence verify: %v", err)
	}
	sb, _ := signed.signingBytes()
	if !strings.Contains(string(sb), "18446744073709551615") {
		t.Errorf("max uint64 not preserved exactly in signing bytes: %s", sb)
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	priv, _ := keyPair(1)
	signed := mustSign(t, baseEnvelope(), priv)
	data, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	// A second JSON value after the envelope is rejected.
	trailing := append(append([]byte(nil), data...), []byte(`{"x":1}`)...)
	if _, err := Decode(trailing); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("Decode(trailing) = %v, want ErrTrailingData", err)
	}
	// The clean envelope still decodes.
	if _, err := Decode(data); err != nil {
		t.Fatalf("Decode(clean) = %v", err)
	}
}

// Sentinel messages must never leak public or private key material.
func TestErrorMessagesNoKeyLeak(t *testing.T) {
	priv, pub := keyPair(1)
	ts := MapTrustStore{"key-1": TrustEntry{PublicKey: pub, ProducerID: "prod-1"}}
	secrets := []string{
		string(pub),
		string(priv),
		base64.StdEncoding.EncodeToString(pub),
		base64.StdEncoding.EncodeToString(priv),
	}

	signed := mustSign(t, baseEnvelope(), priv)
	unknown := signed
	unknown.Producer.KeyID = "nope"
	tampered := signed
	tampered.ID = "tampered"

	errs := []error{
		Verify(unknown, ts, validNow),
		Verify(tampered, ts, validNow),
		Verify(signed, MapTrustStore{}, validNow),
	}
	for _, err := range errs {
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, s := range secrets {
			if s != "" && strings.Contains(err.Error(), s) {
				t.Errorf("error message leaks key material: %q", err)
			}
		}
	}
}
