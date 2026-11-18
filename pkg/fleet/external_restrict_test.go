package fleet

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// extSvc is one Kubernetes service of extK8sFleet: its name, the namespaces
// it runs in and the external systems it declares.
type extSvc struct {
	name string
	ns   []string
	deps []string
}

// extK8sFleet builds a Kubernetes fleet of svcs, one revision each.
func extK8sFleet(t *testing.T, svcs ...extSvc) *Query {
	t.Helper()
	col := &Collection{}
	for _, s := range svcs {
		var deps []contract.Dependency
		for _, d := range s.deps {
			deps = append(deps, extDep(d))
		}
		digest := "sha256:" + s.name
		col.Revisions = append(col.Revisions, RawRevision{Bundle: &contract.Bundle{Contract: extContract(s.name, "1.0.0", deps...), FS: fstest.MapFS{}}, Digest: digest})
		for _, ns := range s.ns {
			col.Targets = append(col.Targets, RawTarget{Scope: ns, Kind: SourceKindKubernetes, Name: s.name, Service: s.name, Digest: digest, Compliance: StatusCompliant})
		}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

// shopView is what a caller who may read only shop, and the cluster scope, sees.
func shopView(ns string) bool { return ns == "shop" || ns == "" }

func TestRestrictHidesExternalOnlyHiddenConsumers(t *testing.T) {
	q := extK8sFleet(t,
		extSvc{"web", []string{"shop"}, []string{"stripe"}},
		extSvc{"vault", []string{"secret"}, []string{"legacy-mq"}})
	r := q.Restrict(shopView, nil)
	if _, err := r.GetExternal("legacy-mq"); err == nil {
		t.Fatal("an external only a hidden service declares is not found")
	}
	ov := r.Overview().Summary
	if ov.Externals != 1 || ov.HiddenExternals != 1 {
		t.Errorf("overview externals = %d hidden %d", ov.Externals, ov.HiddenExternals)
	}
	l, err := r.Entities(EntityFilter{Kinds: []EntityKind{KindExternal}})
	if err != nil || l.Total != 1 || l.Hidden != 1 || l.Entities[0].Key != "stripe" {
		t.Fatalf("list = %+v, %v", l, err)
	}
	if q.Overview().Summary.Externals != 2 {
		t.Error("the unrestricted view keeps both")
	}
	// Restricting twice keeps the count.
	if rr := r.Restrict(func(string) bool { return false }, nil); rr.Overview().Summary.HiddenExternals != 2 {
		t.Errorf("hidden after two restrictions = %d", rr.Overview().Summary.HiddenExternals)
	}
}

func TestRestrictExternalCountsHiddenConsumers(t *testing.T) {
	q := extK8sFleet(t,
		extSvc{"web", []string{"shop"}, []string{"stripe"}},
		extSvc{"pay", []string{"secret"}, []string{"stripe"}},
		extSvc{"bill", []string{"secret"}, []string{"stripe"}})
	v, err := q.Restrict(shopView, nil).GetExternal("external:stripe")
	if err != nil {
		t.Fatal(err)
	}
	e := v.External
	if e.ConsumersTotal != 1 || e.HiddenConsumers != 2 || len(e.Consumers) != 1 || e.Consumers[0] != NewServiceKey("web") || len(e.Sources) != 1 {
		t.Fatalf("restricted stripe = %+v", e)
	}
	for _, src := range e.Sources {
		if src.Service != NewServiceKey("web") {
			t.Errorf("a hidden consumer is named: %+v", src)
		}
	}
	l, _ := q.Restrict(shopView, nil).Entities(EntityFilter{Kinds: []EntityKind{KindExternal}})
	if row := l.Entities[0]; row.Consumers != 1 || row.HiddenConsumers != 0 {
		t.Fatalf("restricted list row = %+v", row)
	}
}

func TestRestrictExternalSourcesTotalCountsVisibleOnly(t *testing.T) {
	q := extK8sFleet(t,
		extSvc{"web", []string{"shop"}, []string{"stripe", "Stripe-API"}},
		extSvc{"pay", []string{"secret"}, []string{"stripe"}})
	// web declares it at two positions of one revision: three declarations.
	full, _ := q.GetExternal("stripe")
	v, _ := q.Restrict(shopView, nil).GetExternal("stripe")
	if full.External.SourcesTotal != 3 || v.External.SourcesTotal != 2 {
		t.Fatalf("sources total = %d of %d", v.External.SourcesTotal, full.External.SourcesTotal)
	}
	if len(v.External.Sources) != 2 {
		t.Errorf("web keeps its two positions: %+v", v.External.Sources)
	}
}

func TestRestrictDropsExternalSourceOfHiddenTarget(t *testing.T) {
	q := extK8sFleet(t,
		extSvc{"web", []string{"shop", "secret"}, nil},
		extSvc{"api", []string{"shop"}, []string{"stripe"}})
	// A source observed on web's target in the hidden namespace (517-S2 sets
	// Target on observed sources).
	snap := q.SnapshotForSerialization()
	var hidden TargetKey
	for k, tr := range snap.Targets {
		if tr.Scope == "secret" {
			hidden = k
		}
	}
	rec := snap.Externals["stripe"]
	web := NewServiceKey("web")
	rec.Sources = append(rec.Sources, ExternalSource{Kind: "observed", Service: web, Target: hidden, Provenance: ProvenanceObserved})
	rec.declarations[web] = 1
	finalizeExternal(rec)
	q = NewQuery(snap)
	v, err := q.Restrict(shopView, nil).GetExternal("stripe")
	if err != nil {
		t.Fatal(err)
	}
	if v.External.ConsumersTotal != 1 || v.External.HiddenConsumers != 1 || v.External.SourcesTotal != 1 || len(v.External.Sources) != 1 || v.External.Sources[0].Target != "" {
		t.Fatalf("restricted = %+v", v.External)
	}
}

// foldFleet: 205 externals; ext-NNN is declared by visible services
// v000..vNNN wherever NNN < visible, and by hidden services h000..h<hidden-1>.
func foldFleet(t *testing.T, visible, hidden int) *Query {
	t.Helper()
	var svcs []extSvc
	for c := 0; c < visible; c++ {
		var deps []string
		for i := c; i < visible; i++ {
			deps = append(deps, fmt.Sprintf("ext-%03d", i))
		}
		svcs = append(svcs, extSvc{fmt.Sprintf("v%03d", c), []string{"shop"}, deps})
	}
	var all []string
	for i := 0; i < 205; i++ {
		all = append(all, fmt.Sprintf("ext-%03d", i))
	}
	for c := 0; c < hidden; c++ {
		svcs = append(svcs, extSvc{fmt.Sprintf("h%03d", c), []string{"secret"}, all})
	}
	return extK8sFleet(t, svcs...)
}

func TestRestrictFoldRanksOnVisibleConsumersOnly(t *testing.T) {
	a := foldFleet(t, 150, 1)
	if !hasLimitation(a.Overview().Meta.Limitations, LimitationExternalCap) {
		t.Fatal("the unrestricted view of 205 externals folds")
	}
	ra := a.Restrict(shopView, nil)
	la, _ := ra.Entities(EntityFilter{Kinds: []EntityKind{KindExternal}, Limit: MaxEntityLimit})
	if la.Total != 150 || la.Hidden != 55 || hasLimitation(la.Meta.Limitations, LimitationExternalCap) {
		t.Fatalf("restricted list total %d hidden %d limitations %+v", la.Total, la.Hidden, la.Meta.Limitations)
	}
	b := foldFleet(t, 150, 3)
	lb, _ := b.Restrict(shopView, nil).Entities(EntityFilter{Kinds: []EntityKind{KindExternal}, Limit: MaxEntityLimit})
	if !reflect.DeepEqual(la.Entities, lb.Entities) || la.Total != lb.Total || la.Hidden != lb.Hidden {
		t.Error("the restricted answer depends on hidden consumer counts")
	}
}

func TestRestrictedLabelNeverNamesAHiddenConsumer(t *testing.T) {
	q := extK8sFleet(t,
		extSvc{"a-payroll", []string{"secret"}, []string{"falcon-acquisition-mq.corp-legacy.co.uk"}},
		extSvc{"z-billing", []string{"shop"}, []string{"billing.corp-legacy.co.uk"}})
	if v, _ := q.GetExternal("host/corp-legacy.co.uk"); v.External.Label != "falcon-acquisition-mq.corp-legacy.co.uk" {
		t.Fatalf("unrestricted label = %q, want the first consumer's spelling", v.External.Label)
	}
	r := q.Restrict(shopView, nil)
	v, err := r.GetExternal("host/corp-legacy.co.uk")
	if err != nil || v.External.Label != "billing.corp-legacy.co.uk" {
		t.Fatalf("restricted label = %+v, %v", v, err)
	}
	l, _ := r.Entities(EntityFilter{Kinds: []EntityKind{KindExternal}})
	if l.Entities[0].Label != "billing.corp-legacy.co.uk" {
		t.Errorf("restricted list label = %q", l.Entities[0].Label)
	}
	if s, _ := r.Search(SearchFilter{Text: "falcon"}); s.ExternalsTotal != 0 {
		t.Errorf("a hidden spelling finds the external: %+v", s.Externals)
	}
}

func TestRestrictFoldCountsAHiddenConsumerOnce(t *testing.T) {
	var all []string
	for i := 0; i < MaxExternalNodes+5; i++ {
		all = append(all, fmt.Sprintf("ext-%03d", i))
	}
	q := extK8sFleet(t, extSvc{"v", []string{"shop"}, all}, extSvc{"h", []string{"secret"}, all})
	v, err := q.Restrict(shopView, nil).GetExternal(string(OtherEgress))
	if err != nil {
		t.Fatal(err)
	}
	if v.External.Folded != 6 || v.External.HiddenConsumers != 1 {
		t.Fatalf("other egress folds %d, hidden %d; want 6 and 1", v.External.Folded, v.External.HiddenConsumers)
	}
}

func TestExplainExternalSaysWhatItLeavesOut(t *testing.T) {
	svcs := []extSvc{{"hidden", []string{"secret"}, []string{"stripe"}}}
	for i := 0; i < MaxExternalSources+5; i++ {
		svcs = append(svcs, extSvc{fmt.Sprintf("s%03d", i), []string{"shop"}, []string{"stripe"}})
	}
	res, err := extK8sFleet(t, svcs...).Restrict(shopView, nil).Explain("external:stripe")
	if err != nil {
		t.Fatal(err)
	}
	var truncated []string
	declared := 0
	for _, r := range res.Reasons {
		switch r.Code {
		case ReasonExternalSourcesTruncated:
			truncated = append(truncated, r.Message)
		case ReasonExternalDeclared:
			declared++
		}
	}
	want := "50 of 55 consumers (55 sources) are listed; 5 more are not, and 1 consumers are hidden by your permissions; pacto fleet get counts them"
	if declared != MaxExternalSources || len(truncated) != 1 || truncated[0] != want {
		t.Fatalf("declared %d, truncated %q", declared, truncated)
	}
	if res, _ := extK8sFleet(t, svcs[1:3]...).Explain("external:stripe"); slices.ContainsFunc(res.Reasons, func(r Reason) bool { return r.Code == ReasonExternalSourcesTruncated }) {
		t.Error("a complete list says it is truncated")
	}
}

// TestRestrictHidesRepositoryExternalOfHiddenService: a view that hides
// vault never names its repository evidence and counts it as a hidden
// consumer; evidence from a revision the view hides goes with it, on a
// visible service too.
func TestRestrictHidesRepositoryExternalOfHiddenService(t *testing.T) {
	q := NewQuery(repoFleet(t, repoSvc{"web", "1.0.0", "shop", stripeRecord()}, repoSvc{"vault", "1.0.0", "secret", stripeRecord()}))
	v, err := q.Restrict(shopView, nil).GetExternal("stripe")
	if err != nil {
		t.Fatal(err)
	}
	if v.External.HiddenConsumers != 1 || !reflect.DeepEqual(v.External.Consumers, []ServiceKey{"web"}) || len(v.InferredSources) != 2 {
		t.Fatalf("view = %+v", v)
	}
	for _, s := range v.InferredSources {
		if s.Service != "web" || s.Revision != "web@sha256:web-1.0.0" {
			t.Errorf("source %+v", s)
		}
	}
	// A visible service whose record-bearing revision the view hides: its
	// evidence and its edge go, and it counts as a hidden consumer.
	out := &FleetSnapshot{Services: map[ServiceKey]*ServiceRecord{"web": {}}, Revisions: map[RevisionKey]*ContractRevision{}, Targets: map[TargetKey]*TargetRecord{},
		Relationships: []Relationship{{FromService: "web", ToExternal: "stripe", Provenance: ProvenanceInferred,
			InferredSources: []InferredSource{{Source: "k8s", Kind: "sdk", Revision: "web@old"}}}}}
	restrictInferredEvidence(out)
	if len(out.Relationships) != 0 {
		t.Errorf("relationships = %+v", out.Relationships)
	}
	rec := &ExternalRecord{Key: "stripe", declarations: map[ServiceKey]int{"web": 1, "pay": 1}, Consumers: []ServiceKey{"pay", "web"}, Sources: []ExternalSource{
		{Kind: ExternalSourceRepository, Service: "web", Revision: "web@old", Provenance: ProvenanceInferred},
		{Kind: ExternalSourceContract, Service: "pay", Provenance: ProvenanceDeclared},
	}}
	out.Services["pay"] = &ServiceRecord{}
	if r := restrictExternal(out, rec); r == nil || r.HiddenConsumers != 1 || !reflect.DeepEqual(r.Consumers, []ServiceKey{"pay"}) {
		t.Errorf("restricted = %+v", r)
	}
}
