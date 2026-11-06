package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
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

var (
	rAPI, rWeb, rDB, rReg = NewServiceKey("api"), NewServiceKey("web"), NewServiceKey("db"), NewServiceKey("reg")
)

// restrictShop is restrictFleet as a caller who may read only shop sees it:
// the restricted query, its snapshot and the original snapshot.
func restrictShop(t *testing.T) (q, r *Query, s, orig *FleetSnapshot) {
	t.Helper()
	q = restrictFleet(t)
	r = q.Restrict(func(ns string) bool { return ns == "shop" || ns == "" })
	return q, r, r.SnapshotForSerialization(), q.SnapshotForSerialization()
}

func TestRestrictNothingHidden(t *testing.T) {
	q := restrictFleet(t)
	if got := q.Namespaces(); !slices.Equal(got, []string{"secret", "shop"}) {
		t.Fatalf("Namespaces = %v", got)
	}
	if q.Restrict(func(string) bool { return true }) != q {
		t.Fatal("nothing hidden must return the same query")
	}
}

func TestRestrictServices(t *testing.T) {
	_, _, s, orig := restrictShop(t)
	api, web, db, reg := rAPI, rWeb, rDB, rReg
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
}

func TestRestrictServiceRecords(t *testing.T) {
	_, _, s, orig := restrictShop(t)
	web, db, reg := rWeb, rDB, rReg
	if d := s.Services[db]; len(d.Targets) != 1 || d.Status != StatusCompliant || orig.Services[db].Status != StatusNonCompliant {
		t.Errorf("db = %+v, want its shop target only and a status from it", d)
	}
	// web's hidden dependents: api and the k8s caller in secret.
	if w := s.Services[web]; w.HiddenDependencies != 1 || w.HiddenDependents != 2 {
		t.Errorf("web hidden counts = %d/%d, want 1/2", w.HiddenDependencies, w.HiddenDependents)
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
}

func TestRestrictRelationships(t *testing.T) {
	_, _, s, _ := restrictShop(t)
	api, web := rAPI, rWeb
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
}

func TestRestrictHiddenConfiguration(t *testing.T) {
	_, r, s, _ := restrictShop(t)
	cfg, _ := r.revisionRefResolutions(s.Services[rWeb].Revisions[0])
	if res := cfg["tuning"]; res == nil || res.Resolved || res.Reason != hiddenMessage {
		t.Errorf("tuning = %+v", res)
	}
}

func TestRestrictQueries(t *testing.T) {
	_, r, _, _ := restrictShop(t)
	api, web := rAPI, rWeb
	nb, err := r.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(web), Depth: 2, Views: []KnowledgeView{ViewExpected, ViewObserved}})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nb.Nodes {
		if n.Ref.Key == string(api) {
			t.Error("the neighbourhood reaches api")
		}
		if n.Focus && (n.HiddenDependencies != 1 || n.HiddenDependents != 2) {
			t.Errorf("focus node = %+v", n)
		}
	}
	if d, err := r.EntityDetail(KindService, string(web)); err != nil || d.Service.HiddenDependencies != 1 || d.Service.HiddenDependents != 2 {
		t.Errorf("web detail = %+v, %v", d, err)
	}
	if nb.UnresolvedDependencies.Total != 0 {
		t.Errorf("a hidden dependency is not an unresolved one: %+v", nb.UnresolvedDependencies)
	}
	if g, err := r.Graph(GraphQuery{Service: "web", Transitive: true}); err != nil || slices.ContainsFunc(g.Nodes, func(n GraphNode) bool { return n.Key == api }) {
		t.Errorf("graph = %+v %v", g, err)
	}
	if res, _ := r.Search(SearchFilter{Text: "api"}); res.Total != 0 {
		t.Errorf("search finds a hidden service: %+v", res)
	}
}

func TestRestrictSnapshotMetadata(t *testing.T) {
	_, _, s, orig := restrictShop(t)
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
}

