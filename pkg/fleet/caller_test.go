package fleet

import (
	"slices"
	"strings"
	"testing"
)

func TestK8sCallerRoundTrip(t *testing.T) {
	for _, tc := range []struct{ cluster, ns, kind, name, want string }{
		{"", "payments", "Deployment", "legacy", "k8s:payments/Deployment/legacy"},
		{clusterA, "payments", "Deployment", "legacy", "k8s:" + clusterA + "/payments/Deployment/legacy"},
		{"a/b%c", "ns", "Job", "x", "k8s:a%2Fb%25c/ns/Job/x"},
	} {
		got := K8sCaller(tc.cluster, tc.ns, tc.kind, tc.name)
		if got != tc.want {
			t.Errorf("K8sCaller(%q, %q, %q, %q) = %q, want %q", tc.cluster, tc.ns, tc.kind, tc.name, got, tc.want)
		}
		c, ns, kind, name, ok := ParseK8sCaller(got)
		if !ok || c != tc.cluster || ns != tc.ns || kind != tc.kind || name != tc.name {
			t.Errorf("ParseK8sCaller(%q) = %q %q %q %q %v", got, c, ns, kind, name, ok)
		}
	}
	for _, bad := range []string{"k8s:ns/name", "k8s:a/b/c/d/e", "legacy-batch", "payments/Deployment/x", "k8s:"} {
		if _, _, _, _, ok := ParseK8sCaller(bad); ok {
			t.Errorf("ParseK8sCaller(%q) must not parse", bad)
		}
	}
}

// callerClusterSources is two clusters that each run payments and each see a
// k8s caller of the same name; a third source with no cluster sees one too.
func callerClusterSources(callerOf ...string) []Source {
	srcs := []Source{
		NewMemorySource("eu", SourceKindKubernetes, &Collection{
			Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext),
			Targets: []RawTarget{workload("payments", "payments")},
			Observed: []ObservedEdge{
				{From: "k8s:batch/Deployment/legacy", To: "payments", Count: 1},
				{From: "k8s:" + clusterB + "/batch/Deployment/already", To: "payments", Count: 1},
			},
		}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{
			Cluster:  clusterOf(clusterB, "us-prod", ClusterNameFromContext),
			Targets:  []RawTarget{workload("payments", "payments")},
			Observed: []ObservedEdge{{From: "k8s:batch/Deployment/legacy", To: "payments", Count: 1}},
		}),
	}
	for _, id := range callerOf {
		srcs = append(srcs, NewMemorySource(id, "observation", &Collection{
			Observed: []ObservedEdge{{From: "k8s:batch/Deployment/legacy", To: "payments", Count: 1}},
		}))
	}
	return srcs
}

func TestTwoClustersSameCallerTwoUncontractedCallers(t *testing.T) {
	snap := buildClusterSnap(t, callerClusterSources("traces")...)
	got := callerNames(snap)
	want := []string{
		"k8s:" + clusterA + "/batch/Deployment/legacy",
		"k8s:" + clusterB + "/batch/Deployment/already",
		"k8s:" + clusterB + "/batch/Deployment/legacy",
		"k8s:batch/Deployment/legacy",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("callers = %v, want %v", got, want)
	}
}

func TestCallersQualifiedOnlyInMultiCluster(t *testing.T) {
	snap := buildClusterSnap(t, NewMemorySource("eu", SourceKindKubernetes, &Collection{
		Cluster:  clusterOf(clusterA, "eu-prod", ClusterNameFromContext),
		Targets:  []RawTarget{workload("payments", "payments")},
		Observed: []ObservedEdge{{From: "k8s:batch/Deployment/legacy", To: "payments", Count: 1}},
	}))
	if got := callerNames(snap); !slices.Equal(got, []string{"k8s:batch/Deployment/legacy"}) {
		t.Errorf("one cluster keeps the caller string unchanged: %v", got)
	}
}

