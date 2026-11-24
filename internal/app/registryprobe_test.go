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
	t.Parallel()
	srv := probedServer(t, testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: true}))
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
	t.Parallel()
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

// TestProbedServerNeverReusesAnAddress: a test server on the port of an
// earlier one with native referrers would take that server's remembered
// answer from the process prober, and a publish reading back its signature
// fails (CI run 44700352090, TestCIPublishRefusedKeepsThePlanModeNote). So
// probedServer drops a server on an address it already handed out.
func TestProbedServerNeverReusesAnAddress(t *testing.T) {
	t.Parallel()
	first := probedServer(t, http.NotFoundHandler())
	addr := first.Listener.Addr().String()
	first.Close()
	reused := 0
	srv := uniqueServer(t, func() *httptest.Server {
		srv := httptest.NewUnstartedServer(http.NotFoundHandler())
		if reused == 0 {
			l, err := net.Listen("tcp", addr)
			if err != nil {
				t.Skipf("the port was taken meanwhile: %v", err)
			}
			_ = srv.Listener.Close()
			srv.Listener = l
		}
		reused++
		srv.Start()
		return srv
	})
	if got := srv.Listener.Addr().String(); got == addr || reused != 2 {
		t.Fatalf("probedServer handed out %s again (%d servers started)", got, reused)
	}
}
