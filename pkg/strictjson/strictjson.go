// Package strictjson decodes exactly one schema-valid JSON value and then EOF.
// It is the single strict-decode boundary used at every wire and storage edge, so
// unknown fields and trailing data are rejected uniformly. It deliberately does
// NOT rely on json.Decoder.More to detect trailing data: More returns false when
// the next byte is '}' or ']', so a lone '}' or ']' after a value would slip past.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// ErrTrailingData reports bytes after the single decoded JSON value.
var ErrTrailingData = errors.New("unexpected data after JSON value")

// Decode strictly decodes exactly one JSON value from r into dst: unknown fields
// are rejected (DisallowUnknownFields) and ANY trailing data — including a lone
// '}' or ']', a second value, a scalar or garbage — is an error. It does not bound
// input size; wrap r in an io.LimitReader at untrusted boundaries.
func Decode(r io.Reader, dst any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	// Exactly-one-value: a second decode must be a clean EOF. Anything else — a
	// further value (err == nil), a stray token, or syntax garbage — is trailing
	// data. RawMessage accepts any single value, so err == nil means real content.
	var tail json.RawMessage
	if err := dec.Decode(&tail); !errors.Is(err, io.EOF) {
		return ErrTrailingData
	}
	return nil
}

// Unmarshal is the []byte convenience over Decode.
func Unmarshal(data []byte, dst any) error {
	return Decode(bytes.NewReader(data), dst)
}
