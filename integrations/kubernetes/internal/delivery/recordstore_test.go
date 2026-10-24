/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// from fails every request with method whose path contains part, starting
// with the nth one (1-based).
func from(n int, method, part string) func(*http.Request) bool {
	seen := 0
	return func(r *http.Request) bool {
		if r.Method != method || !strings.Contains(r.URL.Path, part) {
			return false
		}
		seen++
		return seen >= n
	}
}

func testStore(t *testing.T) (*RecordStore, *testRegistry, ed25519.PrivateKey) {
	t.Helper()
	reg := newTestRegistry(t)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	return &RecordStore{Streams: reg.host + "/delivery/streams", Verify: edVerifier(key.Public())}, reg, key
}

func TestRecordStoreAnchorFailures(t *testing.T) {
	ctx := context.Background()
	s, reg, _ := testStore(t)
	if err := s.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	if AnchorTag("a") == AnchorTag("b") || !strings.HasPrefix(AnchorTag("a"), "stream-") {
		t.Fatal("anchor tags must differ per stream")
	}
	for name, fault := range map[string]func(*http.Request) bool{
		"push":      from(1, http.MethodPut, "/manifests/sha256:"),
		"tag":       from(1, http.MethodPut, "/manifests/stream-"),
		"referrers": from(1, http.MethodGet, "/referrers/"),
	} {
		reg.fault = fault
		if _, err := s.anchor(ctx, "s-"+name); err == nil || errors.Is(err, errUnsupported) {
			t.Errorf("%s: %v", name, err)
		}
	}
	reg.fault = nil
	bad := &RecordStore{Streams: "not a host/x"}
	if _, err := bad.anchor(ctx, "s"); err == nil {
		t.Fatal("opened an unparseable streams repository")
	}
}

// pushOnAnchor attaches a hand-made manifest to the anchor.
func pushOnAnchor(t *testing.T, a *place, ann map[string]string, layers ...[]byte) ocispec.Descriptor {
	t.Helper()
	ctx := context.Background()
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, layers[0], &a.desc, ann)
	if len(layers) > 1 {
		extra := ocireferrer.Descriptor("application/x-extra", layers[1])
		if err := a.repo.Push(ctx, extra, strings.NewReader(string(layers[1]))); err != nil {
			t.Fatal(err)
		}
		art = withLayers(t, art, &a.desc, ann, extra)
	}
	if err := ocireferrer.Push(ctx, a.repo, art); err != nil {
		t.Fatal(err)
	}
	return art.ManifestDesc
}

// withLayers rebuilds art with one more layer.
func withLayers(t *testing.T, art ocireferrer.Artifact, subject *ocispec.Descriptor, ann map[string]string, extra ocispec.Descriptor) ocireferrer.Artifact {
	t.Helper()
	m := ocispec.Manifest{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageManifest, ArtifactType: delivery.BundleMediaType,
		Config: art.ConfigDesc, Layers: []ocispec.Descriptor{art.PayloadDesc, extra}, Subject: subject, Annotations: ann}
	art.Manifest, _ = json.Marshal(m)
	art.ManifestDesc = ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, art.Manifest)
	return art
}

