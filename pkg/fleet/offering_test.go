package fleet

import (
	"context"
	"errors"
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
