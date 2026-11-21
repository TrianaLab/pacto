package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	nightlyName = "k8s:shop/CronJob/nightly"
	agentName   = "k8s:ops/DaemonSet/agent"
	ledgerShop  = "k8s:shop/StatefulSet/ledger"
)

// inferredRestrictFleet: web runs in shop and api in secret. Pacto infers
// cart (Deployment), nightly (CronJob) and ledger (StatefulSet) in shop and
// agent (DaemonSet) in ops, whose image is dropped as unreadable. cart and
// agent were seen calling web, ledger calling api.
func inferredRestrictFleet(t *testing.T) *Query {
	t.Helper()
	mk := func(name string) RawRevision {
		c := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: "t"}}}
		return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{}}, Digest: "sha256:" + name}
	}
	k := SourceKindKubernetes
	declared := NewMemorySource("k8s", k, &Collection{
		Revisions: []RawRevision{mk("web"), mk("api")},
		Targets: []RawTarget{
			{Scope: "shop", Kind: k, Name: "web", Service: "web", Compliance: StatusCompliant},
			{Scope: "secret", Kind: k, Name: "api", Service: "api", Compliance: StatusCompliant},
		},
		Observed: []ObservedEdge{call(cartName, "web"), call(agentName, "web"), call(ledgerShop, "api")},
	})
	agent := rawInferred("ops", "DaemonSet", "agent")
	agent.Image = &InferredValue{Value: "bad\x1bimage", From: "spec.template.spec.containers[name=agent].image"}
	workloads := workloadsSource("k8s-workloads", nil, inferredCart(), rawInferred("shop", "CronJob", "nightly"),
		rawInferred("shop", "StatefulSet", "ledger"), agent)
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, declared, workloads)
	if err != nil {
		t.Fatal(err)
	}
	return NewQuery(snap)
}

func everyNamespace(string) bool        { return true }
func everyWorkload(string, string) bool { return true }

func serviceNames(q *Query) []string {
	var out []string
	for _, s := range q.SnapshotForSerialization().Services {
		out = append(out, s.Name)
	}
	slices.Sort(out)
	return out
}

// answersOf is every answer a restricted view gives, as JSON.
func answersOf(t *testing.T, q *Query) string {
	t.Helper()
	ents, err := q.Entities(EntityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	search, err := q.Search(SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	att, err := q.Attention(AttentionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	web, err := q.EntityDetail(KindService, string(NewServiceKey("web")))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal([]any{q.SnapshotForSerialization(), ents, search, att, web, q.Overview()})
	return string(b)
}

func TestNamespacesIncludeInferred(t *testing.T) {
	if got := namespaceNames(inferredRestrictFleet(t).Namespaces()); !slices.Contains(got, "ops") || !slices.Contains(got, "shop") {
		t.Errorf("namespaces %v miss an inferred namespace", got)
	}
}

func TestInferredWorkloadScopes(t *testing.T) {
	want := []WorkloadScope{{Namespace: "ops", Kind: "DaemonSet"}, {Namespace: "shop", Kind: "CronJob"}, {Namespace: "shop", Kind: "Deployment"}, {Namespace: "shop", Kind: "StatefulSet"}}
	if got := inferredRestrictFleet(t).InferredWorkloadScopes(); !slices.Equal(got, want) {
		t.Errorf("scopes %v, want %v", got, want)
	}
	if got := restrictFleet(t).InferredWorkloadScopes(); len(got) != 0 {
		t.Errorf("a fleet with no inferred service has scopes %v", got)
	}
}

func TestRestrictHidesInferredByNamespace(t *testing.T) {
	r := inferredRestrictFleet(t).Restrict(anyCluster(func(ns string) bool { return ns != "ops" }), anyClusterKind(everyWorkload))
	got := serviceNames(r)
	if slices.Contains(got, agentName) || !slices.Contains(got, cartName) || !slices.Contains(got, nightlyName) {
		t.Errorf("services %v", got)
	}
	if !limitationMentions(r.SnapshotForSerialization(), LimitationHiddenByPermissions, "hidden") {
		t.Error("no hidden-by-permissions limitation")
	}
}

func TestRestrictNeverNamesHiddenInferred(t *testing.T) {
	q := inferredRestrictFleet(t)
	if !strings.Contains(answersOf(t, q), "agent") {
		t.Fatal("the unrestricted answers never name agent: the test proves nothing")
	}
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "ops" }), anyClusterKind(everyWorkload))
	if a := answersOf(t, r); strings.Contains(a, "agent") || strings.Contains(a, string(NewServiceKey(agentName))) {
		t.Errorf("a restricted answer names the hidden agent: %s", a)
	}
	if _, err := r.EntityDetail(KindService, string(NewServiceKey(agentName))); err == nil {
		t.Error("the hidden agent has a detail")
	}
	if web := r.SnapshotForSerialization().Services[NewServiceKey("web")]; web.HiddenDependents != 1 {
		t.Errorf("web counts %d hidden dependents, want agent's one", web.HiddenDependents)
	}
}

