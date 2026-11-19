package deliveryrecord

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

func targetsRepo(t *testing.T) (string, string) {
	t.Helper()
	_, path := setup(t, testutil.ReferrersOptions{})
	host, _, _ := strings.Cut(path, "/")
	return host, host + "/acme/" + delivery.TargetSnapshotRepository
}

// TestPushTargetSnapshotTagsPlainManifest: the snapshot is a plain manifest
// with no subject, one bundle layer, tagged cluster-<uid>.
func TestPushTargetSnapshotTagsPlainManifest(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	_, path := targetsRepo(t)
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	ctx := t.Context()
	bundle, err := delivery.SignTargetSnapshot(ctx, edSigner(key), delivery.NewTargetSnapshotStatement(delivery.TargetSnapshot{
		Cluster: "c-6f1e", AsOf: "2026-11-17T10:00:00Z", Completeness: fleet.CompletenessComplete, Publisher: "key:x"}))
	if err != nil {
		t.Fatal(err)
	}
	desc, err := PushTargetSnapshot(ctx, repo, "c-6f1e", bundle)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.Resolve(ctx, "cluster-c-6f1e")
	if err != nil || got.Digest != desc.Digest {
		t.Fatalf("tag = %v, %v; pushed %v", got, err, desc.Digest)
	}
	rc, err := repo.Fetch(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m)
	if m.Subject != nil || m.ArtifactType != delivery.TargetSnapshotArtifactType || len(m.Layers) != 1 || m.Layers[0].MediaType != delivery.BundleMediaType {
		t.Fatalf("manifest = %s", raw)
	}
	if _, err := PushTargetSnapshot(ctx, repo, "a/b", bundle); err == nil {
		t.Fatal("a cluster that cannot sit in a tag was pushed")
	}
}

func TestPushTargetSnapshotRefusesOversize(t *testing.T) {
	_, path := targetsRepo(t)
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	_, err := PushTargetSnapshot(t.Context(), repo, "c-6f1e", make([]byte, delivery.MaxTargetSnapshotBytes+1))
	if err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatalf("err = %v", err)
	}
}

func TestPushTargetSnapshotRefusesOtherRepository(t *testing.T) {
	host, _ := targetsRepo(t)
	repo, _ := ocireferrer.Open(host+"/acme/streams", ocireferrer.Options{})
	_, err := PushTargetSnapshot(t.Context(), repo, "c-6f1e", []byte("{}"))
	if err == nil || !strings.Contains(err.Error(), "only to pacto-targets") {
		t.Fatalf("err = %v", err)
	}
}

// TestPushTargetSnapshotRegistryErrors: a push or tag the registry refuses is
// an error.
func TestPushTargetSnapshotRegistryErrors(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{})
	host, _, _ := strings.Cut(path, "/")
	f.tagStatus = map[string]int{"cluster-c-6f1e": 400}
	repo, _ := ocireferrer.Open(host+"/acme/pacto-targets", ocireferrer.Options{})
	if _, err := PushTargetSnapshot(t.Context(), repo, "c-6f1e", []byte("{}")); err == nil {
		t.Fatal("a refused tag succeeded")
	}
	down, _ := ocireferrer.Open("127.0.0.1:1/acme/pacto-targets", ocireferrer.Options{Insecure: []string{"127.0.0.1:1"}})
	if _, err := PushTargetSnapshot(t.Context(), down, "c-6f1e", []byte("{}")); err == nil {
		t.Fatal("a push to no registry succeeded")
	}
}

// tagManifest pushes raw as a manifest and tags it.
func tagManifest(t *testing.T, repo *remote.Repository, raw []byte, tag string) {
	t.Helper()
	desc := ocireferrer.Descriptor(ocispec.MediaTypeImageManifest, raw)
	if err := repo.Manifests().PushReference(t.Context(), desc, bytes.NewReader(raw), tag); err != nil {
		t.Fatal(err)
	}
}

func TestListTargetSnapshotTagsKeepsClusterTags(t *testing.T) {
	_, path := targetsRepo(t)
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	for _, tag := range []string{"cluster-b", "latest", "cluster-a", "v1"} {
		tagManifest(t, repo, []byte(`{"schemaVersion":2,"mediaType":"`+ocispec.MediaTypeImageManifest+`","annotations":{"t":"`+tag+`"}}`), tag)
	}
	tags, truncated, err := ListTargetSnapshotTags(t.Context(), repo)
	if err != nil || truncated || !slices.Equal(tags, []string{"cluster-a", "cluster-b"}) {
		t.Fatalf("tags = %v, %v, %v", tags, truncated, err)
	}
	down, _ := ocireferrer.Open("127.0.0.1:1/acme/pacto-targets", ocireferrer.Options{})
	if _, _, err := ListTargetSnapshotTags(t.Context(), down); err == nil {
		t.Fatal("no registry listed tags")
	}
}

