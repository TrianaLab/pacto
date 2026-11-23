//go:build registryconformance

// Package registryconformance proves the registry probe, write-once tags and
// delivery record reads, typed bundles and registry discovery on real registries: zot, which serves the OCI 1.1
// Referrers API, and CNCF distribution, which serves none. It runs in the
// ci-oci job and locally through `make test-registry-conformance`.
//
// GHCR, which cannot run here, answers a tag listing whose last names a tag
// it does not hold with 200 and null tags (zot answers 404); the in-process
// registry's TagsUnknownLast option reproduces it for the unit tests.
package registryconformance

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

func TestRegistryConformance(t *testing.T) {
	for _, tc := range []struct {
		name, env     string
		wantReferrers capabilities.ReferrersMode
	}{
		{"zot", "PACTO_CONFORMANCE_NATIVE_REGISTRY", capabilities.ReferrersNative},
		{"distribution", "PACTO_CONFORMANCE_TAGS_REGISTRY", capabilities.ReferrersWriteOnceTags},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := os.Getenv(tc.env)
			if host == "" {
				t.Skipf("%s is not set: no %s registry to test against", tc.env, tc.name)
			}
			conform(t, host, tc.wantReferrers)
			t.Run("typed bundle", func(t *testing.T) { typedBundle(t, host) })
		})
	}
}

func conform(t *testing.T, host string, want capabilities.ReferrersMode) {
	ctx := t.Context()
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	stream := "conformance/" + hex.EncodeToString(suffix)
	repo, err := ocireferrer.Open(host+"/conformance/streams-"+hex.EncodeToString(suffix), ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}

	// The anchor, pushed and tagged as the recorder does.
	anchor := deliveryrecord.Anchor(stream)
	if err := ocireferrer.Push(ctx, repo, anchor); err != nil {
		t.Fatalf("pushing the anchor: %v", err)
	}
	if err := repo.Tag(ctx, anchor.ManifestDesc, deliveryrecord.AnchorTag(stream)); err != nil {
		t.Fatalf("tagging the anchor: %v", err)
	}

	caps, err := (&ocireferrer.Prober{}).Probe(ctx, repo, anchor.ManifestDesc.Digest.String(), true)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("%s: %s (%s), auth %s", host, caps.Mode(), caps.Evidence(), caps.Auth)
	if caps.Referrers != want || caps.Listing != capabilities.ListingCatalog ||
		caps.ArtifactType != capabilities.ArtifactTypeAccepted || caps.Auth == "" {
		t.Fatalf("capabilities = %+v, want referrers %s, listing catalog, artifactType accepted, an auth scheme", caps, want)
	}
	if m := caps.Mode(); !strings.Contains(m, "referrers="+string(want)) || !strings.Contains(m, "listing=catalog") {
		t.Fatalf("Mode() = %q", m)
	}

	// Three signed records written in the probed mode read back verified.
	writeAndRead(t, repo, anchor.ManifestDesc, stream, caps.Referrers)

	// No write-once stream lands on the shared referrers-tag index.
	if want == capabilities.ReferrersWriteOnceTags {
		noReferrersTagIndex(t, repo)
	}

	// A taken tag is never moved.
	other := ocireferrer.Build(deliveryrecord.AnchorType, deliveryrecord.AnchorType, []byte(`{"other":true}`), nil, nil)
	if err := ocireferrer.PushWriteOnce(ctx, repo, other, deliveryrecord.AnchorTag(stream)); !errors.Is(err, ocireferrer.ErrTagTaken) {
		t.Fatalf("pushing over a taken tag = %v, want ErrTagTaken", err)
	}
}

