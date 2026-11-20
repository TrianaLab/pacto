package inferredcontract

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

func publishOK(t *testing.T, d Deps, root string, snap *fleet.FleetSnapshot) Report {
	t.Helper()
	rep, err := Publish(context.Background(), d, root, snap)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestPublishPushesSignedAnnotated(t *testing.T) {
	host, reg := testRegistry(t)
	d := deps(t, reg)
	root := host + "/demo/pacto-inferred"
	rep := publishOK(t, d, root, stripeFleet(t, []string{"api.stripe.com"}))
	if len(rep.Published) != 1 || rep.Published[0].Version != "0.0.1" || len(rep.Errors) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	repo := root + "/stripe"
	h, err := reg.Head(context.Background(), repo+":0.0.1")
	if err != nil || h == nil || h.Annotations[contract.AnnotationProvenance] != contract.ProvenanceInferred || h.Digest != rep.Published[0].Digest {
		t.Fatalf("head = %+v, %v", h, err)
	}
	if err := publish.VerifyDigest(context.Background(), reg, d.Verify, repo, h.Digest); err != nil {
		t.Errorf("signature: %v", err)
	}
	b, _ := reg.Pull(context.Background(), repo+":0.0.1")
	if b.Contract.Service.External != "stripe" || b.Contract.Service.Name != "stripe" {
		t.Errorf("contract = %+v", b.Contract.Service)
	}
}

func TestPublishUnchangedPushesNothing(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	publishOK(t, d, root, stripeFleet(t, []string{"api.stripe.com"}))
	rep := publishOK(t, d, root, stripeFleet(t, []string{"api.stripe.com"}, "pay-agent"))
	tags, _ := reg.Tags(context.Background(), root+"/stripe")
	if len(rep.Published) != 0 || rep.Unchanged != 1 || !slices.Equal(tags, []string{"0.0.1"}) {
		t.Errorf("report = %+v, tags %v", rep, tags)
	}
}

func TestPublishNeverOverwritesTag(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	publishOK(t, d, root, stripeFleet(t, []string{"api.stripe.com"}))
	d.Registry = hooked{Registry: reg, tags: func(string) []string { return nil }}
	rep := publishOK(t, d, root, stripeFleet(t, []string{"api.stripe.com", "files.stripe.com"}))
	if len(rep.Published) != 0 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, "never overwritten") {
		t.Errorf("report = %+v", rep)
	}
}

func TestPublishSignsUnsignedNewest(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	signer := d.Signer
	d.Signer = failSigner{}
	snap := stripeFleet(t, []string{"api.stripe.com"})
	if rep := publishOK(t, d, root, snap); len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, "signing 0.0.1") {
		t.Fatalf("failed signing = %+v", rep)
	}
	d.Signer = failSigner{}
	d.Now = func() time.Time { return now.Add(time.Hour) }
	if rep := publishOK(t, d, root, snap); len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, "signing the unsigned tag") || len(rep.Published) != 0 {
		t.Fatalf("failed repair = %+v", rep)
	}
	if tags, _ := reg.Tags(context.Background(), root+"/stripe"); !slices.Equal(tags, []string{"0.0.1"}) {
		t.Fatalf("a failed repair pushed %v", tags)
	}
	d.Signer = signer
	rep := publishOK(t, d, root, snap)
	if len(rep.Repaired) != 1 || rep.Repaired[0].Version != "0.0.1" || rep.Unchanged != 1 || len(rep.Published) != 0 {
		t.Fatalf("repair = %+v", rep)
	}
	if err := publish.VerifyDigest(context.Background(), reg, d.Verify, root+"/stripe", rep.Repaired[0].Digest); err != nil {
		t.Errorf("the repaired tag does not verify: %v", err)
	}
}

