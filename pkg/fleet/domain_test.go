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
func domainFleet(t *testing.T, extra ...RawTarget) *Query {
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
		Targets: append([]RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "pay-app", Service: "pay", Domain: "ghcr.io/acme", Digest: "sha256:pay1.1.0", Compliance: StatusNonCompliant, EvidenceAt: fresh},
			{Scope: "prod", Kind: "k8s", Name: "orders-app", Service: "orders", Domain: "ghcr.io/acme", Digest: "sha256:orders1.0.0", Compliance: StatusCompliant, EvidenceAt: fresh},
			{Scope: "prod", Kind: "k8s", Name: "ghost-app", Service: "ghost", Compliance: StatusCompliant, EvidenceAt: fresh},
		}, extra...),
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

func TestEntityDetail_Domain(t *testing.T) {
	q := domainFleet(t)
	d, err := q.EntityDetail(KindDomain, "commerce")
	if err != nil {
		t.Fatal(err)
	}
	dd := d.Domain
	if d.Entity.Kind != KindDomain || d.Entity.Key != "commerce" || dd == nil {
		t.Fatalf("detail = %+v", d)
	}
	if dd.Summary.Services != 2 || dd.Summary.Revisions != 3 || dd.Summary.Targets != 2 || dd.Summary.Compliance.NonCompliant != 1 {
		t.Errorf("summary = %+v", dd.Summary)
	}
	if dd.Ownership.Unowned != 1 || dd.Ownership.Consistent != 1 {
		t.Errorf("ownership = %+v", dd.Ownership)
	}
	if dd.Owners.Total != 1 || dd.Owners.Items[0].Key != "team:pay-team" {
		t.Errorf("owners = %+v", dd.Owners)
	}
	if dd.Services.Total != 2 || dd.Deployments.Total != 2 {
		t.Errorf("services = %+v, deployments = %+v", dd.Services, dd.Deployments)
	}
	al, err := q.Attention(AttentionFilter{BusinessDomain: "commerce"})
	if err != nil {
		t.Fatal(err)
	}
	if al.Total == 0 || dd.Attention.Total != al.Total {
		t.Errorf("attention total = %d, want %d (and not zero)", dd.Attention.Total, al.Total)
	}
}

func TestEntityDetail_DomainNotFound(t *testing.T) {
	q := domainFleet(t)
	for _, key := range []string{"", NoBusinessDomain, "nope"} {
		_, err := q.EntityDetail(KindDomain, key)
		var nf *NotFoundError
		if !errors.As(err, &nf) || nf.Kind != "domain" {
			t.Errorf("EntityDetail(domain, %q) = %v, want a domain NotFoundError", key, err)
		}
	}
}

func TestEntityDetail_DomainInvalidRevision(t *testing.T) {
	bad := mustParse(t, invalidYAML)
	bad.Service.Domain = "commerce"
	src := NewMemorySource("local", "local", &Collection{Revisions: []RawRevision{
		{Bundle: &contract.Bundle{Contract: bad, RawYAML: []byte(invalidYAML), FS: fstest.MapFS{}}},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewQuery(snap).EntityDetail(KindDomain, "commerce")
	if err != nil {
		t.Fatal(err)
	}
	if d.Domain.Summary.InvalidRevisions != 1 {
		t.Errorf("invalid revisions = %d, want 1", d.Domain.Summary.InvalidRevisions)
	}
}

func TestAttention_NoBusinessDomainFilter(t *testing.T) {
	q := domainFleet(t)
	al, err := q.Attention(AttentionFilter{BusinessDomain: NoBusinessDomain, Limit: MaxAttentionLimit})
	if err != nil {
		t.Fatal(err)
	}
	web, ghost := string(NewServiceKeyDomain("ghcr.io/acme", "web")), string(NewServiceKeyDomain("", "ghost"))
	for _, it := range al.Items {
		if it.Service != web && it.Service != ghost {
			t.Errorf("item %s %s belongs to %s, which has a domain", it.Code, it.Label, it.Service)
		}
	}
}

func TestEntityDetail_DomainRespectsRestriction(t *testing.T) {
	q := domainFleet(t, RawTarget{Scope: "secret", Kind: SourceKindKubernetes, Name: "pay-secret", Service: "pay", Domain: "ghcr.io/acme", Compliance: StatusCompliant})
	r := q.Restrict(func(ns string) bool { return ns != "secret" })
	d, err := r.EntityDetail(KindDomain, "commerce")
	if err != nil {
		t.Fatal(err)
	}
	if d.Domain.Summary.Services != 1 {
		t.Errorf("restricted domain counts %d services, want 1", d.Domain.Summary.Services)
	}
	l, err := r.Entities(EntityFilter{Kinds: []EntityKind{KindDomain}})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entities) != 1 || l.Entities[0].Services != 1 {
		t.Errorf("restricted domain list = %+v", l.Entities)
	}
}

func TestOverview_ByDomainCoversEveryService(t *testing.T) {
	q := domainFleet(t)
	s := q.Overview().Summary
	total := 0
	for _, row := range s.ByDomain {
		total += row.Services
	}
	if total != s.Services {
		t.Errorf("domain rows hold %d services, want all %d", total, s.Services)
	}
	if len(s.ByDomain) != 2 || s.Domains != 1 {
		t.Fatalf("byDomain = %+v, domains = %d", s.ByDomain, s.Domains)
	}
	if c := s.ByDomain[0]; c.Domain != "commerce" || c.Services != 2 || c.Targets != 2 || c.NonCompliant != 1 || c.Unowned != 1 {
		t.Errorf("commerce row = %+v", c)
	}
	if last := s.ByDomain[1]; last.Domain != "" || last.Services != 2 || last.Unowned != 1 {
		t.Errorf("no-domain row = %+v, want 2 services with ghost unowned", last)
	}
}

func TestTallyDomains_BoundedNoDomainLast(t *testing.T) {
	var revs []RawRevision
	var targets []RawTarget
	for i := range 10 {
		name := "svc" + string(rune('a'+i))
		k := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: "1.0.0", Domain: "d" + string(rune('a'+i))}}
		revs = append(revs, RawRevision{Bundle: &contract.Bundle{Contract: k, FS: fstest.MapFS{}}, Digest: "sha256:" + name})
	}
	// One unknown and one stale target, so the ordering has something to order by.
	targets = append(targets,
		RawTarget{Scope: "p", Kind: "k8s", Name: "u", Service: "svcj", Compliance: StatusUnknown},
		RawTarget{Scope: "p", Kind: "k8s", Name: "s", Service: "svca", Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow().Add(-48 * time.Hour))},
		RawTarget{Scope: "p", Kind: "k8s", Name: "none", Service: "undeclared"})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow, FreshnessWindow: time.Hour}, NewMemorySource("l", "local", &Collection{Revisions: revs, Targets: targets}))
	if err != nil {
		t.Fatal(err)
	}
	s := NewQuery(snap).Overview().Summary
	if len(s.ByDomain) != overviewScopeLimit || s.Domains != 10 {
		t.Fatalf("rows = %d, domains = %d", len(s.ByDomain), s.Domains)
	}
	if s.ByDomain[0].Domain != "dj" || s.ByDomain[0].Unknown != 1 {
		t.Errorf("first row = %+v, want the domain with the unknown target", s.ByDomain[0])
	}
	if last := s.ByDomain[len(s.ByDomain)-1]; last.Domain != "" || last.Services != 1 {
		t.Errorf("last row = %+v, want the No domain row", last)
	}
	for _, row := range s.ByDomain {
		if row.Domain == "da" && row.Stale != 1 {
			t.Errorf("da row = %+v, want one stale target", row)
		}
	}
}

