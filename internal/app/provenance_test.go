package app

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	provIdentity = "ci@acme.example"
	provIssuer   = "https://token.actions.githubusercontent.com"
)

// provRegistry is an in-process registry that counts requests and fails the
// ones fail selects.
type provRegistry struct {
	host     string
	client   *publish.Client
	srv      *httptest.Server
	requests atomic.Int64
	fail     atomic.Pointer[func(*http.Request) bool]
}

// provenanceTestRegistry returns a referrers-capable registry and a
// *publish.Client over it (publishRegistry returns the narrower interface).
func provenanceTestRegistry(t *testing.T) *provRegistry {
	t.Helper()
	p := &provRegistry{}
	inner := registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0)))
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		if f := p.fail.Load(); f != nil && (*f)(r) {
			// A status the client does not retry, so a failure costs no backoff.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(p.srv.Close)
	p.host = strings.TrimPrefix(p.srv.URL, "http://")
	p.client = publish.NewClient(anonymous, p.host)
	return p
}

func (p *provRegistry) failWhen(f func(*http.Request) bool) { p.fail.Store(&f) }

// keylessSigstore swaps public-good Sigstore for a test one and returns a
// keyless signer for provIdentity.
func keylessSigstore(t *testing.T) (*sigtest.Sigstore, signing.Signer) {
	t.Helper()
	ss := sigtest.New(t)
	oldRoot, oldR := publicGoodRoot, rekorURL
	t.Cleanup(func() { publicGoodRoot, rekorURL = oldRoot, oldR })
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { return ss.Root, nil }
	rekorURL = ""
	t.Setenv("SIGSTORE_ID_TOKEN", sigtest.Token(provIdentity, provIssuer))
	s, err := signing.Keyless(ss.Fulcio, ss.TSA, "", signing.IDToken(""), ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	return ss, s
}

func signedBy(sign *contract.PublishSign) *contract.Contract {
	c := &contract.Contract{Service: contract.Service{Name: "app", Version: "1.0.0"}}
	if sign != nil {
		c.Publish = &contract.Publish{Sign: sign}
	}
	return c
}

func keylessContract(identity string) *contract.Contract {
	return signedBy(&contract.PublishSign{Keyless: true, Identity: identity, Issuer: provIssuer})
}

// pushSigned pushes an image, signs it keyless and returns its oci:// ref.
func pushSigned(t *testing.T, reg *provRegistry, s signing.Signer) string {
	t.Helper()
	ref := pushImage(t, reg.client, reg.host+"/app")
	repo, digest, _ := strings.Cut(ref, "@")
	if err := publish.SignDigest(t.Context(), reg.client, s, repo, digest); err != nil {
		t.Fatal(err)
	}
	return contract.OCIScheme + ref
}

// attach writes a referrer of ref with the given artifact type and annotations.
func attach(t *testing.T, reg *provRegistry, ref, artifactType string, ann map[string]string) {
	t.Helper()
	repo, digest, _ := strings.Cut(contract.TrimOCIScheme(ref), "@")
	head, err := reg.client.Head(t.Context(), repo+"@"+digest)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := v1.NewHash(digest)
	img, _ := mutate.Append(mutate.ConfigMediaType(mutate.MediaType(empty.Image, types.OCIManifestSchema1), types.MediaType(artifactType)),
		mutate.Addendum{Layer: static.NewLayer([]byte("{}"), types.MediaType(artifactType))})
	img = mutate.Annotations(img, ann).(v1.Image)
	img = mutate.Subject(img, v1.Descriptor{MediaType: types.MediaType(head.MediaType), Size: head.Size, Digest: h}).(v1.Image)
	if _, err := reg.client.Write(t.Context(), repo, img); err != nil {
		t.Fatal(err)
	}
}

func checkerFor(reg *provRegistry) *ProvenanceChecker { return newProvenanceChecker(reg.client) }

func wantStatus(t *testing.T, p *fleet.BundleProvenance, status, reason string) {
	t.Helper()
	if p == nil || p.Signature.Status != status || !strings.Contains(p.Signature.Reason, reason) {
		t.Fatalf("got %+v, want %s with %q", p, status, reason)
	}
	if p.Source != "oci" || p.CheckedAt.IsZero() {
		t.Fatalf("source or checkedAt missing: %+v", p)
	}
}

func TestProvenanceUnsigned(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := contract.OCIScheme + pushImage(t, reg.client, reg.host+"/app")
	p := checkerFor(reg).Check(t.Context(), "oci", ref, keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureUnsigned, "No Sigstore signature referrer")
	if p.Referrers.Total != 0 || p.ReferrersUnavailable != "" {
		t.Fatalf("referrers %+v", p)
	}
}

func TestProvenanceVerifiedKeyless(t *testing.T) {
	_, s := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	p := checkerFor(reg).Check(t.Context(), "oci", ref, keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureVerified, "own publish.sign")
	sig := p.Signature
	if sig.Signer != provIdentity || sig.Issuer != provIssuer || sig.Bundles != 1 {
		t.Fatalf("signature %+v", sig)
	}
	if p.Referrers.Total != 1 || p.Referrers.Items[0].ArtifactType != "application/vnd.dev.sigstore.bundle.v0.3+json" {
		t.Fatalf("referrers %+v", p.Referrers)
	}
}

func TestProvenanceOtherIdentity(t *testing.T) {
	_, s := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	p := checkerFor(reg).Check(t.Context(), "oci", ref, keylessContract("release@acme.example"))
	wantStatus(t, p, fleet.SignatureUnverified, "does not verify against release@acme.example")
	if p.Signature.Signer != "release@acme.example" || p.Signature.Issuer != provIssuer {
		t.Fatalf("signer %+v", p.Signature)
	}
}

func TestProvenanceAttestationOnly(t *testing.T) {
	keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := contract.OCIScheme + pushImage(t, reg.client, reg.host+"/app")
	attach(t, reg, ref, delivery.BundleMediaType, map[string]string{deliveryrecord.AnnPredicateType: "https://slsa.dev/provenance/v1"})
	p := checkerFor(reg).Check(t.Context(), "oci", ref, keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureUnsigned, "none of them is a signature")
	if p.Signature.Bundles != 1 {
		t.Fatalf("bundles %d", p.Signature.Bundles)
	}
}

func TestProvenanceNoDeclaredSigner(t *testing.T) {
	_, s := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	for _, c := range []*contract.Contract{nil, signedBy(nil)} {
		p := checkerFor(reg).Check(t.Context(), "oci", ref, c)
		wantStatus(t, p, fleet.SignatureUnknown, "1 Sigstore bundles are attached, but the contract names no publish.sign signer")
	}
}

func TestProvenanceKeySigner(t *testing.T) {
	_, s := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	for _, sign := range []*contract.PublishSign{{Key: "cosign.key"}, {KMS: "awskms:///k"}} {
		p := checkerFor(reg).Check(t.Context(), "oci", ref, signedBy(sign))
		wantStatus(t, p, fleet.SignatureUnknown, "key file or a KMS key")
	}
}

func TestProvenanceKeylessNoIdentity(t *testing.T) {
	_, s := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	p := checkerFor(reg).Check(t.Context(), "oci", ref, signedBy(&contract.PublishSign{Keyless: true, Identity: provIdentity}))
	wantStatus(t, p, fleet.SignatureUnknown, "names no identity and issuer")
}

func TestProvenanceRootUnavailable(t *testing.T) {
	_, s := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	pc := checkerFor(reg)
	clock := time.Now()
	pc.now = func() time.Time { return clock }
	calls := 0
	publicGoodRoot = func(string) (root.TrustedMaterial, error) { calls++; return nil, errors.New("tuf down") }
	p := pc.Check(t.Context(), "oci", ref, keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureUnknown, "trusted root could not be fetched, so the signature could not be checked: tuf down")
	if _, err := pc.trustedRoot(); err == nil || calls != 1 {
		t.Fatalf("a failed root within a minute was fetched again: %v %d", err, calls)
	}
	// The failed root is remembered for a minute, then fetched again.
	clock = clock.Add(provenanceFailTTL + time.Second)
	pc.Check(t.Context(), "oci", ref, keylessContract(provIdentity))
	if calls != 2 {
		t.Fatalf("root fetched %d times", calls)
	}
}

func TestProvenanceRegistryDown(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := contract.OCIScheme + pushImage(t, reg.client, reg.host+"/app")
	reg.failWhen(func(*http.Request) bool { return true })
	p := checkerFor(reg).Check(t.Context(), "oci", ref, keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureUnknown, "The registry could not list what is attached to this digest: ")
	if p.ReferrersUnavailable != p.Signature.Reason || p.Referrers.Total != 0 || len(p.ReferrersUnavailable) > provenanceMaxReason+len("…") {
		t.Fatalf("referrers %+v", p)
	}
}

func TestProvenanceLayerReadFails(t *testing.T) {
	_, s := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	reg.failWhen(func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/blobs/") })
	p := checkerFor(reg).Check(t.Context(), "oci", ref, keylessContract(provIdentity))
	wantStatus(t, p, fleet.SignatureUnknown, "The signature could not be checked: ")
}

func TestProvenanceNoDigest(t *testing.T) {
	reg := provenanceTestRegistry(t)
	for _, ref := range []string{"oci://" + reg.host + "/app:1.0.0", reg.host + "/app@nope"} {
		p := checkerFor(reg).Check(t.Context(), "oci", ref, nil)
		wantStatus(t, p, fleet.SignatureUnknown, "no immutable digest")
		if p.ReferrersUnavailable == "" {
			t.Fatal("referrersUnavailable not set")
		}
	}
	if reg.requests.Load() != 0 {
		t.Fatal("asked the registry about a ref without a digest")
	}
}

func TestProvenanceBudgetSpent(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := contract.OCIScheme + pushImage(t, reg.client, reg.host+"/app")
	before := reg.requests.Load()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	pc := checkerFor(reg)
	p := pc.Check(ctx, "oci", ref, nil)
	wantStatus(t, p, fleet.SignatureUnknown, "ran out of time")
	if p.ReferrersUnavailable != p.Signature.Reason || reg.requests.Load() != before {
		t.Fatalf("a spent budget asked the registry: %+v", p)
	}
	// Not remembered: a live context asks again.
	wantStatus(t, pc.Check(t.Context(), "oci", ref, nil), fleet.SignatureUnsigned, "No Sigstore")
	if reg.requests.Load() == before {
		t.Fatal("the next check did not ask the registry")
	}
}

func TestProvenanceMemo(t *testing.T) {
	_, s := keylessSigstore(t)
	reg := provenanceTestRegistry(t)
	ref := pushSigned(t, reg, s)
	pc := checkerFor(reg)
	clock := time.Now()
	pc.now = func() time.Time { return clock }
	c := keylessContract(provIdentity)
	first := pc.Check(t.Context(), "oci", ref, c)
	wantStatus(t, first, fleet.SignatureVerified, "Signed by")
	n := reg.requests.Load()
	clock = clock.Add(provenanceTTL - time.Second)
	again := pc.Check(t.Context(), "oci", ref, c)
	if reg.requests.Load() != n || !again.CheckedAt.Equal(first.CheckedAt) {
		t.Fatal("a remembered check asked the registry")
	}
	again.Referrers.Items[0].Digest = "mutated"
	if pc.Check(t.Context(), "oci", ref, c).Referrers.Items[0].Digest == "mutated" {
		t.Fatal("the memo aliases what it returned")
	}
	clock = clock.Add(2 * time.Second)
	pc.Check(t.Context(), "oci", ref, c)
	if reg.requests.Load() == n {
		t.Fatal("an expired check was not repeated")
	}
	// A registry that cannot answer is remembered for a minute only.
	reg.failWhen(func(*http.Request) bool { return true })
	clock = clock.Add(provenanceTTL + time.Second)
	wantStatus(t, pc.Check(t.Context(), "oci", ref, c), fleet.SignatureUnknown, "could not list")
	n = reg.requests.Load()
	clock = clock.Add(provenanceFailTTL - time.Second)
	pc.Check(t.Context(), "oci", ref, c)
	if reg.requests.Load() != n {
		t.Fatal("a remembered failure asked the registry")
	}
	clock = clock.Add(2 * time.Second)
	pc.Check(t.Context(), "oci", ref, c)
	if reg.requests.Load() == n {
		t.Fatal("an expired failure was not repeated")
	}
}

func TestProvenanceCreatedAnnotation(t *testing.T) {
	reg := provenanceTestRegistry(t)
	ref := contract.OCIScheme + pushImage(t, reg.client, reg.host+"/app")
	const evidence = "application/vnd.pacto.evidence.record.v1+json"
	attach(t, reg, ref, evidence, map[string]string{"org.opencontainers.image.created": "2026-11-01T10:00:00Z"})
	attach(t, reg, ref, "application/spdx+json", map[string]string{"k": "v"})
	p := checkerFor(reg).Check(t.Context(), "oci", ref, nil)
	if p.Referrers.Total != 2 {
		t.Fatalf("referrers %+v", p.Referrers)
	}
	for _, r := range p.Referrers.Items {
		switch r.ArtifactType {
		case evidence:
			if r.Created == nil || !r.Created.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) {
				t.Errorf("created %v", r.Created)
			}
		default:
			if r.Created != nil {
				t.Errorf("created without the annotation: %v", r.Created)
			}
		}
	}
}

// TestProvenanceArtifactTypeFallback: a referrer without an artifact type is
// listed by its media type.
func TestProvenanceArtifactTypeFallback(t *testing.T) {
	pc := newProvenanceChecker(descRegistry{{MediaType: types.OCIManifestSchema1, Digest: v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("b", 64)}}})
	p := pc.Check(t.Context(), "oci", "oci://r.example/app@sha256:"+strings.Repeat("a", 64), nil)
	if p.Referrers.Items[0].ArtifactType != string(types.OCIManifestSchema1) {
		t.Fatalf("artifact type %+v", p.Referrers.Items[0])
	}
}

