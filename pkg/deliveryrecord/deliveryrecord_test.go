package deliveryrecord

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const stream = "c/ns/payments"

// faulty refuses blob or manifest reads by digest while its switches are on.
type faulty struct {
	inner           http.Handler
	blobs, manifest atomic.Bool
}

func (f *faulty) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && ((f.blobs.Load() && strings.Contains(r.URL.Path, "/blobs/")) ||
		(f.manifest.Load() && strings.Contains(r.URL.Path, "/manifests/sha256:"))) {
		http.Error(w, "denied", http.StatusForbidden)
		return
	}
	f.inner.ServeHTTP(w, r)
}

func setup(t *testing.T, opts testutil.ReferrersOptions) (*faulty, string) {
	t.Helper()
	f := &faulty{inner: testutil.NewReferrersRegistry(opts)}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, strings.TrimPrefix(srv.URL, "http://") + "/delivery/streams"
}

func signed(t *testing.T, key ed25519.PrivateKey, seq int64) []byte {
	t.Helper()
	st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation,
		Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation, Chain: delivery.Chain{Stream: stream, Seq: seq}}}
	b, _, err := delivery.SignRecord(context.Background(), edSigner(key), st)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func attach(t *testing.T, path string, subject ocispec.Descriptor, layerType string, payload []byte, ann map[string]string) {
	t.Helper()
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	if err := ocireferrer.Push(t.Context(), repo, ocireferrer.Build(layerType, layerType, payload, &subject, ann)); err != nil {
		t.Fatal(err)
	}
}

func TestAnchorRef(t *testing.T) {
	first, again := Anchor(stream), Anchor(stream)
	if first.ManifestDesc.Digest != again.ManifestDesc.Digest || first.ManifestDesc.Digest == Anchor("other").ManifestDesc.Digest {
		t.Fatal("the anchor is not a function of the stream")
	}
	ref := AnchorRef("reg/streams", stream)
	repo, desc, err := ParseAnchorRef(ref, stream)
	if err != nil || repo != "reg/streams" || desc.Digest != Anchor(stream).ManifestDesc.Digest {
		t.Fatalf("parse = %s %v %v", repo, desc, err)
	}
	for _, bad := range []string{"reg/streams", "@" + desc.Digest.String(), AnchorRef("reg/streams", "other")} {
		if _, _, err := ParseAnchorRef(bad, stream); err == nil {
			t.Errorf("%q parsed as the anchor of %s", bad, stream)
		}
	}
}

func TestList(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{PageSize: 1})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{PageSize: 1})
	anchor := Anchor(stream)
	if err := ocireferrer.Push(t.Context(), repo, anchor); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	verify := keyVerifier(pub)
	ann := map[string]string{AnnPredicateType: delivery.PredicateOperation}
	good := signed(t, key, 1)
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, good, ann)
	attach(t, path, anchor.ManifestDesc, "application/x-other", []byte("not ours"), nil)
	var tampered struct {
		MediaType            string          `json:"mediaType"`
		VerificationMaterial json.RawMessage `json:"verificationMaterial"`
		DSSEEnvelope         struct {
			Payload     []byte            `json:"payload"`
			PayloadType string            `json:"payloadType"`
			Signatures  []json.RawMessage `json:"signatures"`
		} `json:"dsseEnvelope"`
	}
	_ = json.Unmarshal(signed(t, key, 2), &tampered)
	tampered.DSSEEnvelope.Payload = []byte(strings.Replace(string(tampered.DSSEEnvelope.Payload), `"seq":2`, `"seq":3`, 1))
	tb, _ := json.Marshal(tampered)
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, tb, ann)

	l, err := List(t.Context(), repo, anchor.ManifestDesc, verify)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Records) != 1 || l.Records[0].Statement.Predicate.Chain.Seq != 1 || l.Records[0].Manifest == "" {
		t.Fatalf("records = %+v", l.Records)
	}
	if len(l.Unreadable) != 1 || !strings.Contains(l.Unreadable[0], "signature") {
		t.Fatalf("unreadable = %v", l.Unreadable)
	}
	// Unchecked lists the tampered one too, which is why a reader with no
	// trust config says nothing was verified.
	if l, _ := List(t.Context(), repo, anchor.ManifestDesc, Unchecked); len(l.Records) != 2 {
		t.Fatalf("unchecked records = %d", len(l.Records))
	}

	f.blobs.Store(true)
	if _, err := List(t.Context(), repo, anchor.ManifestDesc, verify); err == nil {
		t.Fatal("a bundle that could not be fetched was not an error")
	}
	f.blobs.Store(false)
	f.manifest.Store(true)
	if _, err := List(t.Context(), repo, anchor.ManifestDesc, verify); err == nil {
		t.Fatal("a manifest that could not be fetched was not an error")
	}
	f.manifest.Store(false)

	old := MaxBytes
	MaxBytes = 10
	if l, _ := List(t.Context(), repo, anchor.ManifestDesc, verify); len(l.Records) != 0 || len(l.Unreadable) != 0 {
		t.Fatalf("oversized manifests were read: %+v", l)
	}
	MaxBytes = old
}

