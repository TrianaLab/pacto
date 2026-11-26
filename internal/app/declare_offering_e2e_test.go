package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// declaredXbucket is the declared xbucket in a fleet that reads the
// installation's advertised offerings through its key and the declared
// versions published (repo@digest), with the installation observing
// xbucket a minute ago.
func (w *offeringWorld) declaredXbucket(t *testing.T, published ...string) *fleet.ServiceRecord {
	t.Helper()
	res := offeringDetection(t, w.host+"/acme", w.kp)
	res.Installation.Offerings.Observed = []string{"xbucket"}
	src := fleetsrc.NewDetectionResultSource("detection", res, readNow)
	src.Freshness = 24 * time.Hour
	src.Contributions = &contributionReader{Store: oci.NewClient(authn.NewMultiKeychain(), oci.WithInsecureRegistries(w.host)), Registry: w.reg,
		Roots: []string{w.host + "/acme"}, Now: readNow}
	var declared []fleet.RawRevision
	for _, ref := range published {
		b, err := w.reg.Pull(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		_, digest, _ := strings.Cut(ref, "@")
		declared = append(declared, fleet.RawRevision{Bundle: b, Domain: w.host + "/acme/offerings", ResolvedRef: "oci://" + ref, Digest: digest})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: readNow, FreshnessWindow: 24 * time.Hour},
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: declared}), src)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range snap.Services {
		if s.Name == "xbucket" && s.Contribution == nil {
			return s
		}
	}
	t.Fatalf("no declared xbucket in %v", snap.Services)
	return nil
}

// TestDeclareOfferingE2E: a person accepts the installation's inferred
// xbucket; a newer contribution makes the declared offering drift; the
// person's re-acceptance at 1.1.0 from it clears the drift.
func TestDeclareOfferingE2E(t *testing.T) {
	t.Parallel()
	w := newOfferingWorld(t)
	w.withOwnerPolicy(t)
	first := w.accepted(t, 2, "1.0.0")
	if s := w.declaredXbucket(t, first.Published); s.OfferingDrift != nil {
		t.Fatalf("drift right after the acceptance: %+v", s.OfferingDrift)
	}
	w.xbucket(t, 3)
	s := w.declaredXbucket(t, first.Published)
	if d := s.OfferingDrift; d == nil || d.State != fleet.DriftDrifted || d.Producer != offeringProducer || d.NewestVersion != "0.0.0-inferred.3.0" {
		t.Fatalf("drift = %+v", d)
	}
	again := w.accepted(t, 3, "1.1.0")
	if again.Tag != "1.1.0" {
		t.Fatalf("%+v", again)
	}
	if s := w.declaredXbucket(t, first.Published, again.Published); s.OfferingDrift != nil {
		t.Fatalf("drift after the re-acceptance: %+v", s.OfferingDrift)
	}
}