func TestRestrictInferredCallerCountsHiddenDependents(t *testing.T) {
	r := inferredRestrictFleet(t).Restrict(anyCluster(everyNamespace), anyClusterKind(func(_, kind string) bool { return kind != "Deployment" }))
	s := r.SnapshotForSerialization()
	if web := s.Services[NewServiceKey("web")]; web.HiddenDependents != 1 {
		t.Errorf("web counts %d hidden dependents, want cart's one", web.HiddenDependents)
	}
	for _, c := range s.UncontractedCallers {
		if c.Caller == cartName {
			t.Error("the hidden cart still calls web")
		}
	}
}

func TestRestrictInferredCountsHiddenCallee(t *testing.T) {
	r := inferredRestrictFleet(t).Restrict(anyCluster(func(ns string) bool { return ns != "secret" }), anyClusterKind(everyWorkload))
	s := r.SnapshotForSerialization()
	ledger := s.Services[NewServiceKey(ledgerShop)]
	if ledger == nil || ledger.HiddenDependencies != 1 {
		t.Fatalf("ledger %+v, want one hidden dependency", ledger)
	}
	for _, c := range s.UncontractedCallers {
		if c.Caller == ledgerShop {
			t.Error("the caller row of a hidden callee stays")
		}
	}
	if !ledger.Inferred() || ledger.Workload == nil || ledger.Status != "" || !reflect.DeepEqual(ledger.Owner, contract.Owner{}) {
		t.Errorf("ledger's copy changed beyond its counts: %+v", ledger)
	}
}

func TestRestrictInferredKeepsStatusEmpty(t *testing.T) {
	r := inferredRestrictFleet(t).Restrict(anyCluster(func(ns string) bool { return ns != "secret" }), anyClusterKind(everyWorkload))
	for _, s := range r.SnapshotForSerialization().Services {
		if s.Inferred() && (s.Status != "" || !s.BusinessDomainUnknown || !s.SummaryUnknown) {
			t.Errorf("%s: status %q domain unknown %v summary unknown %v", s.Name, s.Status, s.BusinessDomainUnknown, s.SummaryUnknown)
		}
	}
}

func TestRestrictRecountsInferredCount(t *testing.T) {
	q := inferredRestrictFleet(t)
	if n := sourceState(t, q.SnapshotForSerialization(), "k8s-workloads").InferredCount; n != 4 {
		t.Fatalf("unrestricted count %d", n)
	}
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "ops" }), anyClusterKind(everyWorkload))
	if n := sourceState(t, r.SnapshotForSerialization(), "k8s-workloads").InferredCount; n != 3 {
		t.Errorf("restricted count %d, want 3", n)
	}
}

func TestRestrictInferredNeedsWorkloadRead(t *testing.T) {
	r := inferredRestrictFleet(t).Restrict(anyCluster(everyNamespace), anyClusterKind(func(ns, kind string) bool { return ns != "shop" || kind != "Deployment" }))
	got := serviceNames(r)
	if slices.Contains(got, cartName) || !slices.Contains(got, nightlyName) || !slices.Contains(got, "web") {
		t.Errorf("services %v", got)
	}
	if a := answersOf(t, r); strings.Contains(a, cartName) || strings.Contains(a, string(NewServiceKey(cartName))) || strings.Contains(a, "ghcr.io/acme/cart") {
		t.Errorf("an answer names the cart the viewer may not list: %s", a)
	}
	if web := r.SnapshotForSerialization().Services[NewServiceKey("web")]; web.HiddenDependents != 1 {
		t.Errorf("web counts %d hidden dependents", web.HiddenDependents)
	}
}

func TestRestrictNilWorkloadVisibleHidesInferred(t *testing.T) {
	q := inferredRestrictFleet(t)
	r := q.Restrict(anyCluster(everyNamespace), anyClusterKind(nil))
	if r == q {
		t.Fatal("a nil workload predicate returned the whole view")
	}
	for _, s := range r.SnapshotForSerialization().Services {
		if s.Inferred() {
			t.Errorf("%s shows with no workload predicate", s.Name)
		}
	}
	if q := restrictFleet(t); q.Restrict(anyCluster(everyNamespace), anyClusterKind(nil)) != q {
		t.Error("a nil workload predicate restricted a fleet with no inferred service")
	}
}

