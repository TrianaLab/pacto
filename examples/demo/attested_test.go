package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoNewsletterOwnerAttested: the fleet `pacto fleet --local
// examples/demo/bundles` builds, with the demo's stand-in CI signature on
// newsletter-service, shows its catalog-info.yaml owner as attested, cites
// the file and leaves the committed record untouched; the WASM demo fleet
// shows the same.
func TestDemoNewsletterOwnerAttested(t *testing.T) {
	recPath := filepath.Join("bundles", "newsletter-service", contract.InferredRecordFile)
	before, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	col, err := fleetsrc.NewLocalSource("local", "bundles").Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	attestDemoRevision(col)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}
	s := snap.Services[fleet.NewServiceKey("newsletter-service")]
	if s == nil {
		t.Fatal("newsletter-service is not in the fleet")
	}
	if s.OwnerProvenance != fleet.OwnerProvenanceAttested || s.Owner.Team != "team-growth" {
		t.Errorf("owner = %+v (%s), want team-growth attested", s.Owner, s.OwnerProvenance)
	}
	if !strings.Contains(s.OwnerSource, "catalog-info.yaml") {
		t.Errorf("owner source = %q, want it to name catalog-info.yaml", s.OwnerSource)
	}
	if w := demoFleet(t).Services[fleet.NewServiceKey("newsletter-service")]; w == nil || w.OwnerProvenance != fleet.OwnerProvenanceAttested {
		t.Errorf("the WASM demo fleet does not attest newsletter-service's owner: %+v", w)
	}
	after, err := os.ReadFile(recPath)
	if err != nil || string(after) != string(before) {
		t.Errorf("the committed record changed on disk (err %v)", err)
	}
}
