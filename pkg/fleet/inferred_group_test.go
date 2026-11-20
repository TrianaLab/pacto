package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// argoCart is cart grouped by the Argo CD Application cart in the Argo CD
// namespace, with the repository the Application names.
func argoCart() RawInferredService {
	r := inferredCart()
	r.Group = &InferredGroup{Rule: "argocd", Name: "cart", From: "label argocd.argoproj.io/instance"}
	r.Repositories = []InferredRepository{{URL: "https://github.com/acme/shop", Path: "deploy/cart", From: "Application argocd/cart spec.source.repoURL", Namespace: "argocd", Kind: "Application"}}
	return r
}

// ambiguous is a workload two groups of one rule claim.
func ambiguous(ns, name string) RawInferredService {
	r := rawInferred(ns, "Deployment", name)
	r.Group = &InferredGroup{Rule: groupRuleWorkload, Ambiguous: "label argocd.argoproj.io/instance names cart and annotation argocd.argoproj.io/tracking-id names checkout"}
	return r
}

func workloadOf(t *testing.T, snap *FleetSnapshot, name string) *InferredWorkload {
	t.Helper()
	s := snap.Services[NewServiceKey(name)]
	if s == nil || s.Workload == nil {
		t.Fatalf("no inferred %s in %v", name, snap.Services)
	}
	return s.Workload
}

func TestInferredGroupCarried(t *testing.T) {
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, argoCart()))
	g := workloadOf(t, snap, cartName).Group
	want := InferredGroup{Rule: "argocd", Name: "cart", From: "label argocd.argoproj.io/instance", Key: "argocd//cart", Label: "Argo CD Application cart"}
	if g == nil || *g != want {
		t.Errorf("group %+v, want %+v", g, want)
	}
	if w := workloadOf(t, snap, cartName); len(w.Repositories) != 1 || w.Repositories[0].URL != "https://github.com/acme/shop" || len(w.Dropped) != 0 {
		t.Errorf("workload %+v", w)
	}
}

func TestInferredGroupKeyClusterQualifiedOnlyMulti(t *testing.T) {
	in := argoCart()
	in.Group.Namespace = "shop"
	one := buildClusterSnap(t, workloadsSource("k8s-workloads", clusterOf(clusterA, "eu", ClusterNameFromContext), in))
	if g := workloadOf(t, one, cartName).Group; g.Key != "argocd/shop/cart" || g.Label != "Argo CD Application shop/cart" {
		t.Errorf("single cluster group %+v", g)
	}
	multi := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
		workloadsSource("k8s-workloads", clusterOf(clusterB, "us", ClusterNameFromContext), in))
	if g := workloadOf(t, multi, K8sCaller(clusterB, "shop", "Deployment", "cart")).Group; g.Key != clusterB+"/argocd/shop/cart" {
		t.Errorf("multi cluster key %q", g.Key)
	}
}

func TestInferredGroupDefaultsToWorkload(t *testing.T) {
	long := strings.Repeat("a", 253)
	amb := ambiguous("shop", "pay")
	amb.Group.Ambiguous = strings.Repeat("x", 500)
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, inferredCart(), rawInferred("shop", "Deployment", long), amb))
	g := workloadOf(t, snap, cartName).Group
	want := InferredGroup{Rule: groupRuleWorkload, Namespace: "shop", Name: "Deployment/cart", From: "no grouping label or annotation", Key: "workload/shop/Deployment/cart", Label: "This workload only"}
	if g == nil || *g != want {
		t.Errorf("group %+v, want %+v", g, want)
	}
	lw := workloadOf(t, snap, "k8s:shop/Deployment/"+long)
	if lw.Group.Name != "Deployment/"+long || len(lw.Dropped) != 0 {
		t.Errorf("long name group %+v dropped %v", lw.Group, lw.Dropped)
	}
	aw := workloadOf(t, snap, "k8s:shop/Deployment/pay")
	if aw.Group.Ambiguous != amb.Group.Ambiguous || aw.Group.Label != "Ungrouped" || aw.Group.Key != "workload/shop/Deployment/pay" || len(aw.Dropped) != 0 {
		t.Errorf("ambiguous group %+v dropped %v", aw.Group, aw.Dropped)
	}
}

