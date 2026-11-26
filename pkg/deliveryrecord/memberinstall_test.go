package deliveryrecord

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

func TestPushMemberInstallRefusesOtherRepository(t *testing.T) {
	host, _ := targetsRepo(t)
	for _, path := range []string{host + "/acme/pacto-index", host + "/acme/pacto-targets-x"} {
		repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
		if _, err := PushMemberInstall(t.Context(), repo, "flux", []byte("x")); err == nil || !strings.Contains(err.Error(), "only to pacto-targets") {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestPushMemberInstallOneLayerMediaType(t *testing.T) {
	_, path := targetsRepo(t)
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	tgz := delivery.InstallArchive([]delivery.InstallFile{{Path: "a.yaml", Content: "a: b\n"}})
	desc, err := PushMemberInstall(t.Context(), repo, "argocd", tgz)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := repo.Fetch(t.Context(), desc)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m)
	if m.Subject != nil || m.ArtifactType != delivery.MemberInstallArtifactType || len(m.Layers) != 1 ||
		m.Layers[0].MediaType != ocispec.MediaTypeImageLayerGzip {
		t.Fatalf("manifest %s", raw)
	}
	layer, _ := repo.Fetch(t.Context(), m.Layers[0])
	got, _ := io.ReadAll(layer)
	_ = layer.Close()
	if string(got) != string(tgz) {
		t.Fatal("the layer is not the archive")
	}
}

// TestMembersTagCarriesDigest: the tag names the engine and the digest, and
// a second push of other content leaves the first tag where it was.
func TestMembersTagCarriesDigest(t *testing.T) {
	_, path := targetsRepo(t)
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	ctx := t.Context()
	first, err := PushMemberInstall(ctx, repo, "flux", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	tag := MemberInstallTag("flux", first.Digest)
	if tag != "members-flux-"+first.Digest.Encoded()[:12] {
		t.Fatalf("tag %s", tag)
	}
	second, err := PushMemberInstall(ctx, repo, "flux", []byte("two"))
	if err != nil || second.Digest == first.Digest {
		t.Fatalf("%v %v", second, err)
	}
	if got, err := repo.Resolve(ctx, tag); err != nil || got.Digest != first.Digest {
		t.Fatalf("first tag now %v, %v", got.Digest, err)
	}
}

// TestPushMemberInstallRegistryErrors: a push or tag the registry refuses
// is an error.
func TestPushMemberInstallRegistryErrors(t *testing.T) {
	f, path := setup(t, testutil.ReferrersOptions{})
	host, _, _ := strings.Cut(path, "/")
	repo, _ := ocireferrer.Open(host+"/acme/pacto-targets", ocireferrer.Options{})
	art := ocireferrer.Build(delivery.MemberInstallArtifactType, delivery.MemberInstallLayerType, []byte("x"), nil, nil)
	f.tagStatus = map[string]int{MemberInstallTag("argocd", art.ManifestDesc.Digest): 400}
	if _, err := PushMemberInstall(t.Context(), repo, "argocd", []byte("x")); err == nil {
		t.Fatal("a refused tag succeeded")
	}
	down, _ := ocireferrer.Open("127.0.0.1:1/acme/pacto-targets", ocireferrer.Options{Insecure: []string{"127.0.0.1:1"}})
	if _, err := PushMemberInstall(t.Context(), down, "argocd", []byte("x")); err == nil {
		t.Fatal("a push to no registry succeeded")
	}
}

// TestMembersTagsNeverListedAsSnapshots: a pushed member install is never
// read as a cluster's snapshot.
func TestMembersTagsNeverListedAsSnapshots(t *testing.T) {
	_, path := targetsRepo(t)
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	for _, engine := range []string{"argocd", "flux"} {
		if _, err := PushMemberInstall(t.Context(), repo, engine, []byte(engine)); err != nil {
			t.Fatal(err)
		}
	}
	tags, truncated, err := ListTargetSnapshotTags(t.Context(), repo)
	if err != nil || truncated || len(tags) != 0 {
		t.Fatalf("tags %v %v %v", tags, truncated, err)
	}
}