type descRegistry []v1.Descriptor

func (descRegistry) Head(context.Context, string) (*publish.Remote, error)           { return nil, nil }
func (descRegistry) Tags(context.Context, string) ([]string, error)                  { return nil, nil }
func (descRegistry) Pull(context.Context, string) (*contract.Bundle, error)          { return nil, nil }
func (descRegistry) Write(context.Context, string, publish.Artifact) (string, error) { return "", nil }
func (descRegistry) Tag(context.Context, string, string, string) error               { return nil }
func (descRegistry) Referrers(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
func (descRegistry) Layer(context.Context, string, string) ([]byte, map[string]string, error) {
	return nil, nil, nil
}
func (d descRegistry) ReferrerDescriptors(context.Context, string, string) ([]v1.Descriptor, error) {
	return d, nil
}

// TestNewProvenanceChecker: the CLI's checker is ready to use.
func TestNewProvenanceChecker(t *testing.T) {
	if p := NewProvenanceChecker(); p.reg == nil || p.now == nil || p.memo == nil {
		t.Fatalf("checker %+v", p)
	}
}

func TestCapReason(t *testing.T) {
	if got := capReason(strings.Repeat("é", 300)); len([]rune(got)) != provenanceMaxReason+1 {
		t.Fatalf("capped to %d runes", len([]rune(got)))
	}
}

// TestFleetWiresProvenance: the OCI source's revisions carry the checker's
// result; without a checker they carry none.
func TestFleetWiresProvenance(t *testing.T) {
	store := fakeFleetStore{bundles: map[string]*contract.Bundle{"ghcr.io/x/a:1.0.0": bundleForFleet("a")}}
	svc := NewService(store, nil)
	for _, pc := range []*ProvenanceChecker{nil, newProvenanceChecker(descRegistry{})} {
		snap, err := svc.Fleet(t.Context(), FleetOptions{OCIRefs: []string{"ghcr.io/x/a:1.0.0"}, Provenance: pc})
		if err != nil || len(snap.Revisions) != 1 {
			t.Fatalf("fleet: %v %+v", err, snap)
		}
		for _, r := range snap.Revisions {
			if (r.Provenance != nil) != (pc != nil) {
				t.Fatalf("checker %v gave provenance %+v", pc != nil, r.Provenance)
			}
		}
	}
}

// TestFleetProvenanceCachedRegistryDown: a digest-pinned ref already pulled
// still yields its revision with the registry down, and its provenance says
// the registry could not list what is attached.
func TestFleetProvenanceCachedRegistryDown(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	reg := provenanceTestRegistry(t)
	y := []byte("pactoVersion: \"2.1\"\nservice: {name: app, version: 1.0.0}\n")
	c, err := contract.Parse(strings.NewReader(string(y)))
	if err != nil {
		t.Fatal(err)
	}
	client := oci.NewClient(anonymous, oci.WithInsecureRegistries(reg.host))
	digest, err := client.Push(t.Context(), reg.host+"/app:1.0.0", &contract.Bundle{Contract: c, RawYAML: y, FS: fstest.MapFS{"pacto.yaml": {Data: y}}})
	if err != nil {
		t.Fatal(err)
	}
	ref := "oci://" + reg.host + "/app@" + digest
	pc := checkerFor(reg)
	build := func() *fleet.ContractRevision {
		t.Helper()
		svc := &Service{BundleStore: oci.NewCachedStore(client)}
		snap, err := svc.Fleet(t.Context(), FleetOptions{OCIRefs: []string{ref}, Provenance: pc})
		if err != nil || len(snap.Revisions) != 1 {
			t.Fatalf("fleet: %v %+v", err, snap)
		}
		for _, r := range snap.Revisions {
			return r
		}
		return nil
	}
	if r := build(); r.Provenance == nil || r.Provenance.Signature.Status != fleet.SignatureUnsigned {
		t.Fatalf("online: %+v", r.Provenance)
	}
	reg.srv.Close()
	pc.memo = map[string]provenanceEntry{} // forget the online check
	r := build()
	if r.Provenance == nil || r.Provenance.Signature.Status != fleet.SignatureUnknown || !strings.Contains(r.Provenance.ReferrersUnavailable, "could not list") {
		t.Fatalf("offline: %+v", r.Provenance)
	}
}
