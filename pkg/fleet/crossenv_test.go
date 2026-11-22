package fleet

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// envTarget is the target of service in ns that runs as the Deployment
// service-env and names env as its environment ("" names none).
func envTarget(ns, service, env string) RawTarget {
	t := workload(ns, service)
	t.Name = service + "-" + env
	t.Workload = &WorkloadRef{Kind: "Deployment", Name: service + "-" + env}
	if env != "" {
		t.Labels = map[string]string{TargetEnvironmentLabel: env}
	}
	return t
}

// wid is the workload id of envTarget(ns, service, env) in cluster.
func wid(cluster, ns, service, env string) string {
	return K8sCaller(cluster, ns, "Deployment", service+"-"+env)
}

// seen is one observed call last seen ago before the fixed clock.
func seen(from, to string, ago time.Duration) ObservedEdge {
	return ObservedEdge{From: from, To: to, Count: 3, LastSeen: fixedNow().Add(-ago)}
}

// envFleet is one cluster with targets and a kernel that saw edges.
func envFleet(t *testing.T, targets []RawTarget, edges ...ObservedEdge) *Query {
	t.Helper()
	return NewQuery(buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: targets}), kernel(edges...)))
}

func crossItems(t *testing.T, q *Query) []AttentionItem {
	t.Helper()
	return attentionOf(t, q, AttentionFilter{Category: categoryCrossEnvironment, Limit: MaxAttentionLimit}).Items
}

func TestCrossEnvironmentCallIsError(t *testing.T) {
	q := envFleet(t, []RawTarget{envTarget("shop", "orders", "prod"), envTarget("shop", "payments", "staging")},
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "staging"), time.Minute))
	items := crossItems(t, q)
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	it := items[0]
	if it.Severity != severityError || it.Code != codeCrossEnvironmentCall || it.Entity.Key != string(NewTargetKey("shop", "kubernetes", "orders-prod")) ||
		it.Label != "orders (prod) calls payments (staging)" || strings.Contains(it.Reason, "last seen") {
		t.Errorf("item %+v", it)
	}
	ov := q.Overview()
	if ov.Summary.CrossEnvironmentCalls != 1 || !slices.ContainsFunc(ov.EntryPoints, func(ep EntryPoint) bool {
		return ep.Label == "Calls across environments" && ep.Count == 1 && ep.Severity == severityError && ep.Category == categoryCrossEnvironment
	}) {
		t.Errorf("summary %d entry points %+v", ov.Summary.CrossEnvironmentCalls, ov.EntryPoints)
	}
}

func TestCrossEnvironmentFoldKeepsEveryPair(t *testing.T) {
	q := envFleet(t, []RawTarget{
		envTarget("shop", "orders", "prod"), envTarget("shop", "orders", "dev"),
		envTarget("shop", "payments", "staging"), envTarget("shop", "payments", "qa"),
	},
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "staging"), time.Minute),
		seen(wid("", "shop", "orders", "dev"), wid("", "shop", "payments", "qa"), time.Minute),
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "staging"), 2*time.Minute))
	n := 0
	for _, rel := range q.snap.Relationships {
		if rel.Provenance == ProvenanceObserved {
			n++
		}
	}
	if items := crossItems(t, q); n != 1 || len(items) != 2 {
		t.Fatalf("observed relationships %d items %+v", n, items)
	}
}

func TestCrossEnvironmentPreviewIsWarning(t *testing.T) {
	c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "payments", Version: "1.0.0"},
		Environments: []contract.Environment{{Name: "pr-12", File: "values/pr-12.yaml", Lifetime: contract.EnvironmentTemporary}}}
	rev := RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{"values/pr-12.yaml": {Data: []byte("a: 1\n")}}}, Digest: "sha256:pay"}
	pay := envTarget("shop", "payments", "pr-12")
	pay.Digest = "sha256:pay"
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Revisions: []RawRevision{rev}, Targets: []RawTarget{envTarget("shop", "orders", "prod"), pay}}),
		kernel(seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "pr-12"), time.Minute))))
	items := crossItems(t, q)
	if len(items) != 1 || items[0].Severity != severityWarning || items[0].Code != codeCrossEnvironmentPreviewCall {
		t.Fatalf("items %+v", items)
	}
	if ep := q.Overview().EntryPoints; !slices.ContainsFunc(ep, func(e EntryPoint) bool {
		return e.Category == categoryCrossEnvironment && e.Severity == severityWarning
	}) {
		t.Errorf("entry points %+v", ep)
	}
}

func TestCrossEnvironmentPreviewNeedsEveryRevisionTemporary(t *testing.T) {
	mk := func(name, digest, lifetime string) RawRevision {
		c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: name, Version: "1.0.0"},
			Environments: []contract.Environment{{Name: "pr-12", File: "v.yaml", Lifetime: lifetime}}}
		return RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{"v.yaml": {Data: []byte("a: 1\n")}}}, Digest: digest}
	}
	pay, cart := envTarget("shop", "payments", "pr-12"), envTarget("shop", "cart", "pr-12")
	pay.Digest, cart.Digest = "sha256:pay", "sha256:cart"
	q := NewQuery(buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{
		Revisions: []RawRevision{mk("payments", "sha256:pay", contract.EnvironmentTemporary), mk("cart", "sha256:cart", contract.EnvironmentPersistent)},
		Targets:   []RawTarget{pay, cart, envTarget("shop", "orders", "prod")},
	})))
	if p := q.previewEnvironments(); p["pr-12"] || p["prod"] {
		t.Error("an environment one running revision declares persistent, or none declares, is a preview")
	}
}

