package fleet

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	ledgerName = "k8s:shop/StatefulSet/ledger"
	sweepName  = "k8s:ops/CronJob/sweep"
)

// inferredWorkloads is the workload source of the inferred tests: cart and
// ledger in shop, sweep in ops.
func inferredWorkloads() Source {
	return workloadsSource("k8s-workloads", nil, inferredCart(), rawInferred("shop", "StatefulSet", "ledger"), rawInferred("ops", "CronJob", "sweep"))
}

// kernel is an observer that saw the given calls.
func kernel(edges ...ObservedEdge) Source {
	return NewMemorySource("kernel", "observation", &Collection{Observed: edges})
}

// call is one observed call.
func call(from, to string) ObservedEdge { return ObservedEdge{From: from, To: to, Count: 3} }

// goldenWith is the golden fleet (owners, domains, an offering and golden
// paths), its other sources and, when inferred is set, the inferred workloads.
func goldenWith(t *testing.T, inferred bool, other ...Source) *Query {
	t.Helper()
	revs := []RawRevision{
		ociRev(acme, templated("http-service", "1.0.0"), pathFiles),
		createdFrom(pathContentHash(t)),
		postgres(),
		ociRev(acme, usesPostgres("orders", "1.0.0"), nil),
	}
	srcs := append([]Source{NewMemorySource("oci", "oci", &Collection{Revisions: revs})}, other...)
	if inferred {
		srcs = append(srcs, inferredWorkloads())
	}
	return NewQuery(buildClusterSnap(t, srcs...))
}

// sameJSON fails when a and b marshal differently.
func sameJSON(t *testing.T, what string, a, b any) {
	t.Helper()
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Errorf("%s changed with inferred services:\n%s\n%s", what, ja, jb)
	}
}

func entityList(t *testing.T, q *Query, f EntityFilter) *EntityList {
	t.Helper()
	l, err := q.Entities(f)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func refKeys(refs []EntityRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Key)
	}
	return out
}

func TestResolverReturnsObservedInferred(t *testing.T) {
	q := goldenWith(t, true)
	resolve := q.snap.ObservedNameResolver()
	if k, res := resolve(cartName); res != ObservedInferred || k != NewServiceKey(cartName) {
		t.Errorf("cart resolves to %q %v", k, res)
	}
	if _, res := resolve("orders"); res != ObservedResolved {
		t.Errorf("orders resolves %v", res)
	}
	if _, res := resolve("k8s:shop/Deployment/gone"); res != ObservedUnknown {
		t.Errorf("an unknown workload resolves %v", res)
	}
}

func TestFoldInferredCallerStaysUncontracted(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, "postgresql")))
	callers := q.snap.UncontractedCallers
	if len(callers) != 1 || callers[0].Caller != cartName || callers[0].ToService != acmeService(q, "postgresql").Key {
		t.Fatalf("uncontracted callers %+v", callers)
	}
	for _, rel := range q.snap.Relationships {
		if rel.Provenance == ProvenanceObserved {
			t.Errorf("an inferred caller made an observed relationship %+v", rel)
		}
	}
	if len(q.snap.observedForward) != 0 || len(q.snap.observedReverse) != 0 {
		t.Errorf("observed indexes %v %v", q.snap.observedForward, q.snap.observedReverse)
	}
}

func TestFoldInferredCallerCarriesEntity(t *testing.T) {
	with := goldenWith(t, true, kernel(call(cartName, "postgresql"), call("k8s:shop/Deployment/gone", "postgresql")))
	without := goldenWith(t, false, kernel(call(cartName, "postgresql"), call("k8s:shop/Deployment/gone", "postgresql")))
	got := map[string]ServiceKey{}
	for _, c := range with.snap.UncontractedCallers {
		got[c.Caller] = c.Entity
	}
	if got[cartName] != NewServiceKey(cartName) || got["k8s:shop/Deployment/gone"] != "" {
		t.Errorf("entities %v", got)
	}
	// Apart from the entity, the callers are exactly what they were.
	for i := range with.snap.UncontractedCallers {
		with.snap.UncontractedCallers[i].Entity = ""
	}
	sameJSON(t, "uncontracted callers", without.snap.UncontractedCallers, with.snap.UncontractedCallers)
}

