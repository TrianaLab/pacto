package main

import (
	"io/fs"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// demoXBucketKey is the key of the contributed xbucket.
var demoXBucketKey = fleet.NewServiceKeyDomain("ghcr.io/acme/pacto-inferred/"+demoOfferingProducer, "xbucket")

func offeringList(t *testing.T, q *fleet.Query, d string) *fleet.EntityList {
	t.Helper()
	res, err := q.Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Declaration: d})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestDemoOfferingBundlesValid: every inferred record is a valid
// contribution, and the declared objectstore a valid contract.
func TestDemoOfferingBundlesValid(t *testing.T) {
	for _, r := range demoInferredOfferings() {
		data, _ := fs.ReadFile(r.Bundle.FS, contract.InferredRecordFile)
		rec, err := contract.ParseInferredRecord(data)
		if err != nil {
			t.Fatal(err)
		}
		if why := contract.ContributionRefusal(r.Bundle, rec); why != "" {
			t.Errorf("%s refused: %s", r.RequestedRef, why)
		}
	}
	rev, b := demoDeclaredObjectstore()
	if res := validation.Validate(b.Contract, b.RawYAML, b.FS); !res.IsValid() {
		t.Fatalf("objectstore 1.0.0 invalid: %+v", res.Errors)
	}
	if rev.Digest == demoObjectstore4.digest() || demoObjectstore3.digest() == demoObjectstore4.digest() {
		t.Fatal("the accepted and the newest record share a digest")
	}
}

// TestDemoInferredOfferingListed: declaration=inferred lists xbucket and
// objectstore with their source object, generation and classes; objectstore
// names the declared offering it was accepted as. The declared list counts
// them apart.
func TestDemoInferredOfferingListed(t *testing.T) {
	q := fleet.NewQuery(demoFleet(t))
	res := offeringList(t, q, fleet.DeclarationInferred)
	got := map[string]*fleet.InferredOfferingFacts{}
	for _, e := range res.Entities {
		got[e.InferredOffering.Offering] = e.InferredOffering
	}
	x, o := got["xbucket"], got["objectstore"]
	if res.Total != 2 || x == nil || o == nil {
		t.Fatalf("inferred offerings = %+v", res.Entities)
	}
	if x.Producer != demoOfferingProducer || x.From != "CompositeResourceDefinition xbuckets.storage.acme.io" || x.Generation != 2 ||
		!slices.Equal(x.Classes, []string{"large", "small"}) || x.AsOf == nil || x.AcceptedAs != nil {
		t.Errorf("xbucket = %+v", x)
	}
	if o.Generation != 4 || o.AcceptedAs == nil || o.AcceptedAs.Label != "objectstore" {
		t.Errorf("objectstore = %+v", o)
	}
	testDeclaredListCountsApart(t, q)
}

// testDeclaredListCountsApart: the declared list holds no inferred offering
// and counts them apart.
func testDeclaredListCountsApart(t *testing.T, q *fleet.Query) {
	t.Helper()
	declared := offeringList(t, q, "")
	if declared.InferredTotal != 2 {
		t.Errorf("declared list inferredTotal = %d", declared.InferredTotal)
	}
	for _, e := range declared.Entities {
		if e.Key == "xbucket" {
			t.Error("the declared list holds the inferred xbucket")
		}
	}
}

// TestDemoOfferingDriftShown: objectstore 1.0.0 was accepted from the
// generation 3 record and the XRD is now at generation 4: it drifted, on the
// service and on the offering page's provider block.
func TestDemoOfferingDriftShown(t *testing.T) {
	snap := demoFleet(t)
	s := snap.Services[fleet.NewServiceKey("objectstore")]
	if s == nil || s.Inferred() {
		t.Fatalf("objectstore = %+v", s)
	}
	d := s.OfferingDrift
	if d == nil || d.State != fleet.DriftDrifted || d.Producer != demoOfferingProducer || d.AcceptedDigest != demoObjectstore3.digest() ||
		!strings.Contains(d.Reason, "now generation 4") || d.NewestVersion != demoObjectstore4.version() {
		t.Fatalf("drift = %+v", d)
	}
	det, err := fleet.NewQuery(snap).EntityDetail(fleet.KindOffering, "objectstore")
	if err != nil {
		t.Fatal(err)
	}
	if p := det.Offering.Providers.Items; len(p) != 1 || p[0].Drift == nil || p[0].Drift.State != fleet.DriftDrifted {
		t.Fatalf("providers = %+v", det.Offering.Providers.Items)
	}
}

