package oci

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// legacyTestDigest is BundleDigest(testBundle()) on the tree before bundles
// were typed, so the legacy packer is proved byte-identical to the old one.
const legacyTestDigest = "sha256:5f64a6133fab1ef520030d1d95ad90b8177a6942391acd295ac41c8f7a6538f2"

// typedTestDigest is what a push of testBundle() writes now.
const typedTestDigest = "sha256:3339a97305e74bbd08537cd8b5af7e5f2abd5852a39d0f84c2f2fbdf07441e42"

func TestBundleDigestsPinned(t *testing.T) {
	ds, err := BundleDigests(testBundle())
	if err != nil {
		t.Fatal(err)
	}
	if ds.Typed != typedTestDigest || ds.Legacy != legacyTestDigest {
		t.Fatalf("digests moved: %+v", ds)
	}
	if d, _ := BundleDigest(testBundle()); d != ds.Typed {
		t.Fatalf("BundleDigest %s is not the typed digest %s", d, ds.Typed)
	}
	if _, err := BundleDigests(&contract.Bundle{}); err == nil {
		t.Fatal("an empty bundle has no digests")
	}
}

func TestLegacyDigestIsTheOldPacker(t *testing.T) {
	img, err := LegacyBundleImage(testBundle())
	if err != nil {
		t.Fatal(err)
	}
	d, err := drainedDigest(img)
	if err != nil || d != legacyTestDigest {
		t.Fatalf("legacy digest %s (%v), want %s", d, err, legacyTestDigest)
	}
	m, _ := img.Manifest()
	if m.ArtifactType != "" || len(m.Annotations) != 0 {
		t.Fatalf("the legacy image is typed: %+v", m)
	}
}

// kindBundle is testBundle() declaring pactoVersion 2.4 and the given kind.
func kindBundle(kind string) *contract.Bundle {
	b := testBundle()
	c := *b.Contract
	c.PactoVersion = "2.4"
	switch kind {
	case KindTemplate:
		c.Service.Template = &contract.Template{}
	case KindOffering:
		c.Service.Offering = "database"
	}
	b.Contract = &c
	return b
}

func TestTypedBundleManifest(t *testing.T) {
	for _, kind := range []string{KindService, KindTemplate, KindOffering} {
		t.Run(kind, func(t *testing.T) {
			img, err := BundleImage(kindBundle(kind))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := drainedDigest(img); err != nil {
				t.Fatal(err)
			}
			checkTypedManifest(t, img, kind)
			checkBundleLabels(t, img)
		})
	}
}

// checkTypedManifest asserts img's manifest is typed as kind and that its
// digest and size are those of the raw manifest.
func checkTypedManifest(t *testing.T, img v1.Image, kind string) {
	t.Helper()
	m, err := img.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.ArtifactType != BundleArtifactType || m.Annotations[AnnotationKind] != kind {
		t.Fatalf("manifest not typed as %s: %+v", kind, m)
	}
	if len(m.Layers) != 1 || m.Layers[0].MediaType != LayerMediaType {
		t.Fatalf("layers: %+v", m.Layers)
	}
	raw, _ := img.RawManifest()
	if got, decided, err := ClassifyManifest(raw); got != kind || !decided || err != nil {
		t.Fatalf("ClassifyManifest = %q %v %v", got, decided, err)
	}
	d, _ := img.Digest()
	size, _ := img.Size()
	if h, n, _ := v1.SHA256(bytes.NewReader(raw)); h != d || n != size {
		t.Fatalf("digest %s size %d do not match the raw manifest", d, size)
	}
}

// checkBundleLabels asserts the three config labels are unchanged by typing.
func checkBundleLabels(t *testing.T, img v1.Image) {
	t.Helper()
	cf, _ := img.ConfigFile()
	want := map[string]string{"io.pacto.name": "test-svc", "io.pacto.version": "1.0.0", "io.pacto.pactoVersion": "2.4"}
	for k, v := range want {
		if cf.Config.Labels[k] != v {
			t.Fatalf("label %s = %q, want %q", k, cf.Config.Labels[k], v)
		}
	}
}

func TestTypedImageUndrainedFails(t *testing.T) {
	img, err := BundleImage(testBundle())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := img.Manifest(); err == nil {
		t.Fatal("an undrained stream has no manifest")
	}
	if _, err := img.RawManifest(); err == nil {
		t.Fatal("an undrained stream has no raw manifest")
	}
	if _, err := img.Digest(); err == nil {
		t.Fatal("an undrained stream has no digest")
	}
	if _, err := img.Size(); err == nil {
		t.Fatal("an undrained stream has no size")
	}
}

func TestBundleKind(t *testing.T) {
	both := kindBundle(KindTemplate).Contract
	both.Service.Offering = "database"
	for want, c := range map[string]*contract.Contract{
		KindService:  kindBundle(KindService).Contract,
		KindTemplate: both,
		KindOffering: kindBundle(KindOffering).Contract,
	} {
		if got := BundleKind(c); got != want {
			t.Fatalf("BundleKind = %s, want %s", got, want)
		}
	}
}

func TestDigestsSameAndFor(t *testing.T) {
	ds := Digests{Typed: "sha256:t", Legacy: "sha256:l"}
	for _, tc := range []struct {
		remote, for_ string
		same         bool
	}{
		{"", "sha256:t", false},
		{"sha256:t", "sha256:t", true},
		{"sha256:l", "sha256:l", true},
		{"sha256:o", "sha256:t", false},
	} {
		if ds.Same(tc.remote) != tc.same || ds.For(tc.remote) != tc.for_ {
			t.Fatalf("remote %q: Same %v For %s", tc.remote, ds.Same(tc.remote), ds.For(tc.remote))
		}
	}
	if (Digests{}).Same("") {
		t.Fatal("an empty remote is never the same bundle")
	}
}

// flakyFS serves its first walk and fails every later Open, so the second
// pack of BundleDigests fails after the first succeeded.
type flakyFS struct {
	fstest.MapFS
	opens *int
}

func (f flakyFS) Open(name string) (fs.File, error) {
	if name != "." {
		*f.opens++
		if *f.opens > 1 {
			return nil, errors.New("gone")
		}
	}
	return f.MapFS.Open(name)
}

func TestBundleDigestsLegacyPackFails(t *testing.T) {
	b := testBundle()
	n := 0
	b.FS = flakyFS{MapFS: fstest.MapFS{"pacto.yaml": &fstest.MapFile{Data: b.RawYAML}}, opens: &n}
	if _, err := BundleDigests(b); err == nil {
		t.Fatal("a failing legacy pack has no digests")
	}
}
