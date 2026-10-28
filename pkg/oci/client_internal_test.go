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
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestClient_Push_BuildImageError(t *testing.T) {
	t.Parallel()
	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	client := NewClient(authn.DefaultKeychain)
	_, err := client.Push(context.Background(), host+"/test/repo:v1", &contract.Bundle{})
	if err == nil {
		t.Error("expected error when the bundle cannot be built into an image")
	}
}

// TestClient_ListTags_PerHostInsecureReachesTheRepository pins the asymmetry that
// broke the in-cluster registry: WithInsecureRegistries was applied when parsing a
// REFERENCE but not a REPOSITORY, so the same host could be pulled from by digest
// while tag listing dialled TLS at a plain-HTTP registry. The host below is the
// shape that exposes it — a Kubernetes service FQDN, which go-containerregistry
// defaults to https (unlike loopback, which is plain HTTP either way, so no test
// registry can catch this).
func TestClient_ListTags_PerHostInsecureReachesTheRepository(t *testing.T) {
	const host = "pacto-registry.pacto-system.svc.cluster.local:5000"
	t.Parallel()
	var asked name.Repository
	list := func(r name.Repository, _ ...remote.Option) ([]string, error) {
		asked = r
		return []string{"1.0.0", "1.1.0"}, nil
	}

	client := NewClient(authn.DefaultKeychain, WithInsecureRegistries(host))
	client.listTags = list
	tags, err := client.ListTags(context.Background(), host+"/demo/checkout")
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 2 {
		t.Errorf("tags = %v, want both published tags", tags)
	}
	if got := asked.Scheme(); got != "http" {
		t.Errorf("tag listing scheme = %q, want http for a per-host insecure registry", got)
	}
	if got := asked.RepositoryStr(); got != "demo/checkout" {
		t.Errorf("listed repository = %q, want demo/checkout", got)
	}
	// A host that was NOT allowed stays https: the allowance is per host, and tag
	// listing must not widen it.
	plain := NewClient(authn.DefaultKeychain)
	plain.listTags = list
	if _, err := plain.ListTags(context.Background(), host+"/demo/checkout"); err != nil {
		t.Fatalf("ListTags (no allowance): %v", err)
	}
	if got := asked.Scheme(); got != "https" {
		t.Errorf("tag listing scheme = %q for a host with no allowance, want https", got)
	}
}

func TestClient_Push_DigestError(t *testing.T) {
	t.Parallel()
	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	client := NewClient(authn.DefaultKeychain)
	client.imageDigest = func(v1.Image) (v1.Hash, error) { return v1.Hash{}, fmt.Errorf("digest failed") }
	_, err := client.Push(context.Background(), host+"/test/repo:v1", testBundle())
	if err == nil {
		t.Error("expected error when the digest cannot be read")
	}
}

// TestClient_Do parses with the plain-HTTP hosts, hands op the reference and
// returns a bad ref as a typed error without calling op.
func TestClient_Do(t *testing.T) {
	t.Parallel()
	c := NewClient(authn.NewMultiKeychain(), WithInsecureRegistries("reg.internal"))
	var scheme string
	err := c.Do(context.Background(), "reg.internal/acme/x:1", func(r name.Reference, _ []remote.Option) error {
		scheme = r.Context().Scheme()
		return nil
	})
	if err != nil || scheme != "http" {
		t.Fatalf("Do = %v, scheme %q; want nil, http", err, scheme)
	}
	called := false
	err = c.Do(context.Background(), "BAD REF", func(name.Reference, []remote.Option) error { called = true; return nil })
	if _, ok := err.(*InvalidRefError); !ok || called {
		t.Fatalf("Do(bad ref) = %v, op called %v; want *InvalidRefError, not called", err, called)
	}
}