// A hidden namespace or name that is a substring of a visible inferred
// service's must never clear its unverified mark or its dropped fields.
func TestRestrictKeepsInferredStateUnderSubstringNames(t *testing.T) {
	down := NewMemorySource("k8s", SourceKindKubernetes, nil)
	down.err = errors.New("unreachable")
	gw := rawInferred("payments-prod", "Deployment", "gw")
	gw.Image = &InferredValue{Value: "bad\x1bimage", From: "spec.template.spec.containers[name=gw].image"}
	q := NewQuery(buildClusterSnap(t, down, workloadsSource("k8s-workloads", nil, gw, rawInferred("payments", "Deployment", "x"))))
	if ov := q.Overview().Summary; ov.InferredServices != 2 || ov.InferredUnverified != 2 {
		t.Fatalf("full view %+v", ov)
	}
	r := q.Restrict(anyCluster(func(ns string) bool { return ns != "" && ns != "payments" }), anyClusterKind(everyWorkload))
	if ov := r.Overview().Summary; ov.InferredServices != 1 || ov.InferredUnverified != 1 {
		t.Errorf("restricted view %+v", ov)
	}
	d, err := r.EntityDetail(KindService, string(NewServiceKey("k8s:payments-prod/Deployment/gw")))
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, l := range d.Service.Limitations.Items {
		codes = append(codes, l.Limitation.Code)
	}
	if !slices.Contains(codes, LimitationDeclarationUnverified) || !slices.Contains(codes, LimitationSourceRecordInvalid) {
		t.Errorf("detail limitations %v", codes)
	}
	s := r.SnapshotForSerialization()
	if !limitationMentions(s, LimitationDeclarationUnverified, "(services: 1, namespaces: 1)") || !limitationMentions(s, LimitationSourceRecordInvalid, "(1)") {
		t.Errorf("snapshot limitations %+v", s.Limitations)
	}
}

func TestInferredCountLeavesDeclaredWorkloadsOut(t *testing.T) {
	snap := declaringSnap(t, WorkloadRef{Kind: "StatefulSet", Name: "ledger"}, clusterA,
		rawInferred("shop", "StatefulSet", "ledger"), rawInferred("web", "StatefulSet", "ledger"), rawInferred("shop/x", "Deployment", "bad"))
	if n := sourceState(t, snap, "k8s-workloads").InferredCount; n != 1 {
		t.Errorf("count %d, want the one inferred record", n)
	}
}

func TestConsumerCompletenessIgnoresOnlyWorkloadSources(t *testing.T) {
	down := NewMemorySource("k8s-workloads", SourceKindKubernetesWorkloads, nil)
	down.err = errors.New("forbidden")
	snap := buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("shop", "web")}}), down)
	if snap.Completeness != CompletenessPartial || snap.ConsumerCompleteness() != CompletenessComplete {
		t.Errorf("snapshot %s consumers %s", snap.Completeness, snap.ConsumerCompleteness())
	}
	r := NewQuery(snap).Restrict(anyCluster(func(ns string) bool { return ns != "shop" }), anyClusterKind(everyWorkload)).SnapshotForSerialization()
	if r.ConsumerCompleteness() != CompletenessPartial {
		t.Error("a restricted view reads complete")
	}
	downCR := NewMemorySource("k8s", SourceKindKubernetes, nil)
	downCR.err = errors.New("unreachable")
	if c := buildClusterSnap(t, downCR).ConsumerCompleteness(); c != CompletenessPartial {
		t.Errorf("an unavailable Pacto source reads %s", c)
	}
}

// A visible inferred service's call to a hidden service was seen, so its
// detail counts it and never reads "none seen".
func TestRestrictInferredDetailCountsHiddenCalls(t *testing.T) {
	r := inferredRestrictFleet(t).Restrict(anyCluster(func(ns string) bool { return ns != "secret" }), anyClusterKind(everyWorkload))
	d, err := r.EntityDetail(KindService, string(NewServiceKey(ledgerShop)))
	if err != nil {
		t.Fatal(err)
	}
	if d.Service.HiddenDependencies != 1 || d.Service.Calls.Total != 0 {
		t.Fatalf("hidden %d calls %+v", d.Service.HiddenDependencies, d.Service.Calls)
	}
	lines := strings.Join(d.Service.InferredLines(fixedNow()), "\n")
	if strings.Contains(lines, "No calls") || strings.Contains(lines, "Calls unknown") || !strings.Contains(lines, "Calls to services you cannot see: 1") {
		t.Errorf("lines:\n%s", lines)
	}
}

func TestRestrictSaysWhyInferredIsHidden(t *testing.T) {
	q := inferredRestrictFleet(t)
	byKind := q.Restrict(anyCluster(everyNamespace), anyClusterKind(func(_, kind string) bool { return kind != "Deployment" })).SnapshotForSerialization()
	if !limitationMentions(byKind, LimitationHiddenByPermissions, "may not list their workload kind") {
		t.Errorf("no workload-kind reason: %+v", limitationsWith(byKind, LimitationHiddenByPermissions))
	}
	byNS := q.Restrict(anyCluster(func(ns string) bool { return ns != "ops" }), anyClusterKind(everyWorkload)).SnapshotForSerialization()
	if limitationMentions(byNS, LimitationHiddenByPermissions, "workload kind") {
		t.Error("a namespace-hidden inferred service blames its workload kind")
	}
}