func TestInferredGroupInvalidDropped(t *testing.T) {
	repo := InferredRepository{URL: "https://github.com/acme/shop", From: "Application argocd/cart spec.source.repoURL", Namespace: "argocd", Kind: "Application"}
	badRule := inferredCart()
	badRule.Group = &InferredGroup{Rule: "operator", Name: "x", From: "y"}
	badFrom := rawInferred("shop", "Deployment", "from")
	badFrom.Group = &InferredGroup{Rule: "helm", Namespace: "shop", Name: "x", From: "label\x1b"}
	badFrom.Repositories = []InferredRepository{{URL: strings.Repeat("h", 600), From: "f", Namespace: "argocd", Kind: "Application"}, repo, repo, repo, repo, repo}
	badFrom.RepositoryUnknown = "bad\x07"
	badAmb := ambiguous("shop", "amb")
	badAmb.Group.Ambiguous = "a\x1bb"
	badKind := rawInferred("shop", "Deployment", "kind")
	badKind.Repositories = []InferredRepository{{URL: "https://x", From: "f", Namespace: "argocd", Kind: "Bucket"}, {From: "f", Namespace: "n", Kind: "Application"}}
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, badRule, badFrom, badAmb, badKind))
	for name, dropped := range map[string][]string{
		cartName:                   {"group"},
		"k8s:shop/Deployment/from": {"group", "repositories[0]", "repositories[5]", "repositoryUnknown"},
		"k8s:shop/Deployment/amb":  {"group"},
		"k8s:shop/Deployment/kind": {"repositories[0]", "repositories[1]"},
	} {
		w := workloadOf(t, snap, name)
		if w.Group.Rule != groupRuleWorkload || w.Group.Ambiguous != "" || !slices.Equal(w.Dropped, dropped) {
			t.Errorf("%s: group %+v dropped %v, want %v", name, w.Group, w.Dropped, dropped)
		}
	}
	if w := workloadOf(t, snap, "k8s:shop/Deployment/from"); len(w.Repositories) != 4 || w.RepositoryUnknown != "" {
		t.Errorf("repositories %+v unknown %q", w.Repositories, w.RepositoryUnknown)
	}
	data, _ := json.Marshal(snap)
	if strings.Contains(string(data), "hhhhhhhhhh") || strings.Contains(string(data), "operator") || strings.Contains(string(data), "Bucket") {
		t.Errorf("a dropped value reached the snapshot: %s", data)
	}
	if !limitationMentions(snap, LimitationSourceRecordInvalid, "group or repositories") {
		t.Errorf("limitations %+v", snap.Limitations)
	}
}

func TestInferredGroupAmbiguousLimitation(t *testing.T) {
	snap := buildClusterSnap(t, workloadsSource("a", nil, ambiguous("shop", "one"), ambiguous("shop", "two"), inferredCart()), workloadsSource("b", nil, ambiguous("ops", "three")))
	got := limitationsWith(snap, LimitationServiceGroupAmbiguous)
	if len(got) != 2 || got[0].Source != "a" || !strings.HasPrefix(got[0].Message, "2 workloads from source a stay ungrouped") || got[1].Source != "b" || !strings.HasPrefix(got[1].Message, "1 workloads") {
		t.Errorf("snapshot limitations %+v", got)
	}
	d, err := NewQuery(snap).EntityDetail(KindService, string(NewServiceKey("k8s:shop/Deployment/one")))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(d.Service.Limitations.Items, func(l AttributedLimitation) bool {
		return l.Limitation.Code == LimitationServiceGroupAmbiguous && strings.HasPrefix(l.Limitation.Message, "k8s:shop/Deployment/one stays ungrouped: label argocd")
	}) {
		t.Errorf("record limitations %+v", d.Service.Limitations.Items)
	}
}

