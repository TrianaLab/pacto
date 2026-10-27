package fleet

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// restrictFleet: web and lone run in shop, api in secret, db in both, reg in no
// namespace. web depends on api and db and configures itself from api; api
// depends on web; web calls api and api calls reg at runtime.
func restrictFleet(t *testing.T) *Query {
	t.Helper()
	mk := func(name string, deps ...string) *contract.Contract {
		var d []contract.Dependency
		for _, dep := range deps {
			d = append(d, contract.Dependency{Name: dep, Ref: "oci://x/" + dep, Required: true})
		}
		return &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}, Dependencies: d}
	}
	web := mk("web", "api", "db")
	web.Configurations = []contract.Configuration{{Name: "tuning", Ref: "oci://x/api"}}
	webLock := &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{
		Kind: contract.ReferenceKindConfig, Name: "tuning", Source: "oci", Ref: "oci://x/api", Digest: "sha256:api",
	}}}
	k := SourceKindKubernetes
	src := NewMemorySource("k8s", "kubernetes", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: web, FS: fstest.MapFS{}}, Digest: "sha256:web", Lock: webLock},
			{Bundle: &contract.Bundle{Contract: mk("api", "web"), FS: fstest.MapFS{}}, Digest: "sha256:api"},
			{Bundle: &contract.Bundle{Contract: mk("db"), FS: fstest.MapFS{}}, Digest: "sha256:db"},
			{Bundle: &contract.Bundle{Contract: mk("reg"), FS: fstest.MapFS{}}, Digest: "sha256:reg"},
			{Bundle: &contract.Bundle{Contract: mk("lone"), FS: fstest.MapFS{}}, Digest: "sha256:lone"},
		},
		Targets: []RawTarget{
			{Scope: "shop", Kind: k, Name: "web", Service: "web", Compliance: StatusCompliant},
			{Scope: "secret", Kind: k, Name: "api", Service: "api", Compliance: StatusCompliant},
			{Scope: "secret", Kind: k, Name: "db", Service: "db", Compliance: StatusNonCompliant},
			{Scope: "shop", Kind: k, Name: "db", Service: "db", Compliance: StatusCompliant},
			{Scope: "shop", Kind: k, Name: "lone", Service: "lone", Compliance: StatusCompliant},
		},
		Observed: []ObservedEdge{
			{From: "web", To: "api", Count: 1},
			{From: "api", To: "reg", Count: 1},
			{From: "k8s:secret/Deployment/x", To: "web", Count: 1},
			{From: "k8s:shop/Deployment/y", To: "web", Count: 1},
		},
		Limitations: []Limitation{{Code: "X", Message: "something about api"}, {Code: "X", Message: "something about web"}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func TestRestrict(t *testing.T) {
	q := restrictFleet(t)
	if got := q.Namespaces(); !slices.Equal(got, []string{"secret", "shop"}) {
		t.Fatalf("Namespaces = %v", got)
	}
	if q.Restrict(func(string) bool { return true }) != q {
		t.Fatal("nothing hidden must return the same query")
	}
	r := q.Restrict(func(ns string) bool { return ns == "shop" })
	s, orig := r.SnapshotForSerialization(), q.SnapshotForSerialization()

	api, web, db, reg := NewServiceKey("api"), NewServiceKey("web"), NewServiceKey("db"), NewServiceKey("reg")
	if s.Services[api] != nil || s.Services[web] == nil || s.Services[db] == nil || s.Services[reg] == nil || len(orig.Services) != 5 {
		t.Fatalf("services = %v", slices.Collect(func(yield func(ServiceKey) bool) {
			for k := range s.Services {
				yield(k)
			}
		}))
	}
	for _, r := range s.Revisions {
		if r.ServiceKey == api {
			t.Error("a hidden service's revision stayed")
		}
	}
	if d := s.Services[db]; len(d.Targets) != 1 || d.Status != StatusCompliant || orig.Services[db].Status != StatusNonCompliant {
		t.Errorf("db = %+v, want its shop target only and a status from it", d)
	}
	if w := s.Services[web]; w.HiddenDependencies != 1 || w.HiddenDependents != 1 {
		t.Errorf("web hidden counts = %d/%d, want 1/1", w.HiddenDependencies, w.HiddenDependents)
	}
	if s.Services[reg].HiddenDependents != 1 || s.Services[reg] == orig.Services[reg] {
		t.Error("reg is called by the hidden api: a copied record with one hidden dependent")
	}
	if lone := NewServiceKey("lone"); s.Services[lone] != orig.Services[lone] {
		t.Error("a service with nothing hidden keeps its record")
	}
	if s.Services[web] == orig.Services[web] || orig.Services[web].HiddenDependencies != 0 {
		t.Error("the original record must stay untouched")
	}

	var hidden []Relationship
	for _, rel := range s.Relationships {
		if rel.FromService == api || rel.ToService == api || rel.ResolvedRevision != "" && strings.Contains(string(rel.ResolvedRevision), "api") {
			t.Errorf("an edge names api: %+v", rel)
		}
		if rel.Hidden {
			hidden = append(hidden, rel)
		}
	}
	if len(hidden) != 2 { // web's dependency on api and its configuration from it
		t.Fatalf("hidden relationships = %+v", hidden)
	}
	for _, h := range hidden {
		if h.FromService != web || h.Reason != hiddenMessage || !h.Resolved || h.missing() {
			t.Errorf("hidden relationship = %+v", h)
		}
	}
	cfg, _ := r.revisionRefResolutions(s.Services[web].Revisions[0])
	if res := cfg["tuning"]; res == nil || res.Resolved || res.Reason != hiddenMessage {
		t.Errorf("tuning = %+v", res)
	}

	nb, err := r.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(web), Depth: 2, Views: []KnowledgeView{ViewExpected, ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nb.Nodes {
		if n.Ref.Key == string(api) {
			t.Error("the neighbourhood reaches api")
		}
		if n.Focus && (n.HiddenDependencies != 1 || n.HiddenDependents != 1) {
			t.Errorf("focus node = %+v", n)
		}
	}
	if d, err := r.EntityDetail(KindService, string(web)); err != nil || d.Service.HiddenDependencies != 1 || d.Service.HiddenDependents != 1 {
		t.Errorf("web detail = %+v, %v", d, err)
	}
	if nb.UnresolvedDependencies.Total != 0 {
		t.Errorf("a hidden dependency is not an unresolved one: %+v", nb.UnresolvedDependencies)
	}
	if g, err := r.Graph(GraphQuery{Service: "web", Transitive: true}); err != nil || slices.ContainsFunc(g.Nodes, func(n GraphNode) bool { return n.Key == api }) {
		t.Errorf("graph = %+v %v", g, err)
	}

	if len(s.UncontractedCallers) != 1 || s.UncontractedCallers[0].Caller != "k8s:shop/Deployment/y" {
		t.Errorf("uncontracted callers = %+v", s.UncontractedCallers)
	}
	if s.Sources[0].TargetCount != 3 || s.Sources[0].RevisionCount != 4 || orig.Sources[0].TargetCount != 5 {
		t.Errorf("source counts = %+v", s.Sources[0])
	}
	var codes []string
	for _, l := range s.Limitations {
		if strings.Contains(l.Message, "api") || strings.Contains(l.Message, "secret") {
			t.Errorf("a limitation names what is hidden: %+v", l)
		}
		codes = append(codes, l.Code)
	}
	if !slices.Contains(codes, LimitationHiddenByPermissions) || !slices.ContainsFunc(s.Limitations, func(l Limitation) bool { return l.Message == "something about web" }) {
		t.Errorf("limitations = %+v", s.Limitations)
	}
	if s.Completeness != CompletenessPartial || s.SnapshotID == orig.SnapshotID || s.SnapshotID != computeSnapshotID(s) {
		t.Errorf("completeness %s, id %s (was %s)", s.Completeness, s.SnapshotID, orig.SnapshotID)
	}
	if res, _ := r.Search(SearchFilter{Text: "api"}); res.Total != 0 {
		t.Errorf("search finds a hidden service: %+v", res)
	}

	// Seeing nothing hides every service with a Kubernetes target and keeps the registry-only one.
	none := q.Restrict(func(string) bool { return false }).SnapshotForSerialization()
	if len(none.Services) != 1 || none.Services[reg] == nil || len(none.Targets) != 0 || len(none.Relationships) != 0 {
		t.Errorf("seeing no namespace leaves %d services, %d targets, %d edges", len(none.Services), len(none.Targets), len(none.Relationships))
	}
}
