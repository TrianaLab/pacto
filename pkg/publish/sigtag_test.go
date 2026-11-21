package publish

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// tagRegistry is a registry that serves no Referrers API, as GHCR and CNCF
// distribution do, unless referrers is set before its first probe. It
// records every manifest path it is asked for.
type tagRegistry struct {
	host      string
	c         *Client
	referrers atomic.Bool
	paths     atomic.Pointer[[]string]
	// deny answers this status to a manifest request whose path ends in it.
	deny atomic.Pointer[denied]
}

type denied struct {
	suffix string
	status int
}

func newTagRegistry(t *testing.T) *tagRegistry {
	t.Helper()
	tr := &tagRegistry{}
	tr.paths.Store(&[]string{})
	plain := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for { // requests run concurrently: copy, then swap only over what was read
			old := tr.paths.Load()
			p := append(slices.Clone(*old), r.URL.Path)
			if tr.paths.CompareAndSwap(old, &p) {
				break
			}
		}
		if d := tr.deny.Load(); d != nil && strings.HasSuffix(r.URL.Path, d.suffix) {
			w.WriteHeader(d.status)
			return
		}
		if tr.referrers.Load() && strings.Contains(r.URL.Path, "/referrers/") {
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			_, _ = w.Write([]byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`))
			return
		}
		plain.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	tr.host = strings.TrimPrefix(srv.URL, "http://")
	tr.c = NewClient(anonymous, tr.host)
	return tr
}

// readIndex reports whether a request read a sha256-<hex> referrers-tag index.
func (tr *tagRegistry) readIndex() bool {
	return slices.ContainsFunc(*tr.paths.Load(), func(p string) bool { return strings.Contains(p, "/manifests/sha256-") })
}

func (tr *tagRegistry) sigTags(t *testing.T, repo, d string) []string {
	t.Helper()
	r, _ := tr.c.open(repo)
	tags, err := ocireferrer.Tags(t.Context(), r, SigTagPrefix(d))
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

// TestSignDigestTagModeClaimsSigTag: on a registry without the Referrers API
// the signature is named by pacto-sig-<subject hex>-<manifest hex12>, and
// verification reads it there.
func TestSignDigestTagModeClaimsSigTag(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	if m, err := tr.c.Mode(ctx, repo, d); err != nil || m != capabilities.ReferrersWriteOnceTags {
		t.Fatalf("mode %q %v", m, err)
	}
	if err := SignDigest(ctx, tr.c, echoSigner{}, repo, d); err != nil {
		t.Fatal(err)
	}
	tr.paths.Store(&[]string{}) // the push updates the shared index; the readers below never read it
	tags := tr.sigTags(t, repo, d)
	refs, err := tr.c.Referrers(ctx, repo, d, delivery.BundleMediaType)
	if err != nil || len(tags) != 1 || len(refs) != 1 || tags[0] != SigTag(d, refs[0]) || len(tags[0]) != len("pacto-sig-")+64+1+12 {
		t.Fatalf("tags %v referrers %v %v", tags, refs, err)
	}
	if err := VerifyDigest(ctx, tr.c, names, repo, d); err != nil {
		t.Fatal(err)
	}
	if tr.readIndex() {
		t.Fatal("a reader read the sha256- index")
	}
}

// TestSignDigestTagModeRetriesTakenSlot: signing the same statement again
// claims the same tag (idempotent); a tag someone moved to another manifest
// is never moved back, and the moved tag is unreadable, never a signature.
func TestSignDigestTagModeRetriesTakenSlot(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	if err := SignDigest(ctx, tr.c, echoSigner{}, repo, d); err != nil {
		t.Fatal(err)
	}
	if err := SignDigest(ctx, tr.c, echoSigner{}, repo, d); err != nil {
		t.Fatalf("a retry of the same signature: %v", err)
	}
	tag := tr.sigTags(t, repo, d)[0]
	_, other := signedImage(t, tr.c, tr.host)
	if err := tr.c.Tag(ctx, repo, other, tag); err != nil {
		t.Fatal(err)
	}
	if err := SignDigest(ctx, tr.c, echoSigner{}, repo, d); !errors.Is(err, ocireferrer.ErrTagTaken) {
		t.Fatalf("a taken slot: %v", err)
	}
	if err := VerifyDigest(ctx, tr.c, names, repo, d); err == nil || !strings.Contains(err.Error(), "a moved tag") {
		t.Fatalf("a moved tag verified: %v", err)
	}
}

// TestSigTagOfAnotherSubjectIsUnreadable: a fresh tag under A's prefix that
// names B's signature by its own digest still names B as its subject, so
// it is never read as A's.
func TestSigTagOfAnotherSubjectIsUnreadable(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, a := signedImage(t, tr.c, tr.host)
	_, b := signedImage(t, tr.c, tr.host)
	if err := SignDigest(ctx, tr.c, echoSigner{}, repo, b); err != nil {
		t.Fatal(err)
	}
	refs, err := tr.c.Referrers(ctx, repo, b, delivery.BundleMediaType)
	if err != nil || len(refs) != 1 {
		t.Fatalf("%v %v", refs, err)
	}
	if err := tr.c.Tag(ctx, repo, refs[0], SigTag(a, refs[0])); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.c.Referrers(ctx, repo, a, delivery.BundleMediaType); err == nil || !strings.Contains(err.Error(), "a moved tag") {
		t.Fatalf("B's signature read as A's: %v", err)
	}
}

// TestVerifyDigestReadsSigTags: in tag mode only a signature the verifier
// accepts counts, and an SBOM tag beside it is never read as one.
func TestVerifyDigestReadsSigTags(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	if err := SignDigest(ctx, tr.c, echoSigner{}, repo, d); err != nil {
		t.Fatal(err)
	}
	img, art := referrerArtifact(SPDXType, []byte("{}"), *subjectOf(t, tr.c, repo, d), nil)
	if err := writeReferrer(ctx, tr.c, capabilities.ReferrersWriteOnceTags, repo, d, img, art); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDigest(ctx, tr.c, func([]byte, []byte) (string, error) { return "", errors.New("wrong key") }, repo, d); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("a bundle the verifier refuses: %v", err)
	}
	if err := VerifyDigest(ctx, tr.c, names, repo, d); err != nil {
		t.Fatal(err)
	}
	descs, err := tr.c.ReferrerDescriptors(ctx, repo, d)
	if err != nil || len(descs) != 2 {
		t.Fatalf("descriptors %v %v", descs, err)
	}
	types := []string{descs[0].ArtifactType, descs[1].ArtifactType}
	slices.Sort(types)
	if types[0] != SPDXType || types[1] != delivery.BundleMediaType {
		t.Fatalf("artifact types %v", types)
	}
}

// TestVerifyDigestIgnoresReferrersTagIndex: a valid signature listed only in
// the shared sha256-<hex> index of a Referrers-less registry is no signature.
func TestVerifyDigestIgnoresReferrersTagIndex(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	writeBundle(t, tr.c, repo, d, SignPredicate, statementOver(d, SignPredicate))
	idx, err := tr.c.Head(ctx, repo+":"+strings.Replace(d, ":", "-", 1))
	if err != nil || idx == nil {
		t.Fatalf("the push did not write the shared index: %v", err)
	}
	tr.paths.Store(&[]string{})
	if err := VerifyDigest(ctx, tr.c, names, repo, d); !errors.Is(err, ErrNoSignature) {
		t.Fatalf("a signature only in the index: %v", err)
	}
	if tr.readIndex() {
		t.Fatal("the reader read the sha256- index")
	}
}

// TestReferrersRefusesTagIndexFallback: on a registry probed native, a
// Referrers API that then answers 404 is an error, never the sha256- index.
func TestReferrersRefusesTagIndexFallback(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	tr.referrers.Store(true)
	if m, err := tr.c.Mode(ctx, repo, d); err != nil || m != capabilities.ReferrersNative {
		t.Fatalf("mode %q %v", m, err)
	}
	tr.referrers.Store(false)
	writeBundle(t, tr.c, repo, d, SignPredicate, statementOver(d, SignPredicate))
	tr.paths.Store(&[]string{})
	if _, err := tr.c.Referrers(ctx, repo, d, delivery.BundleMediaType); err == nil {
		t.Fatal("referrers read the tag index")
	}
	if _, err := tr.c.ReferrerDescriptors(ctx, repo, d); err == nil {
		t.Fatal("referrer descriptors read the tag index")
	}
	if tr.readIndex() {
		t.Fatal("a reader read the sha256- index")
	}
}

// TestSBOMAttachTagMode: the SBOM gets its pacto-sig tag and the next
// publish finds it there.
func TestSBOMAttachTagMode(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	tools := newTools()
	sign := contract.PublishSign{Key: "k"}
	path := "sbom/api.spdx.json"
	got, err := attachSBOM(ctx, tr.c, tools, sign, path, repo, d, "1.0.0", "abc", &Result{})
	if err != nil {
		t.Fatal(err)
	}
	if tags := tr.sigTags(t, repo, d); len(tags) != 1 || tags[0] != SigTag(d, got) {
		t.Fatalf("tags %v for %s", tags, got)
	}
	tools.signed[repo+"@"+got] = true
	again, err := attachSBOM(ctx, tr.c, tools, sign, path, repo, d, "1.0.0", "abc", &Result{})
	if err != nil || again != got {
		t.Fatalf("the signed SBOM was not found on its tag: %s %v", again, err)
	}
}

// failingMode fails the probe, or the write-once tag write.
type failingMode struct {
	*Client
	fail string
}

func (f failingMode) Mode(ctx context.Context, repo, digest string) (capabilities.ReferrersMode, error) {
	if f.fail == "mode" {
		return "", errTest
	}
	return capabilities.ReferrersWriteOnceTags, nil
}

func (f failingMode) PutSigTag(context.Context, string, string, ocireferrer.Artifact) (string, error) {
	return "", errTest
}

// TestTagModeFailures: a probe that fails stops signing and SBOM attach
// before anything is written, and a tag that cannot be written fails them.
func TestTagModeFailures(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	path := "sbom/api.spdx.json"
	for _, fail := range []string{"mode", "tag"} {
		reg := failingMode{Client: tr.c, fail: fail}
		if err := SignDigest(ctx, reg, echoSigner{}, repo, d); !errors.Is(err, errTest) {
			t.Fatalf("%s: sign %v", fail, err)
		}
		if _, err := attachSBOM(ctx, reg, newTools(), contract.PublishSign{Key: "k"}, path, repo, d, "1", "r", &Result{}); err == nil {
			t.Fatalf("%s: attach", fail)
		}
	}
	if _, err := attachSBOM(ctx, failingMode{Client: tr.c, fail: "mode"}, newTools(), contract.PublishSign{Key: "k"}, path, repo, d, "1", "r", &Result{}); !errors.Is(err, errTest) {
		t.Fatalf("attach after a failed probe: %v", err)
	}
}

// TestClientSigTagFailures: a bad repository, an unreachable registry, a
// tag that vanished or cannot be read, and too many tags are each an error,
// never an empty listing.
func TestClientSigTagFailures(t *testing.T) {
	ctx := t.Context()
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	if _, err := tr.c.Mode(ctx, "BAD REF", d); err == nil {
		t.Fatal("mode of a bad ref")
	}
	if _, err := tr.c.PutSigTag(ctx, "BAD REF", d, ocireferrer.Artifact{}); err == nil {
		t.Fatal("tag of a bad ref")
	}
	if _, err := tr.c.sigTagReferrers(ctx, "BAD REF", d); err == nil {
		t.Fatal("sig tags of a bad ref")
	}
	if _, err := tr.c.ReferrerDescriptors(ctx, "BAD REF", d); err == nil {
		t.Fatal("descriptors of a bad ref")
	}
	if _, err := NewClient(anonymous, "127.0.0.1:1").sigTagReferrers(ctx, "127.0.0.1:1/x", d); err == nil {
		t.Fatal("sig tags of an unreachable registry")
	}
	for i, tag := range []string{"000000000000", "000000000001"} {
		if err := tr.c.Tag(ctx, repo, d, SigTagPrefix(d)+tag); err != nil {
			t.Fatal(i, err)
		}
	}
	for status, want := range map[int]string{http.StatusNotFound: "gone", http.StatusBadRequest: "reading the write-once tag"} {
		tr.deny.Store(&denied{suffix: "-000000000000", status: status})
		if _, err := tr.c.sigTagReferrers(ctx, repo, d); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%d: %v", status, err)
		}
	}
	tr.deny.Store(nil)
	// A tag that names another manifest than the one it points at, or a
	// manifest with another subject, was moved: unreadable.
	if _, err := tr.c.sigTagReferrers(ctx, repo, d); err == nil || !strings.Contains(err.Error(), "a moved tag") {
		t.Fatalf("a moved tag: %v", err)
	}
	defer func(n int) { maxSigTags = n }(maxSigTags)
	maxSigTags = 1
	if _, err := tr.c.sigTagReferrers(ctx, repo, d); err == nil || !strings.Contains(err.Error(), "at most 1") {
		t.Fatalf("too many tags: %v", err)
	}
}

// TestScopedClientGuardsTheSigTag: on a registry without the Referrers API a
// scoped client's write-once signature tag goes through the push scope too,
// and the client reaches its insecure hosts over plain HTTP.
func TestScopedClientGuardsTheSigTag(t *testing.T) {
	tr := newTagRegistry(t)
	repo, d := signedImage(t, tr.c, tr.host)
	outside := NewScopedClient(anonymous, oci.NewPushScope("", tr.host+"/other"), nil)
	h, _ := v1.NewHash(d)
	_, art := newReferrer(delivery.BundleMediaType, delivery.BundleMediaType, []byte("{}"), delivery.BundleMediaType, []byte("sig"), nil,
		v1.Descriptor{MediaType: types.OCIManifestSchema1, Size: 1, Digest: h})
	if _, err := outside.PutSigTag(t.Context(), repo, d, art); !errors.Is(err, oci.ErrOutsidePushScope) {
		t.Fatalf("PutSigTag outside the scope = %v, want the push-scope refusal", err)
	}
	if tags := tr.sigTags(t, repo, d); len(tags) != 0 {
		t.Errorf("a signature tag was written outside the scope: %v", tags)
	}
	inside := NewScopedClient(anonymous, oci.NewPushScope("", repo), nil)
	if err := SignDigest(t.Context(), inside, echoSigner{}, repo, d); err != nil {
		t.Fatal(err)
	}
	if r, _ := NewScopedClient(anonymous, oci.NewPushScope(""), nil, "zot.test:5000").open("zot.test:5000/x"); !r.PlainHTTP {
		t.Error("an insecure host of a scoped client is reached over HTTPS")
	}
}
