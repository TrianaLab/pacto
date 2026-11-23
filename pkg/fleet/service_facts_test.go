package fleet

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	factSource = "catalog-a"
	otherReg   = "ghcr.io/other"
	cacheName  = "k8s:shop/StatefulSet/cache"
)

var factAt = fixedNow().Add(-time.Hour)

// catalog is an advisory source outside every contract reporting facts.
func factCatalog(facts ...RawServiceFact) Source {
	return NewMemorySource(factSource, "idp-catalog", &Collection{Advisory: true, ServiceFacts: facts})
}

// fact is name owned by team (none when empty), depending on deps.
func fact(name, team string, deps ...string) RawServiceFact {
	f := RawServiceFact{Name: name, Entity: "component:default/" + name, ObservedAt: &factAt}
	if team != "" {
		f.Owner = contract.Owner{Team: team}
	}
	for _, d := range deps {
		f.DependsOn = append(f.DependsOn, RawFactRef{Name: d, Entity: "component:default/" + d})
	}
	return f
}

func shopRev(domain, name string, owner contract.Owner, bizDomain string, deps ...string) RawRevision {
	c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: "1.0.0", Owner: owner, Domain: bizDomain}}
	for _, d := range deps {
		c.Dependencies = append(c.Dependencies, contract.Dependency{Name: d, Ref: "oci://" + domain + "/" + d, Required: true, Compatibility: "^1.0.0"})
	}
	return ociRev(domain, c, nil)
}

func shopTarget(domain, name, ns string) RawTarget {
	return RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: name + "-" + ns, Service: name, Domain: domain, Digest: testDigest(domain, name, "1.0.0"),
		Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())}
}

// shopFleet is ledger (no owner), payments, orders (depends on payments, in
// commerce) in namespace shop and vault in secret, all in acme, plus extra.
func shopFleet(t *testing.T, extra ...Source) *Query {
	t.Helper()
	team := func(n string) contract.Owner { return contract.Owner{Team: n} }
	col := &Collection{
		Revisions: []RawRevision{shopRev(acme, "ledger", contract.Owner{}, ""), shopRev(acme, "payments", team("team-pay"), ""),
			shopRev(acme, "orders", team("team-orders"), "commerce", "payments"), shopRev(acme, "vault", team("team-sec"), "")},
		Targets: []RawTarget{shopTarget(acme, "ledger", "shop"), shopTarget(acme, "payments", "shop"), shopTarget(acme, "orders", "shop"), shopTarget(acme, "vault", "secret")},
	}
	return NewQuery(buildClusterSnap(t, append([]Source{NewMemorySource("k8s", SourceKindKubernetes, col)}, extra...)...))
}

// otherLedger is a second ledger, in another registry, running in secret.
func otherLedger() Source {
	return NewMemorySource("k8s-other", SourceKindKubernetes, &Collection{Revisions: []RawRevision{shopRev(otherReg, "ledger", contract.Owner{}, "")},
		Targets: []RawTarget{shopTarget(otherReg, "ledger", "secret")}})
}

func shop(q *Query, name string) *ServiceRecord {
	return q.snap.Services[NewServiceKeyDomain(acme, name)]
}

// noSecret hides namespace secret.
func noSecret(q *Query) *Query {
	return q.Restrict(anyCluster(func(ns string) bool { return ns != "secret" }), anyClusterKind(nil))
}

func edgesBetween(rels []Relationship, from, to ServiceKey) []Relationship {
	return slices.DeleteFunc(slices.Clone(rels), func(r Relationship) bool { return r.FromService != from || r.ToService != to })
}

func factLimitations(ls []Limitation, code string) []Limitation {
	return slices.DeleteFunc(slices.Clone(ls), func(l Limitation) bool { return l.Code != code })
}

func TestServiceFactOwnerWhenNoneDeclared(t *testing.T) {
	q := shopFleet(t, workloadsSource("k8s-workloads", nil, rawInferred("shop", "StatefulSet", "cache")), factCatalog(fact("cache", "team-ledger")))
	s := q.snap.Services[NewServiceKey(cacheName)]
	want := &InferredFact{Value: "team:team-ledger", Source: factSource, Where: "component:default/cache", AsOf: &factAt}
	if !s.Owner.IsEmpty() || s.OwnerProvenance != OwnerProvenanceInferred || !reflect.DeepEqual(s.InferredOwner, want) {
		t.Errorf("owner %+v provenance %q inferred %+v", s.Owner, s.OwnerProvenance, s.InferredOwner)
	}
	if s.InferredOwner.AsOf == &factAt {
		t.Error("asOf must be a copy")
	}
}

