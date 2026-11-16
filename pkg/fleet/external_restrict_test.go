package fleet

import (
	"context"
	"fmt"
	"reflect"
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
	r := q.Restrict(shopView)
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
	if rr := r.Restrict(func(string) bool { return false }); rr.Overview().Summary.HiddenExternals != 2 {
		t.Errorf("hidden after two restrictions = %d", rr.Overview().Summary.HiddenExternals)
	}
}

func TestRestrictExternalCountsHiddenConsumers(t *testing.T) {
	q := extK8sFleet(t,
		extSvc{"web", []string{"shop"}, []string{"stripe"}},
		extSvc{"pay", []string{"secret"}, []string{"stripe"}},
		extSvc{"bill", []string{"secret"}, []string{"stripe"}})
	v, err := q.Restrict(shopView).GetExternal("external:stripe")
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
}

func TestRestrictExternalSourcesTotalCountsVisibleOnly(t *testing.T) {
	q := extK8sFleet(t,
		extSvc{"web", []string{"shop"}, []string{"stripe", "Stripe-API"}},
		extSvc{"pay", []string{"secret"}, []string{"stripe"}})
	// web declares it at two positions of one revision: three declarations.
	full, _ := q.GetExternal("stripe")
	v, _ := q.Restrict(shopView).GetExternal("stripe")
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
	v, err := q.Restrict(shopView).GetExternal("stripe")
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
	ra := a.Restrict(shopView)
	la, _ := ra.Entities(EntityFilter{Kinds: []EntityKind{KindExternal}, Limit: MaxEntityLimit})
	if la.Total != 150 || la.Hidden != 55 || hasLimitation(la.Meta.Limitations, LimitationExternalCap) {
		t.Fatalf("restricted list total %d hidden %d limitations %+v", la.Total, la.Hidden, la.Meta.Limitations)
	}
	b := foldFleet(t, 150, 3)
	lb, _ := b.Restrict(shopView).Entities(EntityFilter{Kinds: []EntityKind{KindExternal}, Limit: MaxEntityLimit})
	if !reflect.DeepEqual(la.Entities, lb.Entities) || la.Total != lb.Total || la.Hidden != lb.Hidden {
		t.Error("the restricted answer depends on hidden consumer counts")
	}
}
