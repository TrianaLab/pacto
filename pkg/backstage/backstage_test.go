package backstage

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var update = flag.Bool("update", false, "rewrite testdata/entities.golden.yaml")

var fixed = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// rev is a revision of name at version in the registry domain, adjusted by mut.
func rev(domain, name, version string, mut ...func(*contract.Contract)) fleet.RawRevision {
	c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: version}}
	for _, m := range mut {
		m(c)
	}
	return fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}},
		Domain: domain, Digest: "sha256:" + domain + name + version,
	}
}

func team(t string) func(*contract.Contract) {
	return func(c *contract.Contract) { c.Service.Owner = contract.Owner{Team: t} }
}

func bizDomain(d string) func(*contract.Contract) {
	return func(c *contract.Contract) { c.Service.Domain = d }
}

func deprecated(c *contract.Contract) {
	c.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-10-01"}
}

// target is a Kubernetes target of service name running digest.
func target(name, digest string, owner contract.Owner) fleet.RawTarget {
	return fleet.RawTarget{
		Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: name + "-" + digest, Service: name,
		Digest: digest, Owner: owner, Compliance: fleet.StatusCompliant,
	}
}

// snapshot builds a snapshot from one memory source per collection.
func snapshot(t *testing.T, cols ...*fleet.Collection) *fleet.FleetSnapshot {
	t.Helper()
	var sources []fleet.Source
	for i, c := range cols {
		sources = append(sources, fleet.NewMemorySource("s"+string(rune('a'+i)), "local", c))
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixed }}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func mapSnap(snap *fleet.FleetSnapshot) []Entity { return Map(fleet.NewQuery(snap)) }

func mapCols(t *testing.T, cols ...*fleet.Collection) []Entity {
	t.Helper()
	return mapSnap(snapshot(t, cols...))
}

// byKey finds the Component of service key.
func byKey(t *testing.T, es []Entity, key string) Entity {
	t.Helper()
	for _, e := range es {
		if e.Kind == kindComp && e.Metadata.Annotations[AnnotationServiceKey] == key {
			return e
		}
	}
	t.Fatalf("no component for %s in %+v", key, es)
	return Entity{}
}

