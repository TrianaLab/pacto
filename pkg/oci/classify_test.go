package oci

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func manifestJSON(t *testing.T, artifactType string, anns map[string]string, layer types.MediaType) []byte {
	t.Helper()
	h := v1.Hash{Algorithm: "sha256", Hex: strings.Repeat("a", 64)}
	m := v1.Manifest{SchemaVersion: 2, MediaType: types.OCIManifestSchema1, ArtifactType: artifactType, Annotations: anns,
		Config: v1.Descriptor{MediaType: types.OCIConfigJSON, Size: 1, Digest: h},
		Layers: []v1.Descriptor{{MediaType: layer, Size: 1, Digest: h}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestClassifyManifest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     []byte
		kind    string
		decided bool
		notOurs bool
	}{
		{"typed service", manifestJSON(t, BundleArtifactType, map[string]string{AnnotationKind: KindService}, LayerMediaType), KindService, true, false},
		{"typed template", manifestJSON(t, BundleArtifactType, map[string]string{AnnotationKind: KindTemplate}, LayerMediaType), KindTemplate, true, false},
		{"typed offering", manifestJSON(t, BundleArtifactType, map[string]string{AnnotationKind: KindOffering}, LayerMediaType), KindOffering, true, false},
		{"typed with no kind", manifestJSON(t, BundleArtifactType, nil, LayerMediaType), KindService, true, false},
		{"typed with an unknown kind", manifestJSON(t, BundleArtifactType, map[string]string{AnnotationKind: "robot"}, LayerMediaType), KindService, true, false},
		{"index entry", manifestJSON(t, "application/vnd.pacto.index.entry.v1+json", nil, LayerMediaType), "", false, true},
		{"untyped bundle", manifestJSON(t, "", nil, LayerMediaType), "", false, false},
		{"other image", manifestJSON(t, "", nil, types.OCILayer), "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, decided, err := ClassifyManifest(tc.raw)
			if tc.notOurs != errors.Is(err, ErrNotBundle) || (!tc.notOurs && err != nil) {
				t.Fatalf("err = %v", err)
			}
			if kind != tc.kind || decided != tc.decided {
				t.Fatalf("got %q %v, want %q %v", kind, decided, tc.kind, tc.decided)
			}
		})
	}
	if _, _, err := ClassifyManifest([]byte("{")); err == nil || errors.Is(err, ErrNotBundle) {
		t.Fatalf("an unparsable manifest is an error, got %v", err)
	}
}

func TestClassifyManifestRefusesOversized(t *testing.T) {
	raw := manifestJSON(t, BundleArtifactType, map[string]string{"pad": strings.Repeat("x", MaxBundleManifestBytes)}, LayerMediaType)
	if _, _, err := ClassifyManifest(raw); err == nil || errors.Is(err, ErrNotBundle) {
		t.Fatalf("an oversized manifest is refused, got %v", err)
	}
}

func configJSON(t *testing.T, labels map[string]string) []byte {
	t.Helper()
	raw, err := json.Marshal(v1.ConfigFile{Config: v1.Config{Labels: labels}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestKindFromConfig(t *testing.T) {
	labels := func(v string) map[string]string {
		return map[string]string{"io.pacto.name": "svc", "io.pacto.version": "1.0.0", "io.pacto.pactoVersion": v}
	}
	for _, tc := range []struct {
		name    string
		raw     []byte
		kind    string
		decided bool
		err     bool
		notOurs bool
	}{
		{"no labels", configJSON(t, nil), "", false, true, true},
		{"2.3", configJSON(t, labels("2.3")), KindService, true, false, false},
		{"2.0", configJSON(t, labels("2.0")), KindService, true, false, false},
		{"2.4", configJSON(t, labels("2.4")), "", false, false, false},
		{"2.5", configJSON(t, labels("2.5")), "", false, false, false},
		{"2.10 is newer than 2.4", configJSON(t, labels("2.10")), "", false, false, false},
		{"unparsable version", configJSON(t, labels("two")), "", false, true, false},
		{"unparsable config", []byte("{"), "", false, true, false},
		{"oversized", configJSON(t, map[string]string{"io.pacto.name": strings.Repeat("x", MaxBundleConfigBytes)}), "", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, decided, err := KindFromConfig(tc.raw)
			if (err != nil) != tc.err || errors.Is(err, ErrNotBundle) != tc.notOurs {
				t.Fatalf("err = %v", err)
			}
			if kind != tc.kind || decided != tc.decided {
				t.Fatalf("got %q %v, want %q %v", kind, decided, tc.kind, tc.decided)
			}
		})
	}
}