func TestFoldInferredCalleeKeepsUnknownBehaviour(t *testing.T) {
	q := goldenWith(t, true, kernel(call("orders", cartName), call(sweepName, "k8s:shop/Deployment/gone")))
	if len(q.snap.UncontractedCallers) != 0 {
		t.Errorf("callers %+v", q.snap.UncontractedCallers)
	}
	for _, rel := range q.snap.Relationships {
		if rel.Provenance == ProvenanceObserved {
			t.Errorf("an edge to an inferred service %+v", rel)
		}
	}
	for _, n := range []string{cartName, sweepName} {
		if !limitationMentions(q.snap, LimitationObservedIdentityUnresolved, n+" could not be mapped") {
			t.Errorf("no unknown limitation for %s: %+v", n, q.snap.Limitations)
		}
	}
}

func TestFoldInferredCallerOfExternalStaysUnknown(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, "api.stripe.com")))
	if !limitationMentions(q.snap, LimitationObservedIdentityUnresolved, cartName) {
		t.Errorf("limitations %+v", q.snap.Limitations)
	}
	if len(q.snap.Relationships) != len(goldenWith(t, false).snap.Relationships) {
		t.Error("an inferred caller of an external system made an edge")
	}
}

// overviewFigures is every Overview figure that means declared services.
func overviewFigures(o OverviewSummary) any {
	return []any{o.Services, o.Ownership, o.Knowledge, o.KnowledgeItems, o.GoldenPaths, o.InvalidGoldenPaths, o.ByDomain, o.ServicesNeedingAttention}
}

func TestOverviewCountsInferredApart(t *testing.T) {
	whole := func() Source { return NewMemorySource("k8s", SourceKindKubernetes, &Collection{}) }
	without, with := goldenWith(t, false, whole()).Overview().Summary, goldenWith(t, true, whole()).Overview().Summary
	sameJSON(t, "overview", overviewFigures(without), overviewFigures(with))
	if with.InferredServices != 3 || with.InferredUnverified != 0 || without.InferredServices != 0 {
		t.Errorf("inferred %d unverified %d, without %d", with.InferredServices, with.InferredUnverified, without.InferredServices)
	}
	// With no Pacto source read, no inferred service is verified.
	if sum := goldenWith(t, true).Overview().Summary; sum.InferredUnverified != 3 {
		t.Errorf("unverified %d", sum.InferredUnverified)
	}
	scoped := NewMemorySource("k8s", SourceKindKubernetes, &Collection{
		State:       &SourceState{Status: SourcePartial, Namespaces: []string{"shop"}, NamespacesTotal: 2},
		Limitations: []Limitation{NamespaceUnreadableLimitation("k8s", "ops", "forbidden")},
	})
	sum := goldenWith(t, true, scoped).Overview().Summary
	if sum.InferredServices != 3 || sum.InferredUnverified != 1 {
		t.Errorf("inferred %d unverified %d", sum.InferredServices, sum.InferredUnverified)
	}
}

func TestInferredNeverMatchesOwnershipOrGapFilters(t *testing.T) {
	q := goldenWith(t, true)
	ov := q.Overview().Summary
	if l := entityList(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipUnowned}); l.Total != ov.Ownership.Unowned {
		t.Errorf("unowned list %d != tally %d", l.Total, ov.Ownership.Unowned)
	}
	if l := entityList(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Ownership: OwnershipUnidentified}); slices.Contains(refKeys(l.Entities), string(NewServiceKey(cartName))) {
		t.Error("an inferred service is unidentified")
	}
	if l := entityList(t, q, EntityFilter{Kinds: []EntityKind{KindService}, Gap: GapNotDeployed}); l.Total != ov.Knowledge.Gaps.NotDeployed {
		t.Errorf("not-deployed list %d != tally %d", l.Total, ov.Knowledge.Gaps.NotDeployed)
	}
	for _, lc := range []string{"", "deprecated", "sunset"} {
		r := serviceEntityRef(q.snap.Services[NewServiceKey(cartName)])
		if lc != "" && q.entityLifecycle(r) == lc {
			t.Errorf("an inferred service has lifecycle %s", lc)
		}
	}
	if q.entityLifecycle(serviceEntityRef(q.snap.Services[NewServiceKey(cartName)])) != "" {
		t.Error("an inferred service has a lifecycle")
	}
}

