package signing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/cryptoutils"

	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// TestCosignVerifiesWhatPactoSigns runs the cosign CLI, when it is installed,
// over bundles Pacto wrote: a keyed one and a keyless one under a private
// trusted root. Standard tooling must accept both, with no Pacto involved,
// and refuse a wrong identity.
func TestCosignVerifiesWhatPactoSigns(t *testing.T) {
	if _, err := exec.LookPath("cosign"); err != nil {
		t.Skip("cosign is not installed; the kind e2e runs this check")
	}
	dir := t.TempDir()
	payload := []byte(statement)
	blob := filepath.Join(dir, "blob")
	_ = os.WriteFile(blob, payload, 0o600)
	cosign := func(args ...string) error {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		out, err := exec.CommandContext(ctx, "cosign", args...).CombinedOutput()
		if err != nil {
			t.Logf("cosign %v: %s", args, out)
		}
		return err
	}
	const subject = "0000000000000000000000000000000000000000000000000000000000000000"

	_, key, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := FromPrivateKey(key)
	b, err := Keyed(kp, "").Sign(t.Context(), "application/vnd.in-toto+json", payload)
	if err != nil {
		t.Fatal(err)
	}
	keyed := filepath.Join(dir, "keyed.json")
	_ = os.WriteFile(keyed, b, 0o600)
	pub, _ := cryptoutils.MarshalPublicKeyToPEM(key.Public())
	pubFile := filepath.Join(dir, "key.pub")
	_ = os.WriteFile(pubFile, pub, 0o600)
	if err := cosign("verify-blob-attestation", "--bundle", keyed, "--key", pubFile, "--type", "https://example.com/p",
		"--insecure-ignore-tlog", "--digest", subject, "--digestAlg", "sha256"); err != nil {
		t.Errorf("cosign rejects a keyed bundle: %v", err)
	}

	ss := testutil.New(t)
	tok := testutil.Token("ci@acme.example", "https://issuer.example")
	s, _ := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, ss.Root)
	b, err = s.Sign(t.Context(), "application/vnd.in-toto+json", payload)
	if err != nil {
		t.Fatal(err)
	}
	keyless, rootFile := filepath.Join(dir, "keyless.json"), filepath.Join(dir, "trusted_root.json")
	_ = os.WriteFile(keyless, b, 0o600)
	_ = os.WriteFile(rootFile, ss.RootJSON, 0o600)
	args := func(identity string) []string {
		return []string{"verify-blob-attestation", "--bundle", keyless, "--trusted-root", rootFile, "--type", "https://example.com/p",
			"--certificate-identity", identity, "--certificate-oidc-issuer", "https://issuer.example",
			"--insecure-ignore-tlog", "--insecure-ignore-sct", "--use-signed-timestamps", "--digest", subject, "--digestAlg", "sha256"}
	}
	if err := cosign(args("ci@acme.example")...); err != nil {
		t.Errorf("cosign rejects a keyless bundle: %v", err)
	}
	if err := cosign(args("mallory@acme.example")...); err == nil {
		t.Error("cosign accepted a keyless bundle for another identity")
	}
}
