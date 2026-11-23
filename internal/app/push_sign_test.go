package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// pushEnv is a Service that pushes to an in-memory registry, with a key to
// sign with.
type pushEnv struct {
	host string
	svc  *Service
	reg  publish.Registry
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// newPushEnv serves h and opens no organization index unless a test sets one.
func newPushEnv(t *testing.T, h http.Handler) *pushEnv {
	t.Helper()
	freshProbes(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	reg := publish.NewClient(anonymous, host)
	svc := NewService(oci.NewClient(anonymous, oci.WithInsecureRegistries(host)), nil)
	svc.sys.publishRegistry = func() publish.Registry { return reg }
	svc.sys.refusalRepo = func(string) (*remote.Repository, error) { return nil, errors.New("offline") }
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &pushEnv{host: host, svc: svc, reg: reg, pub: pub, priv: priv}
}

// signed is opts signing with the env's key, kept at keyPath.
func (e *pushEnv) signed(opts PushOptions, keyPath string) PushOptions {
	opts.SignKeyPath, opts.Signer, opts.Verify = keyPath, edSigner(e.priv), keyPolicy(e.pub)
	return opts
}

func (e *pushEnv) verify(t *testing.T, repo, digest string) error {
	t.Helper()
	return publish.VerifyDigest(context.Background(), e.reg, keyPolicy(e.pub), e.host+"/"+repo, digest)
}

// writeContribution writes a contributed bundle: a name and a version.
func writeContribution(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.5\"\nservice:\n  name: checkout\n  version: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func plainRegistry() http.Handler { return registry.New(registry.Logger(log.New(io.Discard, "", 0))) }

// TestPushSignKeySignsDigest: on a registry with the Referrers API and on one
// without, the pushed digest verifies with the key, and a second push signs
// nothing more.
func TestPushSignKeySignsDigest(t *testing.T) {
	for name, h := range map[string]http.Handler{"referrers": testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), "tags": plainRegistry()} {
		t.Run(name, func(t *testing.T) {
			e := newPushEnv(t, h)
			ctx := context.Background()
			opts := e.signed(PushOptions{Ref: "oci://" + e.host + "/acme/svc", Path: writeTestBundle(t), Index: IndexNone}, filepath.Join(t.TempDir(), "k.key"))
			res, err := e.svc.Push(ctx, opts)
			if err != nil || res.Unchanged {
				t.Fatalf("%+v %v", res, err)
			}
			if err := e.verify(t, "acme/svc", res.Digest); err != nil {
				t.Fatalf("the pushed digest does not verify: %v", err)
			}
			again, err := e.svc.Push(ctx, opts)
			if err != nil || !again.Unchanged {
				t.Fatalf("%+v %v", again, err)
			}
			refs, _ := e.reg.Referrers(ctx, e.host+"/acme/svc", res.Digest, delivery.BundleMediaType)
			if len(refs) != 1 {
				t.Errorf("a signed digest is signed once, got %d signatures", len(refs))
			}
		})
	}
}

// TestPushSignKeySignsUnchanged: a bundle pushed unsigned is signed by a push
// of the same directory with a key, which writes nothing else.
func TestPushSignKeySignsUnchanged(t *testing.T) {
	e := newPushEnv(t, plainRegistry())
	ctx := context.Background()
	opts := PushOptions{Ref: "oci://" + e.host + "/acme/svc", Path: writeTestBundle(t), Index: IndexNone}
	res, err := e.svc.Push(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if e.verify(t, "acme/svc", res.Digest) == nil {
		t.Fatal("a push with no key signed")
	}
	again, err := e.svc.Push(ctx, e.signed(opts, filepath.Join(t.TempDir(), "k.key")))
	if err != nil || !again.Unchanged || again.Digest != res.Digest {
		t.Fatalf("%+v %v", again, err)
	}
	if err := e.verify(t, "acme/svc", res.Digest); err != nil {
		t.Fatalf("the unchanged digest was not signed: %v", err)
	}
}

// TestPushSignKeyInsideBundleRefused: a key inside the pushed directory is
// refused before anything is pushed, unless .pactoignore keeps it out.
func TestPushSignKeyInsideBundleRefused(t *testing.T) {
	e := newPushEnv(t, plainRegistry())
	ctx := context.Background()
	dir := writeTestBundle(t)
	key := filepath.Join(dir, "my.key")
	if err := os.WriteFile(key, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := e.signed(PushOptions{Ref: "oci://" + e.host + "/acme/svc", Path: dir, Index: IndexNone}, key)
	if _, err := e.svc.Push(ctx, opts); err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("a key inside the bundle: %v", err)
	}
	if tags, _ := e.reg.Tags(ctx, e.host+"/acme/svc"); len(tags) != 0 {
		t.Fatalf("a refused push wrote %v", tags)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pactoignore"), []byte("my.key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Push(ctx, opts); err != nil {
		t.Fatalf("a key .pactoignore lists: %v", err)
	}
}

// TestPushSetsCreatedAnnotation: a contribution is tagged <name>_<version>
// and its manifest says when it was pushed; the same directory again is
// unchanged, and a contribution pushed by digest is refused.
func TestPushSetsCreatedAnnotation(t *testing.T) {
	e := newPushEnv(t, plainRegistry())
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	e.svc.sys.now = func() time.Time { return now }
	repo := e.host + "/acme/pacto-inferred/my-idp/services"
	opts := PushOptions{Ref: "oci://" + repo, Path: writeContribution(t)}
	res, err := e.svc.Push(ctx, opts)
	if err != nil || res.Ref != repo+":checkout_1.0.0" {
		t.Fatalf("%+v %v", res, err)
	}
	h, err := e.reg.Head(ctx, res.Ref)
	if err != nil || h == nil || h.Digest != res.Digest || h.Annotations[publish.AnnotationCreated] != "2026-11-22T10:00:00Z" {
		t.Fatalf("head = %+v, %v", h, err)
	}
	now = now.Add(time.Hour)
	again, err := e.svc.Push(ctx, opts)
	if err != nil || !again.Unchanged || again.Digest != res.Digest {
		t.Fatalf("the same contribution again: %+v %v", again, err)
	}
	opts.Ref, opts.Force = "oci://"+repo+"@"+res.Digest, true
	if _, err := e.svc.Push(ctx, opts); err == nil || !strings.Contains(err.Error(), "pushed by tag, as "+repo+":checkout_1.0.0") {
		t.Fatalf("a contribution by digest: %v", err)
	}
}

// TestPushContributionRefusesForeignTag: a contribution tagged otherwise than
// <name>_<version>, or whose version no OCI tag can hold, is refused before
// anything is pushed.
func TestPushContributionRefusesForeignTag(t *testing.T) {
	e := newPushEnv(t, plainRegistry())
	ctx := context.Background()
	repo := e.host + "/acme/pacto-inferred/my-idp/services"
	if _, err := e.svc.Push(ctx, PushOptions{Ref: "oci://" + repo + ":ledger_9.0.0", Path: writeContribution(t)}); err == nil ||
		!strings.Contains(err.Error(), "pushed by tag, as "+repo+":checkout_1.0.0") {
		t.Fatalf("another tag: %v", err)
	}
	dir := writeContribution(t)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.5\"\nservice:\n  name: checkout\n  version: 1.0.0+build.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Push(ctx, PushOptions{Ref: "oci://" + repo, Path: dir}); err == nil || !strings.Contains(err.Error(), "version 1.0.0+build.1 holds build metadata") {
		t.Fatalf("a version with '+': %v", err)
	}
	if tags, _ := e.reg.Tags(ctx, repo); len(tags) != 0 {
		t.Errorf("tags = %v", tags)
	}
}

// TestPushToContributionWritesNoIndex: a contribution never reaches the
// organization index, and the result says so.
func TestPushToContributionWritesNoIndex(t *testing.T) {
	e := newPushEnv(t, plainRegistry())
	e.svc.sys.refusalRepo = func(r string) (*remote.Repository, error) {
		t.Errorf("a contribution opened the index %s", r)
		return nil, errors.New("offline")
	}
	ctx := context.Background()
	res, err := e.svc.Push(ctx, PushOptions{Ref: "oci://" + e.host + "/acme/pacto-inferred/my-idp/services", Path: writeContribution(t)})
	if err != nil || res.Index != "contributions are never indexed" {
		t.Fatalf("%+v %v", res, err)
	}
	if tags, _ := e.reg.Tags(ctx, e.host+"/acme/pacto-index"); len(tags) != 0 {
		t.Fatalf("index tags %v", tags)
	}
}

// TestPushContributionRefusesIndexFlag: --index names no index for a
// contribution; none is what it gets anyway.
func TestPushContributionRefusesIndexFlag(t *testing.T) {
	e := newPushEnv(t, plainRegistry())
	ctx := context.Background()
	opts := PushOptions{Ref: "oci://" + e.host + "/acme/pacto-inferred/my-idp/services", Path: writeContribution(t), Index: e.host + "/acme/pacto-index"}
	if _, err := e.svc.Push(ctx, opts); err == nil || !strings.Contains(err.Error(), "contributions are never indexed") {
		t.Fatalf("--index on a contribution: %v", err)
	}
	opts.Index = IndexNone
	if _, err := e.svc.Push(ctx, opts); err != nil {
		t.Fatal(err)
	}
}

type failingSigner struct{}

func (failingSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("no signer")
}

// TestPushSignFailureExitsWithDigest: a signature that fails after the push
// names the pushed digest so the producer runs the push again.
func TestPushSignFailureExitsWithDigest(t *testing.T) {
	e := newPushEnv(t, plainRegistry())
	ctx := context.Background()
	opts := e.signed(PushOptions{Ref: "oci://" + e.host + "/acme/svc", Path: writeTestBundle(t), Index: IndexNone}, filepath.Join(t.TempDir(), "k.key"))
	opts.Signer = failingSigner{}
	_, err := e.svc.Push(ctx, opts)
	h, _ := e.reg.Head(ctx, e.host+"/acme/svc:1.0.0")
	if err == nil || h == nil || !strings.Contains(err.Error(), "pushed "+e.host+"/acme/svc@"+h.Digest+", but signing it failed") ||
		!strings.Contains(err.Error(), "run the push again") {
		t.Fatalf("%v (head %+v)", err, h)
	}
}

// writeFailRegistry fails the write or the tag publish asks for.
type writeFailRegistry struct {
	publish.Registry
	write, tag bool
}

func (r writeFailRegistry) Write(ctx context.Context, repo string, a publish.Artifact) (string, error) {
	if r.write {
		return "", errors.New("write refused")
	}
	return r.Registry.Write(ctx, repo, a)
}

func (r writeFailRegistry) Tag(ctx context.Context, repo, digest, tag string) error {
	if r.tag {
		return errors.New("tag refused")
	}
	return r.Registry.Tag(ctx, repo, digest, tag)
}

// TestPushContributionWriteFails: a contribution the registry refuses to
// store or tag fails the push.
func TestPushContributionWriteFails(t *testing.T) {
	for _, fail := range []writeFailRegistry{{write: true}, {tag: true}} {
		e := newPushEnv(t, plainRegistry())
		fail.Registry = e.reg
		e.svc.sys.publishRegistry = func() publish.Registry { return fail }
		_, err := e.svc.Push(context.Background(), PushOptions{Ref: "oci://" + e.host + "/acme/pacto-inferred/my-idp/services", Path: writeContribution(t)})
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Fatalf("%+v: %v", fail, err)
		}
	}
}

// TestPushContributionKeepsItsRecord: a contribution is pushed with the
// record its producer wrote, never replaced by what Pacto infers from the
// folder; a folder Pacto cannot read or an override it cannot apply is
// refused before anything is pushed.
func TestPushContributionKeepsItsRecord(t *testing.T) {
	e := newPushEnv(t, plainRegistry())
	dir := writeContribution(t)
	export := []byte(`{"checkout":["ledger"]}`)
	sum := sha256.Sum256(export)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{{Path: "export.json", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[ledger]", Value: "ledger", Source: "export.json:1", Confidence: contract.ConfidenceLikely}}}
	for name, data := range map[string][]byte{"export.json": export, contract.InferredRecordFile: rec.Marshal()} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ref := "oci://" + e.host + "/acme/pacto-inferred/my-idp/services"
	res, err := e.svc.Push(context.Background(), PushOptions{Ref: ref, Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.BundleStore.Pull(context.Background(), res.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fs.ReadFile(b.FS, contract.InferredRecordFile); err != nil || string(got) != string(rec.Marshal()) {
		t.Errorf("pushed record = %q, %v", got, err)
	}

	if _, err := e.svc.Push(context.Background(), PushOptions{Ref: ref, Path: filepath.Join(dir, "missing")}); err == nil {
		t.Error("a missing folder was pushed")
	}
	bad := override.Overrides{SetValues: []string{"service.version=2.0.0"}, ValueFiles: []string{filepath.Join(dir, "missing.yaml")}}
	if _, err := e.svc.Push(context.Background(), PushOptions{Ref: ref, Path: dir, Overrides: bad}); err == nil {
		t.Error("an override that cannot be applied was pushed")
	}
}