// TestListTargetSnapshotTagsTruncates: 33 cluster tags keep the first 32, and
// a listing past 10 pages stops; both say they were cut.
func TestListTargetSnapshotTagsTruncates(t *testing.T) {
	_, path := setup(t, testutil.ReferrersOptions{TagsLink: true})
	host, _, _ := strings.Cut(path, "/")
	repo, _ := ocireferrer.Open(host+"/acme/pacto-targets", ocireferrer.Options{})
	for i := range delivery.MaxTargetSnapshots + 1 {
		tag := fmt.Sprintf("cluster-%02d", i)
		tagManifest(t, repo, []byte(`{"schemaVersion":2,"mediaType":"`+ocispec.MediaTypeImageManifest+`","annotations":{"t":"`+tag+`"}}`), tag)
	}
	tags, truncated, err := ListTargetSnapshotTags(t.Context(), repo)
	if err != nil || !truncated || len(tags) != delivery.MaxTargetSnapshots || tags[0] != "cluster-00" {
		t.Fatalf("tags = %v, %v, %v", tags, truncated, err)
	}
	repo.TagListPageSize = 1
	tags, truncated, err = ListTargetSnapshotTags(t.Context(), repo)
	if err != nil || !truncated || len(tags) != maxTargetSnapshotTagPages {
		t.Fatalf("paged tags = %v, %v, %v", tags, truncated, err)
	}
}

// TestFetchTargetSnapshotBounds: a pushed snapshot reads back by tag and by
// digest; an oversized manifest or layer is a bound, a wrong artifact type or
// layer count is refused, and a missing tag or a broken manifest is an error.
func TestFetchTargetSnapshotBounds(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	f, path := setup(t, testutil.ReferrersOptions{})
	host, _, _ := strings.Cut(path, "/")
	repo, _ := ocireferrer.Open(host+"/acme/pacto-targets", ocireferrer.Options{})
	ctx := t.Context()
	bundle, _ := delivery.SignTargetSnapshot(ctx, edSigner(key), delivery.NewTargetSnapshotStatement(delivery.TargetSnapshot{
		Cluster: "c1", AsOf: "2026-11-17T10:00:00Z", Completeness: fleet.CompletenessComplete, Publisher: "key:x"}))
	pushed, err := PushTargetSnapshot(ctx, repo, "c1", bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"cluster-c1", pushed.Digest.String()} {
		got, desc, err := FetchTargetSnapshot(ctx, repo, ref)
		if err != nil || !bytes.Equal(got, bundle) || desc.Digest != pushed.Digest {
			t.Fatalf("%s: fetched %d bytes, %v, %v", ref, len(got), desc.Digest, err)
		}
	}
	layer := func(mediaType string, size int64) string {
		return fmt.Sprintf(`{"mediaType":%q,"digest":"sha256:%064d","size":%d}`, mediaType, 0, size)
	}
	manifest := func(artifactType string, layers ...string) []byte {
		return []byte(`{"schemaVersion":2,"mediaType":"` + ocispec.MediaTypeImageManifest + `","artifactType":"` + artifactType +
			`","config":` + layer(ocispec.MediaTypeEmptyJSON, 2) + `,"layers":[` + strings.Join(layers, ",") + `]}`)
	}
	at := delivery.TargetSnapshotArtifactType
	for tag, c := range map[string]struct {
		raw   []byte
		bound bool
		want  string
	}{
		"cluster-big":    {raw: manifest(at, layer(delivery.BundleMediaType, 1), `{"mediaType":"x","digest":"sha256:`+strings.Repeat("0", 64)+`","size":1,"annotations":{"pad":"`+strings.Repeat("x", 64<<10)+`"}}`), bound: true},
		"cluster-layer":  {raw: manifest(at, layer(delivery.BundleMediaType, delivery.MaxTargetSnapshotBytes+1)), bound: true},
		"cluster-type":   {raw: manifest("application/other", layer(delivery.BundleMediaType, 1)), want: "artifact type"},
		"cluster-two":    {raw: manifest(at, layer(delivery.BundleMediaType, 1), layer(delivery.BundleMediaType, 1)), want: "one bundle layer"},
		"cluster-broken": {raw: []byte(`[]`), want: "manifest"},
	} {
		tagManifest(t, repo, c.raw, tag)
		_, _, err := FetchTargetSnapshot(ctx, repo, tag)
		if err == nil || errors.Is(err, ErrTargetSnapshotBound) != c.bound || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v", tag, err)
		}
	}
	if _, _, err := FetchTargetSnapshot(ctx, repo, "cluster-gone"); err == nil {
		t.Fatal("a missing tag fetched")
	}
	f.manifest.Store(true)
	if _, _, err := FetchTargetSnapshot(ctx, repo, "cluster-c1"); err == nil {
		t.Fatal("a refused manifest fetched")
	}
}
