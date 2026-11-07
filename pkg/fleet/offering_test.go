package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// offerRev is a 2.4 revision of name in ghcr.io/acme that provides offering.
func offerRev(name, version, offering, summary string) RawRevision {
	k := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: version, Offering: offering, Summary: summary}}
	return RawRevision{Bundle: &contract.Bundle{Contract: k, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme", Digest: "sha256:" + name + version}
}

// offeringFleet holds three offerings: postgresql, redis and shared-config with
// two providers, plus payments, which provides none.
func offeringFleet(t *testing.T) *Query {
	t.Helper()
	return buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{
		offerRev("postgresql", "16.0.1", "postgresql", "The primary database"),
		offerRev("redis", "7.0.0", "redis", ""),
		offerRev("platform-app-config", "1.0.0", "shared-config", ""),
		offerRev("platform-worker-config", "1.0.0", "shared-config", ""),
		offerRev("payments", "1.0.0", "", ""),
	}})
}

func acmeService(q *Query, name string) *ServiceRecord {
	return q.snap.Services[NewServiceKeyDomain("ghcr.io/acme", name)]
}

func TestOffering_FromNewestValidRevision(t *testing.T) {
	q := buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{
		offerRev("kv", "1.0.0", "redis", "old"),
		offerRev("kv", "1.1.0", "cache", "A key-value cache"),
	}})
	if s := acmeService(q, "kv"); s.Offering != "cache" || s.Summary != "A key-value cache" {
		t.Fatalf("offering = %q, summary = %q", s.Offering, s.Summary)
	}
}

func TestOffering_InvalidNewestRevisionIgnored(t *testing.T) {
	bad := mustParse(t, invalidYAML)
	bad.PactoVersion, bad.Service.Version, bad.Service.Offering = "2.4", "2.0.0", "cache"
	old := offerRev("bad-svc", "0.9.0", "redis", "")
	old.Domain = ""
	q := buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{
		old, {Bundle: &contract.Bundle{Contract: bad, RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
	}})
	if s := q.snap.Services[NewServiceKeyDomain("", "bad-svc")]; s.Offering != "redis" {
		t.Fatalf("offering = %q, want redis from the newest valid revision", s.Offering)
	}
}

func TestOffering_FallsBackToOperatorReported(t *testing.T) {
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "a", Kind: "k8s", Name: "pg", Service: "pg", Offering: "postgresql", Summary: "The primary database"},
	}})
	if s := q.snap.Services[NewServiceKeyDomain("", "pg")]; s.Offering != "postgresql" || s.Summary != "The primary database" {
		t.Fatalf("offering = %q, summary = %q", s.Offering, s.Summary)
	}
}

func TestOffering_ConflictRaisesLimitation(t *testing.T) {
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "a", Kind: "k8s", Name: "x-a", Service: "x", Offering: "redis", Summary: "one"},
		{Scope: "b", Kind: "k8s", Name: "x-b", Service: "x", Offering: "cache", Summary: "two"},
	}})
	if s := q.snap.Services[NewServiceKeyDomain("", "x")]; s.Offering != "" || s.Summary != "" {
		t.Errorf("offering = %q, summary = %q, want none", s.Offering, s.Summary)
	}
	var got []Limitation
	for _, l := range q.snap.Limitations {
		if l.Code == LimitationOfferingConflict {
			got = append(got, l)
		}
	}
	if len(got) != 1 || !strings.Contains(got[0].Message, "(cache, redis)") {
		t.Errorf("offering conflicts = %+v", got)
	}
}

// Two sources reporting one target with different offerings disagree just the same.
func TestOffering_ConflictWithinTarget(t *testing.T) {
	tgt := func(o string) *Collection {
		return &Collection{Targets: []RawTarget{{Scope: "a", Kind: "k8s", Name: "x", Service: "x", Offering: o, Summary: o}}}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow},
		NewMemorySource("one", SourceKindKubernetes, tgt("redis")), NewMemorySource("two", SourceKindKubernetes, tgt("cache")))
	if err != nil {
		t.Fatal(err)
	}
	if s := snap.Services[NewServiceKeyDomain("", "x")]; s.Offering != "" || s.Summary != "" || !hasLimitation(snap.Limitations, LimitationOfferingConflict) {
		t.Errorf("offering = %q, summary = %q, limitations %+v", s.Offering, s.Summary, snap.Limitations)
	}
}

func TestOffering_NonSlugReportedDropped(t *testing.T) {
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "a", Kind: "k8s", Name: "pg", Service: "pg", Offering: "Postgres SQL", Summary: "two\nlines"},
	}})
	if s := q.snap.Services[NewServiceKeyDomain("", "pg")]; s.Offering != "" || s.Summary != "" {
		t.Fatalf("offering = %q, summary = %q, want none", s.Offering, s.Summary)
	}
	if !validSummary(strings.Repeat("é", 200)) || validSummary(strings.Repeat("a", 201)) || validSummary("") || validSummary("\u001b]52;c;eA==\u0007 DB") || validSummary("a\u009bb") {
		t.Error("the summary rule is one line of 1 to 200 characters")
	}
}

func TestRestrictedView_OfferingFromHiddenTargetDoesNotLeak(t *testing.T) {
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "secret", Kind: SourceKindKubernetes, Name: "x-secret", Service: "x", Offering: "postgresql", Summary: "hidden"},
		{Scope: "open", Kind: SourceKindKubernetes, Name: "x-open", Service: "x"},
	}})
	r := q.Restrict(func(ns string) bool { return ns != "secret" })
	if s := r.snap.Services[NewServiceKeyDomain("", "x")]; s.Offering != "" || s.Summary != "" {
		t.Errorf("restricted offering = %q, summary = %q", s.Offering, s.Summary)
	}
	if got := entityKeys(t, r, EntityFilter{Kinds: []EntityKind{KindOffering}}); len(got) != 0 {
		t.Errorf("restricted offerings = %v", got)
	}
	if q.snap.Services[NewServiceKeyDomain("", "x")].Offering != "postgresql" {
		t.Error("restricting mutated the unrestricted snapshot")
	}
}

