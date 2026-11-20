package deliveryrecord

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// TestReadAtByTagOrDigest: a record is read by its write-once tag or its
// manifest digest and says its predicate; the anchor, a moved tag, a record
// of two layers and an unreadable registry are errors.
func TestReadAtByTagOrDigest(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	ref, err := Push(t.Context(), repo, stream, capabilities.ReferrersWriteOnceTags, delivery.PredicateApproval, approvalAt(t, key, "2026-11-11T00:00:00Z"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _ := strings.Cut(ref, "@")
	for _, reference := range []string{manifest, KindTag(stream, KindApproval, digest.Digest(manifest))} {
		if p, b, err := ReadAt(t.Context(), repo, reference); err != nil || p != delivery.PredicateApproval || claimedTime(b) != "2026-11-11T00:00:00Z" {
			t.Fatalf("%s: %s %v", reference, p, err)
		}
	}
	op := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, signed(t, key, 1), &anchor, map[string]string{AnnPredicateType: delivery.PredicateOperation})
	if err := PutRecord(t.Context(), repo, anchor, stream, 1, op, capabilities.ReferrersWriteOnceTags); err != nil {
		t.Fatal(err)
	}
	if p, _, err := ReadAt(t.Context(), repo, RecordTag(stream, 1, op.ManifestDesc.Digest)); err != nil || p != delivery.PredicateOperation {
		t.Fatalf("operation record: %s %v", p, err)
	}
	if _, _, err := ReadAt(t.Context(), repo, AnchorTag(stream)); err == nil || !strings.Contains(err.Error(), "is not a delivery record") {
		t.Fatalf("the anchor read as a record: %v", err)
	}
}

// TestReadAtRefuses: a moved tag, a record of two layers, a missing tag and
// an unreadable manifest are errors.
func TestReadAtRefuses(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{Unsupported: true})
	repo, anchor := anchored(t, path)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	ref, err := Push(t.Context(), repo, stream, capabilities.ReferrersWriteOnceTags, delivery.PredicateApproval, approvalAt(t, key, "2026-11-11T00:00:00Z"), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, manifest, _ := strings.Cut(ref, "@")
	moved := tagAs(t, path, anchor, approvalAt(t, key, "2026-11-12T00:00:00Z"), delivery.PredicateApproval,
		func(digest.Digest) string { return KindTag(stream, KindApproval, digest.FromString("other")) })
	if _, _, err := ReadAt(t.Context(), repo, KindTag(stream, KindApproval, digest.FromString("other"))); err == nil ||
		!strings.Contains(err.Error(), "does not name the manifest") {
		t.Fatalf("a moved tag read as %s: %v", moved.ManifestDesc.Digest, err)
	}
	two := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, []byte("{}"), &anchor, map[string]string{AnnPredicateType: delivery.PredicateIntent})
	var m ocispec.Manifest
	_ = json.Unmarshal(two.Manifest, &m)
	m.Layers = append(m.Layers, m.Layers[0])
	two.Manifest, _ = json.Marshal(m)
	two.ManifestDesc = ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, two.Manifest)
	if err := ocireferrer.PushWriteOnce(t.Context(), repo, two, "two"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadAt(t.Context(), repo, "two"); err == nil || !strings.Contains(err.Error(), "not one bundle layer") {
		t.Fatalf("two layers: %v", err)
	}
	if _, _, err := ReadAt(t.Context(), repo, "missing"); err == nil {
		t.Fatal("a missing tag read")
	}
	f.manifest.Store(true)
	if _, _, err := ReadAt(t.Context(), repo, manifest); err == nil {
		t.Fatal("an unreadable manifest read")
	}
}