func TestPublishNeverSignsForeignTag(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	repo := root + "/stripe"
	snap := stripeFleet(t, []string{"api.stripe.com"})
	foreign, _ := Bundle("stripe", Surface(stripeFleet(t, []string{"evil.example"}), "stripe", now), "0.0.9", inputs(d, snap))
	digest := pushUnsigned(t, reg, repo, "0.0.9", foreign)
	rep := publishOK(t, d, root, snap)
	if len(rep.Errors) != 1 || rep.Errors[0].Version != "0.0.9" || !strings.Contains(rep.Errors[0].Error, "never used") {
		t.Fatalf("errors = %+v", rep.Errors)
	}
	if len(rep.Published) != 1 || rep.Published[0].Version != "0.0.10" || len(rep.Repaired) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if err := publish.VerifyDigest(context.Background(), reg, d.Verify, repo, digest); err == nil {
		t.Error("the installation key signed a foreign tag")
	}
	if got := hostValues(surfaceAt(t, reg, repo+":0.0.10")); got != "api.stripe.com" {
		t.Errorf("hosts = %s", got)
	}
}

func TestPublishMergeIgnoresUnverifiedPrev(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	repo := root + "/stripe"
	publishOK(t, d, root, stripeFleet(t, []string{"a.stripe.com"}))
	injected, _ := Bundle("stripe", Surface(stripeFleet(t, []string{"a.stripe.com", "evil.example"}), "stripe", now), "0.0.2", contract.InferredInputs{Pacto: "x", Discovery: "1"})
	pushUnsigned(t, reg, repo, "0.0.2", injected)
	partial := stripeFleet(t, []string{"b.stripe.com"})
	partial.Completeness = fleet.CompletenessPartial
	rep := publishOK(t, d, root, partial)
	if len(rep.Published) != 1 || rep.Published[0].Version != "0.0.3" {
		t.Fatalf("report = %+v", rep)
	}
	if got := hostValues(surfaceAt(t, reg, repo+":0.0.3")); got != "a.stripe.com,b.stripe.com" {
		t.Errorf("hosts = %s", got)
	}
}

func TestPartialSnapshotNeverRemoves(t *testing.T) {
	for name, mark := range map[string]func(*fleet.FleetSnapshot){
		"snapshot partial": func(s *fleet.FleetSnapshot) { s.Completeness = fleet.CompletenessPartial },
		"target limitation": func(s *fleet.FleetSnapshot) {
			for _, tg := range s.Targets {
				tg.Limitations = append(tg.Limitations, fleet.Limitation{Code: fleet.LimitationExternalsUnreported, Message: "x"})
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			host, reg := testRegistry(t)
			d, root := deps(t, reg), host+"/demo/pacto-inferred"
			repo := root + "/stripe"
			publishOK(t, d, root, stripeFleet(t, []string{"a.stripe.com", "b.stripe.com"}))
			snap := stripeFleet(t, []string{"a.stripe.com", "c.stripe.com"})
			mark(snap)
			if rep := publishOK(t, d, root, snap); len(rep.Published) != 1 {
				t.Fatalf("report = %+v", rep)
			}
			s := surfaceAt(t, reg, repo+":0.0.2")
			if hostValues(s) != "a.stripe.com,b.stripe.com,c.stripe.com" || s.Completeness.State != "partial" {
				t.Errorf("surface = %s %+v", hostValues(s), s.Completeness)
			}
			// Only a complete snapshot removes a value.
			publishOK(t, d, root, stripeFleet(t, []string{"a.stripe.com"}))
			if got := hostValues(surfaceAt(t, reg, repo+":0.0.3")); got != "a.stripe.com" {
				t.Errorf("complete hosts = %s", got)
			}
		})
	}
}

// noRegistry fails the test on any call.
type noRegistry struct{ publish.Registry }

func TestPublishRefusesWithoutSigner(t *testing.T) {
	d := Deps{Registry: noRegistry{}, Now: func() time.Time { return now }}
	if _, err := Publish(context.Background(), d, "r/pacto-inferred", stripeFleet(t, []string{"api.stripe.com"})); !errors.Is(err, ErrNoSigner) {
		t.Errorf("no signer = %v", err)
	}
	d = deps(t, noRegistry{})
	if _, err := Publish(context.Background(), d, "r/elsewhere", stripeFleet(t, []string{"api.stripe.com"})); err == nil || !strings.Contains(err.Error(), "no pacto-inferred repository") {
		t.Errorf("another root = %v", err)
	}
}