func TestRestrictReadsQualifiedCallerNamespace(t *testing.T) {
	q := NewQuery(buildClusterSnap(t, callerClusterSources()...))
	if ns := q.Namespaces(); !slices.Equal(ns, []string{"batch", "payments"}) {
		t.Fatalf("Namespaces reads the namespace of a qualified caller: %v", ns)
	}
	s := q.Restrict(func(ns string) bool { return ns != "batch" }).SnapshotForSerialization()
	if len(s.UncontractedCallers) != 0 {
		t.Errorf("callers in a hidden namespace are hidden: %v", callerNames(s))
	}
	svc := s.Services[NewServiceKey("payments")]
	if svc == nil || svc.HiddenDependents != 3 {
		t.Errorf("the hidden callers are counted into payments: %+v", svc)
	}
}

func TestRestrictNarrowsClusters(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{workload("orders", "orders")}}),
	)
	s := NewQuery(snap).Restrict(func(ns string) bool { return ns != "orders" }).SnapshotForSerialization()
	if len(s.Clusters) != 1 || s.Clusters[0].ID != clusterA {
		t.Errorf("only the cluster of a visible target stays: %+v", s.Clusters)
	}
	if len(snap.Clusters) != 2 {
		t.Errorf("Restrict must not edit the original: %+v", snap.Clusters)
	}
}

func TestRestrictRecountsClusterUnknown(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("shop", "payments")}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{workload("shop", "orders")}}),
		NewMemorySource("inventory", "inventory", &Collection{Targets: []RawTarget{workload("shop", "ledger"), workload("vault", "keys"), workload("vault", "audit")}}),
	)
	if l := limitationsWith(snap, LimitationClusterUnknown); len(l) != 1 || !strings.HasPrefix(l[0].Message, "3 targets") {
		t.Fatalf("unrestricted: %+v", l)
	}
	s := NewQuery(snap).Restrict(func(ns string) bool { return ns != "vault" }).SnapshotForSerialization()
	l := limitationsWith(s, LimitationClusterUnknown)
	if len(l) != 1 || l[0].Source != "inventory" || !strings.HasPrefix(l[0].Message, "1 targets") || strings.Contains(l[0].Message, "3") {
		t.Errorf("restricted CLUSTER_UNKNOWN counts only the visible clusterless target: %+v", l)
	}
	none := NewQuery(snap).Restrict(func(ns string) bool { return ns != "vault" && ns != "shop" }).SnapshotForSerialization()
	if l := limitationsWith(none, LimitationClusterUnknown); len(l) != 0 {
		t.Errorf("no visible clusterless target, no CLUSTER_UNKNOWN: %+v", l)
	}
}

func TestRestrictRecountsSingleClusterUnknown(t *testing.T) {
	inA := clusterOf(clusterA, "eu-prod", ClusterNameFromContext)
	merged := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: inA, Targets: []RawTarget{workload("shop", "payments"), workload("vault", "keys")}}),
		NewMemorySource("inventory", "inventory", &Collection{Targets: []RawTarget{workload("shop", "payments"), workload("vault", "keys")}}),
	)
	s := NewQuery(merged).Restrict(func(ns string) bool { return ns != "vault" }).SnapshotForSerialization()
	l := limitationsWith(s, LimitationClusterUnknown)
	if len(l) != 1 || l[0].Source != "inventory" || !strings.Contains(l[0].Message, "shop/kubernetes/payments") || strings.Contains(l[0].Message, "vault") {
		t.Errorf("merged keys are rebuilt from visible targets only: %+v", l)
	}
	info := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: inA, Targets: []RawTarget{workload("shop", "payments")}}),
		NewMemorySource("inventory", "inventory", &Collection{Targets: []RawTarget{workload("shop", "ledger"), workload("vault", "keys")}}),
	)
	s = NewQuery(info).Restrict(func(ns string) bool { return ns != "vault" }).SnapshotForSerialization()
	if l := limitationsWith(s, LimitationClusterUnknown); len(l) != 1 || l[0].Source != "" || !strings.HasPrefix(l[0].Message, "1 targets") {
		t.Errorf("informational count is rebuilt from visible targets: %+v", l)
	}
}
