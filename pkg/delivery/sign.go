package delivery

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// Signing format: a DSSE envelope over the Statement, in a sigstore bundle.
const (
	PayloadType     = "application/vnd.in-toto+json"
	BundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"
)

// Bundle is a sigstore bundle v0.3 carrying one DSSE envelope. Byte fields
// marshal as standard base64, as the bundle's protobuf JSON form requires.
type Bundle struct {
	MediaType            string               `json:"mediaType"`
	VerificationMaterial VerificationMaterial `json:"verificationMaterial"`
	DSSEEnvelope         Envelope             `json:"dsseEnvelope"`
}

// VerificationMaterial names the key (by hint) or carries the certificate a
// signature verifies against.
type VerificationMaterial struct {
	PublicKey   *KeyHint     `json:"publicKey,omitempty"`
	Certificate *Certificate `json:"certificate,omitempty"`
}

// KeyHint names a public key the verifier already holds.
type KeyHint struct {
	Hint string `json:"hint"`
}

// Certificate is a DER certificate, as keyless signing produces.
type Certificate struct {
	RawBytes []byte `json:"rawBytes"`
}

// Envelope is a DSSE envelope.
type Envelope struct {
	Payload     []byte      `json:"payload"`
	PayloadType string      `json:"payloadType"`
	Signatures  []Signature `json:"signatures"`
}

// Signature is one DSSE signature.
type Signature struct {
	Sig []byte `json:"sig"`
}

// Signer signs DSSE pre-authentication bytes. Ed25519Signer is the built-in
// one; KMS and keyless signers live with the recorder.
type Signer interface {
	Sign(ctx context.Context, pae []byte) (sig []byte, material VerificationMaterial, err error)
}

// Verifier checks a signature over pae against the bundle's material.
type Verifier func(pae, sig []byte, m VerificationMaterial) error

// PAE is DSSE's pre-authentication encoding of a payload.
func PAE(payloadType string, payload []byte) []byte {
	return fmt.Appendf(nil, "DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload)
}

// SignRecord signs st and returns the bundle and the record id: the sha256 of
// the DSSE payload, so the id does not depend on a signature that a KMS makes
// differently every time.
func SignRecord(ctx context.Context, s Signer, st Statement) (bundle []byte, id string, err error) {
	payload, _ := json.Marshal(st) // plain data; marshalling cannot fail
	sig, m, err := s.Sign(ctx, PAE(PayloadType, payload))
	if err != nil {
		return nil, "", err
	}
	b, _ := json.Marshal(Bundle{MediaType: BundleMediaType, VerificationMaterial: m,
		DSSEEnvelope: Envelope{Payload: payload, PayloadType: PayloadType, Signatures: []Signature{{Sig: sig}}}})
	return b, sha256Hex(payload), nil
}

// OpenRecord verifies a bundle and returns its statement and record id. A
// bundle that is not one signed pacto.dev/operation/v1 statement is an error.
func OpenRecord(bundle []byte, verify Verifier) (Statement, string, error) {
	var b Bundle
	if err := json.Unmarshal(bundle, &b); err != nil {
		return Statement{}, "", fmt.Errorf("bundle: %w", err)
	}
	env := b.DSSEEnvelope
	switch {
	case b.MediaType != BundleMediaType:
		return Statement{}, "", fmt.Errorf("bundle media type %q", b.MediaType)
	case env.PayloadType != PayloadType:
		return Statement{}, "", fmt.Errorf("payload type %q", env.PayloadType)
	case len(env.Signatures) != 1:
		return Statement{}, "", fmt.Errorf("%d signatures, want 1", len(env.Signatures))
	}
	if err := verify(PAE(env.PayloadType, env.Payload), env.Signatures[0].Sig, b.VerificationMaterial); err != nil {
		return Statement{}, "", fmt.Errorf("signature: %w", err)
	}
	var st Statement
	if err := strictjson.Unmarshal(env.Payload, &st); err != nil {
		return Statement{}, "", fmt.Errorf("statement: %w", err)
	}
	if st.Type != StatementType || st.PredicateType != PredicateOperation {
		return Statement{}, "", fmt.Errorf("statement %q with predicate %q", st.Type, st.PredicateType)
	}
	return st, sha256Hex(env.Payload), nil
}

// KeyHintOf returns the hint a key is named by: the hex sha256 of its DER
// public key.
func KeyHintOf(pub crypto.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

type ed25519Signer struct {
	key  ed25519.PrivateKey
	hint string
}

// Ed25519Signer signs with an Ed25519 key, as the recorder does on kind.
func Ed25519Signer(key ed25519.PrivateKey) Signer {
	return ed25519Signer{key: key, hint: KeyHintOf(key.Public())}
}

func (s ed25519Signer) Sign(_ context.Context, pae []byte) ([]byte, VerificationMaterial, error) {
	return ed25519.Sign(s.key, pae), VerificationMaterial{PublicKey: &KeyHint{Hint: s.hint}}, nil
}

// PublicKeyVerifier verifies signatures by one Ed25519, ECDSA or RSA key, the
// key types cosign and KMS produce. ECDSA and RSA sign the sha256 of the
// pre-authentication bytes.
func PublicKeyVerifier(pub crypto.PublicKey) Verifier {
	hint := KeyHintOf(pub)
	return func(pae, sig []byte, m VerificationMaterial) error {
		if m.PublicKey == nil || m.PublicKey.Hint != hint {
			return errors.New("signed by a key the recorder does not hold")
		}
		return verifyWith(pub, pae, sig)
	}
}

func verifyWith(pub crypto.PublicKey, pae, sig []byte) error {
	digest := sha256.Sum256(pae)
	ok := false
	switch k := pub.(type) {
	case ed25519.PublicKey:
		ok = ed25519.Verify(k, pae, sig)
	case *ecdsa.PublicKey:
		ok = ecdsa.VerifyASN1(k, digest[:], sig)
	case *rsa.PublicKey:
		ok = rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) == nil
	default:
		return fmt.Errorf("unsupported key type %T", pub)
	}
	if !ok {
		return errors.New("the signature does not verify")
	}
	return nil
}

// Fulcio certificate extensions naming the OIDC issuer.
var (
	oidIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	oidIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
)

// CertificateVerifier verifies keyless signatures: the bundle's certificate
// must name identity (a SAN URI or email) and issuer, and the signature must
// verify under its key.
// ponytail: no chain or transparency check; registry write access is the trust
// boundary here, and `record verify` (part B) checks the chain against a trust
// root.
func CertificateVerifier(identity, issuer string) Verifier {
	return func(pae, sig []byte, m VerificationMaterial) error {
		if m.Certificate == nil {
			return errors.New("the bundle carries no certificate")
		}
		cert, err := x509.ParseCertificate(m.Certificate.RawBytes)
		if err != nil {
			return err
		}
		ids := slices.Clone(cert.EmailAddresses)
		for _, u := range cert.URIs {
			ids = append(ids, u.String())
		}
		if !slices.Contains(ids, identity) || certIssuer(cert) != issuer {
			return fmt.Errorf("the certificate names %v from %q, want %q from %q", ids, certIssuer(cert), identity, issuer)
		}
		return verifyWith(cert.PublicKey, pae, sig)
	}
}

func certIssuer(cert *x509.Certificate) string {
	for _, ext := range cert.Extensions {
		switch {
		case ext.Id.Equal(oidIssuerV2):
			var s string
			if _, err := asn1.Unmarshal(ext.Value, &s); err == nil {
				return s
			}
		case ext.Id.Equal(oidIssuerV1):
			return string(ext.Value)
		}
	}
	return ""
}
