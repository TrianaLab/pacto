// Package registry publishes run records as OCI referrers. Run records are
// unsigned statistics.
//
// Experimental: not covered by semantic versioning.
package registry

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// Open opens the stats repository at path ("<registry>/<repository>") with
// the shared keychain and insecure-host list.
func Open(path string, kc authn.Keychain, insecure []string) (*remote.Repository, error) {
	return ocireferrer.Open(path, ocireferrer.Options{Keychain: kc, Insecure: insecure})
}

// Anchor is the anchor every record of service is attached to.
func Anchor(service string) ocireferrer.Artifact {
	payload, _ := json.Marshal(map[string]string{"stats": service}) // a string map; marshalling cannot fail
	return ocireferrer.Build(runrecord.AnchorType, runrecord.AnchorLayerType, payload, nil, nil)
}

// Publish attaches r to its service's anchor in repo, writing and tagging the
// anchor first, and returns the record's manifest descriptor. Publishing the
// same record again changes nothing. There is no Confirm: a record is
// statistics, and a reader sees what the listing holds.
func Publish(ctx context.Context, repo *remote.Repository, r runrecord.Record) (ocispec.Descriptor, error) {
	payload, err := runrecord.Encode(r)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	anchor := Anchor(r.Service)
	if err := ocireferrer.Push(ctx, repo, anchor); err != nil {
		return ocispec.Descriptor{}, err
	}
	if err := repo.Tag(ctx, anchor.ManifestDesc, runrecord.AnchorTag(r.Service)); err != nil {
		return ocispec.Descriptor{}, err
	}
	ann := map[string]string{runrecord.AnnRecordedAt: r.RecordedAt.UTC().Format(time.RFC3339)}
	art := ocireferrer.Build(runrecord.ArtifactType, runrecord.LayerType, payload, &anchor.ManifestDesc, ann)
	if err := ocireferrer.Push(ctx, repo, art); err != nil {
		return ocispec.Descriptor{}, err
	}
	return art.ManifestDesc, nil
}
