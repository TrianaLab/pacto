/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// SigningConfig is how the recorder signs, with sigstore-go: exactly one of a
// private key file (kind), a KMS key or keyless signing.
type SigningConfig struct {
	// KeyFile is a PEM private key: Ed25519, ECDSA P-256 or RSA.
	KeyFile string
	// KMSKey is a KMS key URI (awskms://, gcpkms://, azurekms://, hashivault://).
	KMSKey string
	// Keyless signs with a Fulcio certificate for the pod's workload identity.
	Keyless bool
	// Identity and Issuer are the certificate identity and OIDC issuer keyless
	// signing produces, so the recorder can tell its own records apart.
	Identity, Issuer string
	// FulcioURL issues the certificate; TimestampURL is the timestamp
	// authority whose signed time a short-lived certificate verifies at.
	FulcioURL, TimestampURL string
	// TokenFile holds the OIDC token Fulcio checks: a projected service
	// account token for the sigstore audience.
	TokenFile string
	// TrustedRootFile is the Sigstore trusted root (trusted_root.json) naming
	// that Fulcio and timestamp authority. Every keyless record must verify
	// under it: without it a self-signed certificate could name the
	// recorder's identity.
	TrustedRootFile string
}

// NewSigner builds the recorder's signer and the trust config entry for the
// records it writes. The entry is built from the configuration, apart from the
// signer it describes, so every signature is checked against it before it is
// returned, and one probe is signed here: a keyless identity that is not the
// one Fulcio certifies, or a key that signs with another scheme, stops the
// recorder at startup and never writes a record its own stream would reject.
func NewSigner(ctx context.Context, cfg SigningConfig) (delivery.Signer, delivery.TrustedKey, error) {
	s, own, err := newSigner(ctx, cfg)
	if err != nil {
		return nil, own, err
	}
	v, _ := signing.TrustVerifier(delivery.TrustConfig{Keys: []delivery.TrustedKey{own}}) // newSigner built own from a loaded key or root
	checked := selfChecked{Signer: s, verify: v}
	probe := delivery.NewGap("probe", delivery.ObjectMeta{Name: "probe"}, 0, delivery.Chain{Stream: "probe", Seq: 1}, "", "probe")
	if _, _, err := delivery.SignRecord(ctx, checked, probe); err != nil {
		return nil, own, fmt.Errorf("signing a probe record: %w", err)
	}
	return checked, own, nil
}

// selfChecked refuses a signature the recorder's own trust entry does not
// verify, so the operation is retried rather than recorded.
type selfChecked struct {
	delivery.Signer
	verify delivery.Verifier
}

func (s selfChecked) Sign(ctx context.Context, payloadType string, payload []byte) ([]byte, error) {
	b, err := s.Signer.Sign(ctx, payloadType, payload)
	if err != nil {
		return nil, err
	}
	if err := s.verify("", 0, b); err != nil {
		return nil, fmt.Errorf("the recorder's own trust entry rejects its signature: %w", err)
	}
	return b, nil
}

func newSigner(ctx context.Context, cfg SigningConfig) (delivery.Signer, delivery.TrustedKey, error) {
	own := delivery.TrustedKey{Name: "recorder"}
	set := 0
	for _, on := range []bool{cfg.KeyFile != "", cfg.KMSKey != "", cfg.Keyless} {
		if on {
			set++
		}
	}
	if set != 1 {
		return nil, own, errors.New("configure exactly one of a key file, a KMS key or keyless signing")
	}
	var (
		kp  *signing.Keypair
		err error
	)
	switch {
	case cfg.KeyFile != "":
		kp, err = signing.LoadKey(cfg.KeyFile)
	case cfg.KMSKey != "":
		kp, err = signing.KMS(ctx, cfg.KMSKey)
	default:
		return keyless(cfg, own)
	}
	if err != nil {
		return nil, own, err
	}
	own.PublicKey, _ = delivery.PublicKeyPEM(kp.GetPublicKey()) // a key sigstore loaded always marshals
	return signing.Keyed(kp, ""), own, nil
}

func keyless(cfg SigningConfig, own delivery.TrustedKey) (delivery.Signer, delivery.TrustedKey, error) {
	if cfg.Identity == "" || cfg.Issuer == "" || cfg.TrustedRootFile == "" || cfg.FulcioURL == "" || cfg.TokenFile == "" {
		return nil, own, errors.New("keyless signing needs the certificate identity, issuer, trusted root, Fulcio URL and token file")
	}
	raw, err := os.ReadFile(cfg.TrustedRootFile)
	if err != nil {
		return nil, own, err
	}
	tr, err := root.NewTrustedRootFromJSON(raw)
	if err != nil {
		return nil, own, err
	}
	s, err := signing.Keyless(cfg.FulcioURL, cfg.TimestampURL, "", signing.IDToken(cfg.TokenFile), tr)
	if err != nil {
		return nil, own, err
	}
	own.Identity, own.Issuer, own.TrustedRoot = cfg.Identity, cfg.Issuer, string(raw)
	return s, own, nil
}

// RecorderTrust is the trust config the recorder verifies a stream with before
// it extends it: its own key for every stream, plus the keys file lists for
// the streams it names, such as the old key during a rotation. A record
// signed by any other key stops the stream.
func RecorderTrust(own delivery.TrustedKey, file string) (delivery.TrustConfig, error) {
	t := delivery.TrustConfig{Keys: []delivery.TrustedKey{own}}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return t, err
		}
		extra, err := delivery.ParseTrust(raw)
		if err != nil {
			return t, err
		}
		t.Keys = append(t.Keys, extra.Keys...)
	}
	_, err := signing.TrustVerifier(t)
	return t, err
}
