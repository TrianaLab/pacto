package cli_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// TestPushSignKeyAcceptsKeygenSeed: --sign-key takes the seed `pacto evidence
// keygen` writes, and the pushed digest verifies with its public key; a key
// that cannot be read fails before anything is pushed.
func TestPushSignKeyAcceptsKeygenSeed(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := filepath.Join(t.TempDir(), "my-idp.key")
	mustWrite(t, key, base64.StdEncoding.EncodeToString(priv.Seed()))
	store := oci.NewClient(authn.NewMultiKeychain(), oci.WithInsecureRegistries(host))
	dir := testutil.WriteTestBundle(t)

	if out, _, err := execFleetStore(t, store, "push", "oci://"+host+"/acme/svc", "-p", dir, "--index", "none", "--sign-key", filepath.Join(t.TempDir(), "none.key")); err == nil {
		t.Fatalf("an unreadable key pushed:\n%s", out)
	}
	out, _, err := execFleetStore(t, store, "push", "oci://"+host+"/acme/svc", "-p", dir, "--index", "none", "--sign-key", key)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	reg := publish.NewClient(authn.NewMultiKeychain(), host)
	h, err := reg.Head(context.Background(), host+"/acme/svc:1.0.0")
	if err != nil || h == nil {
		t.Fatalf("head = %+v, %v", h, err)
	}
	if err := publish.VerifyDigest(context.Background(), reg, publish.PolicyVerifier(signing.Policy{Key: pub}), host+"/acme/svc", h.Digest); err != nil {
		t.Fatalf("the pushed digest does not verify with the seed's key: %v", err)
	}
}