// invoicesWith builds invoices declaring declared, attesting attested and
// running on a target whose source read viaTarget, with a catalog fact when
// withFact is set.
func invoicesWith(t *testing.T, declared, attested, viaTarget string, withFact bool) *ServiceRecord {
	t.Helper()
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "invoices", Version: "1.0.0"}}
	if declared != "" {
		c.Service.Owner = contract.Owner{Team: declared}
	}
	rr := RawRevision{Digest: "sha256:inv0", Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}}
	if attested != "" {
		rr.Bundle.FS = fstest.MapFS{contract.InferredRecordFile: {Data: attRecord("repository:.github/CODEOWNERS:7", attested, attCommit).Marshal()}}
		rr.Provenance = attProvenance(attRepo, attCommit)
	}
	col := &Collection{Revisions: []RawRevision{rr}}
	if viaTarget != "" {
		col.Targets = []RawTarget{{Scope: "eu", Kind: SourceKindKubernetes, Name: "invoices-eu", Service: "invoices", Digest: "sha256:inv0",
			Owner: contract.Owner{Team: viaTarget}, Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())}}
	}
	srcs := []Source{NewMemorySource("oci", "oci", col)}
	if withFact {
		srcs = append(srcs, factCatalog(fact("invoices", "team-x")))
	}
	return buildClusterSnap(t, srcs...).Services[rInv]
}

func TestOwnerPrecedenceWithInferred(t *testing.T) {
	for _, c := range []struct {
		name                         string
		declared, attested, target   string
		withFact                     bool
		wantOwner, wantProv, wantInf string
	}{
		{"declared", "billing", "", "", true, "billing", OwnerProvenanceDeclared, "team:team-x"},
		{"attested", "", "payments", "", true, "payments", OwnerProvenanceAttested, "team:team-x"},
		{"declared-via-target", "", "", "ops", true, "ops", OwnerProvenanceDeclaredViaTarget, "team:team-x"},
		{"fact only", "", "", "", true, "", OwnerProvenanceInferred, "team:team-x"},
		{"no fact", "", "", "", false, "", "", ""},
	} {
		s := invoicesWith(t, c.declared, c.attested, c.target, c.withFact)
		inf := ""
		if s.InferredOwner != nil {
			inf = s.InferredOwner.Value
		}
		if s.Owner.Team != c.wantOwner || s.OwnerProvenance != c.wantProv || inf != c.wantInf {
			t.Errorf("%s: owner %q provenance %q inferred %q", c.name, s.Owner.Team, s.OwnerProvenance, inf)
		}
	}
}

func TestInferredOwnerIsNeverAClaim(t *testing.T) {
	q := shopFleet(t, factCatalog(fact("ledger", "team-ledger"), fact("payments", "team-other")))
	if n := q.OwnerClaimCount(shop(q, "ledger").Key); n != 0 {
		t.Errorf("ledger claims = %d, want 0", n)
	}
	if n := q.OwnerClaimCount(shop(q, "payments").Key); n != 1 {
		t.Errorf("payments claims = %d, want 1", n)
	}
	if hasLimitation(q.snap.Limitations, LimitationOwnerConflict) {
		t.Errorf("a fact raised an owner conflict: %+v", q.snap.Limitations)
	}
}

