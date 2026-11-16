package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	clusterA = "9f1c4e2a-0000-4000-8000-00000000000a"
	clusterB = "77ad31b0-0000-4000-8000-00000000000b"
)

func clusterOf(id, name, source string) *Cluster {
	return &Cluster{ID: id, Name: name, NameSource: source}
}

func workload(scope, name string) RawTarget {
	return RawTarget{Scope: scope, Kind: "kubernetes", Name: name, Service: name, Compliance: StatusCompliant, EvidenceAt: new(fixedNow())}
}

func buildClusterSnap(t *testing.T, sources ...Source) *FleetSnapshot {
	t.Helper()
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func clusterSourceStatus(t *testing.T, snap *FleetSnapshot, id string) SourceStatus {
	t.Helper()
	for _, st := range snap.Sources {
		if st.ID == id {
			return st.Status
		}
	}
	t.Fatalf("no source %q in %+v", id, snap.Sources)
	return ""
}

func limitationsWith(snap *FleetSnapshot, code string) []Limitation {
	var out []Limitation
	for _, l := range snap.Limitations {
		if l.Code == code {
			out = append(out, l)
		}
	}
	return out
}

func singleTarget(t *testing.T, snap *FleetSnapshot) *TargetRecord {
	t.Helper()
	if len(snap.Targets) != 1 {
		t.Fatalf("want one target, got %d: %v", len(snap.Targets), snap.Targets)
	}
	for _, tr := range snap.Targets {
		return tr
	}
	return nil
}

func TestSingleClusterKeepsThreePartKeys(t *testing.T) {
	snap := buildClusterSnap(t, NewMemorySource("k8s", SourceKindKubernetes, &Collection{
		Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext),
		Targets: []RawTarget{workload("payments", "payments")},
	}))
	key := NewTargetKey("payments", "kubernetes", "payments")
	tr := snap.Targets[key]
	if tr == nil {
		t.Fatalf("want the 3-part key %q, got %v", key, snap.Targets)
	}
	if tr.Cluster != clusterA || tr.ClusterName != "eu-prod" || tr.ClusterNameSource != ClusterNameFromContext || tr.ClusterRelation != ClusterRelationRunsIn {
		t.Errorf("cluster fields = %+v", tr)
	}
	if len(snap.Clusters) != 1 || snap.Clusters[0].ID != clusterA || strings.Join(snap.Clusters[0].Sources, ",") != "k8s" {
		t.Errorf("Clusters = %+v", snap.Clusters)
	}
	if len(limitationsWith(snap, LimitationClusterUnknown)) != 0 || snap.Completeness != CompletenessComplete {
		t.Errorf("one cluster with no clusterless target is complete: %s %+v", snap.Completeness, snap.Limitations)
	}
}

func TestTwoClustersQualifyEveryTargetKey(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{workload("orders", "orders")}}),
	)
	for _, key := range []TargetKey{
		NewClusterTargetKey(clusterA, "payments", "kubernetes", "payments"),
		NewClusterTargetKey(clusterB, "orders", "kubernetes", "orders"),
	} {
		if snap.Targets[key] == nil {
			t.Errorf("missing %q in %v", key, snap.Targets)
		}
	}
	if len(snap.Clusters) != 2 || snap.Clusters[0].ID != clusterB || snap.Clusters[1].ID != clusterA {
		t.Errorf("Clusters must be sorted by id: %+v", snap.Clusters)
	}
}

