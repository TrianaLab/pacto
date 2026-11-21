package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

func TestNewRegistryProbeOpensTheRefRepository(t *testing.T) {
	freshProbes(t)
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: true}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	repo, err := ocireferrer.Open(host+"/team/app", ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	art := ocireferrer.Build("application/x-subject", "application/x-layer", []byte("s"), nil, nil)
	if err := ocireferrer.Push(t.Context(), repo, art); err != nil {
		t.Fatal(err)
	}
	probe := NewRegistryProbe()
	c, err := probe(t.Context(), "oci://"+host+"/team/app@"+art.ManifestDesc.Digest.String())
	if err != nil || c.Host != host || c.Referrers != capabilities.ReferrersWriteOnceTags {
		t.Fatalf("probe = %+v %v", c, err)
	}
	if _, err := probe(t.Context(), "oci://not a ref"); err == nil {
		t.Error("an unparseable ref was probed")
	}
	// go-containerregistry accepts this repository name; oras-go refuses it.
	if _, err := probe(t.Context(), "oci://localhost:5000/a..b:1"); err == nil {
		t.Error("a repository oras-go refuses was probed")
	}
}

func TestFleetWiresRegistryProbe(t *testing.T) {
	store := fakeFleetStore{bundles: map[string]*contract.Bundle{"ghcr.io/x/a:1.0.0": bundleForFleet("a")}}
	var probed []string
	opts := FleetOptions{OCIRefs: []string{"ghcr.io/x/a:1.0.0"}, RegistryProbe: func(_ context.Context, ref string) (capabilities.Capabilities, error) {
		probed = append(probed, ref)
		return capabilities.Capabilities{Host: "ghcr.io", Referrers: capabilities.ReferrersNative, Listing: capabilities.ListingCatalog}, nil
	}}
	snap, err := NewService(store, nil).Fleet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(probed) != 1 || len(snap.Providers) != 1 || snap.Providers[0].ID != fleet.ProviderRegistryPrefix+"ghcr.io" || snap.Providers[0].Source != "oci" {
		t.Fatalf("probed %q, rows %+v", probed, snap.Providers)
	}
}

// TestFreshProbesForgetsAReusedPort: a test server that reuses the port of an
// earlier one with native referrers must be probed again, or a publish
// reading back its signature takes the earlier answer and fails (CI run
// 44700352090, TestCIPublishRefusedKeepsThePlanModeNote).
func TestFreshProbesForgetsAReusedPort(t *testing.T) {
	probeOn := func(t *testing.T, h http.Handler, addr string) (string, capabilities.ReferrersMode) {
		srv := httptest.NewUnstartedServer(h)
		if addr != "" {
			l, err := net.Listen("tcp", addr)
			if err != nil {
				t.Skipf("the port was taken meanwhile: %v", err)
			}
			_ = srv.Listener.Close()
			srv.Listener = l
		}
		srv.Start()
		defer srv.Close()
		host := strings.TrimPrefix(srv.URL, "http://")
		repo, _ := ocireferrer.Open(host+"/team/app", ocireferrer.Options{})
		art := ocireferrer.Build("application/x-subject", "application/x-layer", []byte("s"), nil, nil)
		if err := ocireferrer.Push(t.Context(), repo, art); err != nil {
			t.Fatal(err)
		}
		c, err := ocireferrer.Probes.Probe(t.Context(), repo, art.ManifestDesc.Digest.String(), false)
		if err != nil {
			t.Fatal(err)
		}
		return host, c.Referrers
	}
	freshProbes(t)
	host, mode := probeOn(t, testutil.NewReferrersRegistry(testutil.ReferrersOptions{}), "")
	if mode != capabilities.ReferrersNative {
		t.Fatalf("first server: %s", mode)
	}
	t.Run("reused", func(t *testing.T) {
		freshProbes(t)
		if _, mode := probeOn(t, testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: true}), host); mode != capabilities.ReferrersWriteOnceTags {
			t.Fatalf("the reused port kept the earlier answer: %s", mode)
		}
	})
}