func TestEntities_OfferingKind(t *testing.T) {
	q := offeringFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOffering}, Sort: SortName})
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, e := range l.Entities {
		rows = append(rows, e.Key)
		if e.Kind != KindOffering || e.Offering != e.Key || (e.Key == "shared-config") != (e.Services == 2) {
			t.Errorf("offering row = %+v", e)
		}
	}
	if strings.Join(rows, ",") != "postgresql,redis,shared-config" || l.Aggregate.OfferingCount != 3 {
		t.Fatalf("offerings = %v (aggregate %d)", rows, l.Aggregate.OfferingCount)
	}
	for _, e := range mustEntities(t, q, EntityFilter{}) {
		if e.Kind == KindOffering {
			t.Errorf("a kind-less query returned offering %s", e.Key)
		}
	}
}

func TestEntities_OfferingFilterAndText(t *testing.T) {
	q := offeringFleet(t)
	got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Offering: "shared-config", Sort: SortName})
	if strings.Join(got, ",") != "platform-app-config,platform-worker-config" {
		t.Errorf("shared-config providers = %v", got)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindOffering}, Offering: "redis"}); len(got) != 1 {
		t.Errorf("an offering filter on offerings = %v, want redis", got)
	}
	var iq *InvalidQueryError
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindRevision}, Offering: "redis"}); !errors.As(err, &iq) || iq.Field != "offering" {
		t.Errorf("an offering filter on revisions = %v, want InvalidQueryError", err)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Text: "primary database"}); len(got) != 1 || got[0] != "postgresql" {
		t.Errorf("summary text search = %v", got)
	}
	if s := mustEntities(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Text: "postgresql"}); len(s) != 1 || s[0].Summary != "The primary database" {
		t.Errorf("service ref = %+v, want its summary", s)
	}
}

func mustEntities(t *testing.T, q *Query, f EntityFilter) []EntityRef {
	t.Helper()
	l, err := q.Entities(f)
	if err != nil {
		t.Fatal(err)
	}
	return l.Entities
}

func TestAggregate_OfferingFacet(t *testing.T) {
	l, err := offeringFleet(t).Entities(EntityFilter{Kinds: []EntityKind{KindService}})
	if err != nil {
		t.Fatal(err)
	}
	want := []TypeCount{{Type: "shared-config", Services: 2}, {Type: "postgresql", Services: 1}, {Type: "redis", Services: 1}}
	if !slices.Equal(l.Aggregate.Offerings, want) || l.Aggregate.OfferingTypes != 3 {
		t.Errorf("offering facet = %+v (%d types)", l.Aggregate.Offerings, l.Aggregate.OfferingTypes)
	}
}

func TestSearch_OfferingAndSummary(t *testing.T) {
	q := offeringFleet(t)
	r, err := q.Search(SearchFilter{Offering: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Services) != 1 || r.Services[0].Name != "redis" || r.Services[0].Offering != "redis" {
		t.Errorf("redis search = %+v", r.Services)
	}
	r, err = q.Search(SearchFilter{Text: "PRIMARY"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Services) != 1 || r.Services[0].Summary != "The primary database" {
		t.Errorf("summary search = %+v", r.Services)
	}
}

// ociRev is a revision of c published at oci://domain/name:version.
func ociRev(domain string, c *contract.Contract, files fstest.MapFS) RawRevision {
	if files == nil {
		files = fstest.MapFS{}
	}
	ref := "oci://" + domain + "/" + c.Service.Name + ":" + c.Service.Version
	return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: files}, Domain: domain, RequestedRef: ref, Digest: testDigest(domain, c.Service.Name, c.Service.Version)}
}

// testDigest is the content digest ociRev gives a revision, which a reference
// pins to resolve.
func testDigest(domain, name, version string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(domain+"/"+name+":"+version)))
}

func svc24(name, version, offering string) *contract.Contract {
	return &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: version, Offering: offering}}
}

func offeringDetailFleet(t *testing.T) *Query {
	t.Helper()
	app := svc24("platform-app-config", "1.0.1", "shared-config")
	app.Service.Summary, app.Service.Owner = "Shared application configuration", contract.Owner{Team: "platform"}
	app.Configurations = []contract.Configuration{{Name: "app", Schema: "config/schema.json", Required: true}}
	app.Policies = []contract.Policy{{Name: "p", Schema: "policy/schema.json"}}
	files := fstest.MapFS{"config/schema.json": {Data: []byte(`{"title":"App","properties":{"logLevel":{"type":"string"}}}`)}}
	worker := svc24("platform-worker-config", "1.2.0", "shared-config")
	worker.Workload = contract.WorkloadService
	web := svc24("web", "1.0.0", "")
	web.Configurations = []contract.Configuration{{Name: "app", Ref: "oci://ghcr.io/acme/platform-app-config@" + testDigest("ghcr.io/acme", "platform-app-config", "1.0.1"), Required: true}}
	jobs := svc24("jobs", "1.0.0", "")
	jobs.Dependencies = []contract.Dependency{
		{Name: "platform-worker-config", Ref: "oci://ghcr.io/acme/platform-worker-config", Required: true, Compatibility: "^1.0.0"},
		{Name: "web", Required: true}, {Name: "nowhere", Required: true},
	}
	return buildDomainSnap(t, "oci", &Collection{Targets: []RawTarget{
		{Scope: "prod", Kind: "k8s", Name: "app-config", Service: "platform-app-config", Domain: "ghcr.io/acme", Digest: testDigest("ghcr.io/acme", "platform-app-config", "1.0.1")},
	}, Revisions: []RawRevision{
		ociRev("ghcr.io/acme", svc24("platform-app-config", "1.0.0", "shared-config"), nil),
		ociRev("ghcr.io/acme", app, files), ociRev("ghcr.io/acme", worker, nil),
		ociRev("ghcr.io/acme", web, nil), ociRev("ghcr.io/acme", jobs, nil),
	}})
}

func offeringData(t *testing.T, q *Query, key string) *OfferingDetailData {
	t.Helper()
	d, err := q.EntityDetail(KindOffering, key)
	if err != nil {
		t.Fatal(err)
	}
	if d.Entity.Kind != KindOffering || d.Entity.Key != key {
		t.Fatalf("entity = %+v", d.Entity)
	}
	return d.Offering
}

func TestOfferingDetail_Providers(t *testing.T) {
	o := offeringData(t, offeringDetailFleet(t), "shared-config")
	if o.Providers.Total != 2 || len(o.Providers.Items) != 2 {
		t.Fatalf("providers = %+v", o.Providers)
	}
	wantAppProvider(t, o.Providers.Items[0])
	if a := o.Providers.Items[0].Consume; a == nil || !a.Configuration || a.Dependency || a.Policy {
		t.Errorf("app snippet = %+v", a)
	}
	if w := o.Providers.Items[1].Consume; w == nil || !w.Dependency || w.Compatibility != "^1.0.0" {
		t.Errorf("worker snippet = %+v", w)
	}
}

