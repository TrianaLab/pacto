package deliveryrecord

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

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