func TestRestrictSeeingNothing(t *testing.T) {
	q, reg := restrictFleet(t), rReg
	// Seeing no namespace but the cluster scope hides every service with a
	// Kubernetes target and keeps the registry-only one.
	none := q.Restrict(func(ns string) bool { return ns == "" }).SnapshotForSerialization()
	if len(none.Services) != 1 || none.Services[reg] == nil || len(none.Targets) != 0 || len(none.Relationships) != 0 {
		t.Errorf("seeing no namespace leaves %d services, %d targets, %d edges", len(none.Services), len(none.Targets), len(none.Relationships))
	}
	// Seeing nothing at all hides the registry-only service too.
	if all := q.Restrict(func(string) bool { return false }).SnapshotForSerialization(); len(all.Services) != 0 {
		t.Errorf("seeing nothing leaves %d services", len(all.Services))
	}
}

func TestRestrictUnscoped(t *testing.T) {
	q := restrictFleet(t)
	// Every namespace but not the cluster scope: only reg, with no namespace, goes.
	r := q.Restrict(func(ns string) bool { return ns != "" })
	s := r.SnapshotForSerialization()
	if len(s.Services) != 4 || s.Services[rReg] != nil || s.Sources[0].RevisionCount != 4 {
		t.Errorf("services %d, reg %v, revisions %d", len(s.Services), s.Services[rReg], s.Sources[0].RevisionCount)
	}
	// api only CALLS reg at runtime: an observed edge is not a declared dependency.
	if s.Services[rAPI].HiddenDependencies != 0 {
		t.Errorf("api's observed call to the hidden reg counted as declared: %+v", s.Services[rAPI])
	}
	if res, _ := r.Search(SearchFilter{Text: "reg"}); res.Total != 0 {
		t.Errorf("search finds the hidden reg: %+v", res)
	}
}

func TestRestrictHidesUndeclaredOfHiddenServices(t *testing.T) {
	rv := undeclaredFleet(t).Restrict(func(ns string) bool { return ns != "secret" })
	al := attentionOf(t, rv, AttentionFilter{Category: categoryUndeclared})
	if al.Total != 2 {
		t.Errorf("restricted undeclared total = %d, want 2 (d calls b, pay calls b)", al.Total)
	}
	var labels []string
	for _, it := range al.Items {
		labels = append(labels, it.Label)
		if strings.Contains(it.Label+it.Reason+it.NextStep+it.Entity.Key, "secret") {
			t.Errorf("restricted item names a hidden namespace: %+v", it)
		}
	}
	slices.Sort(labels)
	if !slices.Equal(labels, []string{"d calls b", "pay calls b"}) {
		t.Errorf("restricted undeclared labels = %v", labels)
	}
}

