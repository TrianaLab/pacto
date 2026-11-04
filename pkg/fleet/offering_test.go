package fleet

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// offerRev is a 2.5 revision of name in ghcr.io/acme that provides offering.
func offerRev(name, version, offering, summary string) RawRevision {
	k := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: name, Version: version, Offering: offering, Summary: summary}}
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
	bad.PactoVersion, bad.Service.Version, bad.Service.Offering = "2.5", "2.0.0", "cache"
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
	if !validSummary(strings.Repeat("é", 200)) || validSummary(strings.Repeat("a", 201)) || validSummary("") {
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

func svc25(name, version, offering string) *contract.Contract {
	return &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: name, Version: version, Offering: offering}}
}

func offeringDetailFleet(t *testing.T) *Query {
	t.Helper()
	app := svc25("platform-app-config", "1.0.1", "shared-config")
	app.Service.Summary, app.Service.Owner = "Shared application configuration", contract.Owner{Team: "platform"}
	app.Configurations = []contract.Configuration{{Name: "app", Schema: "config/schema.json", Required: true}}
	app.Policies = []contract.Policy{{Name: "p", Schema: "policy/schema.json"}}
	files := fstest.MapFS{"config/schema.json": {Data: []byte(`{"title":"App","properties":{"logLevel":{"type":"string"}}}`)}}
	worker := svc25("platform-worker-config", "1.2.0", "shared-config")
	worker.Workload = contract.WorkloadService
	web := svc25("web", "1.0.0", "")
	web.Configurations = []contract.Configuration{{Name: "app", Ref: "oci://ghcr.io/acme/platform-app-config@" + testDigest("ghcr.io/acme", "platform-app-config", "1.0.1"), Required: true}}
	jobs := svc25("jobs", "1.0.0", "")
	jobs.Dependencies = []contract.Dependency{
		{Name: "platform-worker-config", Ref: "oci://ghcr.io/acme/platform-worker-config", Required: true, Compatibility: "^1.0.0"},
		{Name: "web", Required: true}, {Name: "nowhere", Required: true},
	}
	return buildDomainSnap(t, "oci", &Collection{Targets: []RawTarget{
		{Scope: "prod", Kind: "k8s", Name: "app-config", Service: "platform-app-config", Domain: "ghcr.io/acme", Digest: testDigest("ghcr.io/acme", "platform-app-config", "1.0.1")},
	}, Revisions: []RawRevision{
		ociRev("ghcr.io/acme", svc25("platform-app-config", "1.0.0", "shared-config"), nil),
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
	if app.Service.Label != "platform-app-config" || app.Summary != "Shared application configuration" || app.Revisions.Total != 2 || app.ActiveRevisions.Total != 1 || app.CurrentRevision == nil || app.CurrentRevision.Version != "1.0.1" {
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
		c := svc25(name, "1.0.0", "")
		ref := "oci://" + domain + "/platform-app-config@" + testDigest(domain, "platform-app-config", "1.0.0")
		c.Configurations = []contract.Configuration{{Name: "app", Ref: ref, Required: true}}
		return c
	}
	p := func() *contract.Contract {
		c := svc25("platform-app-config", "1.0.0", "shared-config")
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
	c := svc25("postgresql", "16.0.1", "postgresql")
	c.Workload = contract.WorkloadService
	sn := snippetFor(t, ociRev("ghcr.io/trianalab/pacto", c, nil))
	want := "dependencies:\n  - name: postgresql\n    ref: oci://ghcr.io/trianalab/pacto/postgresql\n    required: true\n    compatibility: \"^16.0.0\"\n"
	if sn == nil || sn.YAML != want || sn.Compatibility != "^16.0.0" {
		t.Errorf("snippet = %+v, want %q", sn, want)
	}
}

func TestConsumeSnippet_NonSemverOmitsRange(t *testing.T) {
	for _, v := range []string{"latest", "2024.01.01-beta"} {
		c := svc25("redis", v, "redis")
		c.Interfaces = []contract.Interface{{Name: "resp", Type: "tcp"}}
		sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil))
		if sn == nil || sn.Compatibility != "" || strings.Contains(sn.YAML, "compatibility") || !sn.Dependency {
			t.Errorf("%s: snippet = %+v", v, sn)
		}
	}
}

func TestConsumeSnippet_PolicyOnly(t *testing.T) {
	c := svc25("platform-http-policy", "1.0.0", "shared-policy")
	c.Policies = []contract.Policy{{Name: "http", Schema: "policy/schema.json"}}
	sn := snippetFor(t, ociRev("ghcr.io/acme", c, nil))
	want := "policies:\n  - name: shared-policy\n    ref: oci://ghcr.io/acme/platform-http-policy\n"
	if sn == nil || sn.YAML != want || !sn.Policy {
		t.Errorf("snippet = %+v, want %q", sn, want)
	}
}

func TestConsumeSnippet_NoOCIRef(t *testing.T) {
	c := svc25("redis", "7.0.0", "redis")
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
	if sn := snippetFor(t, ociRev("ghcr.io/acme", svc25("empty", "1.0.0", "empty"), nil)); sn != nil {
		t.Errorf("an empty contract: snippet = %+v", sn)
	}
}

// The snippet comes from the newest revision that is not invalid.
func TestConsumeSnippet_InvalidNewestRevisionIgnored(t *testing.T) {
	bad := mustParse(t, invalidYAML)
	bad.PactoVersion, bad.Service.Version, bad.Service.Offering = "2.5", "2.0.0", "cache"
	old := svc25("bad-svc", "1.4.0", "cache")
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
	worker := svc25("platform-worker-config", "1.2.0", "shared-config")
	worker.Workload = contract.WorkloadService
	jobs := func(version, compat string) RawRevision {
		c := svc25("jobs", version, "")
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
	if p.Consume != nil || p.CurrentRevision != nil || p.Interfaces.Total != 0 || p.Revisions.Total != 0 {
		t.Errorf("provider = %+v", p)
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
	pg := svc25("postgresql", "16.0.1", "postgresql")
	pg.Workload = contract.WorkloadService
	cfg := svc25("platform-app-config", "1.0.0", "shared-config")
	cfg.Configurations = []contract.Configuration{{Name: "app", Schema: "s.json", Required: true}}
	pay := svc25("payments", "1.0.0", "")
	pay.Dependencies = []contract.Dependency{
		{Name: "postgresql", Required: true, Compatibility: "^16.0.0"},
		{Name: "orders", Required: true},
	}
	pay.Configurations = []contract.Configuration{{Name: "app", Ref: "oci://ghcr.io/acme/platform-app-config@" + testDigest("ghcr.io/acme", "platform-app-config", "1.0.0"), Required: true}}
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		ociRev("ghcr.io/acme", pg, nil), ociRev("ghcr.io/acme", cfg, nil), ociRev("ghcr.io/acme", pay, nil),
		ociRev("ghcr.io/acme", svc25("orders", "1.0.0", ""), nil),
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
	redis := svc25("redis", "7.0.0", "redis")
	redis.Workload = contract.WorkloadService
	pg := svc25("postgresql", "16.0.1", "relational-db")
	pg.Workload = contract.WorkloadService
	app := svc25("app", "1.0.0", "")
	app.Dependencies = []contract.Dependency{
		{Name: "redis", External: true, Version: "7"},
		{Name: "db", Ref: "oci://other.io/x/postgresql", Required: true},
		{Name: "redis-cache", External: true},
		{Name: "Redis2", External: true},
	}
	local := svc25("local", "1.0.0", "")
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