func TestInferredOwnerStaysUnowned(t *testing.T) {
	base, with := shopFleet(t), shopFleet(t, factCatalog(fact("ledger", "team-ledger")))
	if a, b := base.Overview().Summary.Ownership, with.Overview().Summary.Ownership; a != b || b.Unowned == 0 {
		t.Errorf("ownership %+v, want %+v with ledger unowned", b, a)
	}
	attention := func(q *Query) int {
		l, err := q.Attention(AttentionFilter{Category: categoryUnowned})
		if err != nil {
			t.Fatal(err)
		}
		return l.Total
	}
	if a, b := attention(base), attention(with); a != b || b == 0 {
		t.Errorf("unowned attention %d, want %d", b, a)
	}
	l, err := with.Entities(EntityFilter{Kinds: []EntityKind{KindService}, OwnerKey: "team:team-ledger"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Total != 0 {
		t.Errorf("the inferred owner's estate lists %d services", l.Total)
	}
}

func TestServiceFactDomainNeverFillsDeclared(t *testing.T) {
	orders, ledger := fact("orders", ""), fact("ledger", "")
	orders.BusinessDomain, ledger.BusinessDomain = "billing", "Not A Slug"
	base, q := shopFleet(t), shopFleet(t, factCatalog(orders, ledger))
	if s := shop(q, "orders"); s.BusinessDomain != "commerce" || s.InferredBusinessDomain == nil || s.InferredBusinessDomain.Value != "billing" {
		t.Errorf("orders domain %q inferred %+v", s.BusinessDomain, s.InferredBusinessDomain)
	}
	if s, b := shop(q, "ledger"), shop(base, "ledger"); s.InferredBusinessDomain != nil || s.BusinessDomain != b.BusinessDomain || s.BusinessDomainUnknown != b.BusinessDomainUnknown {
		t.Errorf("ledger domain %q unknown %v inferred %+v", s.BusinessDomain, s.BusinessDomainUnknown, s.InferredBusinessDomain)
	}
}

func TestServiceFactEdgeIsInferred(t *testing.T) {
	q := shopFleet(t, factCatalog(fact("ledger", "", "payments")))
	from, to := shop(q, "ledger").Key, shop(q, "payments").Key
	if got := edgesBetween(q.snap.Relationships, from, to); len(got) != 0 {
		t.Errorf("a fact edge in Relationships: %+v", got)
	}
	got := edgesBetween(q.snap.InferredRelationships, from, to)
	want := []InferredSource{{Source: factSource, Kind: serviceFactKind, Where: "component:default/ledger", Confidence: "possible", ObservedAt: &factAt}}
	if len(got) != 1 || got[0].Provenance != ProvenanceInferred || got[0].Compatibility != contract.CompatibilityUnknown ||
		got[0].Confidence != "possible" || !got[0].Resolved || got[0].To != "payments" || !reflect.DeepEqual(got[0].InferredSources, want) {
		t.Fatalf("fact edges = %+v", got)
	}
	if ConsumesEdge(got[0]) {
		t.Error("a fact edge must never consume")
	}
	if !slices.Contains(q.snap.inferredForward[from], to) || !slices.Contains(q.snap.inferredReverse[to], from) {
		t.Error("the inferred adjacency misses the fact edge")
	}
}

func TestServiceFactEdgeSkipsDeclared(t *testing.T) {
	base, q := shopFleet(t), shopFleet(t, factCatalog(fact("orders", "", "payments", "orders", "nobody")))
	if len(q.snap.InferredRelationships) != 0 || len(q.snap.Relationships) != len(base.snap.Relationships) {
		t.Errorf("inferred %+v relationships %d, want none and %d", q.snap.InferredRelationships, len(q.snap.Relationships), len(base.snap.Relationships))
	}
}

func TestServiceFactEdgeMergesInferred(t *testing.T) {
	// A revision's inferred record already names the edge.
	o := validRecordOpts()
	o.extra = []Source{factCatalog(fact("newsletter", "", "notification-worker"))}
	q := recordFleet(t, o)
	if got := edgesBetween(q.snap.InferredRelationships, rNews, rWorker); len(got) != 0 {
		t.Errorf("a second edge for a record edge: %+v", got)
	}
	g := graphOf(t, q, GraphQuery{Service: "newsletter", IncludeObserved: true})
	if got := edgesBetween(g.Edges, rNews, rWorker); len(got) != 1 {
		t.Errorf("graph draws %d edges newsletter -> notification-worker, want 1: %+v", len(got), got)
	}
	// A runtime inferred call: one edge with both pieces of evidence.
	f := fact("cache", "", "payments")
	q = shopFleet(t, workloadsSource("k8s-workloads", nil, rawInferred("shop", "StatefulSet", "cache")),
		kernel(call(cacheName, "payments")), factCatalog(f, f))
	got := edgesBetween(q.snap.InferredRelationships, NewServiceKey(cacheName), shop(q, "payments").Key)
	if len(got) != 1 || got[0].ObservedCount == 0 || len(got[0].InferredSources) != 1 || got[0].InferredSources[0].Kind != serviceFactKind {
		t.Errorf("merged edges = %+v", got)
	}
}

func TestServiceFactEdgeNotInDeclaredWalk(t *testing.T) {
	q := shopFleet(t, factCatalog(fact("ledger", "", "payments")))
	ledger := shop(q, "ledger").Key
	for _, gq := range []GraphQuery{{Service: "payments", Direction: DirectionDependents, Transitive: true}, {Service: "ledger", Transitive: true}} {
		g := graphOf(t, q, gq)
		if slices.ContainsFunc(g.Edges, func(r Relationship) bool { return r.FromService == ledger }) {
			t.Errorf("%+v: the declared walk sees a fact edge: %+v", gq, g.Edges)
		}
	}
}

func TestServiceFactAmbiguousName(t *testing.T) {
	q := shopFleet(t, otherLedger(), factCatalog(fact("Ledger", "team-ledger"), fact("orders", "", "ledger")))
	for k, s := range q.snap.Services {
		if s.InferredOwner != nil {
			t.Errorf("%s got %+v", k, s.InferredOwner)
		}
	}
	if got := edgesBetween(q.snap.InferredRelationships, shop(q, "orders").Key, shop(q, "ledger").Key); len(got) != 0 {
		t.Errorf("an ambiguous dependency made an edge: %+v", got)
	}
	l := factLimitations(q.snap.Limitations, LimitationServiceFactAmbiguous)
	if len(l) != 1 || l[0].Source != factSource || !strings.HasPrefix(l[0].Message, "1 names") || !strings.HasSuffix(l[0].Message, ": ledger") {
		t.Errorf("limitations = %+v", l)
	}
	if q.snap.Completeness != CompletenessComplete {
		t.Errorf("completeness = %s", q.snap.Completeness)
	}
}

func TestServiceFactDisagreeingEntities(t *testing.T) {
	a, b := fact("ledger", "team-a"), fact("ledger", "team-b")
	b.Entity = "component:other/ledger"
	a.BusinessDomain, b.BusinessDomain = "billing", "billing"
	q := shopFleet(t, factCatalog(a, b))
	s := shop(q, "ledger")
	if s.InferredOwner != nil || s.OwnerProvenance != "" || s.InferredBusinessDomain == nil || s.InferredBusinessDomain.Value != "billing" {
		t.Errorf("owner %+v %q domain %+v", s.InferredOwner, s.OwnerProvenance, s.InferredBusinessDomain)
	}
	if l := factLimitations(q.snap.Limitations, LimitationServiceFactAmbiguous); len(l) != 1 || !strings.Contains(l[0].Message, "ledger") {
		t.Errorf("limitations = %+v", l)
	}
}

func TestServiceFactAmbiguityBounded(t *testing.T) {
	var revs []RawRevision
	var facts []RawServiceFact
	for i := range 7 {
		name := fmt.Sprintf("svc%d", i)
		revs = append(revs, shopRev(acme, name, contract.Owner{}, ""))
		facts = append(facts, fact(name, "team-a"), fact(name, "team-b"))
	}
	snap := buildClusterSnap(t, NewMemorySource("oci", "oci", &Collection{Revisions: revs}), factCatalog(facts...))
	l := factLimitations(snap.Limitations, LimitationServiceFactAmbiguous)
	if len(l) != 1 || !strings.HasPrefix(l[0].Message, "7 names") || !strings.HasSuffix(l[0].Message, "svc0, svc1, svc2, svc3, svc4") {
		t.Errorf("limitations = %+v", l)
	}
}

func TestServiceFactServiceKeyExact(t *testing.T) {
	gone, pay := fact("ledger", "team-a"), fact("ledger", "team-b")
	gone.ServiceKey, pay.ServiceKey = NewServiceKey("nope"), NewServiceKeyDomain(acme, "payments")
	q := shopFleet(t, factCatalog(gone, pay))
	if s := shop(q, "ledger"); s.InferredOwner != nil {
		t.Errorf("a key naming no service matched by name: %+v", s.InferredOwner)
	}
	if s := shop(q, "payments"); s.InferredOwner == nil || s.InferredOwner.Value != "team:team-b" {
		t.Errorf("payments inferred owner %+v", s.InferredOwner)
	}
}

func TestServiceFactSkipsPactoExport(t *testing.T) {
	f := fact("ledger", "team-ledger", "payments")
	f.Exported = true
	q := shopFleet(t, factCatalog(f))
	if s := shop(q, "ledger"); s.InferredOwner != nil || len(q.snap.InferredRelationships) != 0 {
		t.Errorf("an exported entity was read back: %+v %+v", s.InferredOwner, q.snap.InferredRelationships)
	}
}

func TestServiceFactDropsUncleanText(t *testing.T) {
	f := fact("ledger", "team\x00x")
	f.Entity = "component:default/\x07"
	f.BusinessDomain = "billing"
	q := shopFleet(t, factCatalog(f))
	if s := shop(q, "ledger"); s.InferredOwner != nil || s.InferredBusinessDomain == nil || s.InferredBusinessDomain.Where != "" {
		t.Errorf("owner %+v domain %+v", s.InferredOwner, s.InferredBusinessDomain)
	}
}

func TestAdvisoryServiceFactSourceNeverPartial(t *testing.T) {
	base := shopFleet(t).snap
	for name, col := range map[string]*Collection{
		"unavailable": {Advisory: true, ServiceFacts: []RawServiceFact{fact("ledger", "team-ledger")}, State: &SourceState{Status: SourceUnavailable}},
		"limitation":  {Advisory: true, ServiceFacts: []RawServiceFact{fact("ledger", "team-ledger")}, Limitations: []Limitation{{Code: LimitationSourceRecordInvalid, Message: "read stopped"}}},
	} {
		snap := shopFleet(t, NewMemorySource(factSource, "idp-catalog", col)).snap
		if snap.Completeness != base.Completeness || snap.ConsumerCompleteness() != base.ConsumerCompleteness() {
			t.Errorf("%s: completeness %s / %s, want %s", name, snap.Completeness, snap.ConsumerCompleteness(), base.Completeness)
		}
	}
}

func TestRestrictKeepsInferredOwnerMark(t *testing.T) {
	q := noSecret(shopFleet(t, factCatalog(fact("ledger", "team-ledger"))))
	if s := shop(q, "ledger"); s.OwnerProvenance != OwnerProvenanceInferred || s.InferredOwner == nil {
		t.Errorf("restricted ledger %q %+v", s.OwnerProvenance, s.InferredOwner)
	}
	targets := &Collection{Targets: []RawTarget{payTarget("secret", contract.Owner{Team: "pay"}), payTarget("shop", contract.Owner{})}}
	full := NewQuery(buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, targets), factCatalog(fact("pay", "team-x"))))
	if s := payRecord(t, full); s.OwnerProvenance != OwnerProvenanceDeclaredViaTarget {
		t.Fatalf("unrestricted provenance = %q", s.OwnerProvenance)
	}
	s := payRecord(t, full.Restrict(anyCluster(func(ns string) bool { return ns == "shop" || ns == "" }), anyClusterKind(nil)))
	if !s.Owner.IsEmpty() || s.OwnerProvenance != OwnerProvenanceInferred {
		t.Errorf("restricted owner %+v provenance %q, want the inferred fallback", s.Owner, s.OwnerProvenance)
	}
}