func ofKind(es []Entity, kind string) []Entity {
	var out []Entity
	for _, e := range es {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func encode(t *testing.T, es []Entity) string {
	t.Helper()
	var b bytes.Buffer
	if err := Encode(&b, es); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestEntityNameTable(t *testing.T) {
	long := strings.Repeat("a", 70)
	for _, tc := range []struct{ base, want string }{
		{"payments", "payments"},
		{"a/b c", "a-b-c"},
		{"--a..b__", "a..b"},
		{"a//b", "a-b"},
		{"", "x"},
		{"///", "x"},
	} {
		if got := entityName(tc.base, "id", 0, sha256Hex); got != tc.want {
			t.Errorf("entityName(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
	got := assignNames([]nameRequest{{base: long, identity: "k"}}, sha256Hex)[0]
	if want := strings.Repeat("a", 54) + "-" + sha256Hex("k")[:8]; got != want {
		t.Errorf("long name = %q, want %q", got, want)
	}
	for _, n := range []string{got, entityName("p", "k", 64, sha256Hex), entityName(long, "k", 16, sha256Hex)} {
		if len(n) > maxName || !namePattern.MatchString(n) {
			t.Errorf("%q is no valid Backstage name", n)
		}
	}
}

func TestCollisionAcmeOther(t *testing.T) {
	es := mapCols(t,
		&fleet.Collection{Revisions: []fleet.RawRevision{rev("ghcr.io/acme", "payments", "1.0.0")}},
		&fleet.Collection{Revisions: []fleet.RawRevision{rev("ghcr.io/other", "payments", "1.0.0")}},
	)
	comps := ofKind(es, kindComp)
	if len(comps) != 2 {
		t.Fatalf("components = %d, want 2", len(comps))
	}
	seen := map[string]bool{}
	for _, c := range comps {
		key := c.Metadata.Annotations[AnnotationServiceKey]
		if want := "payments-" + sha256Hex(key)[:8]; c.Metadata.Name != want {
			t.Errorf("name = %q, want %q", c.Metadata.Name, want)
		}
		if c.Metadata.Title != "payments" || !strings.Contains(key, "payments") || !strings.Contains(key, "/") {
			t.Errorf("title %q key %q", c.Metadata.Title, key)
		}
		seen[c.Metadata.Name] = true
	}
	if len(seen) != 2 {
		t.Error("suffixes are not distinct")
	}
}

func TestCollisionSuffixStillUnique(t *testing.T) {
	// The fake hash collides on the first 16 hex digits and differs after them.
	fake := func(id string) string { return strings.Repeat("0", 16) + sha256Hex(id)[16:] }
	reqs := []nameRequest{{base: "pay", identity: "a"}, {base: "pay", identity: "b"}, {base: "solo", identity: "c", force: true}}
	names := assignNames(reqs, fake)
	if names[0] == names[1] {
		t.Fatalf("names collide: %v", names)
	}
	for _, n := range names[:2] {
		if len(n) != maxName || !namePattern.MatchString(n) {
			t.Errorf("full-digest name %q", n)
		}
	}
	if names[2] != "solo-00000000" {
		t.Errorf("forced name = %q", names[2])
	}
	// A hash that never tells two identities apart stops at the full digest.
	same := func(string) string { return strings.Repeat("0", 64) }
	if names := assignNames(reqs[:2], same); names[0] != names[1] {
		t.Errorf("names = %v", names)
	}
}

func TestOwnerRefTable(t *testing.T) {
	for _, tc := range []struct {
		owner contract.Owner
		want  string
	}{
		{contract.Owner{Team: "billing"}, "group:default/billing"},
		{contract.Owner{DRI: "alice"}, "user:default/alice"},
		{contract.Owner{Team: "acme-billing"}, "group:default/acme-billing"},
		{contract.Owner{Team: "acme/billing"}, ""},
		{contract.Owner{DRI: "alice@acme.com"}, ""},
		{contract.Owner{Team: strings.Repeat("a", 64)}, ""},
		{contract.Owner{Contacts: []contract.OwnerContact{{Type: "email", Value: "a@b.c"}}}, ""},
	} {
		if got, _ := ownerRef(tc.owner); got != tc.want {
			t.Errorf("ownerRef(%+v) = %q, want %q", tc.owner, got, tc.want)
		}
	}
	es := mapCols(t, &fleet.Collection{Revisions: []fleet.RawRevision{
		rev("", "a", "1.0.0", team("acme/billing")),
		rev("", "b", "1.0.0", team("acme-billing")),
		rev("", "c", "1.0.0", func(c *contract.Contract) { c.Service.Owner = contract.Owner{DRI: "alice@acme.com"} }),
		rev("", "d", "1.0.0", func(c *contract.Contract) {
			c.Service.Owner = contract.Owner{Contacts: []contract.OwnerContact{{Type: "email", Value: "a@b.c"}}}
		}),
	}})
	refs := map[string]bool{}
	for _, tc := range []struct{ key, owner, raw string }{
		{"a", unknown, "team:acme/billing"},
		{"b", "group:default/acme-billing", ""},
		{"c", unknown, "dri:alice@acme.com"},
		{"d", unknown, ""},
	} {
		e := byKey(t, es, tc.key)
		ann := e.Metadata.Annotations
		if e.Spec.Owner != tc.owner || ann[AnnotationOwner] != tc.raw || (ann[AnnotationOwnerUnmapped] == "true") != (tc.raw != "") {
			t.Errorf("%s: owner %q annotations %v", tc.key, e.Spec.Owner, ann)
		}
		if e.Spec.Owner != unknown {
			if refs[e.Spec.Owner] {
				t.Errorf("two owners share %s", e.Spec.Owner)
			}
			refs[e.Spec.Owner] = true
		}
	}
}

func TestOwnerWhitelist(t *testing.T) {
	for _, tc := range []struct{ provenance, want string }{
		{fleet.OwnerProvenanceDeclared, "group:default/pay"},
		{fleet.OwnerProvenanceDeclaredViaTarget, "group:default/pay"},
		{fleet.OwnerProvenanceAttested, "group:default/pay"},
		{fleet.OwnerProvenanceInferred, unknown},
		{"", unknown},
	} {
		snap := snapshot(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("", "pay", "1.0.0", team("pay"))}})
		snap.Services[fleet.NewServiceKey("pay")].OwnerProvenance = tc.provenance
		e := byKey(t, mapSnap(snap), "pay")
		got, has := e.Metadata.Annotations[AnnotationOwnerProvenance]
		if e.Spec.Owner != tc.want || got != tc.provenance || has != (tc.provenance != "") {
			t.Errorf("%q: owner %q, owner-provenance %q (%v)", tc.provenance, e.Spec.Owner, got, has)
		}
	}
}

// A factual provenance with no current claim behind it exports no owner.
func TestOwnerWhitelistNeedsOneClaim(t *testing.T) {
	k := fleet.NewServiceKey("pay")
	es := Map(fleet.NewQuery(&fleet.FleetSnapshot{Services: map[fleet.ServiceKey]*fleet.ServiceRecord{
		k: {Key: k, Name: "pay", Owner: contract.Owner{Team: "pay"}, OwnerProvenance: fleet.OwnerProvenanceAttested},
	}}))
	if e := byKey(t, es, "pay"); e.Spec.Owner != unknown {
		t.Errorf("owner = %q with no claim", e.Spec.Owner)
	}
}

func TestOwnerNeverInferred(t *testing.T) {
	snap := snapshot(t, &fleet.Collection{
		Revisions: []fleet.RawRevision{rev("", "pay", "1.0.0", team("pay")), rev("", "shop", "1.0.0", bizDomain("commerce"), team("shop"))},
		Targets:   []fleet.RawTarget{target("edge", "sha256:none", contract.Owner{Team: "edge"})},
	})
	for _, s := range snap.Services {
		s.OwnerProvenance = fleet.OwnerProvenanceInferred
	}
	for _, e := range mapSnap(snap) {
		if e.Spec.Owner != unknown {
			t.Errorf("%s %s owner = %q from an inferred owner", e.Kind, e.Metadata.Name, e.Spec.Owner)
		}
	}
}

func TestOwnerConflictExportsUnknown(t *testing.T) {
	es := mapCols(t, &fleet.Collection{Targets: []fleet.RawTarget{
		target("pay", "sha256:a", contract.Owner{Team: "pay"}),
		target("pay", "sha256:b", contract.Owner{Team: "ops"}),
	}})
	e := byKey(t, es, "pay")
	if e.Spec.Owner != unknown || e.Metadata.Annotations[AnnotationOwnerConflict] != "true" || e.Metadata.Annotations[AnnotationCompleteness] != "partial" {
		t.Errorf("owner %q annotations %v", e.Spec.Owner, e.Metadata.Annotations)
	}
}

func TestLifecycleTable(t *testing.T) {
	plain := snapshot(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("", "pay", "1.0.0")}})
	dep := snapshot(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("", "pay", "1.0.0", deprecated)}})
	running := func() *fleet.FleetSnapshot {
		return snapshot(t, &fleet.Collection{
			Revisions: []fleet.RawRevision{rev("", "pay", "1.0.0"), rev("", "pay", "2.0.0", deprecated)},
			Targets:   []fleet.RawTarget{target("pay", "sha256:pay1.0.0", contract.Owner{})},
		})
	}
	unreadable := running()
	for _, r := range unreadable.Revisions {
		if r.Contract != nil && r.Contract.Service.Version == "1.0.0" {
			r.Contract = nil
		}
	}
	targetOnly := snapshot(t, &fleet.Collection{Targets: []fleet.RawTarget{target("pay", "sha256:x", contract.Owner{})}})
	for _, tc := range []struct {
		name string
		snap *fleet.FleetSnapshot
		want string
	}{
		{"no lifecycle", plain, unknown},
		{"deprecated", dep, "deprecated"},
		{"newer deprecated, older running", running(), unknown},
		{"running revision unreadable", unreadable, unknown},
		{"target only", targetOnly, unknown},
	} {
		if got := byKey(t, mapSnap(tc.snap), "pay").Spec.Lifecycle; got != tc.want {
			t.Errorf("%s: lifecycle = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSystemTable(t *testing.T) {
	snap := snapshot(t, &fleet.Collection{Revisions: []fleet.RawRevision{
		rev("", "pay", "1.0.0", bizDomain("payments")),
		rev("", "ledger", "1.0.0", bizDomain("payments")),
		rev("", "shop", "1.0.0", bizDomain("commerce")),
		rev("", "plain", "1.0.0"),
	}})
	snap.Services[fleet.NewServiceKey("shop")].BusinessDomainUnknown = true
	es := mapSnap(snap)
	systems := ofKind(es, kindSystem)
	if len(systems) != 1 || systems[0].Metadata.Name != "payments" || systems[0].Spec.Owner != unknown {
		t.Fatalf("systems = %+v, want one payments System", systems)
	}
	for _, tc := range []struct{ key, system, ann string }{
		{"pay", "payments", ""}, {"ledger", "payments", ""}, {"shop", "", unknown}, {"plain", "", ""},
	} {
		e := byKey(t, es, tc.key)
		if e.Spec.System != tc.system || e.Metadata.Annotations[AnnotationBusinessDomain] != tc.ann {
			t.Errorf("%s: system %q annotations %v", tc.key, e.Spec.System, e.Metadata.Annotations)
		}
	}
}

func TestEntityProvenanceTable(t *testing.T) {
	read := target("edge", "sha256:x", contract.Owner{})
	read.BusinessDomainKnown = true
	snap := snapshot(t, &fleet.Collection{
		Revisions: []fleet.RawRevision{
			rev("", "pay", "1.0.0"),
			rev("", "broken", "1.0.0", func(c *contract.Contract) { c.PactoVersion = "0.0" }),
		},
		Targets: []fleet.RawTarget{read},
	})
	snap.Services[fleet.NewServiceKey("pay")].NameInferred = &contract.Inferred{Field: "service.name", Value: "pay", Source: "oci-ref"}
	es := mapSnap(snap)
	for _, key := range []string{"pay", "broken", "edge"} {
		e := byKey(t, es, key)
		if e.Metadata.Annotations[AnnotationProvenance] != provenanceDeclared || len(e.Metadata.Tags) != 0 {
			t.Errorf("%s: %v tags %v, want declared and no tag", key, e.Metadata.Annotations, e.Metadata.Tags)
		}
	}
	if ann := byKey(t, es, "pay").Metadata.Annotations; ann[AnnotationNameProvenance] != provenanceInferred || ann[AnnotationNameSource] != "oci-ref" {
		t.Errorf("name provenance %v", ann)
	}
	if _, has := byKey(t, es, "edge").Metadata.Annotations[AnnotationNameProvenance]; has {
		t.Error("a declared name carries name provenance")
	}

	ghost := fleet.NewServiceKey("ghost")
	inferred := Map(fleet.NewQuery(&fleet.FleetSnapshot{
		Services:    map[fleet.ServiceKey]*fleet.ServiceRecord{ghost: {Key: ghost, Name: "ghost"}},
		GeneratedAt: fixed,
	}))
	e := byKey(t, inferred, "ghost")
	if e.Metadata.Annotations[AnnotationProvenance] != provenanceInferred || len(e.Metadata.Tags) != 1 || e.Metadata.Tags[0] != TagInferred || e.Spec.Owner != unknown {
		t.Errorf("inferred record exported as %+v", e)
	}
}

func TestGoldenPathNotExported(t *testing.T) {
	snap := snapshot(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("", "pay", "1.0.0"), rev("", "starter", "1.0.0")}})
	snap.Services[fleet.NewServiceKey("starter")].Template = true
	for _, e := range mapSnap(snap) {
		if e.Metadata.Annotations[AnnotationServiceKey] == "starter" {
			t.Fatal("a golden path was exported")
		}
	}
}

func TestAsOfIsSnapshotTime(t *testing.T) {
	es := mapCols(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("", "pay", "1.0.0", bizDomain("payments"))}})
	for _, e := range es {
		if got := e.Metadata.Annotations[AnnotationAsOf]; got != "2026-11-16T12:00:00Z" {
			t.Errorf("%s as-of = %q", e.Kind, got)
		}
		if e.Metadata.Annotations[AnnotationCompleteness] != string(fleet.CompletenessComplete) {
			t.Errorf("%s completeness = %v", e.Kind, e.Metadata.Annotations)
		}
	}
}

