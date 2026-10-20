package publish

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// Media types of the artifacts Pacto packages itself.
const (
	FluxConfigType types.MediaType = "application/vnd.cncf.flux.config.v1+json"
	FluxLayerType  types.MediaType = "application/vnd.cncf.flux.content.v1.tar+gzip"
	HelmConfigType types.MediaType = "application/vnd.cncf.helm.config.v1+json"
	HelmLayerType  types.MediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	// SBOMType is the artifactType of the SBOM referrer publish attaches.
	SBOMType = "application/spdx+json"
)

// rawImage is an OCI image manifest Pacto writes byte by byte: one config, one
// layer, annotations and an optional subject. Flux artifacts, Helm charts and
// SBOM referrers are all this shape.
type rawImage struct {
	manifest, config []byte
	layer            v1.Layer
}

func (r rawImage) RawConfigFile() ([]byte, error)                         { return r.config, nil }
func (r rawImage) MediaType() (types.MediaType, error)                    { return types.OCIManifestSchema1, nil }
func (r rawImage) RawManifest() ([]byte, error)                           { return r.manifest, nil }
func (r rawImage) LayerByDigest(v1.Hash) (partial.CompressedLayer, error) { return r.layer, nil }

func describe(mt types.MediaType, b []byte) v1.Descriptor {
	h, size, _ := v1.SHA256(bytes.NewReader(b))
	return v1.Descriptor{MediaType: mt, Size: size, Digest: h}
}

// newArtifact builds the one-layer image. The config blob is never asked for
// through LayerByDigest (remote writes it from RawConfigFile), so the one
// layer answers every digest.
func newArtifact(artifactType string, configType types.MediaType, config []byte, layerType types.MediaType, layer []byte,
	annotations map[string]string, subject *v1.Descriptor) v1.Image {
	m := v1.Manifest{SchemaVersion: 2, MediaType: types.OCIManifestSchema1, ArtifactType: artifactType,
		Config: describe(configType, config), Layers: []v1.Descriptor{describe(layerType, layer)},
		Annotations: annotations, Subject: subject}
	b, _ := json.Marshal(m)
	img, _ := partial.CompressedToImage(rawImage{manifest: b, config: config, layer: static.NewLayer(layer, layerType)})
	return img
}

// sbomArtifact is an SBOM as a referrer of subject. The config media type is
// the artifact type too, so a registry that reports a referrer's type from its
// config (older ones do) lists it the same way.
func sbomArtifact(sbom []byte, subject v1.Descriptor, annotations map[string]string) v1.Image {
	return newArtifact(SBOMType, SBOMType, []byte("{}"), SBOMType, sbom, annotations, &subject)
}

// load turns what a builder wrote to dir into the artifact Pacto pushes, with
// annotations on its top-level manifest.
func load(kind, dir string, annotations map[string]string) (Artifact, error) {
	switch kind {
	case contract.ArtifactImage:
		return loadImage(dir, annotations)
	case contract.ArtifactChart:
		return loadChart(dir, annotations)
	}
	return loadManifests(dir, annotations)
}

// loadImage reads the one image or image index of an OCI image layout.
func loadImage(dir string, annotations map[string]string) (Artifact, error) {
	idx, err := layout.ImageIndexFromPath(dir)
	if err != nil {
		return nil, fmt.Errorf("the build did not write an OCI image layout to $PACTO_OUT: %w", err)
	}
	m, err := idx.IndexManifest()
	if err != nil {
		return nil, err
	}
	if len(m.Manifests) != 1 {
		return nil, fmt.Errorf("the OCI image layout holds %d manifests; publish needs exactly one image or image index", len(m.Manifests))
	}
	d := m.Manifests[0]
	if d.MediaType.IsIndex() {
		child, err := idx.ImageIndex(d.Digest)
		if err != nil {
			return nil, err
		}
		return mutate.Annotations(child, annotations).(v1.ImageIndex), nil
	}
	img, err := idx.Image(d.Digest)
	if err != nil {
		return nil, err
	}
	return mutate.Annotations(img, annotations).(v1.Image), nil
}

// loadChart packages the one chart .tgz in dir as a Helm OCI chart, its
// Chart.yaml as the config.
func loadChart(dir string, annotations map[string]string) (Artifact, error) {
	tgzs, _ := filepath.Glob(filepath.Join(dir, "*.tgz"))
	if len(tgzs) != 1 {
		return nil, fmt.Errorf("the chart build wrote %d .tgz files to $PACTO_OUT; publish needs exactly one", len(tgzs))
	}
	tgz, err := os.ReadFile(tgzs[0])
	if err != nil {
		return nil, err
	}
	meta, err := chartMetadata(tgz)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(tgzs[0]), err)
	}
	return newArtifact("", HelmConfigType, meta, HelmLayerType, tgz, annotations, nil), nil
}

// chartMetadata is the chart's Chart.yaml as JSON.
func chartMetadata(tgz []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no Chart.yaml in the chart")
		}
		if err != nil {
			return nil, err
		}
		if parts := strings.Split(h.Name, "/"); len(parts) != 2 || parts[1] != "Chart.yaml" {
			continue
		}
		var meta map[string]any
		if err := yaml.NewDecoder(tr).Decode(&meta); err != nil {
			return nil, fmt.Errorf("Chart.yaml: %w", err)
		}
		return json.Marshal(meta)
	}
}

// loadManifests packages dir as a Flux artifact: one deterministic tar.gz, so
// the same files always publish under the same digest.
func loadManifests(dir string, annotations map[string]string) (Artifact, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("the manifests build wrote nothing to $PACTO_OUT")
	}
	tgz, err := oci.BundleToTarGz(os.DirFS(dir))
	if err != nil {
		return nil, err
	}
	return newArtifact("", FluxConfigType, []byte("{}"), FluxLayerType, tgz, annotations, nil), nil
}
