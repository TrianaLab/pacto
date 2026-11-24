package oci_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// typedRegistry serves an in-process registry and returns its host.
func typedRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestTypedPushDigestEqualsBundleDigest(t *testing.T) {
	host := typedRegistry(t)
	ref := host + "/acme/svc:1.0.0"
	client := oci.NewClient(authn.DefaultKeychain)
	pushed, err := client.Push(context.Background(), ref, newTestBundle())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := oci.BundleDigest(newTestBundle())
	if pushed != want {
		t.Fatalf("pushed %s, BundleDigest %s", pushed, want)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+host+"/v2/acme/svc/manifests/1.0.0", nil)
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var m v1.Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.ArtifactType != oci.BundleArtifactType || m.Annotations[oci.AnnotationKind] != oci.KindService {
		t.Fatalf("the registry holds an untyped manifest: %+v", m)
	}
	if got := resp.Header.Get("Docker-Content-Digest"); got != "" && got != want {
		t.Fatalf("registry digest %s, want %s", got, want)
	}
}

func TestPullTypedBundle(t *testing.T) {
	host := typedRegistry(t)
	ref := host + "/acme/svc:1.0.0"
	client := oci.NewClient(authn.DefaultKeychain)
	b := newTestBundle()
	if _, err := client.Push(context.Background(), ref, b); err != nil {
		t.Fatal(err)
	}
	got, err := client.Pull(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if got.Contract.Service.Name != b.Contract.Service.Name || got.Contract.Service.Version != b.Contract.Service.Version || string(got.RawYAML) != string(b.RawYAML) {
		t.Fatalf("pulled contract differs: %+v", got.Contract.Service)
	}
	for _, name := range []string{"pacto.yaml", "openapi.yaml", "docs/README.md", "docs/runbook.md"} {
		want, _ := fs.ReadFile(b.FS, name)
		have, err := fs.ReadFile(got.FS, name)
		if err != nil || string(have) != string(want) {
			t.Fatalf("pulled %s differs: %q (%v)", name, have, err)
		}
	}
}