func TestInferredGroupLabels(t *testing.T) {
	for _, tc := range []struct {
		g    InferredGroup
		want string
	}{
		{InferredGroup{Rule: "argocd", Name: "cart"}, "Argo CD Application cart"},
		{InferredGroup{Rule: "argocd", Namespace: "shop", Name: "cart"}, "Argo CD Application shop/cart"},
		{InferredGroup{Rule: "flux-kustomization", Namespace: "flux-system", Name: "apps"}, "Flux Kustomization flux-system/apps"},
		{InferredGroup{Rule: "flux-helmrelease", Namespace: "ops", Name: "log"}, "Flux HelmRelease ops/log"},
		{InferredGroup{Rule: "helm", Namespace: "shop", Name: "exporter"}, "Helm release shop/exporter"},
		{InferredGroup{Rule: "part-of", Namespace: "shop", Name: "store"}, "part-of store in shop"},
		{InferredGroup{Rule: "instance", Namespace: "shop", Name: "store-1"}, "instance store-1 in shop"},
		{InferredGroup{Rule: groupRuleWorkload, Namespace: "shop", Name: "Deployment/cart"}, "This workload only"},
		{InferredGroup{Rule: groupRuleWorkload, Namespace: "shop", Name: "Deployment/cart", Ambiguous: "two"}, "Ungrouped"},
	} {
		if got := groupLabel(tc.g); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.g, got, tc.want)
		}
	}
}

func TestInferredMergeKeepsFirstGroup(t *testing.T) {
	second := inferredCart()
	second.Group = &InferredGroup{Rule: "helm", Namespace: "shop", Name: "other", From: "annotations"}
	// The ids sort the other way, so only Build's argument order can pick "z".
	snap := buildClusterSnap(t, workloadsSource("z", nil, argoCart()), workloadsSource("a", nil, second))
	w := workloadOf(t, snap, cartName)
	if w.Group.Rule != "argocd" || len(w.Repositories) != 1 || w.Source != "z" {
		t.Errorf("merged workload %+v", w)
	}
}

func TestInferredLinesShowGroupAndRepository(t *testing.T) {
	in := argoCart()
	in.Repositories = append(in.Repositories, InferredRepository{URL: "git@github.com:acme/base", From: "Application argocd/cart spec.sources[1].repoURL", Namespace: "argocd", Kind: "Application"})
	in.RepositoryUnknown = "the source is Helm chart redis, not a Git repository"
	snap := buildClusterSnap(t, workloadsSource("k8s-workloads", nil, in, inferredCart(), ambiguous("shop", "amb")))
	got := strings.Join(workloadOf(t, snap, cartName).Lines(), "\n")
	for _, want := range []string{
		"  group Argo CD Application cart (from k8s-workloads: label argocd.argoproj.io/instance)",
		"  group key argocd//cart (pacto fleet entities --group takes it)",
		"  repository https://github.com/acme/shop path deploy/cart (from Application argocd/cart spec.source.repoURL)",
		"  repository git@github.com:acme/base (from Application argocd/cart spec.sources[1].repoURL)",
		"  repository unknown: the source is Helm chart redis, not a Git repository",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lines miss %q:\n%s", want, got)
		}
	}
	snap = buildClusterSnap(t, workloadsSource("k8s-workloads", nil, inferredCart(), ambiguous("shop", "amb")))
	if got := strings.Join(workloadOf(t, snap, cartName).Lines(), "\n"); !strings.Contains(got, "  group This workload only (from k8s-workloads: no grouping label or annotation)") {
		t.Errorf("workload group lines:\n%s", got)
	}
	if got := strings.Join(workloadOf(t, snap, "k8s:shop/Deployment/amb").Lines(), "\n"); !strings.Contains(got, "  ungrouped: label argocd.argoproj.io/instance names cart") || strings.Contains(got, "  group ") {
		t.Errorf("ungrouped lines:\n%s", got)
	}
	if got := (&InferredWorkload{Namespace: "shop", Kind: "Deployment", Name: "cart"}).Lines(); len(got) != 1 {
		t.Errorf("a workload with no group prints %v", got)
	}
}

