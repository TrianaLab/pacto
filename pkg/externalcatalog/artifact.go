package externalcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// The OCI artifact that carries a catalog: an image manifest with an artifact
// type, the OCI empty config and one layer holding the catalog YAML. go-
// containerregistry has no artifactType mutator, so the manifest is built by
// hand; its bytes depend only on the catalog bytes, so its digest does too.
const (
	ArtifactType     = "application/vnd.pacto.external-catalog.v1"
	LayerMediaType   = "application/vnd.pacto.external-catalog.v1+yaml"
	EmptyMediaType   = "application/vnd.oci.empty.v1+json"
	EmptyDigest      = "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	emptyConfigBytes = "{}"
)

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	ArtifactType  string       `json:"artifactType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Manifest returns the OCI image manifest bytes for a catalog.
func Manifest(b []byte) []byte {
	out, _ := json.Marshal(manifest{ // a struct of strings and ints always marshals
		SchemaVersion: 2,
		MediaType:     string(types.OCIManifestSchema1),
		ArtifactType:  ArtifactType,
		Config:        descriptor{MediaType: EmptyMediaType, Digest: EmptyDigest, Size: int64(len(emptyConfigBytes))},
		Layers:        []descriptor{{MediaType: LayerMediaType, Digest: sha256Digest(b), Size: int64(len(b))}},
	})
	return out
}

// Digest returns the digest of the artifact that carries the catalog bytes b.
func Digest(b []byte) string { return sha256Digest(Manifest(b)) }

// rawManifest lets remote.Put write the hand-built manifest as is.
type rawManifest []byte

func (m rawManifest) RawManifest() ([]byte, error) { return m, nil }

func (m rawManifest) MediaType() (types.MediaType, error) { return types.OCIManifestSchema1, nil }

// Write pushes the catalog bytes b to ref (an oci:// prefix is accepted) and
// returns the artifact digest.
func Write(ctx context.Context, c *oci.Client, ref string, b []byte) (string, error) {
	layers := []v1.Layer{static.NewLayer([]byte(emptyConfigBytes), EmptyMediaType), static.NewLayer(b, LayerMediaType)}
	m := Manifest(b)
	err := c.Do(ctx, contract.TrimOCIScheme(ref), func(r name.Reference, opts []remote.Option) error {
		for _, l := range layers {
			if err := remote.WriteLayer(r.Context(), l, opts...); err != nil {
				return err
			}
		}
		return remote.Put(r, rawManifest(m), opts...)
	})
	if err != nil {
		return "", err
	}
	return sha256Digest(m), nil
}

// Read pulls the catalog bytes from ref (an oci:// prefix is accepted). It
// refuses any manifest other than the catalog artifact and any layer over
// [MaxFileBytes].
func Read(ctx context.Context, c *oci.Client, ref string) ([]byte, error) {
	ref = contract.TrimOCIScheme(ref)
	var (
		raw  []byte
		repo name.Repository
	)
	if err := c.Do(ctx, ref, func(r name.Reference, opts []remote.Option) error {
		d, err := remote.Get(r, opts...)
		if err != nil {
			return err
		}
		raw, repo = d.Manifest, r.Context()
		return nil
	}); err != nil {
		return nil, err
	}
	layer, err := checkManifest(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	var data []byte
	err = c.Do(ctx, ref, func(_ name.Reference, opts []remote.Option) error {
		l, err := remote.Layer(repo.Digest(layer.Digest), opts...)
		if err != nil {
			return err
		}
		rc, err := l.Compressed()
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		data, err = io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%s: catalog layer is over the %d-byte limit", ref, MaxFileBytes)
	}
	return data, nil
}

// checkManifest returns the catalog layer of a catalog artifact manifest.
func checkManifest(raw []byte) (descriptor, error) {
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return descriptor{}, fmt.Errorf("decode manifest: %w", err)
	}
	switch {
	case m.ArtifactType != ArtifactType:
		return descriptor{}, fmt.Errorf("artifactType %q, want %q", m.ArtifactType, ArtifactType)
	case m.Config.MediaType != EmptyMediaType || m.Config.Digest != EmptyDigest:
		return descriptor{}, errors.New("config is not the OCI empty descriptor")
	case len(m.Layers) != 1:
		return descriptor{}, fmt.Errorf("%d layers, want exactly one", len(m.Layers))
	case m.Layers[0].MediaType != LayerMediaType:
		return descriptor{}, fmt.Errorf("layer media type %q, want %q", m.Layers[0].MediaType, LayerMediaType)
	case m.Layers[0].Size > MaxFileBytes:
		return descriptor{}, fmt.Errorf("catalog layer of %d bytes is over the %d-byte limit", m.Layers[0].Size, MaxFileBytes)
	}
	return m.Layers[0], nil
}