func wantAppProvider(t *testing.T, app OfferingProvider) {
	t.Helper()
	if app.Ownership == nil || app.Ownership.Owner != "platform" {
		t.Errorf("app ownership = %+v", app.Ownership)
	}
	if app.Service.Label != "platform-app-config" || app.Summary != "Shared application configuration" || app.Versions.Total != 2 || *app.Versions.Items[0].InUse != true || *app.Versions.Items[1].InUse || app.CurrentRevision == nil || app.CurrentRevision.Version != "1.0.1" {
		t.Errorf("app provider = %+v", app)
	}
	if c := app.Configurations.Items; len(c) != 1 || c[0].Content == nil || c[0].Content.Title != "App" || app.Policies.Total != 1 {
		t.Errorf("app configurations = %+v, policies = %+v", app.Configurations, app.Policies)
	}
}

func TestOfferingDetail_Consumers(t *testing.T) {
	o := offeringData(t, offeringDetailFleet(t), "shared-config")
	got := map[string]OfferingUse{}
	for _, u := range o.Consumers.Items {
		got[u.Consumer.Label] = u
	}
	if len(got) != 2 || got["web"].Via != RelationshipConfigRef || got["web"].Provider.Label != "platform-app-config" ||
		got["jobs"].Via != RelationshipDependency || got["jobs"].RangeStatus != RangeSatisfied || got["jobs"].Compatibility != "^1.0.0" {
		t.Errorf("consumers = %+v", o.Consumers.Items)
	}
}

func TestOfferingDetail_SameNameDifferentRegistries(t *testing.T) {
	cfg := func(domain, name string) *contract.Contract {
		c := svc24(name, "1.0.0", "")
		ref := "oci://" + domain + "/platform-app-config@" + testDigest(domain, "platform-app-config", "1.0.0")
		c.Configurations = []contract.Configuration{{Name: "app", Ref: ref, Required: true}}
		return c
	}
	p := func() *contract.Contract {
		c := svc24("platform-app-config", "1.0.0", "shared-config")
		c.Configurations = []contract.Configuration{{Name: "app", Schema: "s.json", Required: true}}
		return c
	}
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		ociRev("ghcr.io/acme", p(), nil), ociRev("partners.acme.com/pacto", p(), nil),
		ociRev("ghcr.io/acme", cfg("ghcr.io/acme", "web"), nil), ociRev("partners.acme.com/pacto", cfg("partners.acme.com/pacto", "partner-web"), nil),
	}})
	o := offeringData(t, q, "shared-config")
	if len(o.Providers.Items) != 2 {
		t.Fatalf("providers = %+v", o.Providers.Items)
	}
	a, b := o.Providers.Items[0], o.Providers.Items[1]
	if a.Service.Key == b.Service.Key || a.Consume.Ref != "oci://ghcr.io/acme/platform-app-config" || b.Consume.Ref != "oci://partners.acme.com/pacto/platform-app-config" {
		t.Errorf("providers %s (%s) and %s (%s)", a.Service.Key, a.Consume.Ref, b.Service.Key, b.Consume.Ref)
	}
	for _, u := range o.Consumers.Items {
		if u.Consumer.Domain != u.Provider.Domain {
			t.Errorf("%s consumes the provider in %s", u.Consumer.Key, u.Provider.Domain)
		}
	}
	if o.Consumers.Total != 2 {
		t.Errorf("consumers = %+v", o.Consumers.Items)
	}
}

func snippetFor(t *testing.T, raw RawRevision) *ConsumeSnippet {
	t.Helper()
	c := raw.Bundle.Contract
	o := offeringData(t, buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{raw}}), c.Service.Offering)
	return o.Providers.Items[0].Consume
}

func TestConsumeSnippet_DependencyRange(t *testing.T) {
	c := svc24("postgresql", "16.0.1", "postgresql")
	c.Workload = contract.WorkloadService
	sn := snippetFor(t, ociRev("ghcr.io/trianalab/pacto", c, nil))
	want := "dependencies:\n  - name: postgresql\n    ref: oci://ghcr.io/trianalab/pacto/postgresql\n    required: true\n    compatibility: \"^16.0.0\"\n"
	if sn == nil || sn.YAML != want || sn.Compatibility != "^16.0.0" {
		t.Errorf("snippet = %+v, want %q", sn, want)
	}
}

// A dependency with a ref needs a compatibility, so a version with no range
// still gets the line, empty, to fill in.
func TestConsumeSnippet_NonSemverAsksForRange(t *testing.T) {
	for _, v := range []string{"latest", "v-next"} {
		c := svc24("redis", v, "redis")
		c.Interfaces = []contract.Interface{{Name: "resp", Type: "tcp"}}
		sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil))
		if sn == nil || sn.Compatibility != "" || !strings.Contains(sn.YAML, "    compatibility: \"\" # set a semver range") || !sn.Dependency {
			t.Errorf("%s: snippet = %+v", v, sn)
		}
	}
}

// Every rendered snippet for a semver provider makes a valid consumer contract.
func TestConsumeSnippet_RangesValidate(t *testing.T) {
	for v, want := range map[string]string{"16.0.1": "^16.0.0", "0.3.1": "^0.3.0", "0.0.4": "^0.0.4", "2024.01.01-beta": "^2024.01.01-beta", "2.0.0-beta.1": "^2.0.0-beta.1", "0.3.1-rc.1": "^0.3.1-rc.1"} {
		c := svc24("redis", v, "redis")
		c.Workload = contract.WorkloadService
		sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil))
		if sn == nil || sn.Compatibility != want {
			t.Errorf("%s: snippet = %+v, want range %s", v, sn, want)
			continue
		}
		consumer := mustParse(t, "pactoVersion: \"2.4\"\nservice:\n  name: app\n  version: 1.0.0\n"+sn.YAML)
		if r := validation.ValidateCrossField(consumer, fstest.MapFS{}); !r.IsValid() {
			t.Errorf("%s: snippet %q is invalid: %+v", v, sn.YAML, r.Errors)
		}
	}
}