func TestMergeTargetWithinOneCluster(t *testing.T) {
	inA := clusterOf(clusterA, "eu-prod", ClusterNameFromContext)
	snap := buildClusterSnap(t,
		NewMemorySource("operator", SourceKindKubernetes, &Collection{Cluster: inA, Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("inventory", "inventory", &Collection{Cluster: inA, Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{workload("orders", "orders")}}),
	)
	tr := snap.Targets[NewClusterTargetKey(clusterA, "payments", "kubernetes", "payments")]
	if tr == nil || strings.Join(tr.Sources, ",") != "inventory,operator" {
		t.Fatalf("one target with both sources, got %+v", tr)
	}
	if snap.Completeness != CompletenessComplete {
		t.Errorf("a merge inside one cluster is complete: %+v", snap.Limitations)
	}
}

func TestMergeFillsClusterFromLaterSource(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("inventory", "inventory", &Collection{Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
	)
	tr := singleTarget(t, snap)
	if tr.Cluster != clusterA || tr.ClusterName != "eu-prod" || tr.ClusterNameSource != ClusterNameFromContext || tr.ClusterRelation != ClusterRelationRunsIn {
		t.Errorf("the merged target must carry the clustered contributor's cluster: %+v", tr)
	}
}

func TestNoMergeAcrossClusters(t *testing.T) {
	a := workload("payments", "payments")
	b := workload("payments", "payments")
	b.Compliance, b.Digest = StatusNonCompliant, "sha256:other"
	snap := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{a}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{b}}),
	)
	if len(snap.Targets) != 2 {
		t.Fatalf("two clusters keep two targets, got %v", snap.Targets)
	}
	for _, tr := range snap.Targets {
		if tr.Quarantined || len(tr.Sources) != 1 {
			t.Errorf("no merge across clusters: %+v", tr)
		}
	}
	if hasLim(snap.Limitations, LimitationTargetFieldConflict) || hasLim(snap.Limitations, LimitationTargetRefConflict) {
		t.Errorf("no merge limitation across clusters: %+v", snap.Limitations)
	}
}

func evidenceTarget(cluster string, at time.Time) RawTarget {
	return RawTarget{Scope: "ci", Kind: "external", Name: "payments", Service: "payments", Compliance: StatusCompliant,
		EvidenceAt: new(at), ReconciledAt: new(at.Add(time.Second)), Cluster: clusterOf(cluster, "", "")}
}

func TestTwoEvidenceSourcesSameProducerTwoTargets(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("evidence-eu", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{evidenceTarget(clusterA, fixedNow())}}),
		NewMemorySource("evidence-us", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{evidenceTarget(clusterB, fixedNow().Add(-time.Minute))}}),
	)
	for _, c := range []string{clusterA, clusterB} {
		tr := snap.Targets[NewClusterTargetKey(c, "ci", "external", "payments")]
		if tr == nil || tr.Cluster != c || tr.ClusterRelation != ClusterRelationReportedThrough {
			t.Errorf("cluster %s: want its own reported-through target, got %+v", c, tr)
		}
	}
	if len(snap.Targets) != 2 || hasLim(snap.Limitations, LimitationClusterConflict) {
		t.Errorf("two evidence records give two targets and no conflict: %v %+v", snap.Targets, snap.Limitations)
	}
}

func TestUnknownClusterInMultiClusterSnapshotIsPartial(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{workload("orders", "orders")}}),
		NewMemorySource("file", SourceKindTargetState, &Collection{Targets: []RawTarget{workload("payments", "payments")}}),
		NewFailingSource("down", SourceKindKubernetes, errors.New("unreachable")),
		&MemorySource{id: "empty", kind: "inventory", collect: func(context.Context) (*Collection, error) { return nil, nil }},
	)
	if got := clusterSourceStatus(t, snap, "file"); got != SourcePartial {
		t.Errorf("the clusterless source is %s, want partial", got)
	}
	lims := limitationsWith(snap, LimitationClusterUnknown)
	if len(lims) != 1 || lims[0].Source != "file" || !strings.Contains(lims[0].Message, "1 targets") {
		t.Errorf("CLUSTER_UNKNOWN = %+v", lims)
	}
	if snap.Completeness != CompletenessPartial {
		t.Errorf("completeness = %s", snap.Completeness)
	}
	if snap.Targets[NewTargetKey("payments", "kubernetes", "payments")] == nil {
		t.Errorf("a clusterless target keeps its 3-part key: %v", snap.Targets)
	}
}

