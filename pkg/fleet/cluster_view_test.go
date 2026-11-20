package fleet

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func clusterHidden(s *FleetSnapshot) []Limitation { return limitationsWith(s, LimitationClusterHidden) }

func clusterRecord(s *FleetSnapshot, id string) *ClusterRecord {
	for i := range s.Clusters {
		if s.Clusters[i].ID == id {
			return &s.Clusters[i]
		}
	}
	return nil
}

func TestWithClusterAccessNamesHiddenClusters(t *testing.T) {
	full := twoClusterFleet(t)
	euView := full.Restrict(euOnly, everyWorkloadIn)
	cases := map[string]string{
		ClusterNoAccess:      "cluster us-prod: you may read no namespace there",
		ClusterReviewFailed:  "cluster us-prod: the review failed or took longer than 5s, so its services are hidden",
		ClusterNotReviewable: "cluster us-prod: Pacto cannot review you there, so its services are hidden",
	}
	for access, msg := range cases {
		t.Run(access, func(t *testing.T) {
			v := euView.WithClusterAccess(full, map[string]string{clusterB: access})
			s := v.SnapshotForSerialization()
			if got := clusterHidden(s); len(got) != 1 || got[0].Source != clusterB || got[0].Message != msg {
				t.Errorf("CLUSTER_HIDDEN = %+v, want one for %s: %q", got, clusterB, msg)
			}
			rec := clusterRecord(s, clusterB)
			if rec == nil || rec.Access != access || rec.Name != "us-prod" {
				t.Errorf("us record re-added with access %q: %+v", access, rec)
			}
			if a := clusterRecord(s, clusterA); a == nil || a.Access != "" {
				t.Errorf("eu record kept unmarked: %+v", a)
			}
			checkAccessedView(t, euView, v)
		})
	}
}

// TestWithClusterAccessUnidentifiedCluster: the one cluster whose id was
// never read is named as the Kubernetes cluster.
func TestWithClusterAccessUnidentifiedCluster(t *testing.T) {
	full := twoClusterFleet(t)
	v := full.Restrict(euOnly, everyWorkloadIn).WithClusterAccess(full, map[string]string{"": ClusterReviewFailed})
	got := clusterHidden(v.SnapshotForSerialization())
	if len(got) != 1 || got[0].Source != "" || got[0].Message != "the Kubernetes cluster: the review failed or took longer than 5s, so its services are hidden" {
		t.Errorf("CLUSTER_HIDDEN = %+v", got)
	}
}

// checkAccessedView checks what WithClusterAccess changed from view to v.
func checkAccessedView(t *testing.T, view, v *Query) {
	t.Helper()
	if v.SnapshotForSerialization().Completeness != CompletenessPartial || v.SnapshotID() == view.SnapshotID() {
		t.Errorf("completeness %q, id %s (was %s)", v.SnapshotForSerialization().Completeness, v.SnapshotID(), view.SnapshotID())
	}
	if len(clusterHidden(view.SnapshotForSerialization())) != 0 || clusterRecord(view.SnapshotForSerialization(), clusterB) != nil {
		t.Error("WithClusterAccess changed the view it was called on")
	}
	// A later restriction that hides more drops the marks; the caller marks
	// the clusters that view hides again.
	euNoShop := func(c, ns string) bool { return c == clusterA && ns != "shop" }
	if again := v.Restrict(euNoShop, everyWorkloadIn).SnapshotForSerialization(); len(clusterHidden(again)) != 0 {
		t.Errorf("a later Restrict kept CLUSTER_HIDDEN: %+v", clusterHidden(again))
	}
}

func TestWithClusterAccessEmptyIsIdentity(t *testing.T) {
	full := twoClusterFleet(t)
	view := full.Restrict(euOnly, everyWorkloadIn)
	for _, access := range []map[string]string{nil, {}} {
		if got := view.WithClusterAccess(full, access); got != view {
			t.Errorf("WithClusterAccess(%v) = a new query, want the same", access)
		}
	}
}

func TestWithClusterAccessKeepsReportedThroughRule(t *testing.T) {
	eu := clusterOf(clusterA, "eu-prod", ClusterNameFromContext)
	full := NewQuery(buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: eu, Targets: []RawTarget{workload("prod", "payments")}}),
		NewMemorySource("evidence", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{evidenceTarget(clusterB, fixedNow())}}),
	))
	view := full.Restrict(euOnly, everyWorkloadIn)
	s := view.WithClusterAccess(full, map[string]string{clusterB: ClusterNoAccess}).SnapshotForSerialization()
	if clusterRecord(s, clusterB) != nil {
		t.Errorf("a cluster only an Evidence Server reported is shown with no visible target: %+v", s.Clusters)
	}
	if got := clusterHidden(s); len(got) != 1 || got[0].Source != clusterB {
		t.Errorf("CLUSTER_HIDDEN = %+v", got)
	}
}

