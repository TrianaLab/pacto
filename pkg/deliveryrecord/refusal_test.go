package deliveryrecord

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

func refusal(t *testing.T, key ed25519.PrivateKey, service, version, at string) []byte {
	t.Helper()
	st := delivery.NewRefusalStatement("sha256:"+strings.Repeat("a", 64), delivery.PublishRefusal{Service: service, Version: version,
		Baseline: "2.4.2", Classification: "BREAKING", RequiredBump: "major", Message: "needs a major bump", At: at})
	b, _, err := delivery.SignRefusal(t.Context(), edSigner(key), st)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestPushAndListRefusals: refusals read back newest first and verified; what
// is not a refusal of this service, or not signed by the trusted key, never
// reads as one.
func TestPushAndListRefusals(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	verify := func(b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
	f, path := setup(t, testutil.ReferrersOptions{})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	ctx := t.Context()
	older, newer := refusal(t, key, "payments", "2.4.3", "2026-10-22T10:00:00Z"), refusal(t, key, "payments", "2.4.4", "2026-10-22T11:00:00Z")
	for _, b := range [][]byte{older, newer, older} {
		if err := PushRefusal(ctx, repo, b); err != nil {
			t.Fatal(err)
		}
	}
	anchor := RefusalAnchor("payments")
	if got, err := repo.Resolve(ctx, RefusalAnchorTag("payments")); err != nil || got.Digest != anchor.ManifestDesc.Digest {
		t.Fatalf("anchor tag = %v %v", got, err)
	}
	// The same refusal under other annotations, another tool's referrer, a
	// manifest with two layers, a refusal of orders and an untrusted one.
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, older,
		map[string]string{AnnPredicateType: delivery.PredicatePublishRefusal, AnnAt: "2026-10-22T09:00:00Z"})
	attach(t, path, anchor.ManifestDesc, "application/x-other", []byte("{}"), nil)
	two := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, []byte("{}"), &anchor.ManifestDesc,
		map[string]string{AnnPredicateType: delivery.PredicatePublishRefusal})
	var m ocispec.Manifest
	_ = json.Unmarshal(two.Manifest, &m)
	m.Layers = append(m.Layers, m.Layers[0])
	two.Manifest, _ = json.Marshal(m)
	two.ManifestDesc = ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, two.Manifest)
	if err := ocireferrer.Push(ctx, repo, two); err != nil {
		t.Fatal(err)
	}
	ann := map[string]string{AnnPredicateType: delivery.PredicatePublishRefusal}
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, refusal(t, key, "orders", "1.5.0", "2026-10-22T08:00:00Z"), ann)
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, refusal(t, other, "payments", "2.4.5", "2026-10-22T07:00:00Z"), ann)

	l, err := ListRefusals(ctx, repo, "payments", verify)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Refusals) != 2 || l.Refusals[0].Statement.Predicate.Version != "2.4.4" || l.Refusals[1].Statement.Predicate.Version != "2.4.3" {
		t.Fatalf("refusals = %+v", l.Refusals)
	}
	joined := strings.Join(l.Unreadable, "\n")
	if len(l.Unreadable) != 3 || !strings.Contains(joined, "not one bundle layer") || !strings.Contains(joined, `names service "orders"`) ||
		!strings.Contains(joined, "signature") {
		t.Fatalf("unreadable = %v", l.Unreadable)
	}
	limitsAndFailures(t, f, repo, older, verify)
}

func limitsAndFailures(t *testing.T, f *faulty, repo *remote.Repository, older []byte, verify func([]byte) error) {
	t.Helper()
	ctx := t.Context()
	old := MaxRefusals
	MaxRefusals = 1
	if l, _ := ListRefusals(ctx, repo, "payments", verify); len(l.Refusals) != 1 || l.Refusals[0].Statement.Predicate.Version != "2.4.4" {
		t.Fatalf("the newest only: %+v", l)
	}
	MaxRefusals = old
	if l, err := ListRefusals(ctx, repo, "nobody", verify); err != nil || len(l.Refusals) != 0 || l.Refusals == nil {
		t.Fatalf("no refusals is an empty list: %+v %v", l, err)
	}
	f.blobs.Store(true)
	if _, err := ListRefusals(ctx, repo, "payments", verify); err == nil {
		t.Fatal("a bundle that could not be fetched was not an error")
	}
	f.blobs.Store(false)
	if err := PushRefusal(ctx, repo, []byte("{")); err == nil {
		t.Fatal("pushed what is not a refusal")
	}
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ = ocireferrer.Open(path, ocireferrer.Options{})
	if _, err := ListRefusals(ctx, repo, "payments", verify); err == nil {
		t.Fatal("listed refusals on a registry without the Referrers API")
	}
	if err := PushRefusal(ctx, repo, older); err == nil {
		t.Fatal("a refusal nobody can discover was pushed as done")
	}
}

// TestPushRefusalWriteFailures: each failed write surfaces.
func TestPushRefusalWriteFailures(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	b := refusal(t, key, "payments", "2.4.3", "2026-10-22T10:00:00Z")
	anchor := RefusalAnchor("payments").ManifestDesc.Digest.String()
	for name, fail := range map[string]func(*http.Request) bool{
		"anchor": func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/manifests/"+anchor) },
		"tag":    func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/manifests/refusals-") },
		"referrer": func(r *http.Request) bool {
			return strings.Contains(r.URL.Path, "/manifests/sha256:") && !strings.HasSuffix(r.URL.Path, anchor)
		},
	} {
		inner := testutil.NewReferrersRegistry(testutil.ReferrersOptions{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut && fail(r) {
				http.Error(w, "denied", http.StatusForbidden)
				return
			}
			inner.ServeHTTP(w, r)
		}))
		repo, _ := ocireferrer.Open(strings.TrimPrefix(srv.URL, "http://")+"/pactos/payments", ocireferrer.Options{})
		if err := PushRefusal(t.Context(), repo, b); err == nil {
			t.Errorf("%s: a failed write was not an error", name)
		}
		srv.Close()
	}
}
