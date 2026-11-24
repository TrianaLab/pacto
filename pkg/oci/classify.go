package oci

import (
	"bytes"
	"errors"
	"fmt"

	msemver "github.com/Masterminds/semver/v3"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// The bounds a registry listing reads a candidate bundle's manifest and config
// with. A caller checks a descriptor's size against them before any fetch,
// since whoever pushes the artifact chooses both sizes.
const (
	MaxBundleManifestBytes = 16 << 10
	MaxBundleConfigBytes   = 64 << 10
)

// ErrNotBundle reports an artifact that is no Pacto bundle.
var ErrNotBundle = errors.New("not a Pacto bundle")

// kindFieldsVersion is the first pactoVersion with service.template and
// service.offering, so an older bundle is always a service.
var kindFieldsVersion = msemver.MustParse("2.4")

// ClassifyManifest reads a bundle's kind from its raw manifest. A typed bundle
// decides from io.pacto.kind (missing or unknown reads KindService); any other
// artifactType is ErrNotBundle; an untyped manifest with a bundle layer is a
// bundle whose kind is undecided (decided false); anything else is ErrNotBundle.
// The kind is whatever the pusher wrote: it picks candidates, never proves one.
func ClassifyManifest(raw []byte) (kind string, decided bool, err error) {
	if len(raw) > MaxBundleManifestBytes {
		return "", false, fmt.Errorf("manifest exceeds %d bytes", MaxBundleManifestBytes)
	}
	m, err := v1.ParseManifest(bytes.NewReader(raw))
	if err != nil {
		return "", false, err
	}
	switch m.ArtifactType {
	case BundleArtifactType:
		switch k := m.Annotations[AnnotationKind]; k {
		case KindTemplate, KindOffering:
			return k, true, nil
		default:
			return KindService, true, nil
		}
	case "":
		for _, l := range m.Layers {
			if l.MediaType == LayerMediaType {
				return "", false, nil
			}
		}
	}
	return "", false, ErrNotBundle
}

// KindFromConfig reads an untyped bundle's kind from its raw config labels. No
// io.pacto.name label is ErrNotBundle; a pactoVersion before 2.4 is
// KindService; 2.4 and later is undecided, and only the pulled contract tells.
func KindFromConfig(raw []byte) (kind string, decided bool, err error) {
	if len(raw) > MaxBundleConfigBytes {
		return "", false, fmt.Errorf("config exceeds %d bytes", MaxBundleConfigBytes)
	}
	cf, err := v1.ParseConfigFile(bytes.NewReader(raw))
	if err != nil {
		return "", false, err
	}
	labels := cf.Config.Labels
	if labels["io.pacto.name"] == "" {
		return "", false, ErrNotBundle
	}
	v, err := msemver.NewVersion(labels["io.pacto.pactoVersion"])
	if err != nil {
		return "", false, fmt.Errorf("bundle pactoVersion: %w", err)
	}
	if v.LessThan(kindFieldsVersion) {
		return KindService, true, nil
	}
	return "", false, nil
}
