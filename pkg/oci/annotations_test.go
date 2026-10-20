package oci_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

func TestClient_Annotations(t *testing.T) {
	client, host := newTestClient(t)
	ctx := context.Background()
	ref := host + "/demo/app:v1"
	img := mutate.Annotations(empty.Image, map[string]string{"pacto.dev/contract": "oci://r/p@sha256:1"}).(v1.Image)
	r, _ := name.ParseReference(ref)
	if err := remote.Write(r, img); err != nil {
		t.Fatal(err)
	}
	got, err := client.Annotations(ctx, ref)
	if err != nil || got["pacto.dev/contract"] != "oci://r/p@sha256:1" {
		t.Fatalf("Annotations() = %v, %v", got, err)
	}

	var refErr *oci.InvalidRefError
	if _, err := client.Annotations(ctx, "!!!"); !errors.As(err, &refErr) {
		t.Fatalf("invalid ref: %v", err)
	}
	if _, err := client.Annotations(ctx, host+"/demo/missing:v1"); err == nil {
		t.Fatal("missing manifest must fail")
	}
}

func TestClient_Annotations_NotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	client := oci.NewClient(authn.DefaultKeychain)
	var bundleErr *oci.InvalidBundleError
	if _, err := client.Annotations(context.Background(), strings.TrimPrefix(srv.URL, "http://")+"/demo/app:v1"); !errors.As(err, &bundleErr) {
		t.Fatalf("garbage manifest: %v", err)
	}
}