func TestListRefusesOddRecords(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	anchor := Anchor(stream)
	_ = ocireferrer.Push(t.Context(), repo, anchor)
	// A record manifest with two layers: pushed by hand.
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, []byte("{}"), &anchor.ManifestDesc,
		map[string]string{AnnPredicateType: delivery.PredicateOperation})
	var m ocispec.Manifest
	_ = json.Unmarshal(art.Manifest, &m)
	m.Layers = append(m.Layers, m.Layers[0])
	art.Manifest, _ = json.Marshal(m)
	art.ManifestDesc = ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, art.Manifest)
	if err := ocireferrer.Push(t.Context(), repo, art); err != nil {
		t.Fatal(err)
	}
	l, err := List(t.Context(), repo, anchor.ManifestDesc, Unchecked)
	if err != nil || len(l.Unreadable) != 1 || !strings.Contains(l.Unreadable[0], "not one bundle layer") {
		t.Fatalf("listing = %+v %v", l, err)
	}

	_, path = setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ = ocireferrer.Open(path, ocireferrer.Options{})
	if _, err := List(t.Context(), repo, anchor.ManifestDesc, Unchecked); err == nil {
		t.Fatal("listed records on a registry without the Referrers API")
	}
}

func TestListStreamBindsRecordsToTheAnchor(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	anchor := Anchor(stream)
	_ = ocireferrer.Push(t.Context(), repo, anchor)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	ann := map[string]string{AnnPredicateType: delivery.PredicateOperation}
	good := signed(t, key, 1)
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, good, ann)
	// The same record in a second manifest, as a re-signed retry leaves it.
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, good,
		map[string]string{AnnPredicateType: delivery.PredicateOperation, AnnSeq: "1"})
	// A record validly signed for another stream, attached to this anchor.
	st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation,
		Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation, Chain: delivery.Chain{Stream: "c/dev/x", Seq: 2}}}
	foreign, _, _ := delivery.SignRecord(t.Context(), edSigner(key), st)
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, foreign, ann)

	l, err := ListStream(t.Context(), repo, stream, keyVerifier(pub))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Records) != 1 || l.Records[0].Statement.Predicate.Chain.Stream != stream {
		t.Fatalf("records = %+v", l.Records)
	}
	if len(l.Unreadable) != 1 || !strings.Contains(l.Unreadable[0], `names stream "c/dev/x"`) {
		t.Fatalf("unreadable = %v", l.Unreadable)
	}
	_, path = setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ = ocireferrer.Open(path, ocireferrer.Options{})
	if _, err := ListStream(t.Context(), repo, stream, Unchecked); err == nil {
		t.Fatal("listed a stream on a registry without the Referrers API")
	}
}

// edSigner signs records with key through sigstore-go.
func edSigner(key ed25519.PrivateKey) delivery.Signer {
	kp, _ := signing.FromPrivateKey(key)
	return signing.Keyed(kp, "")
}

func keyVerifier(pub ed25519.PublicKey) delivery.Verifier {
	return func(_ string, b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
}