// manyFleet is checkout calling n external systems.
func manyFleet(t *testing.T, n int) *fleet.FleetSnapshot {
	t.Helper()
	f := fixture{svcs: []svc{{name: "checkout", ns: "shop"}}}
	for i := range n {
		id := fmt.Sprintf("host/e%02d.example", i)
		u := use("checkout", id, fmt.Sprintf("api.e%02d.example", i), 0)
		u.Class, u.Entry, u.Catalog = "unclassified", "", ""
		f.uses = append(f.uses, u)
	}
	return f.build(t)
}

func TestPublishBoundPerRun(t *testing.T) {
	host, reg := testRegistry(t)
	rep := publishOK(t, deps(t, reg), host+"/demo/pacto-inferred", manyFleet(t, 25))
	if len(rep.Published) != MaxInferredPublishesPerRun || !rep.Capped || len(rep.Errors) != 0 {
		t.Errorf("report = %+v", rep)
	}
}

func TestPublishTagBound(t *testing.T) {
	host, reg := testRegistry(t)
	d := deps(t, reg)
	d.Registry = hooked{Registry: reg, tags: func(string) []string { return make([]string, MaxInferredTags+1) }}
	rep := publishOK(t, d, host+"/demo/pacto-inferred", stripeFleet(t, []string{"api.stripe.com"}))
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, "at most 1000") {
		t.Errorf("report = %+v", rep)
	}
}

func TestPublishSkipsOtherEgress(t *testing.T) {
	host, reg := testRegistry(t)
	snap := stripeFleet(t, []string{"api.stripe.com"})
	snap.Externals[fleet.OtherEgress] = &fleet.ExternalRecord{Key: fleet.OtherEgress, ConsumersTotal: 3}
	snap.Externals["twilio"] = &fleet.ExternalRecord{Key: "twilio"}
	rep := publishOK(t, deps(t, reg), host+"/demo/pacto-inferred", snap)
	if rep.Skipped != 2 || len(rep.Published) != 1 {
		t.Errorf("report = %+v", rep)
	}
}

func TestPublishOneErrorDoesNotStopOthers(t *testing.T) {
	host, reg := testRegistry(t)
	snap := stripeFleet(t, []string{"api.stripe.com"})
	snap.Externals["a: b"] = &fleet.ExternalRecord{Key: "a: b", ConsumersTotal: 1}
	d := deps(t, reg)
	d.Registry = hooked{Registry: reg, tags: func(string) []string { return nil }}
	rep := publishOK(t, d, host+"/demo/pacto-inferred", snap)
	if len(rep.Errors) != 1 || rep.Errors[0].Identity != "a: b" || len(rep.Published) != 1 {
		t.Errorf("report = %+v", rep)
	}
}

// TestPublishRegistryFailures: each failed read or write is an error of its
// external, never a push.
func TestPublishRegistryFailures(t *testing.T) {
	boom := errors.New("boom")
	failHead := func(tag string) func(string) error {
		return func(ref string) error {
			if strings.HasSuffix(ref, ":"+tag) {
				return boom
			}
			return nil
		}
	}
	for name, c := range map[string]struct {
		h    func(publish.Registry) hooked
		want string
	}{
		"head new": {func(r publish.Registry) hooked { return hooked{Registry: r, head: failHead("0.0.2")} }, "boom"},
		"write":    {func(r publish.Registry) hooked { return hooked{Registry: r, write: boom} }, "pushing 0.0.2"},
		"tag":      {func(r publish.Registry) hooked { return hooked{Registry: r, tag: boom} }, "tagging 0.0.2"},
		"head old": {func(r publish.Registry) hooked { return hooked{Registry: r, head: failHead("0.0.1")} }, "reading the tag"},
		"pull":     {func(r publish.Registry) hooked { return hooked{Registry: r, pull: boom} }, "pulling the tag"},
	} {
		t.Run(name, func(t *testing.T) {
			host, reg := testRegistry(t)
			d, root := deps(t, reg), host+"/demo/pacto-inferred"
			publishOK(t, d, root, stripeFleet(t, []string{"a.stripe.com"}))
			d.Registry = c.h(reg)
			rep := publishOK(t, d, root, stripeFleet(t, []string{"a.stripe.com", "b.stripe.com"}))
			if !slices.ContainsFunc(rep.Errors, func(e Entry) bool { return strings.Contains(e.Error, c.want) }) {
				t.Errorf("report = %+v", rep)
			}
		})
	}
	host, reg := testRegistry(t)
	d := deps(t, reg)
	d.Registry = hooked{Registry: listFails{reg}}
	if rep := publishOK(t, d, host+"/demo/pacto-inferred", stripeFleet(t, []string{"a.stripe.com"})); len(rep.Errors) != 1 {
		t.Errorf("a failed listing = %+v", rep)
	}
}

