package app

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	ghIssuer   = "https://token.actions.githubusercontent.com"
	ciWorkflow = "acme/mono/.github/workflows/ci.yml@refs/heads/main"
	ciIdentity = "https://github.com/" + ciWorkflow
)

// privateSigstore swaps public-good Sigstore for a private one for the test
// and sets a GitHub Actions token naming workflow. The fake Fulcio issues for
// sub, so sub is the identity Fulcio would map the workflow to.
func privateSigstore(t *testing.T, workflow string) *sigtest.Sigstore {
	t.Helper()
	ss := sigtest.New(t)
	oldRoot, oldF, oldR, oldT := publicGoodRoot, fulcioURL, rekorURL, tsaURL
	t.Cleanup(func() { publicGoodRoot, fulcioURL, rekorURL, tsaURL = oldRoot, oldF, oldR, oldT })
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return ss.Root, nil }
	fulcioURL, rekorURL, tsaURL = ss.Fulcio, "", ss.TSA
	ciToken(t, workflow)
	return ss
}

func ciToken(t *testing.T, workflow string) {
	t.Setenv("SIGSTORE_ID_TOKEN", sigtest.TokenWith(map[string]string{"iss": ghIssuer, "sub": "https://github.com/" + workflow, "job_workflow_ref": workflow}))
}

