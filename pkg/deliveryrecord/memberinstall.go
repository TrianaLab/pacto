package deliveryrecord

import (
	"context"
	"fmt"
	"path"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// PushMemberInstall writes a generated member install (delivery.MemberInstall
// files as delivery.InstallArchive) to repo as a plain one-layer manifest and
// tags it MemberInstallTag, so a push of other content never rewrites a tag.
// It writes only to a pacto-targets repository; every generated reference to
// it pins the returned digest.
func PushMemberInstall(ctx context.Context, repo *remote.Repository, engine string, tgz []byte) (ocispec.Descriptor, error) {
	if name := path.Base(repo.Reference.Repository); name != delivery.TargetSnapshotRepository {
		return ocispec.Descriptor{}, fmt.Errorf("repository %q: a member install is written only to %s", repo.Reference.Repository, delivery.TargetSnapshotRepository)
	}
	art := ocireferrer.Build(delivery.MemberInstallArtifactType, delivery.MemberInstallLayerType, tgz, nil, nil)
	if err := ocireferrer.Push(ctx, repo, art); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := repo.Tag(ctx, art.ManifestDesc, MemberInstallTag(engine, art.ManifestDesc.Digest)); err != nil {
		return ocispec.Descriptor{}, err
	}
	return art.ManifestDesc, nil
}

// MemberInstallTag is members-<engine>-<first 12 hex of d>.
func MemberInstallTag(engine string, d digest.Digest) string {
	return delivery.MemberInstallTagPrefix + engine + "-" + d.Encoded()[:12]
}
