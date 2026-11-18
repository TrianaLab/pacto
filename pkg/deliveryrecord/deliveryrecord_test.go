package deliveryrecord

import (
	"bytes"
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const stream = "c/ns/payments"

// faulty refuses blob or manifest reads by digest while its switches are on,
// answers a manifest request for a tag in tagStatus with that status, and
// counts manifest HEAD requests by tag.
type faulty struct {
	inner           http.Handler
	blobs, manifest atomic.Bool
	tagStatus       map[string]int
	tagHeads        atomic.Int64
}

func (f *faulty) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, ref, _ := strings.Cut(r.URL.Path, "/manifests/")
	if code := f.tagStatus[ref]; code != 0 {
		http.Error(w, `{"errors":[{"code":"MANIFEST_UNKNOWN"}]}`, code)
		return
	}
	if r.Method == http.MethodHead && strings.HasPrefix(ref, "stream-") {
		f.tagHeads.Add(1)
	}
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

// TestReaderFetchesEachReferrerOnce: a Reader remembers what each referrer
// read as. With every manifest and blob read refused, it still lists what it
// read before, while a fresh read fails.
func TestReaderFetchesEachReferrerOnce(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	anchor := Anchor(stream)
	if err := ocireferrer.Push(t.Context(), repo, anchor); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	ann := map[string]string{AnnPredicateType: delivery.PredicateOperation}
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, signed(t, key, 1), ann)
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, signed(t, other, 2), ann)
	attach(t, path, anchor.ManifestDesc, "application/x-other", []byte("not ours"), nil)
	cached := &Reader{Verify: keyVerifier(pub)}
	if l, err := cached.List(t.Context(), repo, anchor.ManifestDesc); err != nil || len(l.Records) != 1 || len(l.Unreadable) != 1 {
		t.Fatalf("first read: %+v %v", l, err)
	}
	f.blobs.Store(true)
	f.manifest.Store(true)
	if l, err := cached.List(t.Context(), repo, anchor.ManifestDesc); err != nil || len(l.Records) != 1 || len(l.Unreadable) != 1 {
		t.Fatalf("a cached read fetched again: %+v %v", l, err)
	}
	if _, err := List(t.Context(), repo, anchor.ManifestDesc, keyVerifier(pub)); err == nil {
		t.Fatal("a fresh read with every fetch refused must fail")
	}
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
	return func(_ string, _ int64, b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
}

// TestUpstream: a downstream reads what the upstream verified from its
// signed records, and nothing when a record does not verify or the registry
// cannot be read.
func TestUpstream(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	anchor := Anchor(stream)
	_ = ocireferrer.Push(t.Context(), repo, anchor)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	const to = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation,
		Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation, Type: &delivery.RecordType{ID: delivery.TypePromote},
			Intent:   delivery.RecordIntent{Params: &delivery.Params{To: to}},
			Verdicts: []delivery.Verdict{{Gate: delivery.GatePublished, Result: delivery.Pass}},
			Outcome:  &delivery.RecordOutcome{Result: delivery.Succeeded, Verification: delivery.Verified},
			Chain:    delivery.Chain{Stream: stream, Seq: 1}}}
	b, _, _ := delivery.SignRecord(t.Context(), edSigner(key), st)
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, b, map[string]string{AnnPredicateType: delivery.PredicateOperation})

	if s, why := Upstream(t.Context(), repo, stream, keyVerifier(pub), delivery.RecordMark{}, nil); why != "" || len(s.Offered) != 1 || s.Offered[0] != to {
		t.Fatalf("offered = %+v %q", s, why)
	}
	other, mallory, _ := ed25519.GenerateKey(rand.Reader)
	if s, why := Upstream(t.Context(), repo, stream, keyVerifier(other), delivery.RecordMark{}, nil); !strings.HasPrefix(why, "1 referrers on the upstream anchor could not be verified") || s != nil {
		t.Fatalf("an untrusted key offers nothing, and says so: %+v %q", s, why)
	}
	// A record another key signed, attached by anyone with push access, does
	// not blind the follower: it is skipped.
	forged := st
	forged.Predicate.Chain.Seq = 2
	fb, _, _ := delivery.SignRecord(t.Context(), edSigner(mallory), forged)
	attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, fb, map[string]string{AnnPredicateType: delivery.PredicateOperation})
	if s, why := Upstream(t.Context(), repo, stream, keyVerifier(pub), delivery.RecordMark{}, nil); !strings.Contains(why, "1 referrers") || len(s.Offered) != 1 {
		t.Fatalf("a forged referrer blinds the follower, or goes unsaid: %+v %q", s, why)
	}
	_, path = setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ = ocireferrer.Open(path, ocireferrer.Options{})
	if s, why := Upstream(t.Context(), repo, stream, Unchecked, delivery.RecordMark{}, nil); s != nil || !strings.HasPrefix(why, "reading the records") {
		t.Fatalf("an unreadable registry offers nothing: %+v %q", s, why)
	}
}