func TestInferredDetailCallsUnknownWithoutObservedSource(t *testing.T) {
	d, err := goldenWith(t, true).EntityDetail(KindService, string(NewServiceKey(cartName)))
	if err != nil {
		t.Fatal(err)
	}
	if d.Service.CallsObserved || d.Service.Calls.Total != 0 || !slices.ContainsFunc(d.Service.Limitations.Items, func(l AttributedLimitation) bool {
		return l.Limitation.Code == LimitationEvidenceMissing
	}) {
		t.Errorf("calls %+v observed %v limitations %+v", d.Service.Calls, d.Service.CallsObserved, d.Service.Limitations)
	}
	// An available observer that saw no call of cart: none were seen.
	d, err = goldenWith(t, true, kernel(call("orders", "postgresql"))).EntityDetail(KindService, string(NewServiceKey(cartName)))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Service.CallsObserved || d.Service.Calls.Total != 0 || d.Service.Calls.Items == nil {
		t.Errorf("calls %+v observed %v", d.Service.Calls, d.Service.CallsObserved)
	}
}

func TestInferredCallsObservedPerCluster(t *testing.T) {
	eu, us := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	snap := buildClusterSnap(t,
		NewMemorySource("kernel-eu", "observation", &Collection{Cluster: eu, Observed: []ObservedEdge{call("x", "y")}}),
		workloadsSource("wa", eu, inferredCart()), workloadsSource("wb", us, inferredCart()))
	q := NewQuery(snap)
	if !q.callsObserved(&InferredWorkload{Cluster: clusterA}) || q.callsObserved(&InferredWorkload{Cluster: clusterB}) || q.callsObserved(&InferredWorkload{Cluster: "gone"}) {
		t.Error("an observer covers only its own cluster")
	}
}