// Providers of one offering get distinct entry names.
func TestConsumeSnippet_ConfigurationNamedAfterProvider(t *testing.T) {
	c := svc24("platform-app-config", "1.0.0", "shared-config")
	c.Configurations = []contract.Configuration{{Name: "app", Schema: "s.json", Required: true}}
	sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil))
	want := "# values and configuration need pactoVersion \"2.4\"; run pacto lock to pin the reference\n" +
		"configurations:\n  - name: platform-app-config\n    ref: oci://ghcr.io/acme/platform-app-config:1.0.0\n    required: true\n"
	if sn == nil || sn.YAML != want {
		t.Errorf("snippet = %+v, want %q", sn, want)
	}
}

func TestConsumeSnippet_PolicyOnly(t *testing.T) {
	c := svc24("platform-http-policy", "1.0.0", "shared-policy")
	c.Policies = []contract.Policy{{Name: "http", Schema: "policy/schema.json"}}
	sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil))
	want := "policies:\n  - name: platform-http-policy\n    ref: oci://ghcr.io/acme/platform-http-policy\n"
	if sn == nil || sn.YAML != want || !sn.Policy {
		t.Errorf("snippet = %+v, want %q", sn, want)
	}
}

// A policy provider that declares no policy, since a declared one would apply to
// itself, still serves the fixed-path policy/schema.json to consumers.
func TestConsumeSnippet_FixedPathPolicy(t *testing.T) {
	c := svc24("platform-http-policy", "1.0.1", "shared-policy")
	files := fstest.MapFS{"policy/schema.json": {Data: []byte(`{"type":"object"}`)}}
	sn := snippetFor(t, ociRev("ghcr.io/acme", c, files))
	want := "policies:\n  - name: platform-http-policy\n    ref: oci://ghcr.io/acme/platform-http-policy\n"
	if sn == nil || sn.YAML != want || !sn.Policy {
		t.Errorf("snippet = %+v, want %q", sn, want)
	}
}

func TestConsumeSnippet_NoOCIRef(t *testing.T) {
	c := svc24("redis", "7.0.0", "redis")
	c.Workload = contract.WorkloadService
	raw := ociRev("ghcr.io/acme", c, nil)
	raw.RequestedRef = ""
	if sn := snippetFor(t, raw); sn != nil {
		t.Errorf("no reference: snippet = %+v", sn)
	}
	raw.RequestedRef = "file:///src/redis"
	if sn := snippetFor(t, raw); sn != nil {
		t.Errorf("a local file: snippet = %+v", sn)
	}
	// Nothing to consume.
	if sn := snippetFor(t, ociRev("ghcr.io/acme", svc24("empty", "1.0.0", "empty"), nil)); sn != nil {
		t.Errorf("an empty contract: snippet = %+v", sn)
	}
}

// The snippet comes from the newest revision that is not invalid.
func TestConsumeSnippet_InvalidNewestRevisionIgnored(t *testing.T) {
	bad := mustParse(t, invalidYAML)
	bad.PactoVersion, bad.Service.Version, bad.Service.Offering = "2.4", "2.0.0", "cache"
	old := svc24("bad-svc", "1.4.0", "cache")
	old.Workload = contract.WorkloadService
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		ociRev("", old, nil), {Bundle: &contract.Bundle{Contract: bad, RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}, RequestedRef: "oci://bad-svc:2.0.0"},
	}})
	sn := offeringData(t, q, "cache").Providers.Items[0].Consume
	if sn == nil || sn.Compatibility != "^1.0.0" {
		t.Errorf("snippet = %+v, want the range of 1.4.0", sn)
	}
}

func TestOfferingDetail_ProvidersBounded(t *testing.T) {
	var revs []RawRevision
	for i := range MaxOfferingProviders + 5 {
		revs = append(revs, offerRev("p"+strings.Repeat("x", i), "1.0.0", "shared", ""))
	}
	o := offeringData(t, buildDomainSnap(t, "local", &Collection{Revisions: revs}), "shared")
	if o.Providers.Count != MaxOfferingProviders || o.Providers.Total != MaxOfferingProviders+5 || !o.Providers.Truncated || len(o.Providers.Items) != MaxOfferingProviders {
		t.Errorf("providers = %d of %d (truncated %v)", o.Providers.Count, o.Providers.Total, o.Providers.Truncated)
	}
}

func TestOfferingDetail_NotFound(t *testing.T) {
	var nf *NotFoundError
	for _, key := range []string{"nope", ""} {
		if _, err := offeringFleet(t).EntityDetail(KindOffering, key); !errors.As(err, &nf) || nf.Kind != "offering" {
			t.Errorf("%q: err = %v, want NotFoundError", key, err)
		}
	}
}

func TestEntityDetail_OfferingIsCloned(t *testing.T) {
	q := offeringDetailFleet(t)
	first := offeringData(t, q, "shared-config")
	first.Providers.Items[0].Consume.YAML = "mutated"
	first.Consumers.Items[0].Consumer.Label = "mutated"
	second := offeringData(t, q, "shared-config")
	if second.Providers.Items[0].Consume.YAML == "mutated" || second.Consumers.Items[0].Consumer.Label == "mutated" {
		t.Error("mutating an answer changed the next one")
	}
}

// Revisions of one consumer collapse into one row; when their range statuses
// disagree it is unknown.
func TestOfferingUses_RevisionsCollapse(t *testing.T) {
	worker := svc24("platform-worker-config", "1.2.0", "shared-config")
	worker.Workload = contract.WorkloadService
	jobs := func(version, compat string) RawRevision {
		c := svc24("jobs", version, "")
		c.Dependencies = []contract.Dependency{{Name: "platform-worker-config", Required: true, Compatibility: compat}}
		return ociRev("ghcr.io/acme", c, nil)
	}
	for _, tc := range []struct {
		revs []RawRevision
		want string
	}{
		{[]RawRevision{jobs("1.0.0", ""), jobs("1.1.0", "^1.0.0")}, RangeSatisfied},
		{[]RawRevision{jobs("1.0.0", "^1.0.0"), jobs("1.1.0", ""), jobs("1.2.0", "^2.0.0")}, RangeUnknown},
	} {
		q := buildDomainSnap(t, "oci", &Collection{Revisions: append(tc.revs, ociRev("ghcr.io/acme", worker, nil))})
		c := offeringData(t, q, "shared-config").Consumers
		if c.Total != 1 || c.Items[0].RangeStatus != tc.want {
			t.Errorf("consumers = %+v, want one row %s", c.Items, tc.want)
		}
	}
}

