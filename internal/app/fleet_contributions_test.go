package app

import (
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// TestHTTPSourcesWireContributions: with a bundle store each Evidence Server
// source reads contributions under the roots of the refs it is given, with
// the freshness window and clock of the build; without one it reads none.
func TestHTTPSourcesWireContributions(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }
	opts := FleetOptions{EvidenceURLs: []string{"http://ev-a", "http://ev-b"}, Now: now, FreshnessWindow: time.Hour,
		OCIRefs:        []string{"oci://ghcr.io/acme/ledger:1.0.0", "oci://ghcr.io/acme/billing:1.0.0"},
		TargetSnapshot: []string{"oci://europe-docker.pkg.dev/acme/pacto/pacto-targets"}}
	svc := NewService(oci.NewClient(anonymous), nil)
	sources, err := svc.httpSources(opts)
	if err != nil || len(sources) != 2 {
		t.Fatalf("%v %d", err, len(sources))
	}
	a, b := sources[0].(*fleetsrc.EvidenceHTTPSource), sources[1].(*fleetsrc.EvidenceHTTPSource)
	r, _ := a.Contributions.(*contributionReader)
	wantRoots := []string{"ghcr.io/acme", "ghcr.io", "europe-docker.pkg.dev/acme/pacto", "europe-docker.pkg.dev/acme", "europe-docker.pkg.dev"}
	if r == nil || b.Contributions != a.Contributions || r.Store != svc.BundleStore || r.Registry == nil || r.Memo != &svc.contributionsVerified ||
		!slices.Equal(r.Roots, wantRoots) || !r.Now().Equal(now()) || a.Freshness != time.Hour || !a.Now().Equal(now()) {
		t.Errorf("reader = %+v freshness = %v", r, a.Freshness)
	}

	sources, _ = NewService(nil, nil).httpSources(FleetOptions{EvidenceURLs: []string{"http://ev"}})
	src := sources[0].(*fleetsrc.EvidenceHTTPSource)
	if src.Contributions != nil || src.Freshness != fleet.RecentEvidenceWindow || src.Now == nil {
		t.Errorf("no store: %+v", src)
	}
}