// TestListStreamHoldsARevokedKeyToItsHistory: a leaked key revoked at its last
// record verifies that record and the ones it chains from, while a record it
// backdates to a seq it already used is unreadable rather than a fork. A
// per-subject List cannot walk the chain, so it holds back all it signed.
func TestListStreamHoldsARevokedKeyToItsHistory(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	anchor := Anchor(stream)
	_ = ocireferrer.Push(t.Context(), repo, anchor)
	oldPub, old, _ := ed25519.GenerateKey(rand.Reader)
	newPub, next, _ := ed25519.GenerateKey(rand.Reader)
	ann := map[string]string{AnnPredicateType: delivery.PredicateOperation}
	put := func(key ed25519.PrivateKey, seq int64, parent, name string) string {
		st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation,
			Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation, Intent: delivery.RecordIntent{Name: name},
				Chain: delivery.Chain{Stream: stream, Seq: seq, Parent: parent}}}
		b, id, err := delivery.SignRecord(t.Context(), edSigner(key), st)
		if err != nil {
			t.Fatal(err)
		}
		attach(t, path, anchor.ManifestDesc, delivery.BundleMediaType, b, ann)
		return id
	}
	r1 := put(old, 1, "", "r1")
	r2 := put(old, 2, r1, "r2")
	put(next, 3, r2, "r3")
	put(old, 1, "", "forged") // backdated to a seq the key used
	put(old, 4, r2, "late")   // after its revocation
	pem := func(p ed25519.PublicKey) string { s, _ := delivery.PublicKeyPEM(p); return s }
	trust := func(until string) delivery.Verifier {
		v, err := signing.TrustVerifier(delivery.TrustConfig{Keys: []delivery.TrustedKey{
			{Name: "old", PublicKey: pem(oldPub), UntilSeq: 2, UntilRecord: until},
			{Name: "new", PublicKey: pem(newPub)},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	names := func(recs []delivery.StoredRecord) []string {
		var out []string
		for _, r := range recs {
			out = append(out, r.Statement.Predicate.Intent.Name)
		}
		slices.Sort(out)
		return out
	}
	l, err := ListStream(t.Context(), repo, stream, trust(r2))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(l.Records); !slices.Equal(got, []string{"r1", "r2", "r3"}) {
		t.Fatalf("records = %v", got)
	}
	if len(l.Unreadable) != 2 || !slices.ContainsFunc(l.Unreadable, func(s string) bool { return strings.Contains(s, "not in the history that ends at its last record "+r2) }) {
		t.Fatalf("unreadable = %v", l.Unreadable)
	}
	if f := delivery.FoldStream(l.Records); len(f.Problems) != 0 {
		t.Fatalf("the forgery forked the stream: %v", f.Problems)
	}
	// A last record the registry no longer holds vouches for nothing.
	if l, _ := ListStream(t.Context(), repo, stream, trust(strings.Repeat("0", 64))); !slices.Equal(names(l.Records), []string{"r3"}) {
		t.Fatalf("records = %v", names(l.Records))
	}
	l, err = List(t.Context(), repo, anchor.ManifestDesc, trust(r2))
	if err != nil || !slices.Equal(names(l.Records), []string{"r3"}) || len(l.Unreadable) != 4 {
		t.Fatalf("list = %v %v %v", names(l.Records), l.Unreadable, err)
	}
}

// tagRecord writes payload as a record attached to subject under a write-once
// tag, as a registry without the Referrers API holds it. tag overrides the
// tag when set.
func tagRecord(t *testing.T, path string, subject ocispec.Descriptor, payload []byte, seq int64, tag string) ocireferrer.Artifact {
	t.Helper()
	return tagAs(t, path, subject, payload, delivery.PredicateOperation, func(d digest.Digest) string {
		return cmp.Or(tag, RecordTag(stream, seq, d))
	})
}

// tagAs pushes payload as a record manifest of predicate attached to subject
// under the tag tagOf names for its manifest digest.
func tagAs(t *testing.T, path string, subject ocispec.Descriptor, payload []byte, predicate string, tagOf func(digest.Digest) string) ocireferrer.Artifact {
	t.Helper()
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, payload, &subject,
		map[string]string{AnnPredicateType: predicate})
	tag := tagOf(art.ManifestDesc.Digest)
	for _, b := range []struct {
		d    ocispec.Descriptor
		data []byte
	}{{art.ConfigDesc, art.Config}, {art.PayloadDesc, art.Payload}} {
		if err := repo.Push(t.Context(), b.d, bytes.NewReader(b.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.PushReference(t.Context(), art.ManifestDesc, bytes.NewReader(art.Manifest), tag); err != nil {
		t.Fatal(err)
	}
	return art
}

// anchored pushes and tags the stream's anchor at path.
func anchored(t *testing.T, path string) (*remote.Repository, ocispec.Descriptor) {
	t.Helper()
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	anchor := Anchor(stream)
	if err := ocireferrer.Push(t.Context(), repo, anchor); err != nil {
		t.Fatal(err)
	}
	if err := repo.Tag(t.Context(), anchor.ManifestDesc, AnchorTag(stream)); err != nil {
		t.Fatal(err)
	}
	return repo, anchor.ManifestDesc
}

func seqs(recs []delivery.StoredRecord) []int64 {
	var out []int64
	for _, r := range recs {
		out = append(out, r.Statement.Predicate.Chain.Seq)
	}
	slices.Sort(out)
	return out
}

func TestAnchorTagIsStable(t *testing.T) {
	if got := AnchorTag(stream); got != "stream-f6a1b217391d9ebad714d8914704d08b99ad26d30bf2ff68565dc548b099ab2f" {
		t.Fatalf("anchor tag = %s", got)
	}
	if AnchorTag("a") == AnchorTag("b") {
		t.Fatal("anchor tags must differ per stream")
	}
}

func TestListStreamReadsWriteOnceTags(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	for seq := int64(1); seq <= 3; seq++ {
		tagRecord(t, path, anchor, signed(t, key, seq), seq, "")
	}
	l, err := ListStream(t.Context(), repo, stream, keyVerifier(pub))
	if err != nil || !slices.Equal(seqs(l.Records), []int64{1, 2, 3}) || len(l.Unreadable) != 0 {
		t.Fatalf("listing = %+v %v", l, err)
	}
}

func TestListStreamReadsBothForms(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{})
	repo, anchor := anchored(t, path)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	attach(t, path, anchor, delivery.BundleMediaType, signed(t, key, 1), map[string]string{AnnPredicateType: delivery.PredicateOperation})
	tagRecord(t, path, anchor, signed(t, key, 2), 2, "")
	l, err := ListStream(t.Context(), repo, stream, keyVerifier(pub))
	if err != nil || !slices.Equal(seqs(l.Records), []int64{1, 2}) || len(l.Unreadable) != 0 {
		t.Fatalf("listing = %+v %v", l, err)
	}
}

func TestListStreamRefusesTagAttachedElsewhere(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ := anchored(t, path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	other := Anchor("c/ns/other")
	_ = ocireferrer.Push(t.Context(), repo, other)
	tagRecord(t, path, other.ManifestDesc, signed(t, key, 1), 1, "")
	l, err := ListStream(t.Context(), repo, stream, Unchecked)
	if err != nil || len(l.Records) != 0 || len(l.Unreadable) != 1 || !strings.Contains(l.Unreadable[0], "tagged as a record of this stream but attached to") {
		t.Fatalf("listing = %+v %v", l, err)
	}
}

func TestListStreamRefusesTagNamingAnotherManifest(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	tagRecord(t, path, anchor, signed(t, key, 1), 1, AnchorTag(stream)+"-1-"+strings.Repeat("0", 12))
	tagRecord(t, path, anchor, signed(t, key, 2), 2, AnchorTag(stream)+"-2-short")
	l, err := ListStream(t.Context(), repo, stream, Unchecked)
	if err != nil || len(l.Records) != 0 || len(l.Unreadable) != 2 || !strings.Contains(l.Unreadable[0], "does not name the manifest it points at") {
		t.Fatalf("listing = %+v %v", l, err)
	}
}

func TestListStreamSkipsVanishedTag(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	tagRecord(t, path, anchor, signed(t, key, 1), 1, "")
	gone := RecordTag(stream, 2, tagRecord(t, path, anchor, signed(t, key, 2), 2, "").ManifestDesc.Digest)
	f.tagStatus = map[string]int{gone: http.StatusNotFound}
	l, err := ListStream(t.Context(), repo, stream, keyVerifier(pub))
	if err != nil || !slices.Equal(seqs(l.Records), []int64{1}) || !slices.Equal(l.Unreadable, []string{"tag " + gone + " was listed and is gone"}) {
		t.Fatalf("listing = %+v %v", l, err)
	}
	f.tagStatus = map[string]int{gone: http.StatusForbidden}
	if _, err := ListStream(t.Context(), repo, stream, keyVerifier(pub)); err == nil {
		t.Fatal("a tag the registry failed to resolve was not an error")
	}
}

func TestReaderResolvesEachTagOnce(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	tagRecord(t, path, anchor, signed(t, key, 1), 1, "")
	tagRecord(t, path, anchor, signed(t, key, 2), 2, "")
	r := &Reader{Verify: keyVerifier(pub)}
	for range 2 {
		if l, err := r.ListStream(t.Context(), repo, stream); err != nil || len(l.Records) != 2 {
			t.Fatalf("listing = %+v %v", l, err)
		}
	}
	if got := f.tagHeads.Load(); got != 2 {
		t.Fatalf("tag resolves = %d, want 2", got)
	}
}

func TestListSubjectStaysNativeOnly(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	tagRecord(t, path, anchor, signed(t, key, 1), 1, "")
	if _, err := List(t.Context(), repo, anchor, Unchecked); err == nil {
		t.Fatal("a subject listing read tags on a registry without the Referrers API")
	}
}

func TestUpstreamReadsWriteOnceTags(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	const to = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation,
		Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation, Type: &delivery.RecordType{ID: delivery.TypePromote},
			Intent:   delivery.RecordIntent{Params: &delivery.Params{To: to}},
			Verdicts: []delivery.Verdict{{Gate: delivery.GatePublished, Result: delivery.Pass}},
			Outcome:  &delivery.RecordOutcome{Result: delivery.Succeeded, Verification: delivery.Verified},
			Chain:    delivery.Chain{Stream: stream, Seq: 1}}}
	b, _, _ := delivery.SignRecord(t.Context(), edSigner(key), st)
	tagRecord(t, path, anchor, b, 1, "")
	if s, why := Upstream(t.Context(), repo, stream, keyVerifier(pub), delivery.RecordMark{}, nil); why != "" || s == nil || len(s.Offered) != 1 || s.Offered[0] != to {
		t.Fatalf("offered = %+v %q", s, why)
	}
}

func TestRecordTagShape(t *testing.T) {
	d := digest.FromString("m")
	got := RecordTag(stream, math.MaxInt64, d)
	if want := AnchorTag(stream) + "-9223372036854775807-" + d.Encoded()[:12]; got != want {
		t.Fatalf("tag = %s, want %s", got, want)
	}
	if len(got) > 128 {
		t.Fatalf("tag is %d characters, more than an OCI tag holds", len(got))
	}
}

// putRecord writes a record of seq on stream's anchor at path in mode.
func putRecord(t *testing.T, path string, key ed25519.PrivateKey, seq int64, mode capabilities.ReferrersMode) (*remote.Repository, ocireferrer.Artifact) {
	t.Helper()
	repo, anchor := anchored(t, path)
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, signed(t, key, seq), &anchor,
		map[string]string{AnnPredicateType: delivery.PredicateOperation, AnnRegistryMode: string(mode)})
	if err := PutRecord(t.Context(), repo, anchor, stream, seq, art, mode); err != nil {
		t.Fatal(err)
	}
	return repo, art
}

func TestPutRecordNative(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{})
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	repo, _ := putRecord(t, path, key, 1, capabilities.ReferrersNative)
	if tags, _ := ocireferrer.Tags(t.Context(), repo, AnchorTag(stream)+"-"); len(tags) != 0 {
		t.Fatalf("a native record was tagged: %v", tags)
	}
	l, err := List(t.Context(), repo, Anchor(stream).ManifestDesc, keyVerifier(pub))
	if err != nil || !slices.Equal(seqs(l.Records), []int64{1}) {
		t.Fatalf("listing = %+v %v", l, err)
	}
}

func TestPutRecordWriteOnceTags(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	repo, art := putRecord(t, path, key, 1, capabilities.ReferrersWriteOnceTags)
	if got, err := repo.Resolve(t.Context(), RecordTag(stream, 1, art.ManifestDesc.Digest)); err != nil || got.Digest != art.ManifestDesc.Digest {
		t.Fatalf("record tag = %v %v", got.Digest, err)
	}
	l, err := ListStream(t.Context(), repo, stream, keyVerifier(pub))
	if err != nil || !slices.Equal(seqs(l.Records), []int64{1}) || len(l.Unreadable) != 0 {
		t.Fatalf("listing = %+v %v", l, err)
	}
}

func TestPutRecordNativeFailures(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{})
	repo, anchor := anchored(t, path)
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, []byte("r"), &anchor, nil)
	other := ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, []byte("not held"))
	if err := PutRecord(t.Context(), repo, other, stream, 1, art, capabilities.ReferrersNative); err == nil {
		t.Fatal("a record the anchor does not list was confirmed")
	}
	f.tagStatus = map[string]int{art.ManifestDesc.Digest.String(): http.StatusForbidden}
	if err := PutRecord(t.Context(), repo, anchor, stream, 1, art, capabilities.ReferrersNative); err == nil {
		t.Fatal("a failed push succeeded")
	}
}