func TestMetaClustersMatchProductMeta(t *testing.T) {
	q := twoClusterFleet(t)
	meta := q.Status(StatusQuery{}).Meta
	if len(meta.Clusters) != 2 || meta.ClustersTruncated {
		t.Fatalf("Meta.Clusters = %+v", meta.Clusters)
	}
	if pm := q.ProductMeta(); !reflect.DeepEqual(meta.Clusters, pm.Clusters) || meta.ClustersTruncated != pm.ClustersTruncated {
		t.Errorf("Meta.Clusters %+v != ProductMeta.Clusters %+v", meta.Clusters, pm.Clusters)
	}
	for _, c := range meta.Clusters {
		if c.Status != SourcePartial || c.Targets == 0 || c.NameSource != ClusterNameFromContext {
			t.Errorf("summary = %+v", c)
		}
	}
}

func TestProductMetaClustersStatusIsWorstSource(t *testing.T) {
	eu := clusterOf(clusterA, "eu-prod", ClusterNameFromContext)
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: eu, Targets: []RawTarget{workload("prod", "payments")}}),
		NewMemorySource("eu-file", SourceKindTargetState, &Collection{Cluster: eu, State: StaleState(fixedNow(), errors.New("gone"))}),
	))
	got := q.ProductMeta().Clusters
	if len(got) != 1 || got[0].Status != SourceStale || got[0].Targets != 1 || got[0].Name != "eu-prod" {
		t.Errorf("clusters = %+v, want one stale summary with one target", got)
	}
}

func TestProductMetaListsClusterlessKubernetesSource(t *testing.T) {
	eu := clusterOf(clusterA, "eu-prod", ClusterNameFromContext)
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: eu, Targets: []RawTarget{workload("prod", "payments")}}),
		NewFailingSource("us-prod", SourceKindKubernetes, errors.New("dial tcp: refused")),
		NewFailingSource("us-prod-workloads", SourceKindKubernetesWorkloads, errors.New("dial tcp: refused")),
	))
	got := q.ProductMeta().Clusters
	want := ClusterSummary{Name: "us-prod", Status: SourceUnavailable}
	if len(got) != 2 || got[0].ID != clusterA || got[1] != want {
		t.Errorf("clusters = %+v, want eu then %+v", got, want)
	}
}