func TestInferredSkippedByDeclaredLoops(t *testing.T) {
	without, with := goldenWith(t, false), goldenWith(t, true)
	attention := func(q *Query, category string) []AttentionItem {
		l, err := q.Attention(AttentionFilter{Category: category, Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		return l.Items
	}
	entities := func(q *Query, f EntityFilter) any {
		l := entityList(t, q, f)
		l.Aggregate.Matched -= l.Aggregate.InferredServices
		l.Aggregate.InferredServices = 0
		return []any{l.Entities, l.Aggregate}
	}
	detail := func(q *Query, kind EntityKind, key string) any {
		d, err := q.EntityDetail(kind, key)
		if err != nil {
			t.Fatal(err)
		}
		return []any{d.Owner, d.Domain, d.Offering, d.Service}
	}
	t.Run("unowned and knowledge attention", func(t *testing.T) {
		sameJSON(t, "unowned", attention(without, categoryUnowned), attention(with, categoryUnowned))
		sameJSON(t, "knowledge", attention(without, categoryKnowledge), attention(with, categoryKnowledge))
	})
	t.Run("domains", func(t *testing.T) {
		sameJSON(t, "domains", entities(without, EntityFilter{Kinds: []EntityKind{KindDomain}}), entities(with, EntityFilter{Kinds: []EntityKind{KindDomain}}))
		sameJSON(t, "domain detail", detail(without, KindDomain, "commerce"), detail(with, KindDomain, "commerce"))
	})
	t.Run("owners", func(t *testing.T) {
		sameJSON(t, "owners", entities(without, EntityFilter{Kinds: []EntityKind{KindOwner}}), entities(with, EntityFilter{Kinds: []EntityKind{KindOwner}}))
		sameJSON(t, "owner detail", detail(without, KindOwner, "team:orders-team"), detail(with, KindOwner, "team:orders-team"))
		sameJSON(t, "owners in domain", entities(without, EntityFilter{Kinds: []EntityKind{KindOwner}, BusinessDomain: "commerce"}),
			entities(with, EntityFilter{Kinds: []EntityKind{KindOwner}, BusinessDomain: "commerce"}))
	})
	t.Run("offerings", func(t *testing.T) {
		sameJSON(t, "offerings", entities(without, EntityFilter{Kinds: []EntityKind{KindOffering}}), entities(with, EntityFilter{Kinds: []EntityKind{KindOffering}}))
		sameJSON(t, "offering detail", detail(without, KindOffering, "postgresql"), detail(with, KindOffering, "postgresql"))
		if offeringProvenance(with.snap, "") != ProvenanceObserved {
			t.Error("an inferred service declares an offering")
		}
	})
	t.Run("golden paths", func(t *testing.T) {
		f := EntityFilter{Kinds: []EntityKind{KindService}, Template: TemplateOnly}
		sameJSON(t, "golden paths", entities(without, f), entities(with, f))
		path := string(acmeService(with, "http-service").Key)
		sameJSON(t, "golden path detail", detail(without, KindService, path), detail(with, KindService, path))
		sameJSON(t, "created counts", without.createdCounts(), with.createdCounts())
		sameJSON(t, "deployed golden paths", without.deployedTemplateAttention(), with.deployedTemplateAttention())
	})
	t.Run("versions", func(t *testing.T) {
		a, errA := without.OfferingVersions("postgresql", "", "")
		b, errB := with.OfferingVersions("postgresql", "", "")
		if errA != nil || errB != nil {
			t.Fatal(errA, errB)
		}
		sameJSON(t, "versions", a.Providers, b.Providers)
		if _, err := with.OfferingVersions("", "", ""); err == nil {
			t.Error("no offering has providers")
		}
	})
	t.Run("restriction", func(t *testing.T) {
		hidden := map[ServiceKey]bool{acmeService(with, "orders").Key: true}
		origins, created := restrictOrigins(with.snap, hidden)
		if origins[NewServiceKey(cartName)] != nil || len(created) != 0 {
			t.Errorf("origins %v created %v", origins, created)
		}
		if got := targetsHidden(with.snap, with.snap.Services, map[ServiceKey]bool{}); len(got) != 0 {
			t.Errorf("targets hidden %v", got)
		}
	})
}

func TestAggregateExcludesInferred(t *testing.T) {
	f := EntityFilter{Kinds: []EntityKind{KindService}}
	without, with := entityList(t, goldenWith(t, false), f), entityList(t, goldenWith(t, true), f)
	if with.Aggregate.InferredServices != 3 || with.Aggregate.Matched != without.Aggregate.Matched+3 || with.Total != without.Total+3 {
		t.Errorf("aggregate %+v", with.Aggregate)
	}
	with.Aggregate.Matched, with.Aggregate.InferredServices = without.Aggregate.Matched, 0
	sameJSON(t, "aggregate", without.Aggregate, with.Aggregate)
}

func TestEntitiesDeclarationFilter(t *testing.T) {
	q := goldenWith(t, true)
	all := entityList(t, q, EntityFilter{Kinds: []EntityKind{KindService}})
	if !slices.Contains(refKeys(all.Entities), string(NewServiceKey(cartName))) || !slices.Contains(refKeys(all.Entities), string(acmeService(q, "orders").Key)) {
		t.Errorf("default listing %v", refKeys(all.Entities))
	}
	inferred := entityList(t, q, EntityFilter{Declaration: DeclarationInferred})
	want := []string{string(NewServiceKey(sweepName)), string(NewServiceKey(cartName)), string(NewServiceKey(ledgerName))}
	slices.Sort(want)
	got := refKeys(inferred.Entities)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("inferred %v, want %v", got, want)
	}
	if k := ListKinds("", EntityFilter{Declaration: DeclarationInferred}); !slices.Equal(k, []EntityKind{KindService}) {
		t.Errorf("list kinds %v", k)
	}
	declared := entityList(t, q, EntityFilter{Declaration: DeclarationDeclared})
	kinds := map[EntityKind]bool{}
	for _, r := range declared.Entities {
		kinds[r.Kind] = true
		if r.Declaration == DeclarationInferred {
			t.Errorf("declared lists %s", r.Key)
		}
	}
	if !kinds[KindService] || !kinds[KindRevision] || !kinds[KindSource] {
		t.Errorf("declared keeps other kinds: %v", kinds)
	}
}

func TestEntitiesDeclarationFilterRejectsUnknown(t *testing.T) {
	q := goldenWith(t, true)
	var bad *InvalidQueryError
	if _, err := q.Entities(EntityFilter{Declaration: "maybe"}); !errors.As(err, &bad) || bad.Field != "declaration" {
		t.Errorf("err %v", err)
	}
	if _, err := q.Search(SearchFilter{Declaration: "maybe"}); !errors.As(err, &bad) || bad.Field != "declaration" {
		t.Errorf("search err %v", err)
	}
}

func TestEntitiesDeclarationFilterNeedsServices(t *testing.T) {
	var bad *InvalidQueryError
	if _, err := goldenWith(t, true).Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, Declaration: DeclarationInferred}); !errors.As(err, &bad) || bad.Field != "declaration" {
		t.Errorf("err %v", err)
	}
}

