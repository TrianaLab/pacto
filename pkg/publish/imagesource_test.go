package publish

import (
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// labelled is a random image whose config labels the source src.
func labelled(t *testing.T, src string) v1.Image {
	t.Helper()
	img, _ := random.Image(16, 1)
	cfg, _ := img.ConfigFile()
	cfg.Config.Labels = map[string]string{AnnotationSource: src}
	img, err := mutate.ConfigFile(img, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// index is an index of each manifest on its platform ("os/arch").
func index(ms map[string]mutate.Appendable) v1.ImageIndex {
	var idx v1.ImageIndex = empty.Index
	for p, m := range ms {
		os, arch, _ := strings.Cut(p, "/")
		idx = mutate.AppendManifests(idx, mutate.IndexAddendum{Add: m, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: os, Architecture: arch}}})
	}
	return idx
}

// rawManifest is a manifest pushed as it is.
type rawManifest struct {
	mt  types.MediaType
	raw []byte
}

func (r rawManifest) RawManifest() ([]byte, error)        { return r.raw, nil }
func (r rawManifest) MediaType() (types.MediaType, error) { return r.mt, nil }

func TestImageSource(t *testing.T) {
	host, c := testRegistry(t, true)
	ctx := t.Context()
	write := func(a Artifact) string {
		d, err := c.Write(ctx, host+"/acme/app", a)
		if err != nil {
			t.Fatal(err)
		}
		return host + "/acme/app@" + d
	}
	amd, arm := labelled(t, "https://github.com/acme/amd"), labelled(t, "https://github.com/acme/arm")
	annotated := mutate.Annotations(index(map[string]mutate.Appendable{"linux/amd64": amd}), map[string]string{AnnotationSource: "https://github.com/acme/index"}).(v1.ImageIndex)
	for what, c2 := range map[string]struct {
		ref, want string
	}{
		"image label":      {write(amd), "https://github.com/acme/amd"},
		"index annotation": {write(annotated), "https://github.com/acme/index"},
		"amd64 image":      {write(index(map[string]mutate.Appendable{"linux/arm64": arm, "linux/amd64": amd})), "https://github.com/acme/amd"},
		"first image":      {write(index(map[string]mutate.Appendable{"linux/arm64": arm})), "https://github.com/acme/arm"},
		"empty index":      {write(empty.Index), ""},
		"no label":         {write(mustRandom(t)), ""},
	} {
		if got, err := c.ImageSource(ctx, c2.ref); err != nil || got != c2.want {
			t.Errorf("%s: %q %v", what, got, err)
		}
	}
	// An absent image, a nested index with no image for this platform and a
	// manifest whose config is missing are errors, never an empty source.
	nested := write(index(map[string]mutate.Appendable{"windows/arm64": index(map[string]mutate.Appendable{"linux/s390x": arm})}))
	missing := host + "/acme/app@sha256:" + "0000000000000000000000000000000000000000000000000000000000000000"
	noConfig := host + "/acme/app:noconfig"
	raw := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:1111111111111111111111111111111111111111111111111111111111111111","size":2},"layers":[]}`)
	r, _ := name.ParseReference(noConfig, name.Insecure)
	if err := remote.Put(r, rawManifest{types.OCIManifestSchema1, raw}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{missing, nested, noConfig} {
		if got, err := c.ImageSource(ctx, ref); err == nil {
			t.Errorf("%s: %q and no error", ref, got)
		}
	}
}

func mustRandom(t *testing.T) v1.Image {
	t.Helper()
	img, _ := random.Image(16, 1)
	return img
}
