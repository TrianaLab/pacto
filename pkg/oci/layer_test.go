package oci_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/trianalab/pacto/v4/pkg/oci"
)

// pushLayers pushes an image of the given layers to host/app:v1 and returns
// the reference.
func pushLayers(t *testing.T, host string, layers ...[]byte) string {
	t.Helper()
	img := empty.Image
	for _, b := range layers {
		var err error
		if img, err = mutate.AppendLayers(img, static.NewLayer(b, types.MediaType("application/vnd.cncf.flux.content.v1.tar+gzip"))); err != nil {
			t.Fatal(err)
		}
	}
	ref := host + "/app:v1"
	if err := remote.Write(mustRef(t, ref), img); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestLayerReadsTheOneLayer(t *testing.T) {
	c, host := newTestClient(t)
	ref := pushLayers(t, host, []byte("content"))
	mt, data, err := c.Layer(context.Background(), ref, 100)
	if err != nil || mt != "application/vnd.cncf.flux.content.v1.tar+gzip" || string(data) != "content" {
		t.Fatalf("layer: %q %q %v", mt, data, err)
	}
}

func TestLayerRefusesSeveralLayers(t *testing.T) {
	c, host := newTestClient(t)
	ref := pushLayers(t, host, []byte("a"), []byte("b"))
	if _, _, err := c.Layer(context.Background(), ref, 100); err == nil || !strings.Contains(err.Error(), "holds 2 layers") {
		t.Fatalf("two layers: %v", err)
	}
}

func TestLayerRefusesOversize(t *testing.T) {
	c, host := newTestClient(t)
	ref := pushLayers(t, host, []byte("0123456789"))
	if _, _, err := c.Layer(context.Background(), ref, 5); err == nil || !strings.Contains(err.Error(), "over the 5") {
		t.Fatalf("oversize: %v", err)
	}
	// A manifest that understates its layer is cut at the bound.
	layer := static.NewLayer([]byte("0123456789"), types.MediaType("x"))
	repo := mustRef(t, host+"/lie:v1").Context()
	if err := remote.WriteLayer(repo, layer); err != nil {
		t.Fatal(err)
	}
	d, _ := layer.Digest()
	m := v1.Manifest{SchemaVersion: 2, MediaType: types.OCIManifestSchema1,
		Config: v1.Descriptor{MediaType: types.OCIConfigJSON, Size: 2, Digest: emptyConfig(t, repo)},
		Layers: []v1.Descriptor{{MediaType: "x", Size: 1, Digest: d}}}
	if err := remote.Put(repo.Tag("v1"), rawManifest{m}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Layer(context.Background(), repo.Tag("v1").String(), 5); err == nil || !strings.Contains(err.Error(), "over the 5 bytes") {
		t.Fatalf("understated: %v", err)
	}
}

func TestLayerErrors(t *testing.T) {
	c := oci.NewClient(authn.DefaultKeychain)
	if _, _, err := c.Layer(context.Background(), "NOT A REF", 5); err == nil {
		t.Fatal("bad ref")
	}
	_, host := newTestClient(t)
	if _, _, err := c.Layer(context.Background(), host+"/none:v1", 5); err == nil {
		t.Fatal("missing manifest")
	}
	// A manifest that is not JSON, and a blob the registry lost.
	reg := registry.New()
	lost := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/bad/manifests/") {
			w.Header().Set("Content-Type", string(types.OCIManifestSchema1))
			_, _ = w.Write([]byte("not json"))
			return
		}
		if lost && strings.Contains(r.URL.Path, "/blobs/") {
			http.NotFound(w, r)
			return
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	h := strings.TrimPrefix(srv.URL, "http://")
	ref := pushLayers(t, h, []byte("x"))
	lost = true
	if _, _, err := c.Layer(context.Background(), ref, 5); err == nil {
		t.Fatal("lost blob")
	}
	if _, _, err := c.Layer(context.Background(), h+"/bad:v1", 5); err == nil {
		t.Fatal("not JSON")
	}
}

// emptyConfig uploads the config blob "{}" and returns its digest.
func emptyConfig(t *testing.T, repo name.Repository) v1.Hash {
	t.Helper()
	l := static.NewLayer([]byte("{}"), types.OCIConfigJSON)
	if err := remote.WriteLayer(repo, l); err != nil {
		t.Fatal(err)
	}
	d, _ := l.Digest()
	return d
}

type rawManifest struct{ m v1.Manifest }

func (r rawManifest) RawManifest() ([]byte, error) { return json.Marshal(r.m) }
func (r rawManifest) MediaType() (types.MediaType, error) {
	return types.OCIManifestSchema1, nil
}

func mustRef(t *testing.T, s string) name.Reference {
	t.Helper()
	r, err := name.ParseReference(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