func groupFleet(t *testing.T) *Query {
	t.Helper()
	other := rawInferred("shop", "Deployment", "worker")
	other.Group = argoCart().Group
	return NewQuery(buildClusterSnap(t, workloadsSource("k8s-workloads", nil, argoCart(), other, rawInferred("shop", "StatefulSet", "ledger"))))
}

func TestEntitiesGroupFilter(t *testing.T) {
	q := groupFleet(t)
	got := refKeys(entityList(t, q, EntityFilter{Group: "argocd//cart"}).Entities)
	slices.Sort(got)
	want := []string{string(NewServiceKey(cartName)), string(NewServiceKey("k8s:shop/Deployment/worker"))}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("group filter %v, want %v", got, want)
	}
	if k := ListKinds("", EntityFilter{Group: "argocd//cart"}); !slices.Equal(k, []EntityKind{KindService}) {
		t.Errorf("list kinds %v", k)
	}
	var bad *InvalidQueryError
	if _, err := q.Entities(EntityFilter{Kinds: []EntityKind{KindOwner}, Group: "argocd//cart"}); !errors.As(err, &bad) || bad.Field != "group" {
		t.Errorf("owner kind err %v", err)
	}
	if _, err := q.Entities(EntityFilter{Group: strings.Repeat("g", MaxInferredGroupKeyRunes+1)}); !errors.As(err, &bad) || bad.Field != "group" {
		t.Errorf("long group err %v", err)
	}
}

func TestEntityRefCarriesGroupAndRepository(t *testing.T) {
	l := entityList(t, groupFleet(t), EntityFilter{Declaration: DeclarationInferred})
	for _, r := range l.Entities {
		switch r.Key {
		case string(NewServiceKey(cartName)):
			if r.Group == nil || r.Group.Key != "argocd//cart" || r.Repository != "https://github.com/acme/shop" || r.Image == "" {
				t.Errorf("cart row %+v", r)
			}
		case string(NewServiceKey(ledgerName)):
			if r.Group == nil || r.Group.Rule != groupRuleWorkload || r.Repository != "" {
				t.Errorf("ledger row %+v", r)
			}
		}
	}
}

func TestRestrictDropsHiddenRepositories(t *testing.T) {
	q := groupFleet(t)
	before, _ := json.Marshal(q.SnapshotForSerialization())
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "argocd" }), anyClusterKind(func(ns, _ string) bool { return ns != "argocd" }))
	w := workloadOf(t, r.SnapshotForSerialization(), cartName)
	if len(w.Repositories) != 0 || w.RepositoryUnknown != "hidden by your permissions" || w.Group.Key != "argocd//cart" {
		t.Errorf("restricted workload %+v", w)
	}
	for _, e := range entityList(t, r, EntityFilter{Declaration: DeclarationInferred}).Entities {
		if e.Repository != "" {
			t.Errorf("row %s keeps repository %q", e.Key, e.Repository)
		}
	}
	if after, _ := json.Marshal(q.SnapshotForSerialization()); string(after) != string(before) {
		t.Error("Restrict changed the original snapshot")
	}
}

func TestRestrictDropsRepositoryWithoutApplicationRead(t *testing.T) {
	q := groupFleet(t)
	noApps := func(ns, kind string) bool { return ns != "argocd" || kind != "Application" }
	r := q.Restrict(anyCluster(everyNamespace), anyClusterKind(noApps))
	if r == q {
		t.Fatal("a repository the viewer may not read kept the whole query")
	}
	if w := workloadOf(t, r.SnapshotForSerialization(), cartName); len(w.Repositories) != 0 || w.RepositoryUnknown != "hidden by your permissions" {
		t.Errorf("restricted workload %+v", w)
	}
	if q.Restrict(anyCluster(everyNamespace), anyClusterKind(everyWorkload)) != q {
		t.Error("a viewer who may read everything got a copy")
	}
	noNS := argoCart()
	noNS.Repositories[0].Namespace = ""
	qn := NewQuery(buildClusterSnap(t, workloadsSource("k8s-workloads", nil, noNS)))
	if w := workloadOf(t, qn.Restrict(anyCluster(everyNamespace), anyClusterKind(everyWorkload)).SnapshotForSerialization(), cartName); len(w.Repositories) != 0 {
		t.Errorf("a repository with no namespace survived: %+v", w.Repositories)
	}
}