func TestProductMetaClustersAbsentWithoutCluster(t *testing.T) {
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("prod", "payments")}})))
	for name, v := range map[string]any{"ProductMeta": q.ProductMeta(), "Meta": q.Status(StatusQuery{}).Meta} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"clusters"`) {
			t.Errorf("%s carries clusters with no cluster known: %s", name, b)
		}
	}
}

func TestProductMetaClustersBounded(t *testing.T) {
	s := &FleetSnapshot{}
	for i := range MaxMetaClusters + 2 {
		id := "c" + strconv.Itoa(i)
		s.Clusters = append(s.Clusters, ClusterRecord{Cluster: Cluster{ID: id}, Sources: []string{id}})
		st := SourceAvailable
		if i == MaxMetaClusters+1 {
			st = SourceUnavailable
		}
		s.Sources = append(s.Sources, SourceState{ID: id, Kind: SourceKindKubernetes, Status: st})
	}
	got, truncated := clusterSummaries(s)
	if len(got) != MaxMetaClusters || !truncated || got[0].ID != "c"+strconv.Itoa(MaxMetaClusters+1) {
		t.Errorf("bounded clusters: %d, truncated %v, first %+v", len(got), truncated, got[0])
	}
	if got, truncated := clusterSummaries(&FleetSnapshot{Clusters: s.Clusters[:2], Sources: s.Sources[:2]}); len(got) != 2 || truncated || got[0].ID != "c0" {
		t.Errorf("under the cap: %+v %v", got, truncated)
	}
}

func TestEntityFilterCluster(t *testing.T) {
	const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	eu, us := clusterOf(clusterA, "eu-prod", ClusterNameFromContext), clusterOf(clusterB, "us-prod", ClusterNameFromContext)
	pay := workload("prod", "payments")
	pay.Digest = digest
	k := &contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments", Version: "1.0.0"}}
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("oci", "local", &Collection{Revisions: []RawRevision{{Bundle: &contract.Bundle{Contract: k, FS: fstest.MapFS{}}, Digest: digest}}}),
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: eu, Targets: []RawTarget{pay, workload("shop", "shop")}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: us, Targets: []RawTarget{workload("prod", "orders")}}),
		workloadsSource("us-workloads", us, rawInferred("prod", "Deployment", "cart")),
	))
	keys := func(kind EntityKind, cluster string) []string {
		t.Helper()
		l, err := q.Entities(EntityFilter{Kinds: []EntityKind{kind}, Cluster: cluster})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range l.Entities {
			out = append(out, r.Label)
		}
		slices.Sort(out)
		return out
	}
	for _, c := range []struct {
		kind    EntityKind
		cluster string
		want    []string
	}{
		{KindTarget, clusterA, []string{"eu-prod / prod / payments", "eu-prod / shop / shop"}},
		{KindTarget, clusterB, []string{"us-prod / prod / orders"}},
		{KindService, clusterB, []string{"k8s:" + clusterB + "/prod/Deployment/cart", "orders"}},
		{KindService, clusterA, []string{"payments", "shop"}},
		{KindRevision, clusterA, []string{"payments 1.0.0"}},
		{KindRevision, clusterB, nil},
		{KindTarget, "no-such-cluster", nil},
		{KindSource, clusterB, []string{"eu", "oci", "us", "us-workloads"}},
	} {
		if got := keys(c.kind, c.cluster); !slices.Equal(got, c.want) {
			t.Errorf("%s in %s = %v, want %v", c.kind, c.cluster, got, c.want)
		}
	}
}

// TestEntityFilterClusterAnswersHonestly: with no kind, a cluster lists its
// services, revisions and targets only, never the whole fleet's owners and
// sources; an id no cluster has (a display name) says so in a limitation.
func TestEntityFilterClusterAnswersHonestly(t *testing.T) {
	eu, us := clusterOf(clusterA, "eu-prod", ClusterNameFromContext), clusterOf(clusterB, "us-prod", ClusterNameFromContext)
	q := NewQuery(buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: eu, Targets: []RawTarget{workload("prod", "payments")}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: us, Targets: []RawTarget{workload("prod", "orders")}}),
	))
	l, err := q.Entities(EntityFilter{Cluster: clusterB})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range l.Entities {
		if r.Kind != KindService && r.Kind != KindTarget && r.Kind != KindRevision {
			t.Errorf("a cluster-only query lists %s %s", r.Kind, r.Label)
		}
	}
	if hasLimitationCode(l.Meta, LimitationClusterFilterUnknown) {
		t.Error("a known cluster needs no limitation")
	}
	l, err = q.Entities(EntityFilter{Kinds: []EntityKind{KindTarget}, Cluster: "eu-prod"})
	if err != nil || l.Total != 0 || !hasLimitationCode(l.Meta, LimitationClusterFilterUnknown) {
		t.Errorf("a display name as the id: %v %+v", err, l.Meta.Limitations)
	}
}

// failingClusterSource is a live source of a known cluster that fails.
type failingClusterSource struct {
	Source
	c *Cluster
}

func (f failingClusterSource) Cluster() *Cluster { return f.c }

// TestClusterSummariesFoldFailedLiveSources: a failed workload source of an
// identified cluster lowers that cluster's status, and a cluster whose every
// source failed is listed under its own name, never as complete or as a
// raw source id.
func TestClusterSummariesFoldFailedLiveSources(t *testing.T) {
	k := SourceKindKubernetes
	eu, us := clusterOf(clusterA, "eu-prod", ClusterNameFromContext), clusterOf(clusterB, "us-prod", ClusterNameFromContext)
	ap := clusterOf("uid-ap", "ap-prod", ClusterNameFromContext)
	snap := buildClusterSnap(t,
		NewMemorySource(clusterA, k, &Collection{Cluster: eu, Targets: []RawTarget{workload("prod", "web")}}),
		NewMemorySource(clusterB, k, &Collection{Cluster: us, Targets: []RawTarget{workload("prod", "pay")}}),
		failingClusterSource{NewFailingSource(clusterB+"-workloads", SourceKindKubernetesWorkloads, errors.New("forbidden")), us},
		failingClusterSource{NewFailingSource("uid-ap", k, errors.New("connection refused")), ap},
		failingClusterSource{NewFailingSource("uid-ap-workloads", SourceKindKubernetesWorkloads, errors.New("connection refused")), ap},
	)
	got, _ := clusterSummaries(snap)
	want := map[string]ClusterSummary{
		clusterA: {ID: clusterA, Name: "eu-prod", NameSource: ClusterNameFromContext, Status: SourceAvailable, Targets: 1},
		clusterB: {ID: clusterB, Name: "us-prod", NameSource: ClusterNameFromContext, Status: SourceUnavailable, Targets: 1},
		"uid-ap": {ID: "uid-ap", Name: "ap-prod", NameSource: ClusterNameFromContext, Status: SourceUnavailable},
	}
	if len(got) != len(want) {
		t.Fatalf("summaries %+v", got)
	}
	for _, c := range got {
		if c != want[c.ID] {
			t.Errorf("summary %+v, want %+v", c, want[c.ID])
		}
	}
}