// TestListStreamIgnoresApprovalAndIntentRecords: approval and intent records
// on a stream's anchor, as write-once tags under its prefix or as native
// referrers, are not operation records: they never join the chain, never
// count as a fork of a seq and are never unreadable, even when their bundle
// claims a seq the stream already holds.
func TestListStreamIgnoresApprovalAndIntentRecords(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts testutil.ReferrersOptions
	}{{"write-once tags", testutil.ReferrersOptions{Unsupported: true}}, {"native", testutil.ReferrersOptions{}}} {
		t.Run(tc.name, func(t *testing.T) {
			_, path := setup(t, tc.opts)
			repo, anchor := anchored(t, path)
			pub, key, _ := ed25519.GenerateKey(rand.Reader)
			tagRecord(t, path, anchor, signed(t, key, 1), 1, "")
			for kind, predicate := range map[string]string{KindApproval: delivery.PredicateApproval, KindIntent: delivery.PredicateIntent} {
				claimsSeq1 := signed(t, key, 1)
				if tc.opts.Unsupported {
					tagAs(t, path, anchor, claimsSeq1, predicate, func(d digest.Digest) string {
						return KindTag(stream, kind, d)
					})
					continue
				}
				attach(t, path, anchor, delivery.BundleMediaType, claimsSeq1, map[string]string{AnnPredicateType: predicate})
			}
			l, err := ListStream(t.Context(), repo, stream, keyVerifier(pub))
			if err != nil || !slices.Equal(seqs(l.Records), []int64{1}) || len(l.Unreadable) != 0 {
				t.Fatalf("listing = %+v %v", l, err)
			}
			if f := delivery.FoldStream(l.Records); len(f.Problems) != 0 {
				t.Fatalf("approval or intent records forked the chain: %v", f.Problems)
			}
		})
	}
}