// A provider only an operator reports has no revision to describe or consume.
func TestOfferingDetail_TargetOnlyProvider(t *testing.T) {
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "a", Kind: "k8s", Name: "pg", Service: "pg", Offering: "postgresql"},
	}})
	p := offeringData(t, q, "postgresql").Providers.Items[0]
	if p.Consume != nil || p.CurrentRevision != nil || p.Interfaces.Total != 0 || p.Versions.Total != 0 || p.OfferingProvenance != ProvenanceObserved {
		t.Errorf("provider = %+v", p)
	}
	if d, _ := q.EntityDetail(KindOffering, "postgresql"); d.Entity.OfferingProvenance != ProvenanceObserved {
		t.Errorf("offering ref = %+v", d.Entity)
	}
}

// Membership is declared when a contract revision declares the offering and
// observed when only targets report it; an offering any provider declares is
// declared.
func TestOfferingProvenance(t *testing.T) {
	q := buildDomainSnap(t, "local", &Collection{
		Revisions: []RawRevision{offerRev("postgresql", "16.0.1", "postgresql", ""), offerRev("bare", "1.0.0", "", "")},
		Targets: []RawTarget{
			{Scope: "a", Kind: "k8s", Name: "pg2", Service: "pg2", Offering: "postgresql"},
			{Scope: "a", Kind: "k8s", Name: "kv", Service: "kv", Offering: "redis"},
		},
	})
	for name, want := range map[string]string{"postgresql": ProvenanceDeclared, "pg2": ProvenanceObserved, "bare": ""} {
		key := NewServiceKeyDomain("ghcr.io/acme", name)
		if name == "pg2" {
			key = NewServiceKey(name)
		}
		if got := q.snap.Services[key].OfferingProvenance; got != want {
			t.Errorf("%s provenance = %q, want %q", name, got, want)
		}
	}
	refs := map[string]EntityRef{}
	for _, r := range mustEntities(t, q, EntityFilter{Kinds: []EntityKind{KindOffering, KindService}}) {
		refs[r.Key] = r
	}
	if refs["postgresql"].OfferingProvenance != ProvenanceDeclared || refs["redis"].OfferingProvenance != ProvenanceObserved ||
		refs[string(NewServiceKey("pg2"))].OfferingProvenance != ProvenanceObserved {
		t.Errorf("refs = %+v", refs)
	}
	if d, _ := q.EntityDetail(KindOffering, "postgresql"); d.Entity.OfferingProvenance != ProvenanceDeclared || d.Offering.Providers.Items[0].OfferingProvenance == "" {
		t.Errorf("offering detail = %+v", d.Entity)
	}
}

func serviceData(t *testing.T, q *Query, key ServiceKey) *ServiceDetailData {
	t.Helper()
	d, err := q.EntityDetail(KindService, string(key))
	if err != nil {
		t.Fatal(err)
	}
	return d.Service
}

func TestServiceDetail_ConsumedOfferings(t *testing.T) {
	pg := svc24("postgresql", "16.0.1", "postgresql")
	pg.Workload = contract.WorkloadService
	cfg := svc24("platform-app-config", "1.0.0", "shared-config")
	cfg.Configurations = []contract.Configuration{{Name: "app", Schema: "s.json", Required: true}}
	pay := svc24("payments", "1.0.0", "")
	pay.Dependencies = []contract.Dependency{
		{Name: "postgresql", Required: true, Compatibility: "^16.0.0"},
		{Name: "orders", Required: true},
	}
	pay.Configurations = []contract.Configuration{{Name: "app", Ref: "oci://ghcr.io/acme/platform-app-config@" + testDigest("ghcr.io/acme", "platform-app-config", "1.0.0"), Required: true}}
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		ociRev("ghcr.io/acme", pg, nil), ociRev("ghcr.io/acme", cfg, nil), ociRev("ghcr.io/acme", pay, nil),
		ociRev("ghcr.io/acme", svc24("orders", "1.0.0", ""), nil),
	}})
	d := serviceData(t, q, NewServiceKeyDomain("ghcr.io/acme", "payments"))
	c := d.ConsumedOfferings
	if c.Total != 2 {
		t.Fatalf("consumed offerings = %+v", c.Items)
	}
	got := map[string]OfferingUse{}
	for _, u := range c.Items {
		got[u.Offering] = u
	}
	if got["postgresql"].Via != RelationshipDependency || got["postgresql"].RangeStatus != RangeSatisfied || got["shared-config"].Via != RelationshipConfigRef {
		t.Errorf("consumed offerings = %+v", c.Items)
	}
	if len(d.InferredOfferings) != 0 {
		t.Errorf("inferred = %+v, want none: every dependency resolved", d.InferredOfferings)
	}
	if o := serviceData(t, q, NewServiceKeyDomain("ghcr.io/acme", "orders")); o.ConsumedOfferings.Total != 0 || o.InferredOfferings == nil {
		t.Errorf("orders = %+v / %+v, want empty lists", o.ConsumedOfferings, o.InferredOfferings)
	}
}

func TestInferredOfferings_ExactNameOnly(t *testing.T) {
	redis := svc24("redis", "7.0.0", "redis")
	redis.Workload = contract.WorkloadService
	pg := svc24("postgresql", "16.0.1", "relational-db")
	pg.Workload = contract.WorkloadService
	app := svc24("app", "1.0.0", "")
	app.Dependencies = []contract.Dependency{
		{Name: "redis", External: true, Version: "7"},
		{Name: "db", Ref: "oci://other.io/x/postgresql", Required: true},
		{Name: "redis-cache", External: true},
		{Name: "Redis2", External: true},
	}
	local := svc24("local", "1.0.0", "")
	local.Dependencies = []contract.Dependency{{Name: "redis", Required: true}}
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		ociRev("ghcr.io/acme", redis, nil), ociRev("partners.acme.com", pg, nil),
		ociRev("ghcr.io/other", app, nil), ociRev("ghcr.io/acme", local, nil),
	}})
	got := serviceData(t, q, NewServiceKeyDomain("ghcr.io/other", "app")).InferredOfferings
	if len(got) != 2 {
		t.Fatalf("inferred = %+v, want two", got)
	}
	byRef := map[string]InferredOffering{}
	for _, i := range got {
		byRef[i.Ref] = i
	}
	if r := byRef["redis"]; r.Reason != "offering-name" || r.Offering.Key != "redis" || r.Provider != nil {
		t.Errorf("redis = %+v", r)
	}
	if db := byRef["db"]; db.Reason != "provider-name" || db.Offering.Key != "relational-db" || db.Provider == nil || db.Provider.Label != "postgresql" {
		t.Errorf("db = %+v", db)
	}
	l := serviceData(t, q, NewServiceKeyDomain("ghcr.io/acme", "local"))
	if len(l.InferredOfferings) != 0 || l.ConsumedOfferings.Total != 1 || l.ConsumedOfferings.Items[0].Offering != "redis" {
		t.Errorf("local consumes %+v and is suggested %+v", l.ConsumedOfferings.Items, l.InferredOfferings)
	}
}

