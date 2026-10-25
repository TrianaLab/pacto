// Package signing signs and verifies DSSE envelopes in Sigstore bundles with
// sigstore-go: with a key file, a KMS key or keyless with a Fulcio
// certificate. Pacto runs no signing program; what it writes verifies with
// standard Sigstore tooling.
//
// Experimental: not covered by semantic versioning. Delivery is Alpha, and
// this API may change in a minor release.
package signing

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/sign"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/sigstore/sigstore/pkg/signature/kms"
	"github.com/sigstore/sigstore/pkg/signature/options"
	"google.golang.org/protobuf/encoding/protojson"
)

// Public-good Sigstore, which keyless publishing signs with.
const (
	PublicFulcio = "https://fulcio.sigstore.dev"
	PublicRekor  = "https://rekor.sigstore.dev"
	PublicTUF    = "https://tuf-repo-cdn.sigstore.dev"
)

// Timeout bounds every call to a KMS, Fulcio, a timestamp authority, Rekor
// and an OIDC token endpoint: one that does not answer must not hold Pacto.
const Timeout = 30 * time.Second

// Keypair is a sign.Keypair over a sigstore signer: a key file or a KMS key.
// Its hint is the hex sha256 of the DER public key.
type Keypair struct {
	signer  signature.Signer
	pub     crypto.PublicKey
	details signature.AlgorithmDetails
}

// NewKeypair wraps s, whose public key is pub. Ed25519, ECDSA P-256 and RSA
// keys sign with SHA-256; other keys are refused.
func NewKeypair(s signature.Signer, pub crypto.PublicKey) (*Keypair, error) {
	d, err := signature.GetDefaultAlgorithmDetails(pub)
	if err != nil {
		return nil, err
	}
	if d.GetHashType() != crypto.SHA256 && d.GetKeyType() != signature.ED25519 {
		return nil, fmt.Errorf("unsupported key %s: use Ed25519, ECDSA P-256 or RSA", d.GetSignatureAlgorithm())
	}
	return &Keypair{signer: s, pub: pub, details: d}, nil
}

// GetHashAlgorithm implements sign.Keypair.
func (k *Keypair) GetHashAlgorithm() protocommon.HashAlgorithm {
	return protocommon.HashAlgorithm_SHA2_256
}

// GetSigningAlgorithm implements sign.Keypair.
func (k *Keypair) GetSigningAlgorithm() protocommon.PublicKeyDetails {
	return k.details.GetSignatureAlgorithm()
}

// GetHint implements sign.Keypair.
func (k *Keypair) GetHint() []byte { return []byte(Hint(k.pub)) }

// GetKeyAlgorithm implements sign.Keypair.
func (k *Keypair) GetKeyAlgorithm() string {
	switch k.details.GetKeyType() {
	case signature.ECDSA:
		return "ECDSA"
	case signature.RSA:
		return "RSA"
	}
	return "ED25519"
}

// GetPublicKey implements sign.Keypair.
func (k *Keypair) GetPublicKey() crypto.PublicKey { return k.pub }

// GetPublicKeyPem implements sign.Keypair.
func (k *Keypair) GetPublicKeyPem() (string, error) {
	b, err := cryptoutils.MarshalPublicKeyToPEM(k.pub)
	return string(b), err
}

// SignData implements sign.Keypair.
func (k *Keypair) SignData(ctx context.Context, data []byte) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	sum := sha256.Sum256(data)
	sig, err := k.signer.SignMessage(bytes.NewReader(data), options.WithContext(ctx))
	return sig, sum[:], err
}