// TestUpstreamEmptyIsUnknown: a follower that reads a stream only by its
// reference and finds no record there cannot tell a new stream from a wrong
// reference or deleted records, so it offers nothing and says why, never an
// empty and whole upstream.
func TestUpstreamEmptyIsUnknown(t *testing.T) {
	for _, opts := range []testutil.ReferrersOptions{{}, {Unsupported: true}} {
		_, path := setup(t, opts)
		repo, _ := anchored(t, path)
		s, why := Upstream(t.Context(), repo, stream, Unchecked, delivery.RecordMark{}, nil)
		if s != nil || why != noRecordFound {
			t.Fatalf("an empty stream read as known: %+v %q", s, why)
		}
	}
}

// TestPushBothModes: an approval or intent record goes onto the stream's
// anchor in the stream's mode, under its kind's write-once tag where the
// registry serves no Referrers API, and stays out of the operation chain.
func TestPushBothModes(t *testing.T) {
	for _, mode := range []capabilities.ReferrersMode{capabilities.ReferrersNative, capabilities.ReferrersWriteOnceTags} {
		t.Run(string(mode), func(t *testing.T) {
			_, path := setup(t, testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags})
			repo, anchor := anchored(t, path)
			pub, key, _ := ed25519.GenerateKey(rand.Reader)
			for kind, predicate := range map[string]string{KindApproval: delivery.PredicateApproval, KindIntent: delivery.PredicateIntent} {
				ref, err := Push(t.Context(), repo, stream, mode, predicate, signed(t, key, 1), map[string]string{AnnUID: "u1"})
				if err != nil {
					t.Fatal(err)
				}
				repoRef, dgst, _ := strings.Cut(ref, "@")
				if repoRef != strings.TrimPrefix(path, "http://") || !strings.HasPrefix(dgst, "sha256:") {
					t.Fatalf("reference = %s", ref)
				}
				tags, _ := ocireferrer.Tags(t.Context(), repo, AnchorTag(stream)+"-"+kind+"-")
				if want := mode == capabilities.ReferrersWriteOnceTags; (len(tags) == 1) != want {
					t.Fatalf("%s tags = %v in %s", kind, tags, mode)
				}
			}
			descs, tags, err := ocireferrer.Attached(t.Context(), repo, anchor, AnchorTag(stream)+"-")
			if err != nil || len(descs)+len(tags) != 2 {
				t.Fatalf("attached = %v %v %v", descs, tags, err)
			}
			l, err := ListStream(t.Context(), repo, stream, keyVerifier(pub))
			if err != nil || len(l.Records) != 0 || len(l.Unreadable) != 0 {
				t.Fatalf("listing = %+v %v", l, err)
			}
		})
	}
	// A write the registry refuses is an error, never a reference.
	f, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ := anchored(t, path)
	ref, err := Push(t.Context(), repo, stream, capabilities.ReferrersWriteOnceTags, delivery.PredicateIntent, []byte("r"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, dgst, _ := strings.Cut(ref, "@")
	f.tagStatus = map[string]int{KindTag(stream, KindIntent, digest.Digest(dgst)): http.StatusForbidden}
	if _, err := Push(t.Context(), repo, stream, capabilities.ReferrersWriteOnceTags, delivery.PredicateIntent, []byte("r"), nil); err == nil {
		t.Fatal("a refused write returned a reference")
	}
}

func TestKindTagShape(t *testing.T) {
	d := digest.FromString("m")
	if got := KindTag(stream, KindApproval, d); got != AnchorTag(stream)+"-approval-"+d.Encoded()[:12] || len(got) > 93 {
		t.Fatalf("tag = %s", got)
	}
}
