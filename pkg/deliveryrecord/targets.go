package deliveryrecord

import (
	"context"
	"fmt"
	"path"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// PushTargetSnapshot writes a signed target snapshot bundle
// (delivery.SignTargetSnapshot) to repo as a plain one-layer manifest and tags
// it cluster-<uid>. The tag is the one mutable pointer Pacto writes: one
// writer per cluster, and a reader refuses a stale asOf, so re-pointing it to
// an older snapshot reads stale. It writes only to a pacto-targets repository.
func PushTargetSnapshot(ctx context.Context, repo *remote.Repository, cluster string, bundle []byte) (ocispec.Descriptor, error) {
	if name := path.Base(repo.Reference.Repository); name != delivery.TargetSnapshotRepository {
		return ocispec.Descriptor{}, fmt.Errorf("repository %q: a target snapshot is written only to %s", repo.Reference.Repository, delivery.TargetSnapshotRepository)
	}
	if len(bundle) > delivery.MaxTargetSnapshotBytes {
		return ocispec.Descriptor{}, fmt.Errorf("target snapshot is %d bytes, over the %d byte bound", len(bundle), delivery.MaxTargetSnapshotBytes)
	}
	tag, err := delivery.TargetSnapshotTag(cluster)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	art := ocireferrer.Build(delivery.TargetSnapshotArtifactType, delivery.BundleMediaType, bundle, nil, nil)
	if err := ocireferrer.Push(ctx, repo, art); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := repo.Tag(ctx, art.ManifestDesc, tag); err != nil {
		return ocispec.Descriptor{}, err
	}
	return art.ManifestDesc, nil
}