// goldenSources is the golden fleet in two sources: a suffixed collision, a
// deprecated service, an unknown owner, a System, an interface a dependency
// requires, a declared and observed external, an inferred external, an
// observed call and an uncontracted caller.
func goldenSources() []*fleet.Collection {
	checkout := rev("ghcr.io/acme", "checkout", "2.1.0", team("commerce"), bizDomain("commerce"), deprecated, func(c *contract.Contract) {
		c.Service.Summary = "Takes the order and the money"
		c.Dependencies = []contract.Dependency{
			{Name: "payments", Required: true, Requires: []contract.Requirement{{Interface: "rest"}}},
			{Name: "stripe", External: true, Version: "2024-06-20"},
		}
	})
	at := fixed.Add(-time.Hour)
	running := target("checkout", checkout.Digest, contract.Owner{})
	running.Domain = "ghcr.io/acme"
	return []*fleet.Collection{
		{
			Revisions: []fleet.RawRevision{
				rev("ghcr.io/acme", "payments", "1.0.0", team("billing"), bizDomain("payments"), func(c *contract.Contract) {
					c.Interfaces = []contract.Interface{{Name: "rest", Type: "openapi", Ref: "interfaces/openapi.yaml"}}
				}),
				checkout,
				rev("ghcr.io/acme", "legacy", "0.9.0", team("acme/billing")),
			},
			Targets: []fleet.RawTarget{running},
			ExternalUses: []fleet.RawExternalUse{{Service: "checkout", Domain: "ghcr.io/acme", Namespace: "shop", Identity: "host/legacy-mq.example",
				Kind: "env", Where: "deployment/checkout env MQ_URL", Host: "mq.legacy-mq.example", Confidence: "likely"}},
		},
		{
			Revisions: []fleet.RawRevision{rev("ghcr.io/other", "payments", "3.0.0")},
			Observed: []fleet.ObservedEdge{
				{From: "k8s:shop/Deployment/legacy-billing", To: "checkout", Count: 3, LastSeen: at},
				{From: "checkout", To: "api.stripe.com", Count: 2, FirstSeen: at, LastSeen: at},
			},
		},
	}
}

func TestGolden(t *testing.T) {
	got := encode(t, mapCols(t, goldenSources()...))
	path := filepath.Join("testdata", "entities.golden.yaml")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("export differs from %s (run with -update):\n%s", path, got)
	}
}

func TestDeterministicAcrossSourceOrder(t *testing.T) {
	src := goldenSources()
	a := encode(t, mapCols(t, src[0], src[1]))
	b := encode(t, mapCols(t, src[1], src[0]))
	if a != b {
		t.Errorf("source order changed the export:\n%s\n---\n%s", a, b)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestEncodeSeparatorsAndTrailingNewline(t *testing.T) {
	out := encode(t, mapCols(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("", "a", "1.0.0"), rev("", "b", "1.0.0")}}))
	if strings.Count(out, "\n---\n") != 1 || strings.HasPrefix(out, "---") || !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("stream:\n%q", out)
	}
	if !strings.Contains(out, "\nmetadata:\n  name: a\n") {
		t.Errorf("indent is not 2 spaces:\n%s", out)
	}
	if err := Encode(failWriter{}, mapCols(t, &fleet.Collection{Revisions: []fleet.RawRevision{rev("", "a", "1.0.0")}})); err == nil {
		t.Error("a failing writer reported no error")
	}
}