func TestEntitiesUnchangedWithoutInferred(t *testing.T) {
	q := goldenWith(t, false)
	for _, f := range []EntityFilter{{}, {Kinds: []EntityKind{KindService}}} {
		a, b := entityList(t, q, f), entityList(t, q, EntityFilter{Kinds: f.Kinds, Declaration: DeclarationDeclared})
		sameJSON(t, "listing", a, b)
		if a.Aggregate.InferredServices != 0 {
			t.Errorf("inferred %d", a.Aggregate.InferredServices)
		}
		for _, r := range a.Entities {
			if r.Kind == KindService && (r.Declaration != DeclarationDeclared || r.Image != "") {
				t.Errorf("service ref %+v", r)
			}
		}
	}
}

func TestSearchDeclarationFilter(t *testing.T) {
	q := goldenWith(t, true)
	res, err := q.Search(SearchFilter{Declaration: DeclarationInferred})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Errorf("inferred hits %d", res.Total)
	}
	for _, h := range res.Services {
		if h.Declaration != DeclarationInferred {
			t.Errorf("hit %+v", h)
		}
	}
	res, err = q.Search(SearchFilter{Declaration: DeclarationDeclared})
	if err != nil {
		t.Fatal(err)
	}
	all, err := q.Search(SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total+3 != all.Total {
		t.Errorf("declared %d of %d", res.Total, all.Total)
	}
	for _, h := range res.Services {
		if h.Declaration != DeclarationDeclared {
			t.Errorf("hit %+v", h)
		}
	}
}

func TestInferredEntityRefShape(t *testing.T) {
	q := goldenWith(t, true)
	l := entityList(t, q, EntityFilter{Declaration: DeclarationInferred, Text: "cart"})
	if l.Total != 1 {
		t.Fatalf("rows %+v", l.Entities)
	}
	r := l.Entities[0]
	if r.Declaration != DeclarationInferred || r.Scope != "shop" || r.Secondary != "Deployment" || r.Image != "ghcr.io/acme/cart:1.4.2" || r.Label != cartName {
		t.Errorf("ref %+v", r)
	}
	if r := q.serviceListRef(q.snap.Services[NewServiceKey(sweepName)], OriginCount{}); r.Image != "" {
		t.Errorf("a workload with no image shows %q", r.Image)
	}
	if r := serviceEntityRef(&ServiceRecord{Key: "x", Name: "x"}); r.Declaration != DeclarationDeclared {
		t.Errorf("a hand-built record reads %q", r.Declaration)
	}
}

func TestInferredServiceDetail(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, "postgresql"), call(cartName, "orders")))
	d, err := q.EntityDetail(KindService, string(NewServiceKey(cartName)))
	if err != nil {
		t.Fatal(err)
	}
	s := d.Service
	checkCartWorkload(t, s.Workload)
	if got := refKeys(s.Calls.Items); !slices.Equal(got, []string{string(acmeService(q, "orders").Key), string(acmeService(q, "postgresql").Key)}) || !s.CallsObserved {
		t.Errorf("calls %v observed %v", got, s.CallsObserved)
	}
	if got := refKeys(s.Sources.Items); !slices.Equal(got, []string{"k8s-workloads"}) {
		t.Errorf("sources %v", got)
	}
	empty := s.Revisions.Total == 0 && s.Revisions.Items != nil && s.Placement.Items != nil && s.Relationships.Items != nil
	if !empty || s.Ownership != nil || d.Status != "" || d.Entity.Declaration != DeclarationInferred {
		t.Errorf("an inferred detail claims something: %+v", d)
	}
	// The detail never aliases the snapshot.
	s.Workload.Ports[0].Port = 1
	if q.snap.Services[NewServiceKey(cartName)].Workload.Ports[0].Port != 8080 {
		t.Error("the detail aliases the snapshot")
	}
}