// TestDemoInferredOfferingNeverConsumable: the contributed xbucket is an
// inferred service with no offering of its own: nothing consumes it.
func TestDemoInferredOfferingNeverConsumable(t *testing.T) {
	snap := demoFleet(t)
	s := snap.Services[demoXBucketKey]
	if s == nil || !s.Inferred() || s.Contribution == nil || s.Contribution.Cluster != prodClusterID || s.Contribution.KeyID == "" {
		t.Fatalf("xbucket = %+v", s)
	}
	if _, err := fleet.NewQuery(snap).EntityDetail(fleet.KindOffering, "xbucket"); err == nil {
		t.Error("xbucket is an offering a consumer can name")
	}
	if s := snap.Services[fleet.NewServiceKey("xbucket")]; s != nil {
		t.Errorf("xbucket joined the installation domain: %+v", s)
	}
}

// planOfferingIn asks the demo to plan accepting the offering key names.
func planOfferingIn(t *testing.T, key fleet.ServiceKey, in dashboard.OfferingInput) dashboard.DeclareAnswer {
	t.Helper()
	who := "system:serviceaccount:pacto:agent"
	var a dashboard.DeclareAnswer
	post(t, declareDemo(t, &who), "/api/fleet/declare", dashboard.DeclareInput{ServiceKey: string(key), OfferingAcceptance: &in}, &a)
	return a
}

// TestDemoAcceptOfferingPlan: accepting xbucket plans a declared offering
// that names its inferred record, signed by nobody, with no draft.
func TestDemoAcceptOfferingPlan(t *testing.T) {
	a := planOfferingIn(t, demoXBucketKey, dashboard.OfferingInput{Version: "1.0.0", Owner: "team-storage"})
	if a.Refusal != nil || a.Plan == nil {
		t.Fatalf("answer = %+v", a)
	}
	p := a.Plan
	want := contract.OfferingInferredFrom(demoOfferingProducer, "xbucket", demoXBucket.digest())
	if p.InferredFrom != want || p.Version != "1.0.0" || p.Owner != "team-storage" || p.BundleRepo != demoDeclareRegistry+"/xbucket" ||
		!strings.Contains(p.Contract.Text, "offering: xbucket") || !strings.Contains(p.Record.Text, want) {
		t.Errorf("plan = %+v", p)
	}
	if a.Signing.Mode != "none" || a.NoDraft == "" || !strings.HasPrefix(a.Command, "pacto declare offering:xbucket --from oci://"+demoOfferingRepository+"@") ||
		!strings.HasSuffix(a.Command, "--intent "+p.IntentDigest) {
		t.Errorf("answer = %+v", a)
	}
}