func TestRestrictedView_ConflictFromHiddenTargetDoesNotLeak(t *testing.T) {
	q := buildDomainSnap(t, SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "hiddenns", Kind: SourceKindKubernetes, Name: "x-1", Service: "x", Offering: "classified", BusinessDomain: "secretdomain"},
		{Scope: "open", Kind: SourceKindKubernetes, Name: "x-2", Service: "x", Offering: "postgresql", BusinessDomain: "payments"},
	}})
	if !slices.ContainsFunc(q.snap.Limitations, func(l Limitation) bool { return l.Code == LimitationOfferingConflict }) {
		t.Fatal("the unrestricted snapshot has no OFFERING_CONFLICT")
	}
	r := q.Restrict(func(ns string) bool { return ns != "hiddenns" })
	if s := r.snap.Services[NewServiceKeyDomain("", "x")]; s.Offering != "postgresql" {
		t.Errorf("restricted offering = %q, want postgresql", s.Offering)
	}
	for _, l := range r.snap.Limitations {
		if l.Code == LimitationOfferingConflict || l.Code == LimitationDomainConflict ||
			strings.Contains(l.Message, "classified") || strings.Contains(l.Message, "secretdomain") {
			t.Errorf("restricted limitation leaks or contradicts: %+v", l)
		}
	}
}

// Consumer revisions that declare different ranges leave no range to show.
func TestOfferingDetail_ConsumerRevisionsDisagreeOnRange(t *testing.T) {
	pg := svc24("postgresql", "16.0.1", "postgresql")
	pg.Workload = contract.WorkloadService
	jobs := func(version, rng string) *contract.Contract {
		c := svc24("jobs", version, "")
		c.Dependencies = []contract.Dependency{{Name: "postgresql", Ref: "oci://ghcr.io/acme/postgresql", Required: true, Compatibility: rng}}
		return c
	}
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		ociRev("ghcr.io/acme", pg, nil), ociRev("ghcr.io/acme", jobs("1.0.0", "^15.0.0"), nil), ociRev("ghcr.io/acme", jobs("2.0.0", "^16.0.0"), nil),
	}})
	o := offeringData(t, q, "postgresql")
	if len(o.Consumers.Items) != 1 || o.Consumers.Items[0].Compatibility != "" || o.Consumers.Items[0].RangeStatus != RangeUnknown {
		t.Errorf("consumers = %+v", o.Consumers.Items)
	}
}

// pgSchema is the provisioning schema of the demo-shaped postgresql provider.
const pgSchema = `{
  "type": "object",
  "required": ["size", "storageGiB", "highAvailability", "extensions", "database", "labels"],
  "properties": {
    "size": {"type": "string", "enum": ["small", "medium", "large"], "default": "medium"},
    "storageGiB": {"type": "integer", "minimum": 10},
    "highAvailability": {"type": "boolean"},
    "extensions": {"type": "array", "items": {"type": "string"}},
    "database": {"type": "string"},
    "labels": {"type": "object"},
    "backupRetentionDays": {"type": "integer"}
  }
}`

// pgOffering is a demo-shaped postgresql 16.1.0: a workload with an
// interface and one required provisioning configuration.
func pgOffering() *contract.Contract {
	c := svc24("postgresql", "16.1.0", "postgresql")
	c.PactoVersion, c.Workload = "2.4", contract.WorkloadService
	c.Interfaces = []contract.Interface{{Name: "sql", Type: "tcp"}}
	c.Configurations = []contract.Configuration{{Name: "provisioning", Schema: "configuration/schema.json", Required: true}}
	return c
}

// A provider with a workload and a required configuration gets both
// consumption forms; the configuration entry is pinned to the current version
// and carries the schema's required properties as a values skeleton.
func TestConsumeSnippet_BothFormsWithValues(t *testing.T) {
	files := fstest.MapFS{"configuration/schema.json": {Data: []byte(pgSchema)}}
	sn := snippetFor(t, ociRev("ghcr.io/trianalab/pacto", pgOffering(), files))
	want := `dependencies:
  - name: postgresql
    ref: oci://ghcr.io/trianalab/pacto/postgresql
    required: true
    compatibility: "^16.0.0"
# values and configuration need pactoVersion "2.4"; run pacto lock to pin the reference
configurations:
  - name: postgresql
    ref: oci://ghcr.io/trianalab/pacto/postgresql:16.1.0
    required: true
    values:
      size: "medium" # required string, one of small|medium|large, default medium
      storageGiB: 10 # required integer
      highAvailability: false # required boolean
      extensions: [] # required array
      database: "" # required string
      labels: {} # required object
`
	if sn == nil || sn.YAML != want || !sn.Dependency || !sn.Configuration {
		t.Fatalf("snippet = %+v\n got:\n%s\nwant:\n%s", sn, sn.YAML, want)
	}
	consumer := mustParse(t, "pactoVersion: \"2.4\"\nservice:\n  name: app\n  version: 1.0.0\n"+sn.YAML)
	if cfg := consumer.Configurations[0]; len(cfg.Values) != 6 || cfg.Values["size"] != "medium" {
		t.Errorf("parsed values = %+v", cfg.Values)
	}
	if r := validation.ValidateCrossField(consumer, fstest.MapFS{}); !r.IsValid() {
		t.Errorf("snippet is invalid: %+v", r.Errors)
	}
}