func TestDeclaredServiceDetailHasNoWorkload(t *testing.T) {
	q := goldenWith(t, true)
	declared, err := q.EntityDetail(KindService, string(acmeService(q, "orders").Key))
	if err != nil {
		t.Fatal(err)
	}
	if declared.Service.Workload != nil || declared.Service.Calls.Items == nil || declared.Service.Calls.Total != 0 || declared.Service.CallsObserved {
		t.Errorf("declared detail %+v %+v", declared.Service.Workload, declared.Service.Calls)
	}
}

func TestInferredDetailListsItsOwnLimitations(t *testing.T) {
	bad := inferredCart()
	bad.Image.Value = "x\x1b"
	scoped := NewMemorySource("k8s", SourceKindKubernetes, &Collection{
		State:       &SourceState{Status: SourcePartial, Namespaces: []string{"web"}, NamespacesTotal: 2},
		Limitations: []Limitation{NamespaceUnreadableLimitation("k8s", "shop", "forbidden")},
	})
	cart2 := rawInferred("shop", "Deployment", "cart2")
	cart2.Image = &InferredValue{Value: "y\x1b", From: "image"}
	q := NewQuery(buildClusterSnap(t, scoped, workloadsSource("k8s-workloads", nil, bad, cart2)))
	d, err := q.EntityDetail(KindService, string(NewServiceKey(cartName)))
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, l := range d.Service.Limitations.Items {
		codes = append(codes, l.Limitation.Code)
	}
	slices.Sort(codes)
	if !slices.Equal(codes, []string{LimitationDeclarationUnverified, LimitationEvidenceMissing, LimitationSourceRecordInvalid}) {
		t.Errorf("codes %v: %+v", codes, d.Service.Limitations.Items)
	}
}

func TestUncontractedCallerAttentionLinksInferred(t *testing.T) {
	with := goldenWith(t, true, kernel(call(cartName, "postgresql"), call("k8s:shop/Deployment/gone", "postgresql")))
	without := goldenWith(t, false, kernel(call(cartName, "postgresql"), call("k8s:shop/Deployment/gone", "postgresql")))
	items := func(q *Query) []AttentionItem {
		var out []AttentionItem
		for _, it := range q.undeclaredAttention() {
			if it.Code == "UNCONTRACTED_CALLER" {
				out = append(out, it)
			}
		}
		return out
	}
	got := items(with)
	pg := string(acmeService(with, "postgresql").Key)
	for _, it := range got {
		if it.Entity.Key != pg || it.Service != pg {
			t.Errorf("item moved off the called service: %+v", it)
		}
	}
	linked := slices.IndexFunc(got, func(it AttentionItem) bool { return it.Caller != nil })
	if linked < 0 || got[linked].Caller.Key != string(NewServiceKey(cartName)) || got[linked].Caller.Declaration != DeclarationInferred {
		t.Fatalf("no caller link: %+v", got)
	}
	for i := range got {
		got[i].Caller = nil
	}
	sameJSON(t, "uncontracted attention", items(without), got)
	if a, b := without.Overview().Summary.ServicesNeedingAttention, with.Overview().Summary.ServicesNeedingAttention; a != b {
		t.Errorf("services needing attention %d != %d", a, b)
	}
	// A caller whose inferred service the view no longer holds gets no link.
	delete(with.snap.Services, NewServiceKey(cartName))
	for _, it := range items(with) {
		if it.Caller != nil {
			t.Errorf("dangling caller link %+v", it)
		}
	}
}