func TestCrossEnvironmentUnknownRaisesNothing(t *testing.T) {
	q := envFleet(t, []RawTarget{envTarget("shop", "orders", "prod"), envTarget("shop", "payments", "")},
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", ""), time.Minute),
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "nobody", "x"), time.Minute))
	if items := crossItems(t, q); len(items) != 0 {
		t.Errorf("items %+v", items)
	}
}

func TestCrossEnvironmentServiceNamedEndsRaiseNothing(t *testing.T) {
	q := envFleet(t, []RawTarget{envTarget("shop", "orders", "prod"), envTarget("shop", "payments", "staging")}, seen("orders", "payments", time.Minute))
	if !slices.ContainsFunc(q.snap.Relationships, func(r Relationship) bool { return r.Provenance == ProvenanceObserved }) {
		t.Fatal("no observed relationship")
	}
	if items := crossItems(t, q); len(items) != 0 {
		t.Errorf("items %+v", items)
	}
}

func TestCrossEnvironmentAmbiguousClusterRaisesNothing(t *testing.T) {
	a, b := clusterOf(clusterA, "eu", ClusterNameFromContext), clusterOf(clusterB, "us", ClusterNameFromContext)
	build := func(edges ...ObservedEdge) *Query {
		return NewQuery(buildClusterSnap(t,
			NewMemorySource("ka", SourceKindKubernetes, &Collection{Cluster: a, Targets: []RawTarget{envTarget("shop", "orders", "prod")}}),
			NewMemorySource("kb", SourceKindKubernetes, &Collection{Cluster: b, Targets: []RawTarget{envTarget("shop", "payments", "staging")}}),
			kernel(edges...)))
	}
	if items := crossItems(t, build(seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "staging"), time.Minute))); len(items) != 0 {
		t.Errorf("unqualified ends in two clusters raised %+v", items)
	}
	if items := crossItems(t, build(seen(wid(clusterA, "shop", "orders", "prod"), wid(clusterB, "shop", "payments", "staging"), time.Minute))); len(items) != 1 {
		t.Errorf("cluster-qualified ends raised %+v", items)
	}
	if items := crossItems(t, build(seen(wid(clusterB, "shop", "orders", "prod"), wid(clusterB, "shop", "payments", "staging"), time.Minute))); len(items) != 0 {
		t.Errorf("an end in the wrong cluster raised %+v", items)
	}
}

func TestCrossEnvironmentWrongClusterInOneClusterRaisesNothing(t *testing.T) {
	a := clusterOf(clusterA, "eu", ClusterNameFromContext)
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("ka", SourceKindKubernetes, &Collection{Cluster: a, Targets: []RawTarget{envTarget("shop", "orders", "prod"), envTarget("shop", "payments", "staging")}}),
		kernel(seen(wid(clusterB, "shop", "orders", "prod"), wid("", "shop", "payments", "staging"), time.Minute))))
	if items := crossItems(t, q); len(items) != 0 {
		t.Errorf("items %+v", items)
	}
}

func TestCrossEnvironmentStaleIsNotError(t *testing.T) {
	q := envFleet(t, []RawTarget{envTarget("shop", "orders", "prod"), envTarget("shop", "payments", "staging")},
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "staging"), 48*time.Hour))
	items := crossItems(t, q)
	if len(items) != 1 || items[0].Severity != severityWarning || !strings.Contains(items[0].Reason, "last seen 2026-07-27T12:00:00Z") {
		t.Fatalf("items %+v", items)
	}
}

func TestCrossEnvironmentSameEnvironmentRaisesNothing(t *testing.T) {
	q := envFleet(t, []RawTarget{envTarget("shop", "orders", "prod"), envTarget("shop", "payments", "prod")},
		seen(wid("", "shop", "orders", "prod"), wid("", "shop", "payments", "prod"), time.Minute))
	if items := crossItems(t, q); len(items) != 0 {
		t.Errorf("items %+v", items)
	}
	if n, sev := crossEnvironmentTally(nil); n != 0 || sev != "" {
		t.Errorf("tally %d %q", n, sev)
	}
}

func TestCrossEnvironmentRestrictClearsPairs(t *testing.T) {
	q := envFleet(t, []RawTarget{envTarget("shop", "orders", "prod"), envTarget("secret", "payments", "staging"), envTarget("shop", "payments", "prod")},
		seen(wid("", "shop", "orders", "prod"), wid("", "secret", "payments", "staging"), time.Minute))
	if items := crossItems(t, q); len(items) != 1 {
		t.Fatalf("unrestricted items %+v", items)
	}
	r := q.Restrict(func(_, ns string) bool { return ns != "secret" }, nil)
	if r.snap.Services[NewServiceKey("payments")] == nil {
		t.Fatal("payments is hidden")
	}
	if items := crossItems(t, r); len(items) != 0 {
		t.Errorf("restricted items %+v", items)
	}
}

func TestEnvPairsBounded(t *testing.T) {
	var pairs []envPair
	for i := range maxEnvPairs + 2 {
		pairs = addEnvPair(pairs, envPair{from: TargetKey(rune('a' + i)), to: "z"})
	}
	pairs = addEnvPair(pairs, envPair{from: "a", to: "z", lastSeen: new(fixedNow())})
	if len(pairs) != maxEnvPairs || pairs[0].lastSeen == nil {
		t.Errorf("pairs %+v", pairs)
	}
	if got := sortedEnvPairs([]envPair{{from: "b"}, {from: "a", to: "y"}, {from: "a", to: "x"}}); got[0].to != "x" || got[2].from != "b" {
		t.Errorf("sorted %+v", got)
	}
}
