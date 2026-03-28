package oci

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/trianalab/pacto/pkg/contract"
)

func TestClient_Push_BuildImageError(t *testing.T) {
	old := buildImageFn
	buildImageFn = func(b *contract.Bundle) (v1.Image, error) {
		return nil, fmt.Errorf("build failed")
	}
	defer func() { buildImageFn = old }()

	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	client := NewClient(authn.DefaultKeychain, WithNameOptions(name.Insecure))
	_, err := client.Push(context.Background(), host+"/test/repo:v1", testBundle())
	if err == nil {
		t.Error("expected error when buildImageFn fails")
	}
}

func TestClient_Push_DigestError(t *testing.T) {
	old := imageDigestFn
	imageDigestFn = func(img v1.Image) (v1.Hash, error) {
		return v1.Hash{}, fmt.Errorf("digest failed")
	}
	defer func() { imageDigestFn = old }()

	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	client := NewClient(authn.DefaultKeychain, WithNameOptions(name.Insecure))
	_, err := client.Push(context.Background(), host+"/test/repo:v1", testBundle())
	if err == nil {
		t.Error("expected error when imageDigestFn fails")
	}
}