func TestRecordStoreList(t *testing.T) {
	ctx := context.Background()
	s, reg, key := testStore(t)
	a, err := s.anchor(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	// Another tool's referrer is skipped.
	pushOnAnchor(t, a, map[string]string{"other": "tool"}, []byte("x"))
	if recs, err := s.list(ctx, a); err != nil || len(recs) != 0 {
		t.Fatalf("foreign referrer: %v %v", recs, err)
	}
	// A real record reads back.
	st := delivery.NewGap("cid", delivery.ObjectMeta{Name: "st"}, 1, delivery.Chain{Stream: "s", Seq: 1}, a.desc.Digest.String(), "t")
	bundle, id, _ := delivery.SignRecord(ctx, edSigner(key), st)
	if err := s.write(ctx, bundle, st, locations{anchor: a}); err != nil {
		t.Fatal(err)
	}
	if recs, err := s.list(ctx, a); err != nil || len(recs) != 1 || recs[0].ID != id {
		t.Fatalf("record: %v %v", recs, err)
	}
	for name, fault := range map[string]func(*http.Request) bool{
		"listing":  from(1, http.MethodGet, "/referrers/"),
		"manifest": from(1, http.MethodGet, "/manifests/sha256:"),
		"bundle":   from(1, http.MethodGet, "/blobs/"),
	} {
		reg.fault = fault
		if _, err := s.list(ctx, a); err == nil {
			t.Errorf("%s: a failed read listed", name)
		}
	}
	reg.fault = nil
	// A record manifest with two layers cannot be accounted for.
	pushOnAnchor(t, a, map[string]string{deliveryrecord.AnnPredicateType: delivery.PredicateOperation}, bundle, []byte("y"))
	if _, err := s.list(ctx, a); !errors.Is(err, errUnverifiable) {
		t.Fatalf("two layers: %v", err)
	}
}

func TestRecordStoreLocate(t *testing.T) {
	ctx := context.Background()
	s, reg, _ := testStore(t)
	a, _ := s.anchor(ctx, "s")
	apply := &applyRef{repo: reg.host + "/apps/payments", insecure: true}
	l, err := s.locate(ctx, a, apply, reg.from, reg.to)
	if err != nil || l.to == nil || l.from == nil || l.contract == nil {
		t.Fatalf("locate: %+v %v", l, err)
	}
	if l, err := s.locate(ctx, a, nil, reg.from, reg.to); err != nil || l.to != nil || l.anchor == nil {
		t.Fatalf("no apply repository: %+v %v", l, err)
	}
	// Not in the registry, not a digest, no contract annotation.
	missing := "sha256:" + strings.Repeat("e", 64)
	if l, err := s.locate(ctx, a, apply, "latest", missing); err != nil || l.to != nil || l.from != nil {
		t.Fatalf("absent: %+v %v", l, err)
	}
	if l, err := s.locate(ctx, a, apply, "", reg.from); err != nil || l.to == nil || l.contract != nil {
		t.Fatalf("no contract: %+v %v", l, err)
	}
	badCtr := reg.push(t, "apps/payments", "v3", map[string]string{delivery.ContractAnnotation: "oci://not a host/x@" + missing}).Digest.String()
	if l, err := s.locate(ctx, a, apply, "", badCtr); err != nil || l.contract != nil {
		t.Fatalf("unaddressable contract: %+v %v", l, err)
	}
	if _, err := s.locate(ctx, a, &applyRef{repo: "not a host/x"}, "", reg.to); err == nil {
		t.Fatal("opened an unparseable apply repository")
	}
	for name, fault := range map[string]func(*http.Request) bool{
		"resolve to":       from(1, http.MethodHead, "/apps/payments/manifests/"),
		"resolve from":     from(2, http.MethodHead, "/apps/payments/manifests/"),
		"fetch to":         from(1, http.MethodGet, "/apps/payments/manifests/"),
		"resolve contract": from(1, http.MethodHead, "/pactos/payments/manifests/"),
	} {
		reg.fault = fault
		if _, err := s.locate(ctx, a, apply, reg.from, reg.to); err == nil {
			t.Errorf("%s: a failed read located", name)
		}
	}
}

func TestRecordStoreWriteFailures(t *testing.T) {
	ctx := context.Background()
	s, reg, key := testStore(t)
	a, _ := s.anchor(ctx, "s")
	st := delivery.NewGap("cid", delivery.ObjectMeta{Name: "st"}, 1, delivery.Chain{Stream: "s", Seq: 1}, a.desc.Digest.String(), "t")
	bundle, _, _ := delivery.SignRecord(ctx, edSigner(key), st)
	for name, fault := range map[string]func(*http.Request) bool{
		"push":    from(1, http.MethodPut, "/manifests/"),
		"confirm": from(1, http.MethodGet, "/referrers/"),
	} {
		reg.fault = fault
		if err := s.write(ctx, bundle, st, locations{anchor: a}); err == nil {
			t.Errorf("%s: a failed write succeeded", name)
		}
	}
	reg.fault = nil
	if err := classify(errors.New("plain")); errors.Is(err, errUnsupported) {
		t.Fatal("a plain error classified as unsupported")
	}
}
