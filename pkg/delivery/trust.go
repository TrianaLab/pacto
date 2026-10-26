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
	// UntilSeq and UntilRecord revoke the key: UntilRecord is the id of the
	// last record it signed on the stream and UntilSeq that record's seq. It
	// verifies only that record and the records it chains from, so the
	// history it signed stays readable and a record it signs later, or
	// backdates to a seq it already used, is refused. Both or neither: zero
	// means no limit. Seqs count per stream, so a key revoked on several
	// streams gets one entry per stream.
	UntilSeq    int64  `json:"untilSeq,omitempty" yaml:"untilSeq,omitempty"`
	UntilRecord string `json:"untilRecord,omitempty" yaml:"untilRecord,omitempty"`
	// Role is what the key signs: records when empty, or the answers of an
	// eval runner ("runner"). A runner key never verifies a record, and a
	// recorder key never verifies a runner's answer.
	Role string `json:"role,omitempty" yaml:"role,omitempty"`
	// Providers are, on a runner key, the gate providers (a Stage check's
	// provider) whose Pass or Fail a runner must have signed: record verify
	// fails such a verdict that keeps no signed answer, whatever the promoter
	// cited. It lets the auditor, not the promoter, say which verdicts are
	// exams.
	Providers []string `json:"providers,omitempty" yaml:"providers,omitempty"`
}

// RoleRunner is the role of a key that signs eval runners' answers.
const RoleRunner = "runner"

// HistoryOnly is the error of a record that only a revoked key signs, at or
// below the seq it was revoked after: the record is history only if Until,
// the last record the key signed, is it or chains from it. A per-record check
// cannot tell, so the reader of the whole stream decides (pkg/deliveryrecord).
type HistoryOnly struct {
	Until string
}

func (e *HistoryOnly) Error() string {
	return "signed by a revoked key, and not in the history that ends at its last record " + e.Until
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
	var entries recordKeys
	for i, k := range t.Keys {
		if k.Role != "" && k.Role != RoleRunner {
			return nil, fmt.Errorf("trust config key %d (%s): role %q is not empty or %s", i, k.Name, k.Role, RoleRunner)
		}
		if k.Role == RoleRunner { // checked here, used by Runners
			if _, err := RunnersOf([]TrustedKey{k}, open); err != nil {
				return nil, fmt.Errorf("trust config key %d: %w", i, err)
			}
			continue
		}
		if len(k.Providers) > 0 {
			return nil, fmt.Errorf("trust config key %d (%s): providers are for runner keys only", i, k.Name)
		}
		if k.UntilSeq < 0 {
			return nil, fmt.Errorf("trust config key %d (%s): untilSeq %d is negative", i, k.Name, k.UntilSeq)
		}
		if (k.UntilSeq > 0) != (k.UntilRecord != "") {
			return nil, fmt.Errorf("trust config key %d (%s): untilSeq and untilRecord go together", i, k.Name)
		}
		for _, p := range k.Streams {
			if _, err := path.Match(p, ""); err != nil {
				return nil, fmt.Errorf("trust config key %d (%s): stream pattern %q: %w", i, k.Name, p, err)
			}
		}
		v, err := open(k)
		if err != nil {
			return nil, fmt.Errorf("trust config key %d (%s): %w", i, k.Name, err)
		}
		entries = append(entries, recordKey{k.Streams, k.UntilSeq, k.UntilRecord, v})
	}
	return entries.verify, nil
}

// recordKey is one record key a Verifier trusts.
type recordKey struct {
	streams  []string
	untilSeq int64
	until    string
	verify   func([]byte) error
}

type recordKeys []recordKey

// verify is the Verifier of the keys.
func (entries recordKeys) verify(stream string, seq int64, bundle []byte) error {
	var err, revoked, history error = fmt.Errorf("no trusted key may sign records of stream %q", stream), nil, nil
	for _, e := range entries {
		if !covers(e.streams, stream) {
			continue
		}
		if err = e.verify(bundle); err != nil {
			continue
		}
		if e.untilSeq > 0 && seq > e.untilSeq {
			revoked = fmt.Errorf("signed by a key revoked after seq %d of stream %q, and this record is seq %d", e.untilSeq, stream, seq)
			continue
		}
		if e.untilSeq > 0 {
			history = &HistoryOnly{Until: e.until}
			continue
		}
		return nil
	}
	if history != nil {
		return history
	}
	if revoked != nil {
		return revoked
	}
	return err
}

// For is the first key trusted to sign records of stream.
func (t TrustConfig) For(stream string) (TrustedKey, bool) {
	for _, k := range t.Keys {
		if k.Role == "" && covers(k.Streams, stream) {
			return k, true
		}
	}
	return TrustedKey{}, false
}

// RunnerVerifier checks an eval runner's signed answer and names the runner
// whose key or keyless identity signed it.
type RunnerVerifier func(bundle []byte) (runner string, err error)

// Runners is the verifier of the config's runner keys (role runner).
func (t TrustConfig) Runners(open func(TrustedKey) (func(bundle []byte) error, error)) (RunnerVerifier, error) {
	var keys []TrustedKey
	for _, k := range t.Keys {
		if k.Role == RoleRunner {
			keys = append(keys, k)
		}
	}
	return RunnersOf(keys, open)
}

// RunnerProviders are the providers the config's runner keys name: the
// verdicts of their checks a runner must have signed.
func (t TrustConfig) RunnerProviders() []string {
	var out []string
	for _, k := range t.Keys {
		if k.Role == RoleRunner {
			out = append(out, k.Providers...)
		}
	}
	return out
}

// RunnersOf is the verifier of runner keys: an answer counts when one of them
// signed it. Each key needs a name, the runner a verdict cites as who ran the
// exam; streams and revocation are for record keys only.
func RunnersOf(keys []TrustedKey, open func(TrustedKey) (func(bundle []byte) error, error)) (RunnerVerifier, error) {
	type entry struct {
		name   string
		verify func([]byte) error
	}
	var entries []entry
	for _, k := range keys {
		if k.Name == "" {
			return nil, errors.New("a runner key needs a name, so a verdict can say who ran the exam")
		}
		if len(k.Streams) > 0 || k.UntilSeq != 0 || k.UntilRecord != "" {
			return nil, fmt.Errorf("runner key %s: streams, untilSeq and untilRecord are for record keys only", k.Name)
		}
		v, err := open(k)
		if err != nil {
			return nil, fmt.Errorf("runner key %s: %w", k.Name, err)
		}
		name := k.Name
		if k.Identity != "" {
			name += " (" + k.Identity + " from " + k.Issuer + ")"
		}
		entries = append(entries, entry{name, v})
	}
	return func(bundle []byte) (string, error) {
		if len(entries) == 0 {
			return "", errors.New("no runner key is trusted: list one with role: runner")
		}
		for _, e := range entries {
			if e.verify(bundle) == nil {
				return e.name, nil
			}
		}
		return "", errors.New("no trusted runner key or identity signed it")
	}, nil
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