// Hint names a public key: the hex sha256 of its DER form, or "" for a key
// x509 cannot marshal.
func Hint(pub crypto.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// FromPrivateKey is the keypair of a private key in memory.
func FromPrivateKey(key crypto.PrivateKey) (*Keypair, error) {
	sv, err := signature.LoadDefaultSignerVerifier(key)
	if err != nil {
		return nil, err
	}
	pub, _ := sv.PublicKey() // a local key always has its public half
	return NewKeypair(sv, pub)
}

// LoadKey reads a PEM private key: a cosign key file, encrypted with
// COSIGN_PASSWORD, or a plain PKCS#8, PKCS#1 or SEC 1 key.
func LoadKey(path string) (*Keypair, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := cryptoutils.UnmarshalPEMToPrivateKey(raw, func(bool) ([]byte, error) {
		return []byte(os.Getenv("COSIGN_PASSWORD")), nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return FromPrivateKey(key)
}

// noKMSHint names the build to use when a KMS key meets a build without its
// provider.
const noKMSHint = "this build has no provider for that key: the default pacto build leaves cloud KMS out; " +
	"use a build with -tags kms (go install -tags kms github.com/trianalab/pacto/v4/cmd/pacto@<version>) " +
	"or with -tags kms_aws, kms_gcp, kms_azure or kms_hashivault for one provider"

// KMS is the keypair of a KMS key URI (awskms://, gcpkms://, azurekms://,
// hashivault://). The binary must link that provider: the default build links
// none, a build with -tags kms links all four and -tags kms_aws, kms_gcp,
// kms_azure or kms_hashivault links one. The key must sign with
// the default scheme of its public key: ECDSA P-256, Ed25519 or RSA PKCS#1
// v1.5, all with SHA-256. RSA-PSS and SHA-384/512 keys are refused.
func KMS(ctx context.Context, uri string) (*Keypair, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	sv, err := kms.Get(ctx, uri, crypto.SHA256)
	if pnf := (*kms.ProviderNotFoundError)(nil); errors.As(err, &pnf) {
		return nil, fmt.Errorf("%w: %s", err, noKMSHint)
	}
	if err != nil {
		return nil, err
	}
	pub, err := sv.PublicKey(options.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", uri, err)
	}
	kp, err := NewKeypair(sv, pub)
	if err != nil {
		return nil, err
	}
	// The public key alone does not say how the KMS signs: an RSA key may sign
	// with PSS or SHA-512 while the bundle and every verifier assume PKCS#1
	// v1.5 with SHA-256. Sign a probe and verify it the way a bundle is
	// verified, so such a key is refused here rather than at its first
	// signature.
	probe := []byte("pacto signing scheme probe")
	sig, err := sv.SignMessage(bytes.NewReader(probe), options.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", uri, err)
	}
	v, _ := signature.LoadDefaultVerifier(pub) // NewKeypair accepted pub's type
	if v.VerifySignature(bytes.NewReader(sig), bytes.NewReader(probe)) != nil {
		return nil, fmt.Errorf("%s signs with another algorithm than %s: use an ECDSA P-256, Ed25519 or RSA PKCS#1 v1.5 SHA-256 key; RSA-PSS and SHA-384/512 keys are not supported",
			uri, kp.details.GetSignatureAlgorithm())
	}
	return kp, nil
}

// Signer signs DSSE envelopes into Sigstore bundles: with Keypair alone, or
// keyless when Fulcio is set, with a certificate for the identity of the
// token IDToken returns. Timestamps and Rekor add the time a short-lived
// certificate verifies at; Root, when set, must verify every bundle before it
// is returned.
type Signer struct {
	Keypair    sign.Keypair
	Fulcio     sign.CertificateProvider
	IDToken    func(context.Context) (string, error)
	Timestamps []*sign.TimestampAuthority
	Rekor      []sign.Transparency
	Root       root.TrustedMaterial
}

// Sign signs payload, of payloadType, and returns the bundle's JSON.
func (s Signer) Sign(ctx context.Context, payloadType string, payload []byte) ([]byte, error) {
	opts := sign.BundleOptions{Context: ctx, CertificateProvider: s.Fulcio, TimestampAuthorities: s.Timestamps,
		TransparencyLogs: s.Rekor, TrustedRoot: s.Root}
	if s.Fulcio != nil {
		tok, err := s.IDToken(ctx)
		if err != nil {
			return nil, fmt.Errorf("the OIDC token for keyless signing: %w", err)
		}
		opts.CertificateProviderOptions = &sign.CertificateProviderOptions{IDToken: tok}
	}
	b, err := sign.Bundle(&sign.DSSEData{Data: payload, PayloadType: payloadType}, s.Keypair, opts)
	if err != nil {
		return nil, err
	}
	return protojson.Marshal(b)
}

// Keyed signs with kp and, when rekorURL is set, logs every signature there.
func Keyed(kp sign.Keypair, rekorURL string) Signer {
	s := Signer{Keypair: kp}
	if rekorURL != "" {
		s.Rekor = []sign.Transparency{sign.NewRekor(&sign.RekorOptions{BaseURL: rekorURL, Timeout: Timeout})}
	}
	return s
}

// Keyless is a keyless signer: an ephemeral key, a certificate from
// fulcioURL and, when set, a signed timestamp from tsaURL and a Rekor entry
// from rekorURL. A short-lived certificate verifies only at a time someone
// observed, so one of the two is required.
func Keyless(fulcioURL, tsaURL, rekorURL string, idToken func(context.Context) (string, error), trusted root.TrustedMaterial) (Signer, error) {
	if tsaURL == "" && rekorURL == "" {
		return Signer{}, errors.New("keyless signing needs a timestamp authority or a transparency log: a short-lived certificate verifies only at a time one of them observed")
	}
	kp, _ := sign.NewEphemeralKeypair(nil) // ECDSA P-256 key generation does not fail
	s := Signer{Keypair: kp, IDToken: idToken, Root: trusted,
		Fulcio: sign.NewFulcio(&sign.FulcioOptions{BaseURL: fulcioURL, Timeout: Timeout})}
	if tsaURL != "" {
		s.Timestamps = []*sign.TimestampAuthority{sign.NewTimestampAuthority(&sign.TimestampAuthorityOptions{URL: tsaURL, Timeout: Timeout})}
	}
	s.Rekor = Keyed(nil, rekorURL).Rekor
	return s, nil
}

// IDToken returns the OIDC token keyless signing presents to Fulcio: the
// contents of file when it is set (a projected service account token),
// SIGSTORE_ID_TOKEN, or a GitHub Actions token for the sigstore audience.
func IDToken(file string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if file != "" {
			b, err := os.ReadFile(file)
			return strings.TrimSpace(string(b)), err
		}
		if t := os.Getenv("SIGSTORE_ID_TOKEN"); t != "" {
			return t, nil
		}
		if err := HasIDToken(); err != nil {
			return "", err
		}
		return githubToken(ctx, os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")+"&audience=sigstore", os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
	}
}

// HasIDToken reports, without fetching it, whether IDToken("") has a token
// to read: SIGSTORE_ID_TOKEN, or GitHub Actions with id-token: write.
func HasIDToken() error {
	if os.Getenv("SIGSTORE_ID_TOKEN") != "" || (os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL") != "" && os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN") != "") {
		return nil
	}
	return errors.New("no OIDC token: set SIGSTORE_ID_TOKEN, or run in GitHub Actions with id-token: write")
}

func githubToken(ctx context.Context, url, bearer string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Value string `json:"value"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &out) != nil || out.Value == "" {
		return "", fmt.Errorf("GitHub Actions OIDC token: %s", resp.Status)
	}
	return out.Value, nil
}
