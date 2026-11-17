package fleet

import (
	"context"
	"encoding/json"
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
	if got := inferredRestrictFleet(t).Namespaces(); !slices.Contains(got, "ops") || !slices.Contains(got, "shop") {
		t.Errorf("namespaces %v miss an inferred namespace", got)
	}
}

func TestInferredWorkloadScopes(t *testing.T) {
	want := []WorkloadScope{{"ops", "DaemonSet"}, {"shop", "CronJob"}, {"shop", "Deployment"}, {"shop", "StatefulSet"}}
	if got := inferredRestrictFleet(t).InferredWorkloadScopes(); !slices.Equal(got, want) {
		t.Errorf("scopes %v, want %v", got, want)
	}
	if got := restrictFleet(t).InferredWorkloadScopes(); len(got) != 0 {
		t.Errorf("a fleet with no inferred service has scopes %v", got)
	}
}

func TestRestrictHidesInferredByNamespace(t *testing.T) {
	r := inferredRestrictFleet(t).Restrict(func(ns string) bool { return ns != "ops" }, everyWorkload)
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
	r := q.Restrict(func(ns string) bool { return ns != "ops" }, everyWorkload)
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
	r := inferredRestrictFleet(t).Restrict(everyNamespace, func(_, kind string) bool { return kind != "Deployment" })
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
	r := inferredRestrictFleet(t).Restrict(func(ns string) bool { return ns != "secret" }, everyWorkload)
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
	r := inferredRestrictFleet(t).Restrict(func(ns string) bool { return ns != "secret" }, everyWorkload)
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
	r := q.Restrict(func(ns string) bool { return ns != "ops" }, everyWorkload)
	if n := sourceState(t, r.SnapshotForSerialization(), "k8s-workloads").InferredCount; n != 3 {
		t.Errorf("restricted count %d, want 3", n)
	}
}

func TestRestrictInferredNeedsWorkloadRead(t *testing.T) {
	r := inferredRestrictFleet(t).Restrict(everyNamespace, func(ns, kind string) bool { return ns != "shop" || kind != "Deployment" })
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
	r := q.Restrict(everyNamespace, nil)
	if r == q {
		t.Fatal("a nil workload predicate returned the whole view")
	}
	for _, s := range r.SnapshotForSerialization().Services {
		if s.Inferred() {
			t.Errorf("%s shows with no workload predicate", s.Name)
		}
	}
	if q := restrictFleet(t); q.Restrict(everyNamespace, nil) != q {
		t.Error("a nil workload predicate restricted a fleet with no inferred service")
	}
}
