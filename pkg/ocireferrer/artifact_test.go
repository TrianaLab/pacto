package ocireferrer

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/trianalab/pacto/v4/internal/testutil"
)

func open(t *testing.T, opts testutil.ReferrersOptions) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(testutil.NewReferrersRegistry(opts))
	t.Cleanup(srv.Close)
	return srv, strings.TrimPrefix(srv.URL, "http://") + "/team/app"
}

func TestBuildIsDeterministicAndShaped(t *testing.T) {
	subj := Descriptor(ocispec.MediaTypeImageManifest, []byte("subject"))
	ann := map[string]string{"k": "v"}
	a := Build("application/x-test", "application/x-layer", []byte("payload"), &subj, ann)
	b := Build("application/x-test", "application/x-layer", []byte("payload"), &subj, ann)
	if !bytes.Equal(a.Manifest, b.Manifest) || a.ManifestDesc.Digest != b.ManifestDesc.Digest {
		t.Fatal("the same input built two manifests")
	}
	var m ocispec.Manifest
	if err := json.Unmarshal(a.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ArtifactType != "application/x-test" || m.Subject.Digest != subj.Digest || m.Annotations["k"] != "v" ||
		len(m.Layers) != 1 || m.Layers[0].MediaType != "application/x-layer" || m.Config.MediaType != ocispec.MediaTypeEmptyJSON {
		t.Fatalf("manifest = %s", a.Manifest)
	}
	if a.ManifestDesc.ArtifactType != "application/x-test" || a.ManifestDesc.Annotations["k"] != "v" {
		t.Fatalf("descriptor = %+v", a.ManifestDesc)
	}
	a.ConfigDesc.Data[0] = 'x' // must not alias image-spec's package-level slice
	if ocispec.DescriptorEmptyJSON.Data[0] != '{' {
		t.Fatal("Build aliased the canonical empty descriptor")
	}
}

func TestPushConfirmEnumerate(t *testing.T) {
	_, path := open(t, testutil.ReferrersOptions{PageSize: 1})
	repo, err := Open(path, Options{PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	anchor := Build("application/x-anchor", "application/x-layer", []byte(`{"stream":"s"}`), nil, nil)
	if err := Push(ctx, repo, anchor); err != nil {
		t.Fatal(err)
	}
	if err := Enumerate(ctx, repo, anchor.ManifestDesc); err != nil {
		t.Fatal(err)
	}
	rec := Build("application/x-rec", "application/x-layer", []byte("one"), &anchor.ManifestDesc, nil)
	if err := Confirm(ctx, repo, anchor.ManifestDesc, rec.ManifestDesc); err == nil {
		t.Fatal("confirmed an artifact that was never pushed")
	}
	for _, p := range []string{"one", "two"} {
		art := Build("application/x-rec", "application/x-layer", []byte(p), &anchor.ManifestDesc, nil)
		if err := Push(ctx, repo, art); err != nil {
			t.Fatal(err)
		}
		if err := Confirm(ctx, repo, anchor.ManifestDesc, art.ManifestDesc); err != nil {
			t.Fatal(err)
		}
	}
	if err := Enumerate(ctx, repo, anchor.ManifestDesc); err != nil {
		t.Fatal(err)
	}
}

func TestNoTagFallback(t *testing.T) {
	_, path := open(t, testutil.ReferrersOptions{Unsupported: true})
	repo, _ := Open(path, Options{})
	ctx := t.Context()
	anchor := Build("application/x-anchor", "application/x-layer", []byte("a"), nil, nil)
	if err := Push(ctx, repo, anchor); err != nil {
		t.Fatal(err)
	}
	if err := Enumerate(ctx, repo, anchor.ManifestDesc); err == nil {
		t.Fatal("listed referrers on a registry without the Referrers API")
	}
	if err := Confirm(ctx, repo, anchor.ManifestDesc, anchor.ManifestDesc); err == nil {
		t.Fatal("confirmed on a registry without the Referrers API")
	}
}

func TestPushFailures(t *testing.T) {
	srv, path := open(t, testutil.ReferrersOptions{})
	repo, _ := Open(path, Options{})
	srv.Close()
	art := Build("application/x-anchor", "application/x-layer", []byte("a"), nil, nil)
	if err := Push(t.Context(), repo, art); err == nil {
		t.Fatal("pushed to a registry that is gone")
	}
	// A manifest the registry refuses after its blobs went in.
	_, path = open(t, testutil.ReferrersOptions{})
	repo, _ = Open(path, Options{})
	art.Manifest = []byte("not the bytes the descriptor names")
	if err := Push(t.Context(), repo, art); err == nil {
		t.Fatal("pushed a manifest that does not match its descriptor")
	}
}