// TestDemoAcceptOfferingRefusals: a missing version or owner, a version not
// above the declared one and another offering's name are refused, each with
// the command.
func TestDemoAcceptOfferingRefusals(t *testing.T) {
	objectstore := fleet.NewServiceKeyDomain("ghcr.io/acme/pacto-inferred/"+demoOfferingProducer, "objectstore")
	for _, c := range []struct {
		key  fleet.ServiceKey
		in   dashboard.OfferingInput
		want string
	}{
		{demoXBucketKey, dashboard.OfferingInput{}, "VersionRequired"},
		{demoXBucketKey, dashboard.OfferingInput{Version: "1.0"}, "VersionConflict"},
		{demoXBucketKey, dashboard.OfferingInput{Version: "1.0.0-rc.1"}, "VersionConflict"},
		{demoXBucketKey, dashboard.OfferingInput{Version: "1.0.0"}, "OwnerRequired"},
		{demoXBucketKey, dashboard.OfferingInput{Name: "queue", Version: "1.0.0"}, "OfferingMismatch"},
		{objectstore, dashboard.OfferingInput{Version: "1.0.0"}, "VersionConflict"},
	} {
		a := planOfferingIn(t, c.key, c.in)
		if code(a.Refusal) != c.want || a.Refusal.Command == "" || a.Plan != nil {
			t.Errorf("%s %+v: refusal %+v, want %s", c.key, c.in, a.Refusal, c.want)
		}
	}
	// A re-acceptance names no owner.
	if a := planOfferingIn(t, objectstore, dashboard.OfferingInput{Version: "1.1.0"}); a.Refusal != nil || a.Plan.Owner != "" {
		t.Errorf("re-acceptance = %+v", a)
	}
}

// TestDemoAcceptOfferingOpenAndUnknown: an open schema is refused naming
// each path, and a record the tab never read is refused.
func TestDemoAcceptOfferingOpenAndUnknown(t *testing.T) {
	open := demoXRD{slug: "xopen", classes: []string{"a"}, properties: `"extra": {}`}
	w := dashboard.DeclareWorkload{Offering: "xopen", From: demoOfferingRepository + "@" + open.digest()}
	if a := planOffering(w, &dashboard.OfferingInput{Version: "1.0.0"}); code(a.Refusal) != "OfferingSourceInvalid" {
		t.Fatalf("unknown source = %+v", a.Refusal)
	}
	saved := demoXBucket
	t.Cleanup(func() { demoXBucket = saved })
	demoXBucket = open
	a := planOffering(w, &dashboard.OfferingInput{Version: "1.0.0", Owner: "t"})
	if code(a.Refusal) != "OfferingSchemaOpen" || !strings.Contains(a.Refusal.Message, "open at provisioning: schema.properties.extra: close each path") {
		t.Fatalf("open = %+v", a.Refusal)
	}
}

// TestDemoAcceptOfferingSignsNothing: the demo carries out no acceptance.
func TestDemoAcceptOfferingSignsNothing(t *testing.T) {
	who := "alice@example.com"
	h := declareDemo(t, &who)
	for _, in := range []dashboard.OfferingInput{{Version: "1.0.0", Owner: "team-storage"}, {}} {
		var a dashboard.DeclareAcceptAnswer
		post(t, h, "/api/fleet/declare/accept", dashboard.DeclareAcceptInput{ServiceKey: string(demoXBucketKey), Mode: "sign", IntentDigest: "sha256:x", OfferingAcceptance: &in}, &a)
		if a.Refusal == nil || a.Published != "" || a.Refusal.Command == "" {
			t.Errorf("%+v: answer = %+v", in, a)
		}
	}
}

// TestDemoTourAcceptOfferingStep: after the claim step the fleet tour opens
// xbucket's page, the one its key routes to, and lights its Accept fold.
func TestDemoTourAcceptOfferingStep(t *testing.T) {
	steps := tourSteps(t, "FLEET_TOUR")
	claim, accept := -1, -1
	for i, s := range steps {
		if strings.Contains(s.target, `[data-testid="external-owner"]`) {
			claim = i
		}
		if s.title == "Accept an inferred offering" {
			accept = i
		}
	}
	if accept < 0 || accept < claim || steps[accept].gated || steps[accept].target != `'[data-testid="offering-accept"]'` {
		t.Fatalf("accept step at %d after the claim at %d: %+v", accept, claim, steps)
	}
	js, _ := os.ReadFile("tours.js")
	want := `var XBUCKET = "#/fleet/services/` + url.PathEscape(string(demoXBucketKey)) + `";`
	if !strings.Contains(string(js), want) {
		t.Errorf("tours.js lacks %s", want)
	}
}