func TestUnknownClusterMergeInSingleClusterSnapshotIsPartial(t *testing.T) {
	key := NewTargetKey("payments", "kubernetes", "payments")
	for name, col := range map[string]*Collection{
		"available": {Targets: []RawTarget{workload("payments", "payments")}},
		// A source already partial stays partial and still says why.
		"already partial": {Targets: []RawTarget{workload("payments", "payments")}, Limitations: []Limitation{{Code: LimitationSourceRecordInvalid, Message: "bad"}}},
	} {
		t.Run(name, func(t *testing.T) {
			snap := buildClusterSnap(t,
				NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
				NewMemorySource("file", SourceKindTargetState, col),
			)
			if got := clusterSourceStatus(t, snap, "file"); got != SourcePartial {
				t.Errorf("the clusterless source is %s, want partial", got)
			}
			if got := clusterSourceStatus(t, snap, "k8s"); got != SourceAvailable {
				t.Errorf("the clustered source is %s, want available", got)
			}
			lims := limitationsWith(snap, LimitationClusterUnknown)
			if len(lims) != 1 || lims[0].Source != "file" || !strings.Contains(lims[0].Message, string(key)) {
				t.Errorf("CLUSTER_UNKNOWN must name the merged key: %+v", lims)
			}
			if snap.Completeness != CompletenessPartial {
				t.Errorf("completeness = %s", snap.Completeness)
			}
		})
	}
}

func TestUnknownClusterMergeListsAtMostTwentyKeys(t *testing.T) {
	var targets []RawTarget
	for i := range 21 {
		targets = append(targets, workload("ns", "w"+strings.Repeat("x", i)))
	}
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: targets}),
		NewMemorySource("file", SourceKindTargetState, &Collection{Targets: targets}),
	)
	lims := limitationsWith(snap, LimitationClusterUnknown)
	if len(lims) != 1 || !strings.HasSuffix(lims[0].Message, " and 1 more") {
		t.Errorf("CLUSTER_UNKNOWN must bound its key list: %+v", lims)
	}
}

// TestUnknownClusterMergeListsKeysSorted: Build lists the merged keys sorted,
// like the CLUSTER_UNKNOWN a restricted view rebuilds, so one set of keys reads
// the same with and without a restriction.
func TestUnknownClusterMergeListsKeysSorted(t *testing.T) {
	targets := []RawTarget{workload("ns", "b"), workload("ns", "a")}
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: targets}),
		NewMemorySource("file", SourceKindTargetState, &Collection{Targets: targets}),
	)
	lims := limitationsWith(snap, LimitationClusterUnknown)
	if len(lims) != 1 || !strings.HasSuffix(lims[0].Message, ": ns/kubernetes/a, ns/kubernetes/b") {
		t.Errorf("CLUSTER_UNKNOWN must list the merged keys sorted: %+v", lims)
	}
}

func TestUnknownClusterInSingleClusterSnapshotIsInformational(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("file", SourceKindTargetState, &Collection{Targets: []RawTarget{workload("orders", "orders")}}),
	)
	lims := limitationsWith(snap, LimitationClusterUnknown)
	if len(lims) != 1 || lims[0].Source != "" {
		t.Errorf("want one snapshot-level CLUSTER_UNKNOWN, got %+v", lims)
	}
	if snap.Completeness != CompletenessComplete || clusterSourceStatus(t, snap, "file") != SourceAvailable {
		t.Errorf("an informational CLUSTER_UNKNOWN changes no completeness: %s %+v", snap.Completeness, snap.Sources)
	}
}

func TestNoClusterKnownEmitsNoClusterLimitation(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("file", SourceKindTargetState, &Collection{Targets: []RawTarget{workload("orders", "orders")}}),
	)
	if len(snap.Limitations) != 0 || snap.Clusters != nil || snap.Completeness != CompletenessComplete {
		t.Errorf("no cluster known: no limitation, no clusters, complete; got %+v %+v %s", snap.Limitations, snap.Clusters, snap.Completeness)
	}
	for k, tr := range snap.Targets {
		if tr.Cluster != "" || tr.ClusterRelation != "" || k != NewTargetKey(tr.Scope, tr.Kind, tr.Name) {
			t.Errorf("a clusterless target is unchanged: %q %+v", k, tr)
		}
	}
}

