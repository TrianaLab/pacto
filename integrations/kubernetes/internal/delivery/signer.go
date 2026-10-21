/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// SigningConfig is how the recorder signs: exactly one of an Ed25519 key file
// (kind), a cosign KMS key URI or keyless signing.
type SigningConfig struct {
	// Ed25519KeyFile is a PKCS#8 PEM Ed25519 private key.
	Ed25519KeyFile string
	// KMSKey is a cosign KMS URI (awskms://, gcpkms://, azurekms://, hashivault://).
	KMSKey string
	// Keyless signs with a Fulcio certificate for the pod's workload identity.
	Keyless bool
	// Identity and Issuer are the certificate identity and OIDC issuer keyless
	// signing produces, so the recorder can tell its own records apart.
	Identity, Issuer string
	// Cosign is the cosign binary. Empty means "cosign" on PATH.
	Cosign string
}

// cosignTimeout bounds one cosign run: a KMS or Fulcio that does not answer
// must not hold the stream forever.
const cosignTimeout = 2 * time.Minute

// runFunc runs a program with stdin and returns its standard output.
type runFunc func(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error)

func execRun(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		return nil, fmt.Errorf("%s %s: %w: %s", name, args[0], err, msg[:min(len(msg), 4096)])
	}
	return out, nil
}

// NewSigner builds the recorder's signer and the verifier for the records it
// writes. A nil run executes cosign.
func NewSigner(ctx context.Context, cfg SigningConfig, run runFunc) (delivery.Signer, delivery.Verifier, error) {
	set := 0
	for _, on := range []bool{cfg.Ed25519KeyFile != "", cfg.KMSKey != "", cfg.Keyless} {
		if on {
			set++
		}
	}
	if set != 1 {
		return nil, nil, errors.New("configure exactly one of an Ed25519 key file, a KMS key or keyless signing")
	}
	if run == nil {
		run = execRun
	}
	cosign := cosignSigner{path: cfg.Cosign, key: cfg.KMSKey, run: run}
	if cosign.path == "" {
		cosign.path = "cosign"
	}
	switch {
	case cfg.Ed25519KeyFile != "":
		key, err := readEd25519(cfg.Ed25519KeyFile)
		if err != nil {
			return nil, nil, err
		}
		return delivery.Ed25519Signer(key), delivery.PublicKeyVerifier(key.Public()), nil
	case cfg.Keyless:
		if cfg.Identity == "" || cfg.Issuer == "" {
			return nil, nil, errors.New("keyless signing needs the certificate identity and issuer")
		}
		return cosign, delivery.CertificateVerifier(cfg.Identity, cfg.Issuer), nil
	}
	pub, err := cosign.publicKey(ctx)
	if err != nil {
		return nil, nil, err
	}
	cosign.hint = delivery.KeyHintOf(pub)
	return cosign, delivery.PublicKeyVerifier(pub), nil
}

func readEd25519(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s holds no PEM block", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s holds a %T, not an Ed25519 key", path, key)
	}
	return ed, nil
}

// cosignSigner signs DSSE pre-authentication bytes with `cosign sign-blob`:
// with a KMS key, or keyless with a Fulcio certificate. Rekor upload is off.
// ECDSA and RSA keys sign the sha256 of the blob, which is what a DSSE
// verifier checks.
type cosignSigner struct {
	path, key, hint string
	run             runFunc
}

func (c cosignSigner) publicKey(ctx context.Context) (crypto.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, cosignTimeout)
	defer cancel()
	out, err := c.run(ctx, nil, c.path, "public-key", "--key", c.key)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(out)
	if block == nil {
		return nil, errors.New("cosign public-key printed no PEM block")
	}
	return x509.ParsePKIXPublicKey(block.Bytes)
}

// Sign implements delivery.Signer.
func (c cosignSigner) Sign(ctx context.Context, pae []byte) ([]byte, delivery.VerificationMaterial, error) {
	var m delivery.VerificationMaterial
	dir, err := os.MkdirTemp("", "pacto-record-")
	if err != nil {
		return nil, m, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sigPath, certPath := filepath.Join(dir, "sig"), filepath.Join(dir, "cert")
	args := []string{"sign-blob", "--yes", "--tlog-upload=false", "--b64=false", "--output-signature", sigPath}
	if c.key != "" {
		args = append(args, "--key", c.key)
	} else {
		args = append(args, "--output-certificate", certPath)
	}
	ctx, cancel := context.WithTimeout(ctx, cosignTimeout)
	defer cancel()
	// The blob comes on stdin ("-"), so it never touches the disk.
	if _, err := c.run(ctx, pae, c.path, append(args, "-")...); err != nil {
		return nil, m, err
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return nil, m, err
	}
	if c.key != "" {
		m.PublicKey = &delivery.KeyHint{Hint: c.hint}
		return sig, m, nil
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, m, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, m, errors.New("cosign wrote no certificate")
	}
	m.Certificate = &delivery.Certificate{RawBytes: block.Bytes}
	return sig, m, nil
}
