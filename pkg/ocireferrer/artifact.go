// Package ocireferrer is Pacto's one write path for OCI 1.1 referrers: evidence
// records and delivery operation records both go through it. It opens a
// repository with the native Referrers API required (no tag fallback), builds a
// deterministic referrer manifest, pushes it and confirms the Referrers API
// lists it before anyone calls the write done.
package ocireferrer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
)

// ManifestSchemaVersion is the only OCI image-manifest schema version Pacto
// writes. image-spec exports no constant for it.
const ManifestSchemaVersion = 2

// Artifact is one referrer: the payload blob, the empty config blob every OCI
// image manifest requires, and the manifest binding them to a subject. Every
// field is a deterministic function of the inputs, so writing the same content
// twice yields the same digest instead of a second copy.
type Artifact struct {
	Payload      []byte
	PayloadDesc  ocispec.Descriptor
	Config       []byte
	ConfigDesc   ocispec.Descriptor
	Manifest     []byte
	ManifestDesc ocispec.Descriptor
}

// Build renders payload as a one-layer OCI 1.1 artifact of artifactType. A nil
// subject builds a plain manifest (a stream anchor); annotations may be nil.
func Build(artifactType, layerMediaType string, payload []byte, subject *ocispec.Descriptor, annotations map[string]string) Artifact {
	payloadDesc := Descriptor(layerMediaType, payload)
	// The canonical OCI empty-JSON descriptor. Both the blob and the inline copy
	// are cloned: a struct copy would leave Data aliasing image-spec's
	// package-level slice, so a caller writing through the returned Artifact
	// could corrupt every later build.
	configDesc := ocispec.DescriptorEmptyJSON
	configDesc.Data = bytes.Clone(configDesc.Data)
	config := bytes.Clone(configDesc.Data)

	// Built by hand rather than with oras.PackManifest: that helper stamps an
	// org.opencontainers.image.created annotation, which would give the same
	// content a different digest on every write.
	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: ManifestSchemaVersion},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config:       configDesc,
		Layers:       []ocispec.Descriptor{payloadDesc},
		Subject:      subject,
		Annotations:  annotations,
	}
	manifestBytes, _ := json.Marshal(manifest) // descriptors and strings; marshalling cannot fail
	manifestDesc := Descriptor(ocispec.MediaTypeImageManifest, manifestBytes)
	manifestDesc.ArtifactType = artifactType
	manifestDesc.Annotations = annotations
	return Artifact{
		Payload: payload, PayloadDesc: payloadDesc,
		Config: config, ConfigDesc: configDesc,
		Manifest: manifestBytes, ManifestDesc: manifestDesc,
	}
}

// Descriptor is the content-addressed descriptor of exactly these bytes.
func Descriptor(mediaType string, data []byte) ocispec.Descriptor {
	return ocispec.Descriptor{MediaType: mediaType, Digest: digest.FromBytes(data), Size: int64(len(data))}
}

// Push writes art: blobs first, then the manifest that references them.
func Push(ctx context.Context, repo *remote.Repository, art Artifact) error {
	blobs := []struct {
		desc ocispec.Descriptor
		data []byte
	}{{art.ConfigDesc, art.Config}, {art.PayloadDesc, art.Payload}}
	for _, b := range blobs {
		if err := repo.Push(ctx, b.desc, bytes.NewReader(b.data)); err != nil {
			return err
		}
	}
	return repo.Push(ctx, art.ManifestDesc, bytes.NewReader(art.Manifest))
}

// Confirm checks that the Referrers API already lists want among the referrers
// of subject. A listing that cannot be enumerated and a listing that omits the
// artifact are the same answer: it is not discoverable.
func Confirm(ctx context.Context, repo *remote.Repository, subject, want ocispec.Descriptor) error {
	found := false
	err := repo.Referrers(ctx, subject, "", func(page []ocispec.Descriptor) error {
		for _, d := range page {
			found = found || d.Digest == want.Digest
		}
		return nil
	})
	if err == nil && !found {
		err = errors.New("the Referrers API did not list it")
	}
	if err != nil {
		return fmt.Errorf("the published artifact %s is not discoverable: %w", want.Digest, err)
	}
	return nil
}

// Enumerate walks subject's Referrers listing without fetching anything: the
// cheapest proof that native discovery works.
func Enumerate(ctx context.Context, repo *remote.Repository, subject ocispec.Descriptor) error {
	return repo.Referrers(ctx, subject, "", func([]ocispec.Descriptor) error { return nil })
}
