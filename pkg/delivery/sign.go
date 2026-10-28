package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

// Signing format: a DSSE envelope over the Statement, in a Sigstore bundle.
const (
	PayloadType     = "application/vnd.in-toto+json"
	BundleMediaType = "application/vnd.dev.sigstore.bundle.v0.3+json"
)

// Signer signs a DSSE payload into a Sigstore bundle and returns its JSON.
// pkg/signing implements it with sigstore-go; this package holds no crypto,
// so the browser dashboard that reads records does not link it.
type Signer interface {
	Sign(ctx context.Context, payloadType string, payload []byte) (bundle []byte, err error)
}

// Verifier checks a record's bundle, for the record at seq of stream: a trust
// config trusts each key for some streams only, and a revoked key up to a seq.
// pkg/signing.TrustVerifier builds one.
type Verifier func(stream string, seq int64, bundle []byte) error

// SignRecord signs st and returns the bundle and the record id: the sha256 of
// the DSSE payload, so the id does not depend on a signature that a KMS makes
// differently every time.
func SignRecord(ctx context.Context, s Signer, st Statement) (bundle []byte, id string, err error) {
	return signStatement(ctx, s, st)
}

func signStatement(ctx context.Context, s Signer, st any) (bundle []byte, id string, err error) {
	payload, _ := json.Marshal(st) // plain data; marshalling cannot fail
	bundle, err = s.Sign(ctx, PayloadType, payload)
	if err != nil {
		return nil, "", err
	}
	return bundle, sha256Hex(payload), nil
}

// envelope is the part of a bundle OpenRecord reads before it verifies.
type envelope struct {
	MediaType    string `json:"mediaType"`
	DSSEEnvelope struct {
		Payload     []byte            `json:"payload"`
		PayloadType string            `json:"payloadType"`
		Signatures  []json.RawMessage `json:"signatures"`
	} `json:"dsseEnvelope"`
}

// OpenRecord verifies a bundle and returns its statement and record id. A
// bundle that is not one signed delivery.pacto.trianalab.io/operation/v1 statement is an error.
// The payload is decoded before the signature is checked, because the stream
// it names decides which keys may have signed it; nothing it says is trusted
// until the signature verifies. A record only a revoked key signs comes back
// with its statement, its id and a *HistoryOnly error: the reader of the
// stream decides whether it is history.
func OpenRecord(bundle []byte, verify Verifier) (Statement, string, error) {
	var st Statement
	payload, err := openStatement(bundle, PredicateOperation, &st)
	if err != nil {
		return Statement{}, "", err
	}
	if err := verify(st.Predicate.Chain.Stream, st.Predicate.Chain.Seq, bundle); err != nil {
		if h := (*HistoryOnly)(nil); errors.As(err, &h) {
			return st, sha256Hex(payload), fmt.Errorf("signature: %w", err)
		}
		return Statement{}, "", fmt.Errorf("signature: %w", err)
	}
	return st, sha256Hex(payload), nil
}

// openStatement checks a bundle carries one DSSE-signed in-toto statement and
// decodes it into st, which must then be of the predicate want. It returns the
// payload, unverified: the caller checks the signature.
func openStatement(bundle []byte, want string, st any) ([]byte, error) {
	var b envelope
	if err := json.Unmarshal(bundle, &b); err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}
	env := b.DSSEEnvelope
	switch {
	case b.MediaType != BundleMediaType:
		return nil, fmt.Errorf("bundle media type %q", b.MediaType)
	case env.PayloadType != PayloadType:
		return nil, fmt.Errorf("payload type %q", env.PayloadType)
	case len(env.Signatures) != 1:
		return nil, fmt.Errorf("%d signatures, want 1", len(env.Signatures))
	}
	var head struct {
		Type          string `json:"_type"`
		PredicateType string `json:"predicateType"`
	}
	if err := json.Unmarshal(env.Payload, &head); err != nil {
		return nil, fmt.Errorf("statement: %w", err)
	}
	if head.Type != StatementType || head.PredicateType != want {
		return nil, fmt.Errorf("statement %q with predicate %q", head.Type, head.PredicateType)
	}
	if err := strictjson.Unmarshal(env.Payload, st); err != nil {
		return nil, fmt.Errorf("statement: %w", err)
	}
	return env.Payload, nil
}