func TestRestrictDropsServiceFactEdgeToHiddenService(t *testing.T) {
	full := shopFleet(t, factCatalog(fact("ledger", "", "vault")))
	ledger, vault := shop(full, "ledger").Key, shop(full, "vault").Key
	if len(edgesBetween(full.snap.InferredRelationships, ledger, vault)) != 1 {
		t.Fatalf("no fact edge ledger -> vault: %+v", full.snap.InferredRelationships)
	}
	q := noSecret(full)
	if len(q.snap.InferredRelationships) != 0 || slices.Contains(q.snap.inferredForward[ledger], vault) {
		t.Errorf("the edge to a hidden service stayed: %+v", q.snap.InferredRelationships)
	}
	if s := shop(q, "ledger"); s.HiddenDependencies != shop(noSecret(shopFleet(t)), "ledger").HiddenDependencies {
		t.Errorf("hidden dependencies = %d", s.HiddenDependencies)
	}
}

func TestRestrictServiceFactEdgeNeverCountsHidden(t *testing.T) {
	base := noSecret(shopFleet(t))
	q := noSecret(shopFleet(t, factCatalog(fact("vault", "", "payments"))))
	if a, b := shop(base, "payments").HiddenDependents, shop(q, "payments").HiddenDependents; a != b {
		t.Errorf("payments hidden dependents %d, want %d", b, a)
	}
	if q.snap.Completeness != base.snap.Completeness {
		t.Errorf("completeness %s, want %s", q.snap.Completeness, base.snap.Completeness)
	}
	spy := "k8s:secret/Deployment/spy"
	runtime := noSecret(shopFleet(t, workloadsSource("k8s-workloads", nil, rawInferred("secret", "Deployment", "spy")), kernel(call(spy, "payments"))))
	if n := shop(runtime, "payments").HiddenDependents; n != shop(base, "payments").HiddenDependents+1 {
		t.Errorf("a runtime inferred edge from a hidden caller counts %d", n)
	}
	// The availability answer reads HiddenDependents: a fact edge never turns it partial.
	if base.availabilityPartial(shop(base, "payments")) || q.availabilityPartial(shop(q, "payments")) {
		t.Error("a fact edge from a hidden service made payments' availability partial")
	}
	if !runtime.availabilityPartial(shop(runtime, "payments")) {
		t.Error("a runtime inferred edge from a hidden caller left availability complete")
	}
}

