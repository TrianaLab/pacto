package fleet

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// unreadCarrier is the hub's carrier of n unread clusters.
func unreadCarrier(n int) Source {
	return NewMemorySource("cluster-joins", SourceKindClusterJoins, &Collection{UnreadClusters: n, Limitations: []Limitation{{
		Code: LimitationClustersUnread, Message: "2 clusters publish to ghcr.io/acme/pacto-targets and are not members of this hub, so consumers there are unknown",
	}}})
}

// TestUnreadSourceReadsNoRuntime: the carrier is no source state, so it
// reads no runtime, verifies no declaration and leaves every count as is.
func TestUnreadSourceReadsNoRuntime(t *testing.T) {
	eu, us := clusterOf(clusterA, "eu-prod", ClusterNameFromContext), clusterOf(clusterB, "us-prod", ClusterNameFromContext)
	fleets := map[string][]Source{
		"registry only": {NewMemorySource("oci", "oci", &Collection{})},
		"two clusters": {
			NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: eu, Targets: []RawTarget{workload("prod", "payments")}, State: &SourceState{Namespaces: []string{"prod"}, NamespacesTotal: 1}}),
			NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: us, Targets: []RawTarget{workload("prod", "ledger")}}),
		},
	}
	for name, sources := range fleets {
		t.Run(name, func(t *testing.T) {
			without := buildClusterSnap(t, sources...)
			with := buildClusterSnap(t, append(slices.Clone(sources), unreadCarrier(2))...)
			if with.UnreadClusters != 2 || without.UnreadClusters != 0 {
				t.Fatalf("unread %d / %d", with.UnreadClusters, without.UnreadClusters)
			}
			if hasTargetSource(with) != hasTargetSource(without) {
				t.Error("the carrier reads a runtime")
			}
			if len(with.Sources) != len(without.Sources) || !reflect.DeepEqual(sourceCounts(with.Sources), sourceCounts(without.Sources)) {
				t.Errorf("sources %+v", with.Sources)
			}
			if with.Completeness != without.Completeness {
				t.Errorf("completeness %s / %s", with.Completeness, without.Completeness)
			}
			vw, vo := declarationVerified(with), declarationVerified(without)
			for _, c := range []string{"", clusterA, clusterB} {
				for _, ns := range []string{"", "prod", "audit"} {
					if vw(c, ns) != vo(c, ns) {
						t.Errorf("declarationVerified(%q, %q) differs", c, ns)
					}
				}
			}
			if !slices.ContainsFunc(with.Limitations, func(l Limitation) bool { return l.Code == LimitationClustersUnread && l.Source == "cluster-joins" }) {
				t.Errorf("limitations %+v", with.Limitations)
			}
		})
	}
}

// TestUnreadClustersSurviveLimitationCap: CLUSTERS_UNREAD is about the
// whole answer, so the cap keeps it.
func TestUnreadClustersSurviveLimitationCap(t *testing.T) {
	ls := make([]Limitation, 0, MaxMetaLimitations+2)
	for range MaxMetaLimitations + 1 {
		ls = append(ls, Limitation{Code: LimitationSourceRecordInvalid})
	}
	ls = append(ls, Limitation{Code: LimitationClustersUnread})
	kept, trunc := BoundLimitations(ls)
	if !trunc || !slices.ContainsFunc(kept, func(l Limitation) bool { return l.Code == LimitationClustersUnread }) {
		t.Fatalf("CLUSTERS_UNREAD dropped by the cap")
	}
}

// TestRestrictKeepsClustersUnread: a hidden service whose name is in the
// limitation's prose never drops it, and the count stays.
func TestRestrictKeepsClustersUnread(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("secret", "cluster"), workload("secret", "acme"), workload("prod", "web")}}),
		unreadCarrier(2))
	view := NewQuery(snap).Restrict(func(_, ns string) bool { return ns == "prod" }, nil).SnapshotForSerialization()
	if view.Services[NewServiceKey("acme")] != nil {
		t.Fatal("acme is visible")
	}
	if view.UnreadClusters != 2 {
		t.Errorf("unread = %d", view.UnreadClusters)
	}
	if !slices.ContainsFunc(view.Limitations, func(l Limitation) bool {
		return l.Code == LimitationClustersUnread && strings.Contains(l.Message, "acme")
	}) {
		t.Errorf("limitations %+v", view.Limitations)
	}
}

// TestClusterSummaryAsOfOldest: a cluster's asOf is the oldest last
// successful read among its sources.
func TestClusterSummaryAsOfOldest(t *testing.T) {
	eu, us := clusterOf(clusterA, "eu-prod", ClusterNameFromContext), clusterOf(clusterB, "us-prod", ClusterNameFromContext)
	old := fixedNow().Add(-14 * time.Minute)
	snap := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: eu, Targets: []RawTarget{workload("prod", "web")}}),
		NewMemorySource("eu-snap", SourceKindTargetSnapshot, &Collection{Cluster: eu, State: StaleState(old, nil)}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: us, Targets: []RawTarget{workload("prod", "pay")}}),
	)
	got := map[string]ClusterSummary{}
	for _, c := range NewQuery(snap).ClusterSummaries() {
		got[c.ID] = c
	}
	if a := got[clusterA]; a.AsOf == nil || !a.AsOf.Equal(old) || a.Status != SourceStale {
		t.Errorf("eu %+v", a)
	}
	if b := got[clusterB]; b.AsOf == nil || !b.AsOf.Equal(fixedNow()) {
		t.Errorf("us %+v", b)
	}
	if olderSync(nil, nil) != nil {
		t.Error("never read has no asOf")
	}
}
