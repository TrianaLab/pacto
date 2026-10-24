package delivery

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"path"

	"gopkg.in/yaml.v3"
)

// TrustConfig says which keys may sign the records of which streams. A
// reader trusts a record only when a key listed for its stream signed it, so a
// dev recorder cannot sign prod history, and listing an old key next to a new
// one is how a key is rotated.
type TrustConfig struct {
	Keys []TrustedKey `json:"keys" yaml:"keys"`
}

// TrustedKey is one key, or one keyless identity, and the streams it may sign.
type TrustedKey struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	// PublicKey is a PEM public key (Ed25519, ECDSA or RSA).
	PublicKey string `json:"publicKey,omitempty" yaml:"publicKey,omitempty"`
	// Identity and Issuer name a keyless signer: the certificate's SAN and
	// its OIDC issuer.
	Identity string `json:"identity,omitempty" yaml:"identity,omitempty"`
	Issuer   string `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	// TrustedRoot is a Sigstore trusted root (trusted_root.json): the
	// certificate authority a keyless certificate must chain to and the
	// timestamp authority that saw it signed. A keyless entry needs one:
	// without it a self-signed certificate could name any identity and issuer.
	TrustedRoot string `json:"trustedRoot,omitempty" yaml:"trustedRoot,omitempty"`
	// Streams are path.Match patterns over stream ids
	// (<cluster>/<namespace>/<stage>), such as "*/prod/*". Empty means every
	// stream.
	Streams []string `json:"streams,omitempty" yaml:"streams,omitempty"`
}

// ParseTrust reads a trust config: YAML or JSON, or a bare PEM public key,
// which is trusted for every stream.
func ParseTrust(raw []byte) (TrustConfig, error) {
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("-----BEGIN")) {
		t := TrustConfig{Keys: []TrustedKey{{PublicKey: string(raw)}}}
		_, err := t.Verifier(check)
		return t, err
	}
	var t TrustConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil {
		return TrustConfig{}, fmt.Errorf("trust config: %w", err)
	}
	if len(t.Keys) == 0 {
		return TrustConfig{}, errors.New("trust config: no keys")
	}
	_, err := t.Verifier(check)
	return t, err
}

// Digest is the sha256 of the config's JSON form, which a record cites.
func (t TrustConfig) Digest() string {
	b, _ := json.Marshal(t) // strings only; marshalling cannot fail
	return sha256Hex(b)
}

// Verifier checks a record's signature against the keys trusted for its
// stream; open turns one key into the check of a bundle (pkg/signing does).
func (t TrustConfig) Verifier(open func(TrustedKey) (func(bundle []byte) error, error)) (Verifier, error) {
	type entry struct {
		streams []string
		verify  func([]byte) error
	}
	var entries []entry
	for i, k := range t.Keys {
		for _, p := range k.Streams {
			if _, err := path.Match(p, ""); err != nil {
				return nil, fmt.Errorf("trust config key %d (%s): stream pattern %q: %w", i, k.Name, p, err)
			}
		}
		v, err := open(k)
		if err != nil {
			return nil, fmt.Errorf("trust config key %d (%s): %w", i, k.Name, err)
		}
		entries = append(entries, entry{k.Streams, v})
	}
	return func(stream string, bundle []byte) error {
		err := fmt.Errorf("no trusted key may sign records of stream %q", stream)
		for _, e := range entries {
			if !covers(e.streams, stream) {
				continue
			}
			if err = e.verify(bundle); err == nil {
				return nil
			}
		}
		return err
	}, nil
}

// For is the first key trusted to sign records of stream.
func (t TrustConfig) For(stream string) (TrustedKey, bool) {
	for _, k := range t.Keys {
		if covers(k.Streams, stream) {
			return k, true
		}
	}
	return TrustedKey{}, false
}

func covers(patterns []string, stream string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, stream); ok {
			return true
		}
	}
	return false
}

// check validates a key's shape without crypto beyond the standard library;
// pkg/signing checks the rest when it builds the verifier.
func check(k TrustedKey) (func([]byte) error, error) {
	switch {
	case k.PublicKey != "" && k.Identity == "" && k.Issuer == "" && k.TrustedRoot == "":
		block, _ := pem.Decode([]byte(k.PublicKey))
		if block == nil {
			return nil, errors.New("publicKey holds no PEM block")
		}
		if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
			return nil, fmt.Errorf("publicKey: %w", err)
		}
		return nil, nil
	case k.PublicKey == "" && k.Identity != "" && k.Issuer != "":
		if !json.Valid([]byte(k.TrustedRoot)) {
			return nil, errors.New("a keyless entry needs trustedRoot, the Sigstore trusted root (JSON) its certificate must chain to")
		}
		return nil, nil
	}
	return nil, errors.New("set publicKey, or identity, issuer and trustedRoot, not both")
}

// PublicKeyPEM is pub as a PEM public key, for a trust config.
func PublicKeyPEM(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}