func TestClusterIdentityValidatedAtIngest(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		snap := buildClusterSnap(t, NewMemorySource("file", SourceKindTargetState, &Collection{
			Cluster: clusterOf("-not a uid", "eu-prod", ClusterNameFromFile), Targets: []RawTarget{workload("payments", "payments")},
		}))
		if snap.Clusters != nil || singleTarget(t, snap).Cluster != "" {
			t.Errorf("an invalid id is dropped: %+v", snap.Clusters)
		}
		lims := limitationsWith(snap, LimitationSourceRecordInvalid)
		if len(lims) != 1 || lims[0].Source != "file" || clusterSourceStatus(t, snap, "file") != SourcePartial {
			t.Errorf("an invalid id is a record problem of its source: %+v %+v", lims, snap.Sources)
		}
	})
	for name, c := range map[string]*Cluster{
		"over-long name":      clusterOf(clusterA, strings.Repeat("n", 64), ClusterNameFromFile),
		"control character":   clusterOf(clusterA, "eu\x00prod", ClusterNameFromFile),
		"unknown name source": clusterOf(clusterA, "eu-prod", "cloud"),
	} {
		t.Run(name, func(t *testing.T) {
			snap := buildClusterSnap(t, NewMemorySource("file", SourceKindTargetState, &Collection{Cluster: c, Targets: []RawTarget{workload("payments", "payments")}}))
			tr := singleTarget(t, snap)
			if tr.Cluster != clusterA || tr.ClusterName != clusterA[:8] || tr.ClusterNameSource != ClusterNameFromUID || tr.ClusterNameReportedBy != "" {
				t.Errorf("an invalid name falls back to the uid: %+v", tr)
			}
			if clusterSourceStatus(t, snap, "file") != SourceAvailable {
				t.Errorf("an invalid name drops only the name: %+v", snap.Sources)
			}
		})
	}
}

func TestClusterNameRankedBySource(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("file", SourceKindTargetState, &Collection{Cluster: clusterOf(clusterA, "from-file", ClusterNameFromFile), Targets: []RawTarget{workload("a", "a")}}),
		NewMemorySource("ev-1", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{{Scope: "ci", Kind: "external", Name: "x", Cluster: clusterOf(clusterA, "from-label", ClusterNameFromNodeLabel)}}}),
		NewMemorySource("ev-2", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{{Scope: "ci", Kind: "external", Name: "y", Cluster: clusterOf(clusterA, "later-label", ClusterNameFromNodeLabel)}}}),
	)
	c := snap.Clusters[0]
	if c.Name != "from-label" || c.NameSource != ClusterNameFromNodeLabel || c.ReportedBy != "ev-1" {
		t.Errorf("node-label outranks file and the earlier source breaks a tie: %+v", c)
	}
	if strings.Join(c.Sources, ",") != "file,ev-1,ev-2" {
		t.Errorf("every reporting source is listed: %v", c.Sources)
	}
}

func TestReportedClusterNameNeverOverridesLiveName(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("evidence-x", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{evidenceTarget(clusterA, fixedNow())}}),
		NewMemorySource("rogue", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{{Scope: "ci", Kind: "external", Name: "y", Cluster: clusterOf(clusterA, "prod-DRAINED", ClusterNameFromNodeLabel)}}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
	)
	c := snap.Clusters[0]
	if c.Name != "eu-prod" || c.NameSource != ClusterNameFromContext || c.ReportedBy != "" {
		t.Errorf("the name the process read wins: %+v", c)
	}

	reported := buildClusterSnap(t,
		NewMemorySource("evidence-x", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{{Scope: "ci", Kind: "external", Name: "y", Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromNodeLabel)}}}),
	)
	if tr := singleTarget(t, reported); tr.ClusterName != "eu-prod" || tr.ClusterNameReportedBy != "evidence-x" {
		t.Errorf("a reported name keeps who reported it: %+v", tr)
	}
}

