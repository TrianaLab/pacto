package app

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"context"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/sigstore/sigstore/pkg/signature/kms"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/random"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

// fakeRun stands in for git, docker, syft and the declared command. Every
// call is recorded as "name arg...".
type fakeRun struct {
	calls []string
	fail  string // a call prefix that fails
}

func (f *fakeRun) run(_ context.Context, _ string, env []string, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, call)
	if f.fail != "" && strings.HasPrefix(call, f.fail) {
		return nil, errors.New("injected: " + call)
	}
	switch name {
	case "git":
		return []byte("abc123\n"), nil
	case "docker":
		p, _ := layout.Write(outOf(env), empty.Index)
		img, _ := random.Image(32, 1)
		return nil, p.AppendImage(img)
	case "syft":
		return []byte(`{"spdxVersion":"SPDX-2.3"}`), nil
	}
	return nil, os.WriteFile(filepath.Join(outOf(env), "deployment.yaml"), []byte("kind: Deployment\n"), 0o644)
}

func outOf(env []string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PACTO_OUT="); ok {
			return v
		}
	}
	return ""
}

func publishRegistry(t *testing.T) (string, publish.Registry) {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return host, publish.NewClient(authn.DefaultKeychain, host)
}

func writePublishBundle(t *testing.T, host, extra string) string {
	t.Helper()
	dir := t.TempDir()
	y := `pactoVersion: "2.1"
service: {name: payments, version: 1.0.0}
` + extra + `publish:
  bundle: oci://` + host + `/pacto/payments
  deploy: manifests
  artifacts:
    - {name: api, type: image, ref: "oci://` + host + `/payments", sbom: true, build: {dockerfile: {context: ., file: Dockerfile, platforms: [linux/amd64]}}}
    - {name: manifests, type: manifests, ref: "oci://` + host + `/deploy/payments", build: {command: [./render.sh, --flag]}}
  sign: {key: cosign.key, transparencyLog: false}
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func publishService(f *fakeRun, reg publish.Registry) *Service {
	return &Service{sys: sysDeps{run: f.run, publishRegistry: func() publish.Registry { return reg }}}
}

// TestPublishPlan: --plan lists what is missing and runs nothing but git.
func TestPublishPlan(t *testing.T) {
	host, reg := publishRegistry(t)
	f := &fakeRun{}
	res, err := publishService(f, reg).Publish(context.Background(), PublishOptions{Path: writePublishBundle(t, host, ""), Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Plan.Ready || res.Plan.Missing != 3 || res.Plan.Revision != "abc123" || len(f.calls) != 1 {
		t.Fatalf("plan: %+v calls %v", res.Plan, f.calls)
	}
}

// signingKey writes a cosign key file, encrypted with COSIGN_PASSWORD, into
// dir, keeps it out of the bundle and returns its public key.
func signingKey(t *testing.T, dir string) crypto.PublicKey {
	t.Helper()
	priv, pub, err := cryptoutils.GeneratePEMEncodedECDSAKeyPair(elliptic.P256(), cryptoutils.StaticPasswordFunc([]byte("pw")))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSIGN_PASSWORD", "pw")
	_ = os.WriteFile(filepath.Join(dir, "cosign.key"), priv, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "cosign.pub"), pub, 0o600)
	_ = os.WriteFile(filepath.Join(dir, ".pactoignore"), []byte("cosign.key\ncosign.pub\n"), 0o600)
	k, _ := cryptoutils.UnmarshalPEMToPublicKey(pub)
	return k
}

// TestPublishRunsTheTools: publish builds with docker buildx and the declared
// command, makes an SBOM with syft and signs every digest with sigstore-go,
// so each verifies under the public key, then a second run signs nothing.
func TestPublishRunsTheTools(t *testing.T) {
	host, reg := publishRegistry(t)
	f := &fakeRun{}
	svc := publishService(f, reg)
	dir := writePublishBundle(t, host, "")
	pub := signingKey(t, dir)
	res, err := svc.Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{host + "/payments@" + res.Digests["api"], host + "/payments@" + res.SBOMs["api"],
		host + "/deploy/payments@" + res.Digests["manifests"], host + "/pacto/payments@" + res.Digests[publish.BundleItem]}
	for _, ref := range refs {
		repo, digest, _ := strings.Cut(ref, "@")
		if err := publish.VerifyDigest(t.Context(), reg, keyPolicy(pub), repo, digest); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
		independentlyVerified(t, dir, ref)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{
		"docker buildx build --output type=oci,dest=",
		"--label org.opencontainers.image.version=1.0.0 --label org.opencontainers.image.revision=r1 --file Dockerfile --platform linux/amd64 .",
		"./render.sh --flag",
		"syft scan registry:" + host + "/payments@",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("no call with %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "cosign") {
		t.Errorf("publish ran cosign:\n%s", joined)
	}
	again, err := svc.Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil || len(again.Actions) != 0 {
		t.Fatalf("second run: %v %v", err, again.Actions)
	}
}

func keyPolicy(pub crypto.PublicKey) publish.BundleVerifier {
	return bundleVerifier(signing.Policy{Key: pub})
}

// TestBundleVerifierReturnsTheSignedPredicate: the predicate type comes from
// the signed statement, so an attestation by the right key over the right
// digest is told apart from a signature whatever its referrer says.
func TestBundleVerifierReturnsTheSignedPredicate(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(key)
	d := sha256.Sum256([]byte("artifact"))
	st := fmt.Appendf(nil, `{"_type":"https://in-toto.io/Statement/v1","subject":[{"digest":{"sha256":%q}}],"predicateType":"https://slsa.dev/provenance/v1","predicate":{}}`,
		hex.EncodeToString(d[:]))
	b, err := signing.Keyed(kp, "").Sign(t.Context(), "application/vnd.in-toto+json", st)
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := keyPolicy(key.Public())(b, d[:]); err != nil || pt != "https://slsa.dev/provenance/v1" {
		t.Fatalf("predicate %q %v", pt, err)
	}
	if _, err := keyPolicy(key.Public())(b, make([]byte, 32)); err == nil {
		t.Fatal("verified over another digest")
	}
}

// independentlyVerified checks ref with the cosign CLI when it is installed:
// what Pacto signs verifies with standard tooling, as Flux's does. The kind
// e2e runs it everywhere; here it is skipped without cosign.
func independentlyVerified(t *testing.T, dir, ref string) {
	t.Helper()
	if _, err := exec.LookPath("cosign"); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "cosign", "verify", "--key", filepath.Join(dir, "cosign.pub"),
		"--insecure-ignore-tlog", "--allow-http-registry", ref).CombinedOutput()
	if err != nil {
		t.Errorf("cosign does not verify %s: %v\n%s", ref, err, out)
	}
}

func TestPublishRefusals(t *testing.T) {
	host, reg := publishRegistry(t)
	ctx := context.Background()
	f := &fakeRun{}
	if _, err := publishService(f, reg).Publish(ctx, PublishOptions{Path: writeInvalidBundle(t)}); err == nil {
		t.Fatal("an invalid contract is refused")
	}
	local := "dependencies: [{name: orders, ref: ../orders, required: true, compatibility: ^1.0.0}]\n"
	res, err := publishService(f, reg).Publish(ctx, PublishOptions{Path: writePublishBundle(t, host, local), Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Ready || !slices.ContainsFunc(res.Plan.Checks, func(c publish.Check) bool { return c.Name == "references" && !c.Passed }) {
		t.Fatalf("a local reference is a failed check: %+v", res.Plan.Checks)
	}
	withKey := writePublishBundle(t, host, "")
	_ = os.WriteFile(filepath.Join(withKey, "cosign.key"), []byte("secret"), 0o600)
	res, _ = publishService(f, reg).Publish(ctx, PublishOptions{Path: withKey, Plan: true})
	if res.Plan.Ready || !slices.ContainsFunc(res.Plan.Checks, func(c publish.Check) bool { return c.Name == "signing key" && !c.Passed }) {
		t.Fatalf("a signing key inside the bundle is a failed check: %+v", res.Plan.Checks)
	}
	if err := keyOutsideBundle(&contract.Contract{Publish: &contract.Publish{Sign: &contract.PublishSign{Key: "/abs/cosign.key"}}}, nil); err != nil {
		t.Fatal(err)
	}
	nogit := &fakeRun{fail: "git"}
	res, _ = publishService(nogit, reg).Publish(ctx, PublishOptions{Path: writePublishBundle(t, host, ""), Plan: true})
	if res.Plan.Revision != "" || res.Plan.Ready {
		t.Fatalf("no git revision: %+v", res.Plan)
	}
}

// pushImage pushes a random image and returns its repo@digest.
func pushImage(t *testing.T, reg publish.Registry, repo string) string {
	t.Helper()
	img, _ := random.Image(32, 1)
	d, err := reg.Write(t.Context(), repo, img)
	if err != nil {
		t.Fatal(err)
	}
	return repo + "@" + d
}

// TestExecToolsSignKeyed: a key file and a KMS key sign once, keep a valid
// signature, and a failing key or log surfaces.
func TestExecToolsSignKeyed(t *testing.T) {
	ctx := t.Context()
	host, reg := publishRegistry(t)
	dir := t.TempDir()
	pub := signingKey(t, dir)
	tools := newExecTools(dir, nil, reg)
	off := false
	key := contract.PublishSign{Key: "cosign.key", TransparencyLog: &off}
	ref := pushImage(t, reg, host+"/app")
	if err := tools.Verify(ctx, key, ref); err == nil {
		t.Fatal("an unsigned digest verified")
	}
	if signed, err := tools.Sign(ctx, key, ref); err != nil || !signed {
		t.Fatalf("sign: %v %v", signed, err)
	}
	if err := tools.Verify(ctx, key, ref); err != nil {
		t.Fatalf("verify after signing: %v", err)
	}
	independentlyVerified(t, dir, ref)
	if signed, err := tools.Sign(ctx, key, ref); err != nil || signed {
		t.Fatalf("an existing signature is kept: %v %v", signed, err)
	}
	repo, digest, _ := strings.Cut(ref, "@")
	if err := publish.VerifyDigest(ctx, reg, keyPolicy(pub), repo, digest); err != nil {
		t.Fatalf("the key file's signature: %v", err)
	}
}

func TestExecToolsSignKMS(t *testing.T) {
	ctx := t.Context()
	host, reg := publishRegistry(t)
	tools := newExecTools(t.TempDir(), nil, reg)
	off := false
	ref := pushImage(t, reg, host+"/app")
	kmsKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sv, _ := signature.LoadSignerVerifier(kmsKey, crypto.SHA256)
	loads := 0
	kms.AddProvider("apptestkms://", func(context.Context, string, crypto.Hash, ...signature.RPCOption) (kms.SignerVerifier, error) {
		loads++
		return testKMS{sv}, nil
	})
	viaKMS := contract.PublishSign{KMS: "apptestkms://k", TransparencyLog: &off}
	if signed, err := tools.Sign(ctx, viaKMS, ref); err != nil || !signed {
		t.Fatalf("KMS sign: %v %v", signed, err)
	}
	if err := tools.Verify(ctx, viaKMS, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Sign(ctx, viaKMS, pushImage(t, reg, host+"/bpp")); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("one run loaded the KMS key %d times, not once", loads)
	}
	repo, digest, _ := strings.Cut(ref, "@")
	if err := publish.VerifyDigest(ctx, reg, keyPolicy(kmsKey.Public()), repo, digest); err != nil {
		t.Fatalf("the KMS signature: %v", err)
	}
}

func TestExecToolsSignFailures(t *testing.T) {
	ctx := t.Context()
	host, reg := publishRegistry(t)
	dir := t.TempDir()
	signingKey(t, dir)
	tools := newExecTools(dir, nil, reg)
	off := false
	key := contract.PublishSign{Key: "cosign.key", TransparencyLog: &off}
	ref := pushImage(t, reg, host+"/app")
	for name, s := range map[string]contract.PublishSign{
		"missing key": {Key: "/abs/missing", TransparencyLog: &off},
		"no KMS":      {KMS: "nokms://k", TransparencyLog: &off},
	} {
		if _, err := tools.Sign(ctx, s, ref); err == nil {
			t.Errorf("%s: signed", name)
		}
		if err := tools.Verify(ctx, s, ref); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
	// The transparency log is on by default; a log that fails stops signing.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	defer down.Close()
	old := rekorURL
	rekorURL = down.URL
	defer func() { rekorURL = old }()
	if _, err := tools.Sign(ctx, contract.PublishSign{Key: "cosign.key"}, pushImage(t, reg, host+"/bpp")); err == nil {
		t.Fatal("signed with the transparency log down")
	}
	if _, err := tools.Sign(ctx, key, "BAD REF@sha256:1"); err == nil {
		t.Fatal("signed a broken reference")
	}
}

// testKMS is a KMS provider backed by a local key.
type testKMS struct{ signature.SignerVerifier }

func (testKMS) CreateKey(context.Context, string) (crypto.PublicKey, error) { return nil, nil }
func (testKMS) CryptoSigner(context.Context, func(error)) (crypto.Signer, crypto.SignerOpts, error) {
	return nil, nil, nil
}
func (testKMS) SupportedAlgorithms() []string { return nil }
func (testKMS) DefaultAlgorithm() string      { return "" }

// TestExecToolsSignKeyless: keyless signing pins the configured identity and
// issuer: the CI identity signs and verifies, another identity fails before
// any tag, and an unreachable trusted root fails closed.
func TestExecToolsSignKeyless(t *testing.T) {
	ctx := t.Context()
	host, reg := publishRegistry(t)
	ss := sigtest.New(t)
	oldRoot, oldF, oldR, oldT := publicGoodRoot, fulcioURL, rekorURL, tsaURL
	defer func() { publicGoodRoot, fulcioURL, rekorURL, tsaURL = oldRoot, oldF, oldR, oldT }()
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return ss.Root, nil }
	fulcioURL, rekorURL, tsaURL = ss.Fulcio, "", ss.TSA
	t.Setenv("SIGSTORE_ID_TOKEN", sigtest.Token("ci@acme.example", "https://token.actions.githubusercontent.com"))
	tools := newExecTools(t.TempDir(), nil, reg)
	ci := contract.PublishSign{Keyless: true, Identity: "ci@acme.example", Issuer: "https://token.actions.githubusercontent.com"}
	ref := pushImage(t, reg, host+"/app")
	if signed, err := tools.Sign(ctx, ci, ref); err != nil || !signed {
		t.Fatalf("keyless sign: %v %v", signed, err)
	}
	if err := tools.Verify(ctx, ci, ref); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]contract.PublishSign{
		"identity": {Keyless: true, Identity: "mallory@acme.example", Issuer: ci.Issuer},
		"issuer":   {Keyless: true, Identity: ci.Identity, Issuer: "https://evil.example"},
	} {
		if err := tools.Verify(ctx, s, ref); err == nil {
			t.Errorf("another %s verified", name)
		}
	}
	// A CI token for another identity signs, but its signature fails the
	// pinned verify before any tag is written.
	wrong := contract.PublishSign{Keyless: true, Identity: "release@acme.example", Issuer: ci.Issuer}
	if _, err := tools.Sign(ctx, wrong, pushImage(t, reg, host+"/bpp")); err == nil ||
		!strings.Contains(err.Error(), `identity "release@acme.example", issuer "https://token.actions.githubusercontent.com"`) {
		t.Errorf("a signature by the wrong identity must fail: %v", err)
	}
	// A root from another Sigstore does not trust this one's certificates.
	other := sigtest.New(t)
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return other.Root, nil }
	tools = newExecTools(t.TempDir(), nil, reg) // a run caches the root it loaded
	if err := tools.Verify(ctx, ci, ref); err == nil {
		t.Error("verified under an untrusted root")
	}
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return nil, errors.New("tuf down") }
	tools = newExecTools(t.TempDir(), nil, reg)
	if err := tools.Verify(ctx, ci, ref); err == nil || !strings.Contains(err.Error(), "trusted root") {
		t.Errorf("no trusted root: %v", err)
	}
}

func TestExecToolsHelpers(t *testing.T) {
	ctx := context.Background()
	tools := newExecTools(t.TempDir(), (&fakeRun{}).run, nil)
	t.Setenv("PACTO_INSECURE_REGISTRIES", "zot.local:5000")
	for ref, want := range map[string]bool{"localhost:5000/a": true, "127.0.0.1/a": true, "zot.local:5000/a": true, "ghcr.io/a": false} {
		if insecureHost(ref) != want {
			t.Errorf("insecureHost(%s) != %v", ref, want)
		}
	}
	if _, err := tools.SBOM(ctx, "ghcr.io/a@sha256:1"); err != nil {
		t.Fatal(err)
	}
}

func TestExecRun(t *testing.T) {
	out, err := execRun(context.Background(), t.TempDir(), []string{"X=1"}, "sh", "-c", "echo $X")
	if err != nil || strings.TrimSpace(string(out)) != "1" {
		t.Fatalf("%q %v", out, err)
	}
	_, err = execRun(context.Background(), t.TempDir(), nil, "sh", "-c", "echo nope >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("the error carries stderr: %v", err)
	}
	if long := tail(strings.Repeat("x", 3000)); len(long) > 2010 || !strings.HasPrefix(long, "…") {
		t.Fatalf("tail: %d", len(long))
	}
	if defaultPublishRegistry() == nil {
		t.Fatal("the default registry")
	}
}