// A provider with several configurations gets a selector on each entry; several
// required configurations get one entry each, named apart.
func TestConsumeSnippet_SelectorOnlyForSeveralConfigurations(t *testing.T) {
	c := svc24("platform-app-config", "1.0.0", "shared-config")
	c.Configurations = []contract.Configuration{{Name: "app", Schema: "a.json", Required: true}, {Name: "extra", Schema: "a.json"}}
	sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil))
	if sn == nil || !strings.Contains(sn.YAML, "  - name: platform-app-config\n    ref: oci://ghcr.io/acme/platform-app-config:1.0.0\n    configuration: app\n    required: true\n") || strings.Contains(sn.YAML, "extra") {
		t.Errorf("one required of two: %+v", sn)
	}
	c.Configurations[1].Required = true
	sn = snippetFor(t, ociRev("ghcr.io/acme", c, nil))
	if sn == nil || !strings.Contains(sn.YAML, "  - name: platform-app-config-app\n") || !strings.Contains(sn.YAML, "  - name: platform-app-config-extra\n    ref: oci://ghcr.io/acme/platform-app-config:1.0.0\n    configuration: extra\n") {
		t.Errorf("two required: %+v", sn)
	}
}

// A provider configuration that is itself a reference takes its skeleton from
// the schema the reference resolves to.
func TestConsumeSnippet_SkeletonThroughReference(t *testing.T) {
	base := svc24("base-config", "1.0.0", "")
	base.Configurations = []contract.Configuration{{Name: "base", Schema: "s.json", Required: true}}
	files := fstest.MapFS{"s.json": {Data: []byte(`{"required":["region"],"properties":{"region":{"type":"string","enum":["eu","us"]}}}`)}}
	c := svc24("platform-app-config", "1.0.0", "shared-config")
	c.Configurations = []contract.Configuration{{Name: "app", Ref: "oci://ghcr.io/acme/base-config@" + testDigest("ghcr.io/acme", "base-config", "1.0.0"), Required: true}}
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{ociRev("ghcr.io/acme", base, files), ociRev("ghcr.io/acme", c, nil)}})
	sn := offeringData(t, q, "shared-config").Providers.Items[0].Consume
	if sn == nil || !strings.HasSuffix(sn.YAML, "    values:\n      region: \"eu\" # required string, one of eu|us\n") {
		t.Errorf("snippet = %+v", sn)
	}
	// An unresolved reference gives no skeleton.
	c.Configurations[0].Ref = "oci://ghcr.io/acme/base-config:1.0.0"
	if sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil)); sn == nil || strings.Contains(sn.YAML, "values:") {
		t.Errorf("unresolved: %+v", sn)
	}
}

func TestValuesSkeleton(t *testing.T) {
	for _, tc := range []struct{ name, schema, want string }{
		{"unparsable", `{`, ""},
		{"nothing required", `{"properties":{"a":{"type":"string"}}}`, ""},
		{"type list and no property", `{"required":["a","b"],"properties":{"a":{"type":["number","null"]}}}`, "    values:\n      a: 0 # required number\n      b: \"\" # required\n"},
		{"quoted key and object default", `{"required":["true"],"properties":{"true":{"type":"object","default":{"x":1}}}}`, "    values:\n      \"true\": {\"x\":1} # required object, default {\"x\":1}\n"},
		{"enum without default", `{"required":["count"],"properties":{"count":{"type":"integer","enum":[3,5]}}}`, "    values:\n      count: 3 # required integer, one of 3|5\n"},
	} {
		if _, _, got := valuesSkeleton([]byte(tc.schema), "s.json"); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	if _, _, got := valuesSkeleton([]byte("required: [a]\nproperties: {a: {type: boolean}}\n"), "s.yaml"); got != "    values:\n      a: false # required boolean\n" {
		t.Errorf("yaml schema: %q", got)
	}
}

// A required property no candidate satisfies is left out of the values, listed
// as missing and written as a comment; a property holding a $ref cannot be
// checked standalone, so it is missing too.
func TestValuesSkeleton_OmitsInvalidCandidates(t *testing.T) {
	schema := `{"required":["name","shared","storageGi","size"],"properties":{
	  "name":{"type":"string","minLength":3,"pattern":"^[a-z]+$"},
	  "shared":{"$ref":"#/$defs/shared"},
	  "storageGi":{"type":"integer","minimum":10},
	  "size":{"type":"string","enum":["small","large"]}},
	  "$defs":{"shared":{"type":"string"}}}`
	values, missing, block := valuesSkeleton([]byte(schema), "s.json")
	want := "    values:\n" +
		"      # name: required string, set a value\n" +
		"      # shared: required, set a value\n" +
		"      storageGi: 10 # required integer\n" +
		"      size: \"small\" # required string, one of small|large\n"
	if block != want || !slices.Equal(missing, []string{"name", "shared"}) || len(values) != 2 || values["storageGi"] != float64(10) || values["size"] != "small" {
		t.Errorf("values %v, missing %v, block:\n%s", values, missing, block)
	}
	// Nothing kept: the block stays an object, never null.
	values, missing, block = valuesSkeleton([]byte(`{"required":["name"],"properties":{"name":{"type":"string","minLength":3}}}`), "s.json")
	if block != "    values: {}\n      # name: required string, set a value\n" || len(values) != 0 || values == nil || !slices.Equal(missing, []string{"name"}) {
		t.Errorf("values %v, missing %v, block %q", values, missing, block)
	}
	// An invalid default falls through to the next candidate.
	if values, _, _ := valuesSkeleton([]byte(`{"required":["n"],"properties":{"n":{"type":"number","default":1,"minimum":5}}}`), "s.json"); values["n"] != float64(5) {
		t.Errorf("values %v, want the minimum", values)
	}
}

// The values a skeleton keeps validate against the provider's schema once the
// consumer sets the missing ones.
func TestValuesSkeleton_ValidatesAgainstSchema(t *testing.T) {
	values, missing, _ := valuesSkeleton([]byte(pgSchema), "configuration/schema.json")
	if len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(pgSchema), &doc); err != nil {
		t.Fatal(err)
	}
	if !validation.ValueAccepted(doc, values) {
		t.Errorf("values %v do not validate against the provider schema", values)
	}
}