func TestClusterRelation(t *testing.T) {
	inA := clusterOf(clusterA, "eu-prod", ClusterNameFromContext)
	cases := map[string]struct {
		kind string
		want string
	}{
		"kubernetes":   {SourceKindKubernetes, ClusterRelationRunsIn},
		"evidence":     {SourceKindEvidenceHTTP, ClusterRelationReportedThrough},
		"target state": {SourceKindTargetState, ClusterRelationReportedThrough},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			snap := buildClusterSnap(t, NewMemorySource("s", c.kind, &Collection{Cluster: inA, Targets: []RawTarget{workload("payments", "payments")}}))
			if got := singleTarget(t, snap).ClusterRelation; got != c.want {
				t.Errorf("relation = %q, want %q", got, c.want)
			}
		})
	}
	// Merged, runs-in wins whatever the order.
	k8s := NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: inA, Targets: []RawTarget{workload("payments", "payments")}})
	file := NewMemorySource("file", SourceKindTargetState, &Collection{Cluster: inA, Targets: []RawTarget{workload("payments", "payments")}})
	for _, order := range [][]Source{{k8s, file}, {file, k8s}} {
		if got := singleTarget(t, buildClusterSnap(t, order...)).ClusterRelation; got != ClusterRelationRunsIn {
			t.Errorf("merged relation = %q, want runs-in", got)
		}
	}
}

func TestEvidenceRecordReportedWithTwoClustersConflicts(t *testing.T) {
	noTimes := RawTarget{Scope: "ci", Kind: "external", Name: "other", Cluster: clusterOf(clusterB, "", "")}
	snap := buildClusterSnap(t,
		NewMemorySource("ev-1", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{evidenceTarget(clusterA, fixedNow())}}),
		NewMemorySource("ev-2", SourceKindEvidenceHTTP, &Collection{Targets: []RawTarget{evidenceTarget(clusterB, fixedNow()), evidenceTarget(clusterA, fixedNow()), noTimes}}),
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterB, "us", ClusterNameFromContext), Targets: []RawTarget{workload("ci", "payments")}}),
	)
	lims := limitationsWith(snap, LimitationClusterConflict)
	if len(lims) != 1 || lims[0].Source != "ev-2" || !strings.Contains(lims[0].Message, " 1 evidence records") {
		t.Errorf("want one CLUSTER_CONFLICT on the later source, got %+v", lims)
	}
	if clusterSourceStatus(t, snap, "ev-2") != SourcePartial || clusterSourceStatus(t, snap, "ev-1") != SourceAvailable {
		t.Errorf("the later source is partial: %+v", snap.Sources)
	}
	for _, c := range []string{clusterA, clusterB} {
		if snap.Targets[NewClusterTargetKey(c, "ci", "external", "payments")] == nil {
			t.Errorf("both targets stay: %v", snap.Targets)
		}
	}
}

func TestDisplayNameMultiCluster(t *testing.T) {
	snap := buildClusterSnap(t,
		NewMemorySource("eu", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
		NewMemorySource("us", SourceKindKubernetes, &Collection{Cluster: clusterOf(clusterB, "us-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")}}),
	)
	tr := snap.Targets[NewClusterTargetKey(clusterA, "payments", "kubernetes", "payments")]
	if got := tr.DisplayName(); got != "eu-prod / payments / payments" {
		t.Errorf("DisplayName = %q", got)
	}
}

func TestDisplayNameSingleClusterUnchanged(t *testing.T) {
	snap := buildClusterSnap(t, NewMemorySource("eu", SourceKindKubernetes, &Collection{
		Cluster: clusterOf(clusterA, "eu-prod", ClusterNameFromContext), Targets: []RawTarget{workload("payments", "payments")},
	}))
	if got := singleTarget(t, snap).DisplayName(); got != "payments/kubernetes/payments" {
		t.Errorf("DisplayName = %q", got)
	}
}