// writeAndRead writes three signed records on stream in mode and checks
// ListStream reads exactly those three back, verified.
func writeAndRead(t *testing.T, repo *remote.Repository, anchor ocispec.Descriptor, stream string, mode capabilities.ReferrersMode) {
	ctx := t.Context()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(key)
	signer := signing.Keyed(kp, "")
	for seq := int64(1); seq <= 3; seq++ {
		st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation,
			Predicate: delivery.RecordPredicate{Kind: delivery.RecordOperation, Chain: delivery.Chain{Stream: stream, Seq: seq}}}
		b, _, err := delivery.SignRecord(ctx, signer, st)
		if err != nil {
			t.Fatal(err)
		}
		art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, b, &anchor,
			map[string]string{deliveryrecord.AnnPredicateType: delivery.PredicateOperation, deliveryrecord.AnnRegistryMode: string(mode)})
		if err := deliveryrecord.PutRecord(ctx, repo, anchor, stream, seq, art, mode); err != nil {
			t.Fatalf("writing record %d: %v", seq, err)
		}
	}
	verify := func(_ string, _ int64, b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
	l, err := deliveryrecord.ListStream(ctx, repo, stream, verify)
	if err != nil {
		t.Fatalf("reading the stream: %v", err)
	}
	var seqs []int64
	for _, r := range l.Records {
		seqs = append(seqs, r.Statement.Predicate.Chain.Seq)
	}
	slices.Sort(seqs)
	if !slices.Equal(seqs, []int64{1, 2, 3}) || len(l.Unreadable) != 0 {
		t.Fatalf("stream read back as seqs %v, unreadable %v", seqs, l.Unreadable)
	}
}

// noReferrersTagIndex fails when repo holds a tag of the shared sha256-<hex>
// referrers-tag index.
func noReferrersTagIndex(t *testing.T, repo *remote.Repository) {
	tags, err := ocireferrer.Tags(t.Context(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range tags {
		if strings.HasPrefix(tag, "sha256-") {
			t.Fatalf("a write-once stream wrote the referrers-tag index %s", tag)
		}
	}
}

// templateYAML is the golden path http-service 1.0.0.
const templateYAML = "pactoVersion: \"2.4\"\nservice:\n  name: http-service\n  version: 1.0.0\n" +
	"  owner: {team: paths}\n  template:\n    plugins:\n      - name: dockerfile\n"

// typedBundle pushes a golden path to <host>/conformance/paths/http-service,
// checks its manifest is typed and pulls it back, then discovers it under
// <host>/conformance as a golden path in a complete answer: both registries
// serve _catalog.
func typedBundle(t *testing.T, host string) {
	ctx := t.Context()
	c, err := contract.Parse(strings.NewReader(templateYAML))
	if err != nil {
		t.Fatal(err)
	}
	b := &contract.Bundle{Contract: c, RawYAML: []byte(templateYAML), FS: fstest.MapFS{"pacto.yaml": {Data: []byte(templateYAML)}}}
	client := oci.NewClient(authn.NewMultiKeychain())
	ref := host + "/conformance/paths/http-service:1.0.0"
	if _, err := client.Push(ctx, ref, b); err != nil {
		t.Fatalf("pushing a typed bundle: %v", err)
	}

	repo, err := ocireferrer.Open(host+"/conformance/paths/http-service", ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, rc, err := repo.FetchReference(ctx, "1.0.0")
	if err != nil {
		t.Fatalf("fetching the manifest: %v", err)
	}
	raw, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.ArtifactType != oci.BundleArtifactType || m.Annotations[oci.AnnotationKind] != oci.KindTemplate {
		t.Fatalf("manifest artifactType %q, kind %q", m.ArtifactType, m.Annotations[oci.AnnotationKind])
	}

	pulled, err := client.Pull(ctx, ref)
	if err != nil || pulled.Contract.Service.Name != "http-service" || pulled.Contract.Service.Template == nil {
		t.Fatalf("pulling the typed bundle: %v", err)
	}

	snap, err := app.NewService(client, nil).Fleet(ctx, app.FleetOptions{Discover: []string{host + "/conformance"}})
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	list, err := fleet.NewQuery(snap).Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindService}, Template: fleet.TemplateOnly})
	if err != nil || list.Total != 1 || snap.Completeness != fleet.CompletenessComplete {
		t.Fatalf("golden paths %+v (%v), completeness %s, sources %+v", list, err, snap.Completeness, snap.Sources)
	}
}