func TestAttention_UnownedMatchesOwnershipTally(t *testing.T) {
	for name, q := range map[string]*Query{"domain": domainFleet(t)} {
		al, err := q.Attention(AttentionFilter{Category: "unowned", Limit: MaxAttentionLimit})
		if err != nil {
			t.Fatal(err)
		}
		if want := q.Overview().Summary.Ownership.Unowned; al.Total != want || want == 0 {
			t.Errorf("%s: unowned items = %d, want the ownership tally %d (non-zero)", name, al.Total, want)
		}
		for _, it := range al.Items {
			if it.Severity != "info" || it.Entity.Kind != KindService || it.Code != "UNOWNED_SERVICE" {
				t.Errorf("%s: item = %+v", name, it)
			}
		}
	}
}

func TestAttention_UnownedExcludedByOwnerFilter(t *testing.T) {
	q := domainFleet(t)
	al, err := q.Attention(AttentionFilter{OwnerKey: "team:pay-team", Category: "unowned"})
	if err != nil {
		t.Fatal(err)
	}
	if al.Total != 0 {
		t.Errorf("an owner's backlog holds %d unowned items", al.Total)
	}
}

func TestOverview_UnownedEntryPoint(t *testing.T) {
	q := domainFleet(t)
	ov := q.Overview()
	var ep *EntryPoint
	for i := range ov.EntryPoints {
		if ov.EntryPoints[i].Category == "unowned" {
			ep = &ov.EntryPoints[i]
		}
	}
	if ep == nil || ep.Label != "Services with no declared owner" || ep.Count != 2 || ep.Severity != "info" || ep.View != EntryPointAttention {
		t.Fatalf("unowned entry point = %+v", ep)
	}
	// orders and ghost are unowned; only pay (non-compliant) needs attention.
	if ov.Summary.ServicesNeedingAttention != 1 {
		t.Errorf("services needing attention = %d, want 1 (unowned does not count)", ov.Summary.ServicesNeedingAttention)
	}
}

// Detail answers are built from cloned records, and the business domain must
// survive the clone.
func TestEntityDetail_RefsCarryBusinessDomain(t *testing.T) {
	q := domainFleet(t)
	tk := string(NewTargetKey("prod", "k8s", "pay-app"))
	d, err := q.EntityDetail(KindTarget, tk)
	if err != nil {
		t.Fatal(err)
	}
	if d.Entity.BusinessDomain != "commerce" || d.Target.Revision == nil || d.Target.Revision.BusinessDomain != "commerce" {
		t.Errorf("target detail refs = %+v / %+v", d.Entity, d.Target.Revision)
	}
	rd, err := q.EntityDetail(KindRevision, d.Target.Revision.Key)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Entity.BusinessDomain != "commerce" {
		t.Errorf("revision detail ref = %+v", rd.Entity)
	}
}

func TestSearch_BusinessDomain(t *testing.T) {
	q := domainFleet(t)
	names := func(f SearchFilter) []string {
		res, err := q.Search(f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range res.Services {
			out = append(out, h.Name+"="+h.BusinessDomain)
		}
		return out
	}
	if got := strings.Join(names(SearchFilter{BusinessDomain: "commerce"}), ","); got != "orders=commerce,pay=commerce" {
		t.Errorf("commerce search = %s", got)
	}
	if got := strings.Join(names(SearchFilter{BusinessDomain: NoBusinessDomain}), ","); got != "web=,ghost=" {
		t.Errorf("no-domain search = %s", got)
	}
}