// listFails fails every tag listing.
type listFails struct{ publish.Registry }

func (listFails) Tags(context.Context, string) ([]string, error) {
	return nil, errors.New("listing failed")
}

func fstestMap(raw string) fstest.MapFS { return fstest.MapFS{"pacto.yaml": {Data: []byte(raw)}} }

func TestPublishSkipsTagWithoutSurface(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	raw := "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.0.1\n"
	c, _ := contract.Parse(strings.NewReader(raw))
	pushUnsigned(t, reg, root+"/stripe", "0.0.1", &contract.Bundle{Contract: c, RawYAML: []byte(raw), FS: fstestMap(raw)})
	rep := publishOK(t, d, root, stripeFleet(t, []string{"a.stripe.com"}))
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, "no inferred surface") || len(rep.Published) != 1 {
		t.Errorf("report = %+v", rep)
	}
}

func TestPublishReadsOnlyTheNewestTags(t *testing.T) {
	host, reg := testRegistry(t)
	d, root := deps(t, reg), host+"/demo/pacto-inferred"
	repo := root + "/stripe"
	snap := stripeFleet(t, []string{"a.stripe.com"})
	foreign, _ := Bundle("stripe", Surface(stripeFleet(t, []string{"evil.example"}), "stripe", now), "0.0.1", inputs(d, snap))
	for i := 1; i <= maxTagsVerified+1; i++ {
		pushUnsigned(t, reg, repo, fmt.Sprintf("0.0.%d", i), foreign)
	}
	rep := publishOK(t, d, root, snap)
	if len(rep.Errors) != maxTagsVerified || len(rep.Published) != 1 || rep.Published[0].Version != "0.0.12" {
		t.Errorf("report = %+v", rep)
	}
}

// seen records every request the guard let through.
type seen struct {
	base http.RoundTripper
	mu   sync.Mutex
	reqs []string
}

func (s *seen) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	return s.base.RoundTrip(r)
}

func TestPublishOnlyUnderInferredPrefix(t *testing.T) {
	host, _ := testRegistry(t)
	rec := &seen{base: http.DefaultTransport}
	guarded := publish.NewClientWith(anonymous, oci.WithTransport(oci.NewPushScope(host+"/demo").Guard(rec)))
	d := deps(t, guarded)
	rep := publishOK(t, d, host+"/demo/pacto-inferred", stripeFleet(t, []string{"api.stripe.com"}))
	if len(rep.Published) != 1 || len(rep.Errors) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	digest := rep.Published[0].Digest
	var image, tag, referrer bool
	for _, r := range rec.reqs {
		image = image || r == "PUT /v2/demo/pacto-inferred/stripe/manifests/"+digest
		tag = tag || r == "PUT /v2/demo/pacto-inferred/stripe/manifests/0.0.1"
		referrer = referrer || (strings.HasPrefix(r, "PUT /v2/demo/pacto-inferred/stripe/manifests/sha256:") && !strings.HasSuffix(r, digest))
	}
	if !image || !tag || !referrer {
		t.Errorf("the guard saw image %v, tag %v, signature %v: %v", image, tag, referrer, rec.reqs)
	}
	outside := publish.NewClientWith(anonymous, oci.WithTransport(oci.NewPushScope(host+"/other").Guard(nil)))
	d.Registry = outside
	rep = publishOK(t, d, host+"/demo/pacto-inferred", stripeFleet(t, []string{"api.stripe.com", "files.stripe.com"}))
	if len(rep.Published) != 0 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0].Error, oci.ErrOutsidePushScope.Error()) {
		t.Errorf("outside the scope = %+v", rep)
	}
}