// withoutSign drops the publish.sign line writePublishBundle wrote.
func withoutSign(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(dir, "pacto.yaml")
	raw, _ := os.ReadFile(p)
	s := strings.Replace(string(raw), "  sign: {key: cosign.key, transparencyLog: false}\n", "", 1)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPublishKeylessWithoutSignSection: a publish section with no sign signs
// every digest keyless, and each signature verifies against the identity the
// CI token names and no other.
func TestPublishKeylessWithoutSignSection(t *testing.T) {
	ss := privateSigstore(t, ciWorkflow)
	host, reg := publishRegistry(t)
	dir := writePublishBundle(t, host, "")
	withoutSign(t, dir)
	res, err := publishService(&fakeRun{}, reg).Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	ours := publish.PolicyVerifier(signing.Policy{Identity: ciIdentity, Issuer: ghIssuer, Root: ss.Root})
	other := publish.PolicyVerifier(signing.Policy{Identity: "https://github.com/acme/other/.github/workflows/ci.yml@refs/heads/main", Issuer: ghIssuer, Root: ss.Root})
	for _, ref := range []string{host + "/payments@" + res.Digests["api"], host + "/pacto/payments@" + res.Digests[publish.BundleItem]} {
		repo, digest, _ := strings.Cut(ref, "@")
		if err := publish.VerifyDigest(t.Context(), reg, ours, repo, digest); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
		if err := publish.VerifyDigest(t.Context(), reg, other, repo, digest); err == nil {
			t.Errorf("%s verified as another identity", ref)
		}
	}
}

// TestKeylessIdentityFromTheToken: with no declared identity the signer reads
// it from the token, and a token it cannot read refuses before signing.
func TestKeylessIdentityFromTheToken(t *testing.T) {
	privateSigstore(t, ciWorkflow)
	host, reg := publishRegistry(t)
	ref := pushImage(t, reg, host+"/app")
	t.Setenv("SIGSTORE_ID_TOKEN", sigtest.TokenWith(map[string]string{"iss": "https://idp.example", "sub": "x"}))
	if _, err := newExecTools(t.TempDir(), nil, reg).Sign(t.Context(), contract.PublishSign{}, ref); err == nil || !strings.Contains(err.Error(), `"https://idp.example"`) {
		t.Errorf("an unreadable identity must refuse: %v", err)
	}
	t.Setenv("SIGSTORE_ID_TOKEN", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	if err := newExecTools(t.TempDir(), nil, reg).Verify(t.Context(), contract.PublishSign{}, ref); err == nil || !strings.Contains(err.Error(), "no OIDC token") {
		t.Errorf("no token: %v", err)
	}
}

// signCounter counts the signatures publish asks for.
type signCounter struct {
	execTools
	signs int
}

func (c *signCounter) Sign(ctx context.Context, sign contract.PublishSign, ref string) (bool, error) {
	c.signs++
	return c.execTools.Sign(ctx, sign, ref)
}

// TestAdoptOnlySignedByTheTokenIdentity: a tag whose provenance claims this
// version and revision but whose signature is by another identity is a
// conflict, and nothing is signed.
func TestAdoptOnlySignedByTheTokenIdentity(t *testing.T) {
	privateSigstore(t, "acme/other/.github/workflows/ci.yml@refs/heads/main")
	host, reg := publishRegistry(t)
	img, _ := random.Image(32, 1)
	forged := mutate.Annotations(img, map[string]string{publish.AnnotationVersion: "1.0.0", publish.AnnotationRevision: "r1"}).(v1.Image)
	d, err := reg.Write(t.Context(), host+"/payments", forged)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Tag(t.Context(), host+"/payments", d, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := newExecTools(t.TempDir(), nil, reg).Sign(t.Context(), contract.PublishSign{}, host+"/payments@"+d); err != nil {
		t.Fatal(err)
	}
	ciToken(t, ciWorkflow)
	dir := writePublishBundle(t, host, "")
	withoutSign(t, dir)
	raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	tools := &signCounter{execTools: newExecTools(dir, nil, reg)}
	res, err := publish.Apply(t.Context(), reg, tools, publish.Input{Bundle: &contract.Bundle{Contract: c, RawYAML: raw, FS: os.DirFS(dir)}, Revision: "r1"})
	if !errors.Is(err, publish.ErrNotReady) || res.Plan.Items[0].State != publish.Conflict || tools.signs != 0 {
		t.Fatalf("a tag signed by another identity was adopted: %v %+v signs %d", err, res.Plan.Items[0], tools.signs)
	}
}

// TestKeepRefusalNilSignKeyless: with no publish.sign a version refusal is
// kept, signed keyless as the CI identity.
func TestKeepRefusalNilSignKeyless(t *testing.T) {
	ss := privateSigstore(t, ciWorkflow)
	freshProbes(t)
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := publish.NewClient(anonymous, host)
	dir := writePublishBundle(t, host, "workload: service\n")
	withoutSign(t, dir)
	svc := publishService(&fakeRun{}, reg)
	svc.sys.now = func() time.Time { return time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC) }
	if _, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"}); err != nil {
		t.Fatal(err)
	}
	bumpVersion(t, dir, "1.0.1", "job")
	res, err := svc.Publish(t.Context(), PublishOptions{Path: dir, Revision: "r2"})
	if !errors.Is(err, publish.ErrNotReady) || res.Refusal != "the refusal is kept, signed, in "+host+"/pacto/payments" {
		t.Fatalf("refused publish: %v %q", err, res.Refusal)
	}
	repo, _ := ocireferrer.Open(host+"/pacto/payments", ocireferrer.Options{})
	p := signing.Policy{Identity: ciIdentity, Issuer: ghIssuer, Root: ss.Root}
	got, err := deliveryrecord.ListRefusals(t.Context(), repo, "payments", func(b []byte) error { _, err := p.Verify(b, nil); return err })
	if err != nil || len(got.Refusals) != 1 {
		t.Fatalf("refusals: %+v %v", got, err)
	}
}

// noReferrers is a registry whose referrers can never be listed.
type noReferrers struct{ publish.Registry }

func (noReferrers) Referrers(context.Context, string, string, string) ([]string, error) {
	return nil, errors.New("referrers unavailable")
}

// TestSignReadBackVersusMismatch: a signature read and rejected names the
// identity; a read-back that failed says only that it could not verify.
func TestSignReadBackVersusMismatch(t *testing.T) {
	key := func() *signing.Keypair {
		_, k, _ := ed25519.GenerateKey(nil)
		kp, err := signing.FromPrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		return kp
	}
	signer, other := key(), key()
	host, reg := publishRegistry(t)
	ref := pushImage(t, reg, host+"/app")
	tools := signerTools(reg, signing.Keyed(signer, ""), signing.Policy{Key: other.GetPublicKey()})
	if _, err := tools.Sign(t.Context(), contract.PublishSign{}, ref); err == nil || !strings.Contains(err.Error(), "does not verify against the configured identity") {
		t.Errorf("mismatch: %v", err)
	}
	ref = pushImage(t, reg, host+"/app")
	tools = signerTools(noReferrers{reg}, signing.Keyed(signer, ""), signing.Policy{Key: signer.GetPublicKey()})
	if _, err := tools.Sign(t.Context(), contract.PublishSign{}, ref); err == nil || !strings.Contains(err.Error(), "could not be read back to verify") || strings.Contains(err.Error(), "identity") {
		t.Errorf("read-back: %v", err)
	}
}