// TestRestrictRebuildsTargetOwner: a Kubernetes-only service takes its owner
// from its targets, so the owner only a hidden target reported, and the
// conflict it caused, never show.
func TestRestrictRebuildsTargetOwner(t *testing.T) {
	k := SourceKindKubernetes
	src := NewMemorySource("k8s", "kubernetes", &Collection{Targets: []RawTarget{
		{Scope: "shop", Kind: k, Name: "x-shop", Service: "x", Compliance: StatusCompliant, Owner: contract.Owner{Team: "visible-team"}},
		{Scope: "aaa-secret", Kind: k, Name: "x-sec", Service: "x", Compliance: StatusCompliant, Owner: contract.Owner{Team: "hidden-team"}},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
	if err != nil {
		t.Fatal(err)
	}
	rv := NewQuery(snap).Restrict(func(ns string) bool { return ns == "shop" })
	s := rv.SnapshotForSerialization()
	if got := s.Services[NewServiceKey("x")].Owner.Team; got != "visible-team" {
		t.Errorf("restricted owner = %q, want visible-team", got)
	}
	for _, l := range s.Limitations {
		if l.Code == LimitationOwnerConflict {
			t.Errorf("restricted snapshot keeps %v", l)
		}
	}
}

// TestRestrictHidesCallerNamedLikeHiddenService: an uncontracted caller whose
// name a hidden service shares may be that service, and "matches more than one
// service" would count it, so it goes, whether one or every match is hidden.
func TestRestrictHidesCallerNamedLikeHiddenService(t *testing.T) {
	for name, payNS := range map[string][2]string{"one hidden": {"shop", "secret"}, "both hidden": {"secret", "secret"}} {
		t.Run(name, func(t *testing.T) {
			mk := func(name, domain string) RawRevision {
				c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
				return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Domain: domain, Digest: "sha256:" + domain + name}
			}
			k := SourceKindKubernetes
			src := NewMemorySource("k8s", "kubernetes", &Collection{
				Revisions: []RawRevision{mk("b", ""), mk("pay", "eu"), mk("pay", "us")},
				Targets: []RawTarget{
					{Scope: "shop", Kind: k, Name: "b", Service: "b", Compliance: StatusCompliant},
					{Scope: payNS[0], Kind: k, Name: "pay-eu", Service: "pay", Domain: "eu", Compliance: StatusCompliant},
					{Scope: payNS[1], Kind: k, Name: "pay-us", Service: "pay", Domain: "us", Compliance: StatusCompliant},
				},
				Observed: []ObservedEdge{{From: "pay", To: "b", Count: 1}},
			})
			snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, src)
			if err != nil {
				t.Fatal(err)
			}
			if len(snap.UncontractedCallers) != 1 || !snap.UncontractedCallers[0].Ambiguous {
				t.Fatalf("fixture: want one ambiguous caller, got %+v", snap.UncontractedCallers)
			}
			rv := NewQuery(snap).Restrict(func(ns string) bool { return ns == "shop" })
			if al := attentionOf(t, rv, AttentionFilter{Category: categoryUndeclared}); al.Total != 0 {
				t.Errorf("restricted undeclared = %+v, want none", al.Items)
			}
		})
	}
}

// TestRestrictObservedGraph: a hidden service reached only by an observed call
// is neither a node nor an edge of a restricted graph.
func TestRestrictObservedGraph(t *testing.T) {
	q, r, _, _ := restrictShop(t)
	if res, _ := q.Graph(GraphQuery{Service: "reg", Direction: DirectionDependents, IncludeObserved: true}); len(res.Nodes) != 1 || res.Nodes[0].Key != rAPI {
		t.Fatalf("fixture: api's observed call reaches reg unrestricted, got %+v", res.Nodes)
	}
	if res, _ := r.Graph(GraphQuery{Service: "reg", Direction: DirectionDependents, IncludeObserved: true}); len(res.Nodes) != 0 || len(res.Edges) != 0 {
		t.Errorf("restricted: hidden observed caller of reg shows: %+v %+v", res.Nodes, res.Edges)
	}
	for _, dir := range []Direction{DirectionDependencies, DirectionDependents} {
		res, err := r.Graph(GraphQuery{Service: "web", Direction: dir, Transitive: true, IncludeObserved: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range res.Nodes {
			if n.Key == rAPI || slices.Contains(n.Path, rAPI) {
				t.Errorf("%s: hidden api is a node", dir)
			}
		}
		for _, e := range res.Edges {
			if e.FromService == rAPI || e.ToService == rAPI {
				t.Errorf("%s: edge names hidden api: %+v", dir, e)
			}
		}
	}
}

// restrictedVisible is a namespaced reader who may read every namespace but
// "hidden", and not the cluster scope, like internal/authn's viewer.
func restrictedVisible(ns string) bool { return ns != "" && ns != "hidden" }

// bareCallerFleet: payments runs in shop and ledger in hidden, each with an
// owner conflict. Mira sees legacy-batch, ledger and k8s callers of payments
// and two names it cannot resolve; a trace source sees cron-x call payments and
// two names it cannot resolve.
func bareCallerFleet(t *testing.T, extra ...ObservedEdge) *Query {
	t.Helper()
	k := SourceKindKubernetes
	kube := NewMemorySource("k8s", k, &Collection{Targets: []RawTarget{
		{Scope: "shop", Kind: k, Name: "payments-a", Service: "payments", Compliance: StatusCompliant, Owner: contract.Owner{Team: "pay-a"}},
		{Scope: "shop", Kind: k, Name: "payments-b", Service: "payments", Compliance: StatusCompliant, Owner: contract.Owner{Team: "pay-b"}},
		{Scope: "hidden", Kind: k, Name: "ledger-a", Service: "ledger", Compliance: StatusCompliant, Owner: contract.Owner{Team: "ledger-a"}},
		{Scope: "hidden", Kind: k, Name: "ledger-b", Service: "ledger", Compliance: StatusCompliant, Owner: contract.Owner{Team: "ledger-b"}},
	}})
	mira := NewMemorySource("mira", SourceKindMira, &Collection{ObservedUndeclaredOnly: true, Observed: append([]ObservedEdge{
		{From: "legacy-batch", To: "payments", Count: 3},
		{From: "ghost-a", To: "ghost-b", Count: 1},
		{From: "ledger", To: "payments", Count: 2},
	}, extra...)})
	traces := NewMemorySource("traces", "observation", &Collection{Observed: []ObservedEdge{
		{From: "cron-x", To: "payments", Count: 1},
		{From: "ghost-c", To: "ghost-d", Count: 1},
	}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, kube, mira, traces)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func callerNames(s *FleetSnapshot) []string {
	var names []string
	for _, c := range s.UncontractedCallers {
		names = append(names, c.Caller)
	}
	return names
}

func TestRestrictHidesCallersWithoutNamespace(t *testing.T) {
	q := bareCallerFleet(t)
	orig := q.SnapshotForSerialization()
	if names := callerNames(orig); !slices.Contains(names, "legacy-batch") || !slices.Contains(names, "cron-x") {
		t.Fatalf("fixture: callers = %v", names)
	}
	s := q.Restrict(restrictedVisible).SnapshotForSerialization()
	if names := callerNames(s); len(names) != 0 {
		t.Errorf("restricted callers = %v, want none", names)
	}
	if pay := s.Services[NewServiceKey("payments")]; pay == nil || pay.HiddenDependents != 3 {
		t.Errorf("payments = %+v, want legacy-batch, cron-x and ledger counted", pay)
	}
	checkBareLimitations(t, s.Limitations)
}

// checkBareLimitations: limitations name nothing hidden, each source's
// unresolved names are one count and only visible conflicts stay.
func checkBareLimitations(t *testing.T, limitations []Limitation) {
	t.Helper()
	unresolved := map[string]int{}
	var codes []string
	for _, l := range limitations {
		codes = append(codes, l.Code)
		for _, name := range []string{"ghost", "ledger", "legacy-batch", "cron-x"} {
			if strings.Contains(l.Message, name) {
				t.Errorf("a limitation names %s: %+v", name, l)
			}
		}
		if l.Code == LimitationObservedIdentityUnresolved {
			unresolved[l.Source]++
			if !strings.HasPrefix(l.Message, "2 ") {
				t.Errorf("aggregate message = %q, want a count of 2", l.Message)
			}
		}
		if l.Code == LimitationOwnerConflict && !strings.Contains(l.Message, "payments") {
			t.Errorf("a conflict about a hidden service stayed: %+v", l)
		}
	}
	if len(unresolved) != 2 || unresolved["mira"] != 1 || unresolved["traces"] != 1 {
		t.Errorf("aggregate unresolved limitations per source = %v, want one each for mira and traces", unresolved)
	}
	if !slices.Contains(codes, LimitationHiddenByPermissions) || !slices.Contains(codes, LimitationOwnerConflict) {
		t.Errorf("limitation codes = %v", codes)
	}
}

func TestRestrictKeepsNamespacedCallers(t *testing.T) {
	q := bareCallerFleet(t, ObservedEdge{From: "k8s:shop/Deployment/x", To: "payments", Count: 1}, ObservedEdge{From: "k8s:hidden/Deployment/y", To: "payments", Count: 1})
	s := q.Restrict(restrictedVisible).SnapshotForSerialization()
	if names := callerNames(s); !slices.Equal(names, []string{"k8s:shop/Deployment/x"}) {
		t.Errorf("restricted callers = %v, want only the shop one", names)
	}
	if pay := s.Services[NewServiceKey("payments")]; pay.HiddenDependents != 4 {
		t.Errorf("payments hidden dependents = %d, want the hidden k8s caller counted with the three others", pay.HiddenDependents)
	}
}

// TestRestrictHidesCallersInNamespacesWithoutTargets: a k8s caller in a
// namespace no target runs in is still a namespace the viewer's permissions
// decide, so the viewer is asked about it and, refused, sees only a count.
func TestRestrictHidesCallersInNamespacesWithoutTargets(t *testing.T) {
	q := bareCallerFleet(t, ObservedEdge{From: "k8s:fraud/Deployment/scanner", To: "payments", Count: 1})
	if got := q.Namespaces(); !slices.Equal(got, []string{"fraud", "hidden", "shop"}) {
		t.Fatalf("Namespaces = %v, want the caller's namespace too", got)
	}
	var asked []string
	r := q.Restrict(func(ns string) bool { asked = append(asked, ns); return ns != "fraud" })
	if !slices.Contains(asked, "fraud") {
		t.Errorf("Restrict asked %v, never about the caller's namespace", asked)
	}
	s := r.SnapshotForSerialization()
	if names := callerNames(s); slices.Contains(names, "k8s:fraud/Deployment/scanner") {
		t.Errorf("restricted callers = %v, want the fraud caller hidden", names)
	}
	if pay := s.Services[NewServiceKey("payments")]; pay.HiddenDependents != 1 {
		t.Errorf("payments hidden dependents = %d, want the fraud caller counted", pay.HiddenDependents)
	}
	for _, l := range s.Limitations {
		if strings.Contains(l.Message, "fraud") || strings.Contains(l.Message, "scanner") {
			t.Errorf("a limitation names the hidden caller: %+v", l)
		}
	}
}

func TestRestrictHidesCallersWhenNothingElseIsHidden(t *testing.T) {
	k := SourceKindKubernetes
	build := func(observed ...ObservedEdge) *Query {
		kube := NewMemorySource("k8s", k, &Collection{Targets: []RawTarget{{Scope: "shop", Kind: k, Name: "payments", Service: "payments", Compliance: StatusCompliant}}})
		mira := NewMemorySource("mira", SourceKindMira, &Collection{ObservedUndeclaredOnly: true, Observed: observed})
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, kube, mira)
		if err != nil {
			t.Fatal(err)
		}
		return NewQuery(snap)
	}
	q := build(ObservedEdge{From: "legacy-batch", To: "payments", Count: 1})
	r := q.Restrict(restrictedVisible)
	if r == q {
		t.Fatal("a restricted view with a bare caller must not return the full query")
	}
	s := r.SnapshotForSerialization()
	if len(s.UncontractedCallers) != 0 || s.Services[NewServiceKey("payments")].HiddenDependents != 1 {
		t.Errorf("callers %v, payments %+v", callerNames(s), s.Services[NewServiceKey("payments")])
	}
	if unresolvedOnly := build(ObservedEdge{From: "ghost-a", To: "ghost-b", Count: 1}); unresolvedOnly.Restrict(restrictedVisible) == unresolvedOnly {
		t.Error("a restricted view with an unresolved name must not return the full query")
	}
	if clean := build(); clean.Restrict(restrictedVisible) != clean {
		t.Error("a restricted view with nothing to hide must return the full query")
	}
}

func TestRestrictKeepsUnrelatedLimitations(t *testing.T) {
	k := SourceKindKubernetes
	kube := NewMemorySource("k8s", k, &Collection{Targets: []RawTarget{{Scope: "shop", Kind: k, Name: "payments", Service: "payments", Compliance: StatusCompliant}}})
	mira := NewMemorySource("mira", SourceKindMira, &Collection{ObservedUndeclaredOnly: true, Observed: []ObservedEdge{{From: "source", To: "payments", Count: 1}}})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, kube, mira, NewFailingSource("oci", "oci", context.DeadlineExceeded))
	if err != nil {
		t.Fatal(err)
	}
	s := NewQuery(snap).Restrict(restrictedVisible).SnapshotForSerialization()
	if !slices.ContainsFunc(s.Limitations, func(l Limitation) bool { return l.Code == LimitationSourceUnavailable }) {
		t.Errorf("an unrelated limitation went: %+v", s.Limitations)
	}
}

func TestRestrictUnrestrictedKeepsCallers(t *testing.T) {
	q := bareCallerFleet(t)
	if q.Restrict(func(string) bool { return true }) != q {
		t.Error("an unrestricted view must return the full query with every caller")
	}
}

func TestRestrictCallersSnapshotID(t *testing.T) {
	q := bareCallerFleet(t)
	a, b := q.Restrict(restrictedVisible).SnapshotForSerialization(), q.Restrict(restrictedVisible).SnapshotForSerialization()
	if a.SnapshotID == q.SnapshotForSerialization().SnapshotID || a.SnapshotID != b.SnapshotID {
		t.Errorf("restricted ids %s/%s, full %s", a.SnapshotID, b.SnapshotID, q.SnapshotForSerialization().SnapshotID)
	}
}

// A hidden consumer that only references a visible service's configuration is
// one of its hidden dependents, and a visible consumer of a hidden provider
// counts it as a hidden dependency.
func TestRestrictCountsHiddenConfigRefConsumer(t *testing.T) {
	mk := func(name, ref, dgst string) (*contract.Contract, *lock.Lock) {
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0"}}
		if ref == "" {
			return c, nil
		}
		c.Configurations = []contract.Configuration{{Name: "db", Ref: ref}}
		return c, &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{Kind: contract.ReferenceKindConfig, Name: "db", Source: "oci", Ref: ref, Digest: dgst}}}
	}
	hiddenC, hiddenL := mk("hidden-consumer", "oci://x/shared", "sha256:shared")
	visibleC, visibleL := mk("visible-consumer", "oci://x/secret-db", "sha256:secret-db")
	shared, _ := mk("shared", "", "")
	secretDB, _ := mk("secret-db", "", "")
	k := SourceKindKubernetes
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", "kubernetes", &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: hiddenC, FS: fstest.MapFS{}}, Digest: "sha256:hc", Lock: hiddenL},
			{Bundle: &contract.Bundle{Contract: visibleC, FS: fstest.MapFS{}}, Digest: "sha256:vc", Lock: visibleL},
			{Bundle: &contract.Bundle{Contract: shared, FS: fstest.MapFS{}}, Digest: "sha256:shared"},
			{Bundle: &contract.Bundle{Contract: secretDB, FS: fstest.MapFS{}}, Digest: "sha256:secret-db"},
		},
		Targets: []RawTarget{
			{Scope: "secret", Kind: k, Name: "hc", Service: "hidden-consumer", Compliance: StatusCompliant},
			{Scope: "secret", Kind: k, Name: "db", Service: "secret-db", Compliance: StatusCompliant},
			{Scope: "shop", Kind: k, Name: "vc", Service: "visible-consumer", Compliance: StatusCompliant},
			{Scope: "shop", Kind: k, Name: "shared", Service: "shared", Compliance: StatusCompliant},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	s := NewQuery(snap).Restrict(func(ns string) bool { return ns == "shop" }).SnapshotForSerialization()
	if got := s.Services[NewServiceKey("shared")].HiddenDependents; got != 1 {
		t.Errorf("shared HiddenDependents = %d, want 1", got)
	}
	if got := s.Services[NewServiceKey("visible-consumer")].HiddenDependencies; got != 1 {
		t.Errorf("visible-consumer HiddenDependencies = %d, want 1", got)
	}
}

// offeringRestrictFleet: pgprov declares the relational-db offering and runs only
// where an adapter reports it (no Kubernetes target); vaultdb runs in hush and
// is configured by ref from orders, which runs in shop. hiddenapp (hush) and
// orders depend on pgprov; loner has no offering and no target; reported has
// an offering only its target reports; mixed reports two offerings from two
// namespaces. extra adds Kubernetes sources.
func offeringRestrictFleet(t *testing.T, extra ...Source) *Query {
	t.Helper()
	k := SourceKindKubernetes
	raw := func(y string) RawRevision {
		c := mustParse(t, y)
		return RawRevision{Bundle: &contract.Bundle{Contract: c, RawYAML: []byte(y), FS: fstest.MapFS{"db.json": {Data: []byte(`{"type":"object","properties":{"size":{"enum":["zebrasize","large"]}}}`)}}}, Digest: refDigest(c.Service.Name)}
	}
	dep := "dependencies:\n  - {name: pgprov, ref: \"oci://r/pgprov\", required: true, compatibility: \"^1.0.0\"}\n"
	src := NewMemorySource("k8s", k, &Collection{
		Revisions: []RawRevision{
			raw("pactoVersion: \"2.6\"\nservice: {name: pgprov, version: 1.0.0, offering: relational-db, owner: {team: platform}}\n"),
			raw("pactoVersion: \"2.6\"\nservice: {name: vaultdb, version: 9.9.9, lifecycle: {deprecated: \"2026-01-01\", successor: \"oci://r/pgsuccessor\"}}\nconfigurations:\n  - {name: db, required: false, schema: db.json}\n"),
			raw("pactoVersion: \"2.6\"\nservice: {name: orders, version: 1.0.0}\nconfigurations:\n  - {name: db, required: false, ref: \"oci://r/db-bundle@" + refDigest("vaultdb") + "\", values: {size: huge}}\n" + dep),
			raw("pactoVersion: \"2.6\"\nservice: {name: hiddenapp, version: 1.0.0}\n" + dep),
			raw("pactoVersion: \"2.6\"\nservice: {name: loner, version: 1.0.0}\n"),
			raw("pactoVersion: \"2.6\"\nservice: {name: reported, version: 1.0.0}\n"),
		},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "evidence-http", Name: "adapter-pg-prod-xyz", Service: "pgprov"},
			{Scope: "hush", Kind: k, Name: "vaultdb", Service: "vaultdb"},
			{Scope: "shop", Kind: k, Name: "orders", Service: "orders"},
			{Scope: "hush", Kind: k, Name: "hiddenapp", Service: "hiddenapp"},
			{Scope: "prod", Kind: "evidence-http", Name: "reported", Service: "reported", Offering: "relational-db"},
			{Scope: "shop", Kind: k, Name: "mixed-a", Service: "mixed", Offering: "cache"},
			{Scope: "hush", Kind: k, Name: "mixed-b", Service: "mixed", Offering: "kv"},
		},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, append([]Source{src}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func shopOnly(ns string) bool { return ns == "shop" }

func TestRestrictOfferingProvider(t *testing.T) {
	q := offeringRestrictFleet(t)
	if !strings.Contains(findingMessages(revisionOf(t, q.snap, "orders")), "zebrasize") {
		t.Fatalf("the unrestricted orders findings name no provider enum:\n%s", findingMessages(revisionOf(t, q.snap, "orders")))
	}
	r := q.Restrict(shopOnly)
	checkOfferingOnlyProvider(t, q, r)
	o := offeringData(t, r, "relational-db")
	if o.HiddenConsumers != 1 || o.Consumers.Total != 1 || o.Consumers.Items[0].Consumer.Label != "orders" || o.Providers.Items[0].OfferingProvenance != ProvenanceDeclared {
		t.Errorf("offering = %+v", o)
	}
	orders := revisionOf(t, r.snap, "orders")
	if stageOf(orders, "configuration-refs").Status != "skip" || !strings.Contains(findingMessages(orders), "CONFIG_REF_UNVERIFIED") || orders == revisionOf(t, q.snap, "orders") {
		t.Errorf("orders stages = %+v findings:\n%s", orders.Stages, findingMessages(orders))
	}
	if !strings.Contains(findingMessages(revisionOf(t, q.snap, "orders")), "zebrasize") {
		t.Error("restricting changed the unrestricted revision")
	}
	if st := r.snap.Services[NewServiceKey("orders")].Status; st == StatusInvalid || q.snap.Services[NewServiceKey("orders")].Status != StatusInvalid {
		t.Errorf("restricted orders status = %s, unrestricted = %s", st, q.snap.Services[NewServiceKey("orders")].Status)
	}
	assertNoLeak(t, r, orders.Key, "hush", "hiddenapp", "adapter-pg-prod-xyz", "vaultdb", "9.9.9", "zebrasize", "pgsuccessor")
}

// A configuration ref that reaches a hidden provider through a visible
// provider's own ref is revalidated too, and its service status follows.
func TestRestrictChainedConfigRef(t *testing.T) {
	k := SourceKindKubernetes
	raw := func(y string) RawRevision {
		c := mustParse(t, y)
		return RawRevision{Bundle: &contract.Bundle{Contract: c, RawYAML: []byte(y), FS: fstest.MapFS{"db.json": {Data: []byte(`{"type":"object","properties":{"size":{"enum":["zebrasize","large"]}}}`)}}}, Digest: refDigest(c.Service.Name)}
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("k8s", k, &Collection{
		Revisions: []RawRevision{
			raw("pactoVersion: \"2.6\"\nservice: {name: hprov, version: 1.0.0}\nconfigurations:\n  - {name: db, required: false, schema: db.json}\n"),
			raw("pactoVersion: \"2.6\"\nservice: {name: pmid, version: 1.0.0}\nconfigurations:\n  - {name: db, required: false, ref: \"oci://r/h@" + refDigest("hprov") + "\"}\n"),
			raw("pactoVersion: \"2.6\"\nservice: {name: cons, version: 1.0.0}\nconfigurations:\n  - {name: db, required: false, ref: \"oci://r/m@" + refDigest("pmid") + "\", values: {size: huge}}\n"),
		},
		Targets: []RawTarget{
			{Scope: "hush", Kind: k, Name: "hprov", Service: "hprov"},
			{Scope: "shop", Kind: k, Name: "pmid", Service: "pmid"},
			{Scope: "shop", Kind: k, Name: "cons", Service: "cons"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQuery(snap)
	if !strings.Contains(findingMessages(revisionOf(t, q.snap, "cons")), "zebrasize") {
		t.Fatalf("the unrestricted cons findings name no provider enum:\n%s", findingMessages(revisionOf(t, q.snap, "cons")))
	}
	r := q.Restrict(shopOnly)
	cons := revisionOf(t, r.snap, "cons")
	if strings.Contains(findingMessages(cons), "zebrasize") || !cons.Valid || r.snap.Services[NewServiceKey("cons")].Status == StatusInvalid {
		t.Errorf("cons valid = %v status = %s findings:\n%s", cons.Valid, r.snap.Services[NewServiceKey("cons")].Status, findingMessages(cons))
	}
}

// checkOfferingOnlyProvider: in r, pgprov shows with its revision, owner and
// offering and no target; every other unscoped service is hidden.
func checkOfferingOnlyProvider(t *testing.T, q, r *Query) {
	t.Helper()
	s := r.snap
	pg := s.Services[NewServiceKey("pgprov")]
	if pg == nil || len(pg.Revisions) != 1 || pg.Owner.Team != "platform" || pg.OfferingProvenance != ProvenanceDeclared || len(pg.Targets) != 0 || pg.HiddenDependents != 1 {
		t.Fatalf("pgprov = %+v", pg)
	}
	if !s.targetsHidden[pg.Key] || len(s.Targets) != 2 {
		t.Errorf("targetsHidden = %v, targets = %v", s.targetsHidden, slices.Collect(maps.Keys(s.Targets)))
	}
	for _, name := range []string{"loner", "reported", "vaultdb", "hiddenapp"} {
		if s.Services[NewServiceKey(name)] != nil {
			t.Errorf("%s is visible", name)
		}
	}
	if hasLimitation(s.Limitations, LimitationOfferingConflict) || !hasLimitation(q.snap.Limitations, LimitationOfferingConflict) {
		t.Error("OFFERING_CONFLICT was not rebuilt from the view")
	}
	if s.SnapshotID == q.snap.SnapshotID {
		t.Error("snapshot id not recomputed")
	}
}

// assertNoLeak marshals the restricted snapshot and the offering, provider and
// revision details and fails on any of words.
func assertNoLeak(t *testing.T, r *Query, rev RevisionKey, words ...string) {
	t.Helper()
	bodies := []any{r.SnapshotForSerialization()}
	for _, d := range []struct {
		kind EntityKind
		key  string
	}{{KindOffering, "relational-db"}, {KindService, string(NewServiceKey("pgprov"))}, {KindService, string(NewServiceKey("orders"))}, {KindRevision, string(rev)}} {
		got, err := r.EntityDetail(d.kind, d.key)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, got)
	}
	for _, b := range bodies {
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range words {
			if strings.Contains(string(data), w) {
				t.Errorf("restricted answer contains %q", w)
			}
		}
	}
}

// With a Kubernetes source down or partial, the absence of a target proves
// nothing, so a declared provider stays hidden and counted.
func TestRestrictOfferingProviderPlacementUnknown(t *testing.T) {
	for name, src := range map[string]Source{
		"unavailable": NewFailingSource("k8s-2", SourceKindKubernetes, errors.New("down")),
		"partial":     NewMemorySource("k8s-2", SourceKindKubernetes, &Collection{Limitations: []Limitation{{Code: "X", Message: "x"}}}),
	} {
		t.Run(name, func(t *testing.T) {
			s := offeringRestrictFleet(t, src).Restrict(shopOnly).snap
			if s.Services[NewServiceKey("pgprov")] != nil || s.Services[NewServiceKey("orders")].HiddenDependencies != 2 {
				t.Errorf("pgprov visible or orders = %+v", s.Services[NewServiceKey("orders")])
			}
		})
	}
}
