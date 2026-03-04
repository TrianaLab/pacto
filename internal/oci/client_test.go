package oci_test

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/trianalab/pacto/internal/oci"
	"github.com/trianalab/pacto/pkg/contract"
)

func newTestClient(t *testing.T) (*oci.Client, string) {
	t.Helper()
	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	client := oci.NewClient(authn.DefaultKeychain, oci.WithNameOptions(name.Insecure))
	return client, host
}

func newTestBundle() *contract.Bundle {
	port := 8080
	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service:      contract.ServiceIdentity{Name: "test-svc", Version: "1.0.0"},
			Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: &port}},
			Runtime: contract.Runtime{
				Workload: "service",
				State: contract.State{
					Type:            "stateless",
					Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
					DataCriticality: "low",
				},
				Health: contract.Health{Interface: "api", Path: "/health"},
			},
		},
		FS: fstest.MapFS{
			"pacto.yaml": &fstest.MapFile{Data: []byte(`pactoVersion: "1.0"
service:
  name: test-svc
  version: "1.0.0"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`)},
		},
	}
}

func TestClient_PushPull_Roundtrip(t *testing.T) {
	client, host := newTestClient(t)
	ctx := context.Background()
	b := newTestBundle()

	ref := host + "/test/repo:v1"

	digest, err := client.Push(ctx, ref, b)
	if err != nil {
		t.Fatalf("Push() error: %v", err)
	}
	if digest == "" {
		t.Fatal("Push() returned empty digest")
	}

	got, err := client.Pull(ctx, ref)
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if got.Contract.Service.Name != b.Contract.Service.Name {
		t.Errorf("Service.Name = %q, want %q", got.Contract.Service.Name, b.Contract.Service.Name)
	}
	if got.Contract.Service.Version != b.Contract.Service.Version {
		t.Errorf("Service.Version = %q, want %q", got.Contract.Service.Version, b.Contract.Service.Version)
	}
	if got.Contract.PactoVersion != b.Contract.PactoVersion {
		t.Errorf("PactoVersion = %q, want %q", got.Contract.PactoVersion, b.Contract.PactoVersion)
	}
	if len(got.Contract.Interfaces) != len(b.Contract.Interfaces) {
		t.Errorf("len(Interfaces) = %d, want %d", len(got.Contract.Interfaces), len(b.Contract.Interfaces))
	}
}

func TestClient_Push_InvalidRef(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()
	b := newTestBundle()

	_, err := client.Push(ctx, "!!!", b)
	if err == nil {
		t.Fatal("expected error for invalid reference")
	}
}

func TestClient_Pull_NotFound(t *testing.T) {
	client, host := newTestClient(t)
	ctx := context.Background()

	_, err := client.Pull(ctx, host+"/nonexistent/repo:latest")
	if err == nil {
		t.Fatal("expected error for nonexistent image")
	}

	var notFound *oci.ArtifactNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("expected ArtifactNotFoundError, got %T: %v", err, err)
	}
}

func TestClient_Resolve(t *testing.T) {
	client, host := newTestClient(t)
	ctx := context.Background()
	b := newTestBundle()

	ref := host + "/test/resolve:v1"

	_, err := client.Push(ctx, ref, b)
	if err != nil {
		t.Fatalf("Push() error: %v", err)
	}

	digest, err := client.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}

	if !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("digest = %q, want sha256: prefix", digest)
	}
}

func TestClient_Resolve_NotFound(t *testing.T) {
	client, host := newTestClient(t)
	ctx := context.Background()

	_, err := client.Resolve(ctx, host+"/nonexistent/repo:latest")
	if err == nil {
		t.Fatal("expected error for nonexistent reference")
	}
}

func TestClient_Pull_InvalidRef(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	_, err := client.Pull(ctx, "!!!")
	if err == nil {
		t.Fatal("expected error for invalid reference")
	}
	if !strings.Contains(err.Error(), "invalid reference") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid reference")
	}
}

func TestClient_Resolve_InvalidRef(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := context.Background()

	_, err := client.Resolve(ctx, "!!!")
	if err == nil {
		t.Fatal("expected error for invalid reference")
	}
	if !strings.Contains(err.Error(), "invalid reference") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "invalid reference")
	}
}

func TestClient_Pull_InvalidImage(t *testing.T) {
	// Push an image with no pacto.yaml, then pull it.
	// This triggers the imageToBundle error path in Pull.
	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	client := oci.NewClient(authn.DefaultKeychain, oci.WithNameOptions(name.Insecure))

	// Push an image that has a layer but no pacto.yaml using remote.Write directly.
	ref, err := name.ParseReference(host+"/test/invalid:v1", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}

	// Use a tarball layer with just a dummy file.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data := []byte("not a contract")
	_ = tw.WriteHeader(&tar.Header{Name: "dummy.txt", Size: int64(len(data)), Mode: 0644})
	_, _ = tw.Write(data)
	_ = tw.Close()

	layerBytes := buf.Bytes()
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(layerBytes)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}

	if err := remote.Write(ref, img, remote.WithAuthFromKeychain(authn.DefaultKeychain)); err != nil {
		t.Fatal(err)
	}

	// Now pull through the client. It should pull successfully but fail to
	// extract the bundle because there's no pacto.yaml.
	ctx := context.Background()
	_, err = client.Pull(ctx, host+"/test/invalid:v1")
	if err == nil {
		t.Fatal("expected error for image without pacto.yaml")
	}
	if !strings.Contains(err.Error(), "failed to extract bundle") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "failed to extract bundle")
	}
}

func TestClient_Push_RemoteWriteError(t *testing.T) {
	// Push to a server that immediately closes connections, causing remote.Write to fail.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":[{"code":"DENIED","message":"denied"}]}`, http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	client := oci.NewClient(authn.DefaultKeychain, oci.WithNameOptions(name.Insecure))
	ctx := context.Background()
	b := newTestBundle()

	_, err := client.Push(ctx, host+"/test/repo:v1", b)
	if err == nil {
		t.Fatal("expected error for push to broken registry")
	}
}

func TestWithRemoteOptions(t *testing.T) {
	// Create a client that uses WithRemoteOptions.
	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	client := oci.NewClient(
		authn.DefaultKeychain,
		oci.WithNameOptions(name.Insecure),
		oci.WithRemoteOptions(), // call with no extra options to cover the function
	)

	ctx := context.Background()
	b := newTestBundle()
	ref := host + "/test/remote-opts:v1"

	_, err := client.Push(ctx, ref, b)
	if err != nil {
		t.Fatalf("Push() with WithRemoteOptions error: %v", err)
	}
}
