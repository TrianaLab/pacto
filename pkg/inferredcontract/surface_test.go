package inferredcontract

import (
	"context"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func TestSurfaceHostsPortsFromInferredSources(t *testing.T) {
	snap := stripeFleet(t, []string{"api.stripe.com", "files.stripe.com:8443"})
	s := Surface(snap, "stripe", now)
	if hostValues(&s) != "api.stripe.com,files.stripe.com:8443" || len(s.Ports) != 1 || s.Ports[0].Value != "8443" {
		t.Fatalf("surface = %+v", s)
	}
	h := s.Hosts[0]
	if !reflect.DeepEqual(h.Sources, []string{"k8s"}) || h.Catalog != externalcatalog.DefaultDigest() {
		t.Errorf("host provenance = %+v", h)
	}
	if s.Identity != "stripe" || s.Class != "vendor-api" || s.AsOf != "2026-11-17T10:00:00Z" || s.Completeness.State != "complete" {
		t.Errorf("surface header = %+v", s)
	}
	if s.Catalog == nil || s.Catalog.Entry != "stripe" || s.Catalog.Digest != externalcatalog.DefaultDigest() {
		t.Errorf("catalog = %+v", s.Catalog)
	}
}

func TestSurfaceClientsFromRepositoryRecord(t *testing.T) {
	major := 76
	snap := fixture{svcs: []svc{{name: "checkout", ns: "shop", fsys: sdkRecord(&major)}, {name: "billing", ns: "shop", fsys: sdkRecord(nil)}}}.build(t)
	s := Surface(snap, "stripe", now)
	if len(s.Clients) != 1 || s.Clients[0].Value != "stripe-go/76" || s.Clients[0].Sources[0] != "k8s" {
		t.Fatalf("clients = %+v", s.Clients)
	}
	if len(s.Hosts) != 0 || len(s.Ports) != 0 {
		t.Errorf("an sdk source names no host: %+v", s)
	}
	// Another identity's record names no client of stripe.
	if c := Surface(snap, "twilio", now).Clients; c != nil {
		t.Errorf("twilio clients = %+v", c)
	}
}

func TestSurfaceSeenWindow(t *testing.T) {
	f := fixture{svcs: []svc{{name: "checkout", ns: "shop"}, {name: "billing", ns: "shop"}},
		uses: []fleet.RawExternalUse{use("checkout", "stripe", "api.stripe.com", 30), use("billing", "stripe", "api.stripe.com", 2)}}
	f.uses = append(f.uses, use("billing", "stripe", "undated.stripe.com", 0))
	f.uses[2].ObservedAt = nil
	s := Surface(f.build(t), "stripe", now)
	if s.Hosts[0].FirstSeen != "2026-11-17T09:29:00Z" || s.Hosts[0].LastSeen != "2026-11-17T09:57:00Z" {
		t.Errorf("window = %+v", s.Hosts[0])
	}
	if s.Hosts[1].FirstSeen != "" || s.Hosts[1].LastSeen != "" {
		t.Errorf("an undated value has no window: %+v", s.Hosts[1])
	}
}

func TestInferredSurfaceNamesNoConsumer(t *testing.T) {
	snap := stripeFleet(t, []string{"api.stripe.com"}, "pay-agent")
	b, err := Bundle("stripe", Surface(snap, "stripe", now), "0.0.1", inputs(Deps{Pacto: "4.7.0"}, snap), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pacto.yaml", contract.InferredRecordFile} {
		data, _ := fs.ReadFile(b.FS, name)
		if strings.Contains(string(data), "checkout") || strings.Contains(string(data), "pay-agent") {
			t.Errorf("%s names a consumer:\n%s", name, data)
		}
	}
}

func TestBundleMinimalPactoYAML(t *testing.T) {
	snap := stripeFleet(t, []string{"api.stripe.com"})
	b, err := Bundle("stripe", Surface(snap, "stripe", now), "0.0.1", inputs(Deps{Pacto: "4.7.0"}, snap), "")
	if err != nil {
		t.Fatal(err)
	}
	want := "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.0.1\n  external: stripe\n"
	if string(b.RawYAML) != want || b.Contract.Service.External != "stripe" {
		t.Errorf("pacto.yaml = %q", b.RawYAML)
	}
	rec, err := contract.ReadInferredRecord(b.FS)
	if err != nil || rec.Surface == nil || rec.Surface.AsOf != "2026-11-17T10:00:00Z" || rec.Surface.Completeness.State != "complete" {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	if rec.Inputs.Discovery != Version || rec.Inputs.Pacto != "4.7.0" || len(rec.Inputs.Catalog) != 1 || len(rec.Fields) != 0 {
		t.Errorf("inputs = %+v", rec.Inputs)
	}
}

func TestBundleValidates(t *testing.T) {
	if _, err := Bundle("Stripe", contract.InferredSurface{}, "0.0.1", contract.InferredInputs{}, ""); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("an invalid identity = %v", err)
	}
	if _, err := Bundle("a: b", contract.InferredSurface{}, "0.0.1", contract.InferredInputs{}, ""); err == nil {
		t.Error("an identity that breaks the YAML was accepted")
	}
}

func TestBundleDeterministic(t *testing.T) {
	snap := stripeFleet(t, []string{"b.stripe.com", "a.stripe.com"})
	in := inputs(Deps{Pacto: "4.7.0"}, snap)
	b1, _ := Bundle("stripe", Surface(snap, "stripe", now), "0.0.1", in, "")
	b2, _ := Bundle("stripe", Surface(snap, "stripe", now), "0.0.1", in, "")
	if digestOf(b1) != digestOf(b2) {
		t.Error("the same surface packs to two digests")
	}
}

func TestServiceNameFromIdentity(t *testing.T) {
	long := strings.Repeat("a", 62) + ".bcdefgh"
	for id, want := range map[fleet.ExternalKey]string{
		"stripe":                           "stripe",
		"host/corp-legacy.example":         "corp-legacy-example",
		"aws/rds/123456789012/eu-west-1/x": "x",
		fleet.ExternalKey("host/" + long):  strings.Repeat("a", 62),
	} {
		if got := ServiceName(id); got != want {
			t.Errorf("ServiceName(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestNewCallerPublishesNothing(t *testing.T) {
	ctx := context.Background()
	one := stripeFleet(t, []string{"api.stripe.com"})
	two := stripeFleet(t, []string{"api.stripe.com"}, "pay-agent")
	old, _ := Bundle("stripe", Surface(one, "stripe", now), "0.0.1", inputs(Deps{}, one), "")
	cur, _ := Bundle("stripe", Surface(two, "stripe", now.Add(time.Hour)), "0.0.2", inputs(Deps{}, two), "")
	if Changed(ctx, old, cur) {
		t.Error("a new caller of a known host changed the surface")
	}
}

func TestSurfaceChangePublishes(t *testing.T) {
	one := stripeFleet(t, []string{"api.stripe.com"})
	two := stripeFleet(t, []string{"api.stripe.com", "files.stripe.com"})
	old, _ := Bundle("stripe", Surface(one, "stripe", now), "0.0.1", inputs(Deps{}, one), "")
	cur, _ := Bundle("stripe", Surface(two, "stripe", now), "0.0.2", inputs(Deps{}, two), "")
	if !Changed(context.Background(), old, cur) {
		t.Error("a new host is no change")
	}
}

func TestVersionBumpAloneIsNoChange(t *testing.T) {
	snap := stripeFleet(t, []string{"api.stripe.com"})
	s := Surface(snap, "stripe", now)
	old, _ := Bundle("stripe", s, "0.0.1", inputs(Deps{}, snap), "")
	cur, _ := Bundle("stripe", s, "0.0.7", inputs(Deps{}, snap), "")
	if Changed(context.Background(), old, cur) {
		t.Error("a version bump alone is a change")
	}
}

func TestMergeKeepsPreviousValues(t *testing.T) {
	prev := contract.InferredSurface{Hosts: []contract.SurfaceValue{{Value: "a", Sources: []string{"old"}}, {Value: "b", Sources: []string{"old"}}}}
	cur := contract.InferredSurface{Hosts: []contract.SurfaceValue{{Value: "a", Sources: []string{"new"}}, {Value: "c", Sources: []string{"new"}}}}
	got := Merge(prev, cur)
	if hostValues(&got) != "a,b,c" || got.Hosts[0].Sources[0] != "new" || got.Hosts[1].Sources[0] != "old" || got.Ports != nil {
		t.Errorf("merged = %+v", got)
	}
}

func TestPartialCompleteness(t *testing.T) {
	snap := stripeFleet(t, []string{"api.stripe.com"})
	snap.Completeness = fleet.CompletenessPartial
	snap.Limitations = append(snap.Limitations, fleet.Limitation{Code: "SOURCE_FAILED"}, fleet.Limitation{Code: "SOURCE_FAILED"})
	c := Surface(snap, "stripe", now).Completeness
	if c.State != "partial" || !reflect.DeepEqual(c.Limitations, []string{"SOURCE_FAILED"}) {
		t.Errorf("completeness = %+v", c)
	}
	lim := fixture{svcs: []svc{{name: "checkout", ns: "shop"}}, uses: []fleet.RawExternalUse{use("checkout", "stripe", "api.stripe.com", 0)},
		lims: []fleet.Limitation{{Code: fleet.LimitationExternalsUnreported, Message: "x"}}}.build(t)
	if c := Surface(lim, "stripe", now).Completeness; c.State != "partial" || !reflect.DeepEqual(c.Limitations, []string{fleet.LimitationExternalsUnreported}) {
		t.Errorf("target limitation completeness = %+v", c)
	}
}

func TestNextVersion(t *testing.T) {
	for _, c := range []struct {
		tags []string
		want string
	}{{nil, "0.0.1"}, {[]string{"0.0.9", "0.0.12", "latest", "1.0.0", "0.0.01"}, "0.0.13"}} {
		if got := NextVersion(c.tags); got != c.want {
			t.Errorf("NextVersion(%v) = %s, want %s", c.tags, got, c.want)
		}
	}
	if got := versions([]string{"0.0.2", "0.0.10", "0.0.1"}); !reflect.DeepEqual(got, []string{"0.0.10", "0.0.2", "0.0.1"}) {
		t.Errorf("versions = %v", got)
	}
}

func TestRepository(t *testing.T) {
	if got := Repository("zot:5000/demo/pacto-inferred/", "aws/rds/1/eu/x"); got != "zot:5000/demo/pacto-inferred/aws/rds/1/eu/x" {
		t.Errorf("Repository = %s", got)
	}
}