// TestRestrictHidesRepositoryReasonFromHiddenObject: a repository reason is
// another object's content, like the repository itself: it shows only to a
// viewer allowed to list every object it was read from.
func TestRestrictHidesRepositoryReasonFromHiddenObject(t *testing.T) {
	unresolved := func(why string, from ...WorkloadScope) *Query {
		in := argoCart()
		in.Repositories, in.RepositoryUnknown, in.RepositoryUnknownFrom = nil, why, from
		return NewQuery(buildClusterSnap(t, workloadsSource("k8s-workloads", nil, in)))
	}
	noApps := func(_, kind string) bool { return kind != "Application" }
	q := unresolved("the source is Helm chart payments-internal, not a Git repository", WorkloadScope{Namespace: "argocd", Kind: "Application"})
	if !slices.Contains(q.InferredWorkloadScopes(), WorkloadScope{Namespace: "argocd", Kind: "Application"}) {
		t.Errorf("the reason's object is never reviewed: %v", q.InferredWorkloadScopes())
	}
	r := q.Restrict(anyCluster(everyNamespace), anyClusterKind(noApps))
	if r == q {
		t.Fatal("a reason read from a hidden object kept the whole query")
	}
	if w := workloadOf(t, r.SnapshotForSerialization(), cartName); w.RepositoryUnknown != "hidden by your permissions" || w.Group.Key != "argocd//cart" {
		t.Errorf("restricted workload %+v", w)
	}
	if q.Restrict(anyCluster(everyNamespace), anyClusterKind(everyWorkload)) != q {
		t.Error("a viewer who may list the Application got a copy")
	}
	// No namespace to check, or an object Pacto cannot name, hides it from every viewer.
	for _, from := range []WorkloadScope{{Kind: "Application"}, {Namespace: "argocd", Kind: "Secret"}} {
		w := workloadOf(t, unresolved("no Application named cart was read", from).Restrict(anyCluster(everyNamespace), anyClusterKind(everyWorkload)).SnapshotForSerialization(), cartName)
		if w.RepositoryUnknown != "hidden by your permissions" {
			t.Errorf("%+v: reason %q", from, w.RepositoryUnknown)
		}
	}
	// A reason that names no object shows to everyone.
	q = unresolved("the cluster does not serve Applications")
	if w := workloadOf(t, q.Restrict(anyCluster(everyNamespace), anyClusterKind(noApps)).SnapshotForSerialization(), cartName); w.RepositoryUnknown != "the cluster does not serve Applications" {
		t.Errorf("public reason %q", w.RepositoryUnknown)
	}
}

// TestInvalidGroupSaysItWasRead: a group a source sent and Pacto left out
// never reads as no grouping label at all.
func TestInvalidGroupSaysItWasRead(t *testing.T) {
	in := inferredCart()
	in.Group = &InferredGroup{Rule: "argocd", Name: "cart", From: "label\x00"}
	w := workloadOf(t, buildClusterSnap(t, workloadsSource("k8s-workloads", nil, in)), cartName)
	if w.Group.From != droppedGroupFrom || w.Group.Rule != groupRuleWorkload || !slices.Contains(w.Dropped, "group") {
		t.Errorf("workload %+v %+v", w.Group, w.Dropped)
	}
	if got := strings.Join(w.Lines(), "\n"); strings.Contains(got, noGroupingFrom) {
		t.Errorf("lines claim no grouping label:\n%s", got)
	}
}

