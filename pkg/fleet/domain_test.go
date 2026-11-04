package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// domainFleet is a small fleet whose services cover every business-domain case:
// pay moved from payments (v1.0.0) to commerce (v1.1.0); orders declares commerce
// and no owner; web declares no domain; ghost is a target-only service no source
// read a contract for.
func domainFleet(t *testing.T) *Query {
	t.Helper()
	c := func(name, version, domain string, owner contract.Owner) RawRevision {
		k := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: version, Domain: domain, Owner: owner}, Readiness: readyContract()}
		return RawRevision{Bundle: &contract.Bundle{Contract: k, FS: fstest.MapFS{}}, Domain: "ghcr.io/acme", Digest: "sha256:" + name + version}
	}
	fresh := ptrTime(fixedNow().Add(-time.Minute))
	src := NewMemorySource("local", "local", &Collection{
		Revisions: []RawRevision{
			c("pay", "1.0.0", "payments", contract.Owner{Team: "pay-team"}),
			c("pay", "1.1.0", "commerce", contract.Owner{Team: "pay-team"}),
			c("orders", "1.0.0", "commerce", contract.Owner{}),
			c("web", "1.0.0", "", contract.Owner{Team: "web-team"}),
		},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "pay-app", Service: "pay", Domain: "ghcr.io/acme", Digest: "sha256:pay1.1.0", Compliance: StatusNonCompliant, EvidenceAt: fresh},
			{Scope: "prod", Kind: "k8s", Name: "orders-app", Service: "orders", Domain: "ghcr.io/acme", Digest: "sha256:orders1.0.0", Compliance: StatusCompliant, EvidenceAt: fresh},
			{Scope: "prod", Kind: "k8s", Name: "ghost-app", Service: "ghost", Compliance: StatusCompliant, EvidenceAt: fresh},
		},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return NewQuery(snap)
}

func entityKeys(t *testing.T, q *Query, f EntityFilter) []string {
	t.Helper()
	l, err := q.Entities(f)
	if err != nil {
		t.Fatalf("Entities(%+v): %v", f, err)
	}
	var keys []string
	for _, e := range l.Entities {
		keys = append(keys, e.Label)
	}
	return keys
}

func TestBuild_BusinessDomainNewestRevisionWins(t *testing.T) {
	q := domainFleet(t)
	pay := q.snap.Services[NewServiceKeyDomain("ghcr.io/acme", "pay")]
	if pay.BusinessDomain != "commerce" {
		t.Fatalf("pay business domain = %q, want commerce (its newest revision)", pay.BusinessDomain)
	}
	for _, l := range q.snap.Limitations {
		if strings.Contains(strings.ToLower(l.Message), "domain") {
			t.Errorf("a domain move must raise no limitation, got %+v", l)
		}
	}
}

// A service the operator reports with no published revision still gets the domain
// the contract it runs declares.
func TestBuild_BusinessDomainFromTargetsWithoutRevisions(t *testing.T) {
	src := NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{
		{Scope: "a", Kind: "k8s", Name: "x", Service: "x", BusinessDomain: "payments"},
		{Scope: "b", Kind: "k8s", Name: "x", Service: "x"},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	if got := q.snap.Services[NewServiceKeyDomain("", "x")].BusinessDomain; got != "payments" {
		t.Fatalf("business domain = %q, want payments", got)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindDomain}}); len(got) != 1 || got[0] != "payments" {
		t.Fatalf("domains = %v", got)
	}
}

func TestEntities_DomainKind(t *testing.T) {
	q := domainFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindDomain}})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entities) != 1 || l.Entities[0].Key != "commerce" || l.Entities[0].Kind != KindDomain {
		t.Fatalf("domains = %+v, want only commerce", l.Entities)
	}
	if e := l.Entities[0]; e.Services != 2 || e.Targets != 2 || e.BusinessDomain != "commerce" {
		t.Errorf("commerce = %+v, want 2 services and 2 targets", e)
	}
	if l.Aggregate.Domains != 1 {
		t.Errorf("aggregate domains = %d, want 1", l.Aggregate.Domains)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindDomain}, Text: "comm"}); len(got) != 1 {
		t.Errorf("text search over domains = %v", got)
	}
	if got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindDomain}, BusinessDomain: "payments"}); len(got) != 0 {
		t.Errorf("a domain filter on domains matches the domain itself, got %v", got)
	}
}

func TestEntities_NoBusinessDomainBucket(t *testing.T) {
	q := domainFleet(t)
	got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, BusinessDomain: NoBusinessDomain, Sort: SortName})
	if strings.Join(got, ",") != "ghost,web" {
		t.Fatalf("no-domain services = %v, want ghost and web", got)
	}
}

func TestEntities_BusinessDomainOnTargets(t *testing.T) {
	q := domainFleet(t)
	l, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindTarget}, BusinessDomain: "commerce"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entities) != 2 {
		t.Fatalf("commerce targets = %+v", l.Entities)
	}
	for _, e := range l.Entities {
		if e.BusinessDomain != "commerce" {
			t.Errorf("target ref %s carries business domain %q", e.Key, e.BusinessDomain)
		}
	}
	revs := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindRevision}, BusinessDomain: "commerce"})
	// pay 1.0.0 declared payments, but it is grouped by its service.
	if len(revs) != 3 {
		t.Errorf("commerce revisions = %v, want all three of pay and orders", revs)
	}
}

func TestEntities_RegistryDomainUnchanged(t *testing.T) {
	q := domainFleet(t)
	got := entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Domain: "ghcr.io/acme"})
	if strings.Join(got, ",") != "orders,pay,web" {
		t.Fatalf("registry filter = %v", got)
	}
	got = entityKeys(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Domain: "ghcr.io/acme", BusinessDomain: NoBusinessDomain})
	if strings.Join(got, ",") != "web" {
		t.Fatalf("registry and business domain compose, got %v", got)
	}
}

func TestEntities_OwnerFilterRejectedOnDomainsOnly(t *testing.T) {
	q := domainFleet(t)
	_, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindDomain}, Owner: "x"})
	var iq *InvalidQueryError
	if !errors.As(err, &iq) {
		t.Fatalf("err = %v, want InvalidQueryError", err)
	}
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindSource}, BusinessDomain: "x"}); !errors.As(err, &iq) {
		t.Fatalf("a business domain on sources only = %v, want InvalidQueryError", err)
	}
}

// On an every-kind query a business domain narrows to what belongs to a service.
func TestEntities_BusinessDomainSkipsOwnersAndSources(t *testing.T) {
	q := domainFleet(t)
	l, err := q.Entities(EntityFilter{BusinessDomain: "commerce"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entities {
		if e.Kind == KindOwner || e.Kind == KindSource {
			t.Errorf("%s %s matched a business domain", e.Kind, e.Key)
		}
	}
	if l.Aggregate.Services != 2 || l.Aggregate.Domains != 1 {
		t.Errorf("aggregate = %+v", l.Aggregate)
	}
}