func TestRestrictRebuildsServiceFactAmbiguous(t *testing.T) {
	a, b := fact("payments", "team-a"), fact("payments", "team-b")
	full := shopFleet(t, otherLedger(), factCatalog(fact("ledger", "team-ledger"), a, b))
	if l := factLimitations(full.snap.Limitations, LimitationServiceFactAmbiguous); len(l) != 1 || !strings.HasPrefix(l[0].Message, "2 names") {
		t.Fatalf("full limitations = %+v", l)
	}
	l := factLimitations(noSecret(full).snap.Limitations, LimitationServiceFactAmbiguous)
	if len(l) != 1 || !strings.HasPrefix(l[0].Message, "1 names") || strings.Contains(l[0].Message, "ledger") {
		t.Errorf("restricted limitations = %+v", l)
	}
	none := full.Restrict(anyCluster(func(ns string) bool { return ns != "secret" && ns != "shop" }), anyClusterKind(nil))
	if l := factLimitations(none.snap.Limitations, LimitationServiceFactAmbiguous); len(l) != 0 {
		t.Errorf("limitations with every match hidden = %+v", l)
	}
}

func TestNoServiceFactsNoChange(t *testing.T) {
	empty := NewMemorySource(factSource, "idp-catalog", &Collection{Advisory: true, ServiceFacts: []RawServiceFact{}})
	a, b, c := shopFleet(t, factCatalog()).snap, shopFleet(t, empty).snap, shopFleet(t).snap
	if !reflect.DeepEqual(a, b) {
		t.Error("a nil and an empty facts list built different snapshots")
	}
	for name, eq := range map[string]bool{
		"services":      reflect.DeepEqual(a.Services, c.Services),
		"relationships": reflect.DeepEqual(a.Relationships, c.Relationships),
		"inferred":      reflect.DeepEqual(a.InferredRelationships, c.InferredRelationships),
		"limitations":   reflect.DeepEqual(a.Limitations, c.Limitations),
		"completeness":  a.Completeness == c.Completeness,
	} {
		if !eq {
			t.Errorf("a facts source with no facts changed the %s", name)
		}
	}
}

// TestServiceFactFieldsHaveNoGateReader pins that no gate, impact, delivery,
// query, product, offering or attention code reads an inferred fact: they are
// presentation only.
func TestServiceFactFieldsHaveNoGateReader(t *testing.T) {
	var files []string
	for _, glob := range []string{"../impact/*.go", "../delivery/*.go", "query.go", "product.go", "offering_list.go", "attention*.go"} {
		m, err := filepath.Glob(glob)
		if err != nil || len(m) == 0 {
			t.Fatalf("glob %s: %v %v", glob, m, err)
		}
		files = append(files, m...)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && (id.Name == "InferredOwner" || id.Name == "InferredBusinessDomain") {
				t.Errorf("%s reads %s", fset.Position(id.Pos()), id.Name)
			}
			return true
		})
	}
}

// fixtures above must hold what the tests assume.
func TestShopFleetFixture(t *testing.T) {
	q := shopFleet(t)
	for _, n := range []string{"ledger", "payments", "orders", "vault"} {
		if s := shop(q, n); s == nil || len(s.Targets) != 1 {
			t.Fatalf("%s = %+v", n, s)
		}
	}
	if shop(noSecret(q), "vault") != nil {
		t.Fatal("vault must be hidden from a reader without secret")
	}
}
