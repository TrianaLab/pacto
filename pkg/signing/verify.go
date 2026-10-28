package signing

import (
	"crypto"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// Policy is who must have signed a bundle: the holder of Key, or the keyless
// Identity (a certificate SAN) from Issuer (its OIDC issuer) under a
// certificate authority Root trusts.
type Policy struct {
	Key              crypto.PublicKey
	Identity, Issuer string
	Root             root.TrustedMaterial
	// Tlog requires a transparency log entry, as public-good keyless signing
	// writes.
	Tlog bool
}

// Verify checks bundle (JSON) against p. digest, the sha256 of the artifact
// the envelope's statement names, is checked too unless it is nil. A keyless
// bundle verifies only at a time a timestamp authority or the transparency
// log observed: its certificate lives minutes. Without a Root it verifies
// nothing, since anyone can mint a certificate naming any identity.
func (p Policy) Verify(raw, digest []byte) (*verify.VerificationResult, error) {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	var (
		tm    root.TrustedMaterial
		vopts []verify.VerifierOption
		popt  verify.PolicyOption
	)
	switch {
	case p.Key != nil:
		v, err := signature.LoadDefaultVerifier(p.Key)
		if err != nil {
			return nil, err
		}
		tm = root.NewTrustedPublicKeyMaterial(func(string) (root.TimeConstrainedVerifier, error) {
			return root.NewExpiringKey(v, time.Time{}, time.Time{}), nil
		})
		vopts, popt = []verify.VerifierOption{verify.WithNoObserverTimestamps()}, verify.WithKey()
	case p.Root == nil:
		return nil, errors.New("no trusted root: a certificate that chains to nothing proves no identity")
	default:
		id, err := verify.NewShortCertificateIdentity(p.Issuer, "", p.Identity, "")
		if err != nil {
			return nil, err
		}
		tm, popt = p.Root, verify.WithCertificateIdentity(id)
		vopts = []verify.VerifierOption{verify.WithObserverTimestamps(1)}
		if p.Tlog {
			vopts = append(vopts, verify.WithTransparencyLog(1))
		}
		if len(p.Root.CTLogs()) > 0 {
			vopts = append(vopts, verify.WithSignedCertificateTimestamps(1))
		}
	}
	v, _ := verify.NewVerifier(tm, vopts...) // the options above always validate
	art := verify.WithoutArtifactUnsafe()
	if digest != nil {
		art = verify.WithArtifactDigest("sha256", digest)
	}
	return v.Verify(&b, verify.NewPolicy(art, popt))
}

// PublicGoodRoot fetches public-good Sigstore's trusted root through TUF from
// mirror (tuf.DefaultMirror in production), caching it under the user's home.
func PublicGoodRoot(mirror string) (root.TrustedMaterial, error) {
	f := fetcher.NewDefaultFetcher()
	f.SetHTTPClient(&http.Client{Timeout: Timeout})
	return root.FetchTrustedRootWithOptions(tuf.DefaultOptions().WithRepositoryBaseURL(mirror).WithFetcher(f))
}

// TrustVerifier is the record verifier of a trust config: each record must
// verify under a key or keyless identity its stream may use.
func TrustVerifier(t delivery.TrustConfig) (delivery.Verifier, error) {
	return t.Verifier(OpenKey)
}

// OpenKey is the check of a bundle against one trusted key or keyless
// identity: what a trust config and delivery.NewGates turn keys into.
func OpenKey(k delivery.TrustedKey) (func([]byte) error, error) {
	p, err := policyOf(k)
	if err != nil {
		return nil, err
	}
	return func(b []byte) error { _, err := p.Verify(b, nil); return err }, nil
}

// WithoutSCT reports whether a keyless record under trustedRoot carries no
// signed certificate timestamp to check: the root lists no CT log, as a private
// Sigstore without one does. cosign checks an SCT unless it is told not to
// (--insecure-ignore-sct), where Verify only checks one the root can vouch for.
func WithoutSCT(trustedRoot string) bool {
	tr, err := root.NewTrustedRootFromJSON([]byte(trustedRoot))
	return err == nil && len(tr.CTLogs()) == 0
}

func policyOf(k delivery.TrustedKey) (Policy, error) {
	switch {
	case k.PublicKey != "" && k.Identity == "" && k.Issuer == "" && k.TrustedRoot == "":
		pub, err := cryptoutils.UnmarshalPEMToPublicKey([]byte(k.PublicKey))
		if err != nil {
			return Policy{}, fmt.Errorf("publicKey: %w", err)
		}
		return Policy{Key: pub}, nil
	case k.PublicKey == "" && k.Identity != "" && k.Issuer != "":
		if k.TrustedRoot == "" {
			return Policy{}, errors.New("a keyless entry needs trustedRoot, the Sigstore trusted root its certificate must chain to")
		}
		tr, err := root.NewTrustedRootFromJSON([]byte(k.TrustedRoot))
		if err != nil {
			return Policy{}, fmt.Errorf("trustedRoot: %w", err)
		}
		return Policy{Identity: k.Identity, Issuer: k.Issuer, Root: tr}, nil
	}
	return Policy{}, errors.New("set publicKey, or identity, issuer and trustedRoot, not both")
}