func TestObservedConsumersUnchangedWithInferredCaller(t *testing.T) {
	consumers := func(inferred bool) ObservedConsumersPreview {
		d, err := goldenWith(t, inferred, kernel(call(cartName, "postgresql"))).EntityDetail(KindOffering, "postgresql")
		if err != nil {
			t.Fatal(err)
		}
		return d.Offering.ObservedConsumers
	}
	with := consumers(true)
	sameJSON(t, "observed consumers", consumers(false), with)
	if with.Total != 1 || with.Items[0].Caller != cartName || !with.Items[0].Uncontracted {
		t.Errorf("rows %+v", with.Items)
	}
}

func TestInferredLines(t *testing.T) {
	w := &InferredWorkload{Source: "w", Cluster: "eu", Namespace: "shop", Kind: "Deployment", Name: "cart",
		Image:  &InferredValue{Value: "reg/cart:1", From: "Deployment image"},
		Ports:  []InferredPort{{Port: 8080, Name: "http", Protocol: "TCP", From: "Deployment port"}, {Port: 9090, From: "Deployment metrics"}},
		Probes: []InferredProbe{{Kind: "readiness", Path: "/healthz", Port: "8080", From: "Deployment readiness"}, {Kind: "liveness", From: "Deployment liveness"}}}
	want := []string{
		"Workload: Deployment eu shop/cart",
		"  image reg/cart:1 (from w: Deployment image)",
		"  port 8080 http TCP (from w: Deployment port)",
		"  port 9090 (from w: Deployment metrics)",
		"  readiness probe /healthz port 8080 (from w: Deployment readiness)",
		"  liveness probe (from w: Deployment liveness)",
	}
	if got := w.Lines(); !slices.Equal(got, want) {
		t.Errorf("lines:\n%q\nwant\n%q", got, want)
	}
	asOf := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	d := &ServiceDetailData{Workload: &InferredWorkload{Source: "w", Namespace: "shop", Kind: "CronJob", Name: "sweep"}}
	if got := d.InferredLines(asOf); !slices.Equal(got, []string{"Workload: CronJob shop/sweep", "Calls unknown: Pacto reads no traffic source here"}) {
		t.Errorf("unknown calls: %q", got)
	}
	d.CallsObserved = true
	d.Limitations = attributedLimitationsPreview([]AttributedLimitation{{Limitation: Limitation{Code: LimitationDeclarationUnverified, Message: "m"}}})
	if got := d.InferredLines(asOf); !slices.Equal(got[1:], []string{"No calls to declared services seen as of 2026-11-17T00:00:00Z", "  - [DECLARATION_UNVERIFIED] m"}) {
		t.Errorf("no calls: %q", got)
	}
	d.Calls = RefPreview{Total: 3, Count: 1, Truncated: true, Items: []EntityRef{{Label: "orders"}}}
	if got := d.InferredLines(asOf); !slices.Equal(got[1:4], []string{"Calls (3):", "  orders", "  2 more"}) {
		t.Errorf("calls: %q", got)
	}
}

func TestGraphAndExplainOfInferredServiceSayItIsInferred(t *testing.T) {
	q := goldenWith(t, true, kernel(call(cartName, "postgresql")))
	g, err := q.Graph(GraphQuery{Service: cartName, IncludeObserved: true, Transitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if g.Meta.Completeness != CompletenessPartial || g.Meta.Limitations[0].Code != LimitationServiceInferred || !strings.Contains(g.Meta.Limitations[0].Message, "pacto_fleet_entity") {
		t.Errorf("graph meta %+v", g.Meta)
	}
	ex, err := q.Explain(cartName)
	if err != nil {
		t.Fatal(err)
	}
	if codes := reasonCodes(ex.Reasons); !slices.Equal(codes, []string{LimitationDeclarationUnverified, LimitationServiceInferred}) {
		t.Errorf("explain reasons %v", codes)
	}
	if d, _ := q.Graph(GraphQuery{Service: "orders"}); slices.ContainsFunc(d.Meta.Limitations, func(l Limitation) bool { return l.Code == LimitationServiceInferred }) {
		t.Error("a declared root is marked inferred")
	}
}

func reasonCodes(rs []Reason) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}