// The demo postgresql gives storageGi its minimum, and the values it keeps
// validate against its provisioning schema.
func TestConsumeSnippet_DemoPostgresql(t *testing.T) {
	dir := "../../examples/demo/bundles/postgresql/v16.1.0"
	raw, err := os.ReadFile(dir + "/pacto.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(dir + "/configuration/provisioning.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	rev := ociRev("ghcr.io/trianalab/pacto", mustParse(t, string(raw)), nil)
	rev.Bundle.FS = os.DirFS(dir)
	sn := snippetFor(t, rev)
	if sn == nil || len(sn.Entries.Configurations) != 1 {
		t.Fatalf("snippet = %+v", sn)
	}
	values := sn.Entries.Configurations[0].Values
	var doc map[string]any
	if err := json.Unmarshal(schema, &doc); err != nil {
		t.Fatal(err)
	}
	if values["storageGi"] != float64(10) || !validation.ValueAccepted(doc, values) {
		t.Errorf("values %v, yaml:\n%s", values, sn.YAML)
	}
}

// Entries hold exactly what YAML says: decoding the YAML into a contract gives
// the same dependency, configurations and policy.
func TestConsumeSnippet_EntriesMatchYAML(t *testing.T) {
	asJSON := func(v any) string {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	check := func(name string, sn *ConsumeSnippet) {
		t.Helper()
		if sn == nil {
			t.Fatalf("%s: no snippet", name)
		}
		consumer := mustParse(t, "pactoVersion: \"2.6\"\nservice:\n  name: app\n  version: 1.0.0\n"+sn.YAML)
		var cfgs []contract.Configuration
		for _, e := range sn.Entries.Configurations {
			cfgs = append(cfgs, e.Configuration)
		}
		var deps []contract.Dependency
		if sn.Entries.Dependency != nil {
			deps = append(deps, *sn.Entries.Dependency)
		}
		var pols []contract.Policy
		if sn.Entries.Policy != nil {
			pols = append(pols, *sn.Entries.Policy)
		}
		if asJSON(consumer.Dependencies) != asJSON(deps) || asJSON(consumer.Configurations) != asJSON(cfgs) || asJSON(consumer.Policies) != asJSON(pols) {
			t.Errorf("%s: YAML decodes to %s %s %s, entries are %s", name, asJSON(consumer.Dependencies), asJSON(consumer.Configurations), asJSON(consumer.Policies), asJSON(sn.Entries))
		}
	}
	check("both forms", snippetFor(t, ociRev("ghcr.io/trianalab/pacto", pgOffering(), fstest.MapFS{"configuration/schema.json": {Data: []byte(pgSchema)}})))
	two := svc25("platform-app-config", "1.0.0", "shared-config")
	two.Configurations = []contract.Configuration{{Name: "app", Schema: "a.json", Required: true}, {Name: "extra", Schema: "a.json", Required: true}}
	check("selectors", snippetFor(t, ociRev("ghcr.io/acme", two, nil)))
	pol := svc25("platform-http-policy", "1.0.0", "shared-policy")
	pol.Policies = []contract.Policy{{Name: "http", Schema: "policy/schema.json"}}
	check("policy", snippetFor(t, ociRev("ghcr.io/acme", pol, nil)))
	semverless := svc25("redis", "latest", "redis")
	semverless.Workload = contract.WorkloadService
	check("no range", snippetFor(t, ociRev("ghcr.io/acme", semverless, nil)))
}

// A deprecated current revision says so, with its successor when it names one.
func TestConsumeSnippet_DeprecatedComment(t *testing.T) {
	c := pgOffering()
	c.Configurations = nil
	c.Service.Lifecycle = &contract.InterfaceLifecycle{Deprecated: "2026-10-01"}
	if sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil)); sn == nil || !strings.HasPrefix(sn.YAML, "# postgresql 16.1.0 is deprecated since 2026-10-01\ndependencies:\n") {
		t.Errorf("snippet = %+v", sn)
	}
	c.Service.Lifecycle.Successor = "oci://ghcr.io/acme/postgresql-17"
	if sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil)); sn == nil || !strings.HasPrefix(sn.YAML, "# postgresql 16.1.0 is deprecated since 2026-10-01; successor oci://ghcr.io/acme/postgresql-17\n") {
		t.Errorf("snippet = %+v", sn)
	}
}

// The offering page's current revision is the revision the snippet pins, even
// when a target runs an older one that declares no offering; service detail
// keeps the newest in use.
func TestConsumeSnippetMatchesCurrent(t *testing.T) {
	old := svc24("postgresql", "16.0.0", "")
	old.Workload = contract.WorkloadService
	q := buildDomainSnap(t, "oci", &Collection{
		Revisions: []RawRevision{ociRev("ghcr.io/acme", old, nil), ociRev("ghcr.io/acme", pgOffering(), fstest.MapFS{"configuration/schema.json": {Data: []byte(pgSchema)}})},
		Targets:   []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pg", Service: "postgresql", Domain: "ghcr.io/acme", Digest: testDigest("ghcr.io/acme", "postgresql", "16.0.0")}},
	})
	p := offeringData(t, q, "postgresql").Providers.Items[0]
	if p.CurrentRevision == nil || p.CurrentRevision.Version != "16.1.0" || p.Consume == nil || !strings.Contains(p.Consume.YAML, "postgresql:16.1.0") || p.Configurations.Total != 1 || p.Interfaces.Total != 1 {
		t.Errorf("provider = %+v", p)
	}
	d, err := q.EntityDetail(KindService, string(NewServiceKeyDomain("ghcr.io/acme", "postgresql")))
	if err != nil || d.Service.CurrentRevision == nil || d.Service.CurrentRevision.Version != "16.0.0" {
		t.Errorf("service detail current = %+v, %v", d.Service.CurrentRevision, err)
	}
}

// Service detail picks the newest revision in use, else the newest known.
func TestServiceDetailCurrentRevisionUnchanged(t *testing.T) {
	a, b := offerRev("kv", "1.0.0", "", ""), offerRev("kv", "1.1.0", "", "")
	for _, tc := range []struct {
		targets []RawTarget
		want    string
	}{
		{[]RawTarget{{Scope: "a", Kind: "k8s", Name: "kv", Service: "kv", Domain: "ghcr.io/acme", Digest: "sha256:kv1.0.0"}}, "1.0.0"},
		{nil, "1.1.0"},
	} {
		q := buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{a, b}, Targets: tc.targets})
		d, err := q.EntityDetail(KindService, string(NewServiceKeyDomain("ghcr.io/acme", "kv")))
		if err != nil || d.Service.CurrentRevision == nil || d.Service.CurrentRevision.Version != tc.want {
			t.Errorf("current = %+v, want %s (%v)", d.Service.CurrentRevision, tc.want, err)
		}
	}
}
