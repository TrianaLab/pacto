package deliveryrecord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
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

// ErrTargetSnapshotBound wraps every target snapshot read that hit a bound: a
// manifest or a layer too large.
var ErrTargetSnapshotBound = errors.New("target snapshot over its bound")

const (
	// maxTargetSnapshotManifest bounds a snapshot manifest: one layer, no more.
	maxTargetSnapshotManifest = 64 << 10
	// maxTargetSnapshotTagPages bounds one tag listing of a pacto-targets
	// repository.
	maxTargetSnapshotTagPages = 10
)

// errTagPages stops a tag listing past its page bound.
var errTagPages = errors.New("tag pages bound")

// ListTargetSnapshotTags lists the cluster-* tags of repo, sorted: at most
// delivery.MaxTargetSnapshots of them over at most 10 pages. truncated says
// it stopped at either bound, so some clusters were not listed.
func ListTargetSnapshotTags(ctx context.Context, repo *remote.Repository) (tags []string, truncated bool, err error) {
	tags, truncated, err = ListClusterTags(ctx, repo)
	if len(tags) > delivery.MaxTargetSnapshots {
		tags, truncated = tags[:delivery.MaxTargetSnapshots], true
	}
	return tags, truncated, err
}

// ListClusterTags lists every cluster-* tag of repo over at most 10 pages,
// sorted. truncated says it stopped at the page bound.
func ListClusterTags(ctx context.Context, repo *remote.Repository) (tags []string, truncated bool, err error) {
	pages := 0
	err = repo.Tags(ctx, "", func(page []string) error {
		if pages++; pages > maxTargetSnapshotTagPages {
			return errTagPages
		}
		for _, t := range page {
			if strings.HasPrefix(t, delivery.TargetSnapshotTagPrefix) {
				tags = append(tags, t)
			}
		}
		return nil
	})
	if errors.Is(err, errTagPages) {
		err, truncated = nil, true
	}
	if err != nil {
		return nil, false, err
	}
	slices.Sort(tags)
	return tags, truncated, nil
}

// FetchTargetSnapshot resolves reference (a cluster-<uid> tag or a digest) in
// repo and returns the bundle its manifest carries, digest-verified, and the
// manifest's descriptor. The manifest must be at most 64 KiB, of the target
// snapshot artifact type, with one bundle layer of at most
// delivery.MaxTargetSnapshotBytes; a bound it breaks wraps
// ErrTargetSnapshotBound. The bundle is unverified: the caller opens it.
func FetchTargetSnapshot(ctx context.Context, repo *remote.Repository, reference string) ([]byte, ocispec.Descriptor, error) {
	desc, err := repo.Resolve(ctx, reference)
	if err != nil {
		return nil, ocispec.Descriptor{}, err
	}
	if desc.Size > maxTargetSnapshotManifest {
		return nil, desc, fmt.Errorf("%w: manifest of %d bytes", ErrTargetSnapshotBound, desc.Size)
	}
	raw, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		return nil, desc, err
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, desc, fmt.Errorf("manifest: %w", err)
	}
	switch {
	case m.ArtifactType != delivery.TargetSnapshotArtifactType:
		return nil, desc, fmt.Errorf("artifact type %q, want %s", m.ArtifactType, delivery.TargetSnapshotArtifactType)
	case len(m.Layers) != 1 || m.Layers[0].MediaType != delivery.BundleMediaType:
		return nil, desc, errors.New("not one bundle layer")
	case m.Layers[0].Size > delivery.MaxTargetSnapshotBytes:
		return nil, desc, fmt.Errorf("%w: layer of %d bytes", ErrTargetSnapshotBound, m.Layers[0].Size)
	}
	bundle, err := content.FetchAll(ctx, repo, m.Layers[0])
	return bundle, desc, err
}