func TestRestrictKeepsGroupOfVisibleWorkload(t *testing.T) {
	r := groupFleet(t).Restrict(anyCluster(func(ns string) bool { return ns == "shop" }), anyClusterKind(func(ns, _ string) bool { return ns == "shop" }))
	snap := r.SnapshotForSerialization()
	for _, name := range []string{cartName, "k8s:shop/Deployment/worker"} {
		if g := workloadOf(t, snap, name).Group; g == nil || g.Key != "argocd//cart" || g.From == "" {
			t.Errorf("%s group %+v", name, g)
		}
	}
}

func TestInferredWorkloadScopesIncludeRepositories(t *testing.T) {
	got := groupFleet(t).InferredWorkloadScopes()
	if !slices.Contains(got, WorkloadScope{Namespace: "argocd", Kind: "Application"}) || !slices.Contains(got, WorkloadScope{Namespace: "shop", Kind: "Deployment"}) {
		t.Errorf("scopes %v", got)
	}
	if !slices.Contains(namespaceNames(groupFleet(t).Namespaces()), "argocd") {
		t.Error("the repository namespace is never reviewed")
	}
}

func TestRestrictRebuildsGroupAmbiguousCount(t *testing.T) {
	q := NewQuery(buildClusterSnap(t, workloadsSource("k8s-workloads", nil, ambiguous("shop", "one"), ambiguous("ops", "two"))))
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "ops" }), anyClusterKind(everyWorkload))
	got := limitationsWith(r.SnapshotForSerialization(), LimitationServiceGroupAmbiguous)
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "1 workloads from source k8s-workloads") {
		t.Errorf("restricted limitations %+v", got)
	}
}

func TestInferredRepositorySurvivesClone(t *testing.T) {
	q := groupFleet(t)
	if w := workloadOf(t, q.Snapshot(), cartName); w.Repositories[0].Namespace != "argocd" || w.Repositories[0].Kind != "Application" {
		t.Errorf("snapshot repository %+v", w.Repositories)
	}
	d, err := q.EntityDetail(KindService, string(NewServiceKey(cartName)))
	if err != nil {
		t.Fatal(err)
	}
	if r := d.Service.Workload.Repositories; r[0].Namespace != "argocd" || r[0].Kind != "Application" || d.Service.Workload.Group.Key != "argocd//cart" {
		t.Errorf("detail workload %+v", d.Service.Workload)
	}
}

func TestGroupingNeverChangesGateInputs(t *testing.T) {
	build := func(grouped bool) *Query {
		cart, ledger := inferredCart(), rawInferred("shop", "StatefulSet", "ledger")
		if grouped {
			cart = argoCart()
			ledger = ambiguous("shop", "ledger")
			ledger.Kind = "StatefulSet"
		}
		declared := NewMemorySource("k8s", SourceKindKubernetes, &Collection{
			Targets:  []RawTarget{workload("shop", "web")},
			Observed: []ObservedEdge{call(cartName, "web"), call(ledgerShop, "web")},
		})
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, declared, workloadsSource("k8s-workloads", nil, cart, ledger))
		if err != nil {
			t.Fatal(err)
		}
		return NewQuery(snap)
	}
	plain, grouped := build(false), build(true)
	sameJSON(t, "overview", plain.Overview().Summary, grouped.Overview().Summary)
	pa, _ := plain.Attention(AttentionFilter{})
	ga, _ := grouped.Attention(AttentionFilter{})
	sameJSON(t, "attention", pa.Items, ga.Items)
	sameJSON(t, "uncontracted callers", plain.SnapshotForSerialization().UncontractedCallers, grouped.SnapshotForSerialization().UncontractedCallers)
	pg, _ := plain.Graph(GraphQuery{Service: "web", Direction: DirectionDependents, IncludeObserved: true})
	gg, _ := grouped.Graph(GraphQuery{Service: "web", Direction: DirectionDependents, IncludeObserved: true})
	sameJSON(t, "dependents graph", []any{pg.Nodes, pg.Edges, pg.Meta.Completeness}, []any{gg.Nodes, gg.Edges, gg.Meta.Completeness})
	if plain.SnapshotForSerialization().Completeness != grouped.SnapshotForSerialization().Completeness {
		t.Error("grouping changed the completeness")
	}
}
