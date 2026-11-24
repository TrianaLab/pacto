package impact

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata goldens")

const (
	euID = "9f1c4e2a-0000-4000-8000-00000000000a"
	usID = "77ad31b0-0000-4000-8000-00000000000b"
)

// clusterRev is a revision of name with deps, read from the registry.
func clusterRev(name string, deps ...contract.Dependency) fleet.RawRevision {
	return fleet.RawRevision{Bundle: mkBundle(svcContract(name, "1.0.0", contract.Owner{Team: name}, deps...))}
}

// onPayments is a required dependency on payments within rng.
func onPayments(rng string) contract.Dependency {
	return contract.Dependency{Name: "payments", Ref: "ghcr.io/acme/payments:1.0.0", Required: true, Compatibility: rng}
}

// clusterSource is a Kubernetes source of one cluster running svcs in prod,
// read at asOf (stale when it is before evidenceNow).
func clusterSource(id, name, nameSource string, asOf time.Time, svcs ...string) fleet.Source {
	col := &fleet.Collection{Cluster: &fleet.Cluster{ID: id, Name: name, NameSource: nameSource}}
	for _, s := range svcs {
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: "prod", Kind: "deployment", Name: s, Service: s, Compliance: fleet.StatusCompliant, EvidenceAt: &asOf})
	}
	if asOf.Before(evidenceNow) {
		col.State = fleet.StaleState(asOf, nil)
	}
	return fleet.NewMemorySource("k8s-"+name, fleet.SourceKindKubernetes, col)
}

// clusterFleet builds the registry revisions revs with the cluster sources.
func clusterFleet(t *testing.T, revs []fleet.RawRevision, sources ...fleet.Source) *fleet.FleetSnapshot {
	t.Helper()
	all := append([]fleet.Source{fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: revs})}, sources...)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, all...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// paymentsRevs: payments, checkout (refuses 2.0.0) and ledger (accepts it).
func paymentsRevs() []fleet.RawRevision {
	return []fleet.RawRevision{clusterRev("payments"), clusterRev("checkout", onPayments("^1.0.0")), clusterRev("ledger", onPayments(">=1.0.0"))}
}

// breakPayments analyzes a BREAKING payments 2.0.0 over snap.
func breakPayments(snap *fleet.FleetSnapshot) *Result {
	old := svcContract("payments", "1.0.0", contract.Owner{Team: "payments"})
	old.Interfaces = []contract.Interface{{Name: "api", Type: contract.InterfaceTypeGRPC, Ref: "api.proto"}}
	return Analyze(context.Background(), old, svcContract("payments", "2.0.0", contract.Owner{Team: "payments"}), fstest.MapFS{}, fstest.MapFS{}, snap, Options{})
}

// euUS is checkout in eu-prod and ledger in us-prod, us-prod read usAge ago.
func euUS(t *testing.T, usAge time.Duration) *fleet.FleetSnapshot {
	return clusterFleet(t, paymentsRevs(),
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "checkout", "payments"),
		clusterSource(usID, "us-prod", fleet.ClusterNameFromContext, evidenceNow.Add(-usAge), "ledger"))
}

func TestClustersEmptyWithOneCluster(t *testing.T) {
	snap := clusterFleet(t, paymentsRevs(), clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "checkout", "ledger"))
	res := breakPayments(snap)
	if res.Clusters != nil || res.UnreadClusters != 0 {
		t.Fatalf("clusters %+v unread %d", res.Clusters, res.UnreadClusters)
	}
	for _, c := range res.Consumers {
		if c.Clusters != nil {
			t.Errorf("%s clusters %v", c.Service, c.Clusters)
		}
	}
}

func TestImpactConsumerClustersUseDisplayNames(t *testing.T) {
	snap := clusterFleet(t, paymentsRevs(),
		clusterSource(euID, "9f1c4e2a", fleet.ClusterNameFromUID, evidenceNow, "checkout"),
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow.Add(-14*time.Minute), "payments"),
		clusterSource(usID, "us-prod", fleet.ClusterNameFromContext, evidenceNow, "ledger"))
	res := breakPayments(snap)
	names := map[string]string{}
	for _, c := range res.Clusters {
		names[c.ID] = c.Name
	}
	if names[euID] != "eu-prod" || names[usID] != "us-prod" {
		t.Fatalf("names %v", names)
	}
	if c := rowOf(t, res, "checkout"); !slices.Equal(c.Clusters, []string{euID}) {
		t.Errorf("checkout clusters %v", c.Clusters)
	}
	// A stale cluster is named by its display name, never its uid.
	if c := rowOf(t, res, "checkout"); strings.Contains(c.Note, euID) {
		t.Errorf("note names the uid: %q", c.Note)
	}
}

func TestImpactSameServiceTwoClusters(t *testing.T) {
	snap := clusterFleet(t, paymentsRevs(),
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "ledger"),
		clusterSource(usID, "us-prod", fleet.ClusterNameFromContext, evidenceNow, "ledger"))
	res := breakPayments(snap)
	c := rowOf(t, res, "ledger")
	if !slices.Equal(c.Clusters, []string{usID, euID}) || len(c.Targets) != 2 {
		t.Fatalf("ledger %+v", c)
	}
	if c.CompatibilityVerdict != CompatibilityCompatible {
		t.Errorf("two fresh clusters keep the verdict: %s", c.CompatibilityVerdict)
	}
}

func TestStaleClusterTurnsCompatibleUnknown(t *testing.T) {
	res := breakPayments(euUS(t, 14*time.Minute))
	c := rowOf(t, res, "ledger")
	if c.CompatibilityVerdict != CompatibilityUnknown || !strings.Contains(c.Note, "cluster us-prod last published 14m ago, so Pacto cannot tell which revision of ledger runs there now") {
		t.Fatalf("ledger %+v", c)
	}
	if !slices.Equal(c.Clusters, []string{usID}) {
		t.Errorf("clusters %v", c.Clusters)
	}
}

func TestOneStaleClusterTurnsCompatibleUnknown(t *testing.T) {
	snap := clusterFleet(t, paymentsRevs(),
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "ledger"),
		clusterSource(usID, "us-prod", fleet.ClusterNameFromContext, evidenceNow.Add(-3*time.Hour), "ledger"))
	c := rowOf(t, breakPayments(snap), "ledger")
	if c.CompatibilityVerdict != CompatibilityUnknown || !strings.Contains(c.Note, "cluster us-prod last published 3h ago") || strings.Contains(c.Note, "eu-prod") {
		t.Fatalf("ledger %+v", c)
	}
}

func TestStaleClusterKeepsIncompatible(t *testing.T) {
	snap := clusterFleet(t, paymentsRevs(),
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow.Add(-72*time.Hour), "checkout"),
		clusterSource(usID, "us-prod", fleet.ClusterNameFromContext, evidenceNow, "ledger"))
	res := breakPayments(snap)
	if c := rowOf(t, res, "checkout"); c.CompatibilityVerdict != CompatibilityIncompatible || strings.Contains(c.Note, "last published") {
		t.Fatalf("checkout %+v", c)
	}
}

func TestStaleClusterKeepsUnmet(t *testing.T) {
	unmet := []contract.Mismatch{{Consumer: "ledger"}}
	c := AffectedConsumer{Service: "ledger", CompatibilityVerdict: CompatibilityCompatible, Unmet: unmet, Clusters: []string{usID}}
	staleClustersUnknown(&c, map[string]fleet.ClusterSummary{usID: {ID: usID, Name: "us-prod", Status: fleet.SourceStale}}, evidenceNow)
	if c.CompatibilityVerdict != CompatibilityUnknown || len(c.Unmet) != 1 || !strings.Contains(c.Note, "us-prod could not be read") {
		t.Fatalf("%+v", c)
	}
	// A cluster with no status is unavailable; a policy row keeps its range verdict apart.
	r := AffectedConsumer{Service: "ref", CompatibilityVerdict: CompatibilityIncompatible, policyVerdict: CompatibilityIncompatible,
		rangeVerdict: CompatibilityCompatible, Note: "policy", Clusters: []string{"gone"}}
	staleClustersUnknown(&r, nil, evidenceNow)
	if r.CompatibilityVerdict != CompatibilityIncompatible || r.rangeVerdict != CompatibilityUnknown || r.Note != "policy; cluster gone could not be read, so Pacto cannot tell which revision of ref runs there now" {
		t.Fatalf("%+v", r)
	}
	// A cluster with no summary is named by its uid prefix, never the whole uid.
	u := AffectedConsumer{Service: "ref", CompatibilityVerdict: CompatibilityCompatible, Clusters: []string{usID}}
	staleClustersUnknown(&u, nil, evidenceNow)
	if !strings.HasPrefix(u.Note, "cluster "+usID[:8]+" could not be read") || strings.Contains(u.Note, usID) {
		t.Fatalf("%+v", u)
	}
}

func TestPublishedAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{-time.Minute: "0m", 59 * time.Minute: "59m", 47 * time.Hour: "47h", 72 * time.Hour: "3d"} {
		at := evidenceNow.Add(-d)
		if got := publishedAgo(&at, evidenceNow); got != "last published "+want+" ago" {
			t.Errorf("%v: %q", d, got)
		}
	}
}

func TestStaleClusterPastCapStillCounts(t *testing.T) {
	sources := []fleet.Source{clusterSource(usID, "us-prod", fleet.ClusterNameFromContext, evidenceNow.Add(-time.Hour), "ledger")}
	// Unavailable clusters fill the cap ahead of the stale one.
	for i := range fleet.MaxMetaClusters {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		sources = append(sources, fleet.NewMemorySource("k8s-"+id, fleet.SourceKindKubernetes, &fleet.Collection{
			Cluster: &fleet.Cluster{ID: id, Name: "c" + id[24:]}, State: &fleet.SourceState{Status: fleet.SourceUnavailable}}))
	}
	res := breakPayments(clusterFleet(t, paymentsRevs(), sources...))
	if slices.ContainsFunc(res.Clusters, func(c fleet.ClusterSummary) bool { return c.ID == usID }) {
		t.Fatal("the stale cluster is within the cap")
	}
	if res.ClustersPastBound != 1 {
		t.Fatalf("clusters past the cap: %d", res.ClustersPastBound)
	}
	if c := rowOf(t, res, "ledger"); c.CompatibilityVerdict != CompatibilityUnknown {
		t.Fatalf("ledger %+v", c)
	}
}

// unreadCarrier is the hub's carrier of n unread clusters (fleet's
// "cluster-joins" kind, spelled out so pkg/impact names no join symbol).
func unreadCarrier(n int) fleet.Source {
	return fleet.NewMemorySource("cluster-joins", "cluster-joins", &fleet.Collection{UnreadClusters: n,
		Limitations: []fleet.Limitation{{Code: fleet.LimitationClustersUnread, Message: "1 cluster is not a member"}}})
}

func TestBreakingWithUnreadClustersIsUnverified(t *testing.T) {
	snap := clusterFleet(t, []fleet.RawRevision{clusterRev("payments")},
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "payments"), unreadCarrier(1))
	res := breakPayments(snap)
	if res.Completeness != fleet.CompletenessComplete || res.UnreadClusters != 1 {
		t.Fatalf("completeness %s unread %d", res.Completeness, res.UnreadClusters)
	}
	if res.Gate != GateUnverified || res.GateCode() != GateCodeClustersUnread || !strings.Contains(res.GateReason, "1 cluster publishes to this hub and is not a member") {
		t.Fatalf("gate %s %s %q", res.Gate, res.GateCode(), res.GateReason)
	}
}

func TestUnreadGateSurvivesLimitationCap(t *testing.T) {
	snap := clusterFleet(t, []fleet.RawRevision{clusterRev("payments")},
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "payments"), unreadCarrier(2))
	for range fleet.MaxMetaLimitations + 1 {
		snap.Limitations = append(snap.Limitations, fleet.Limitation{Code: "NOISE"})
	}
	res := breakPayments(snap)
	if !res.LimitationsTruncated || res.Gate != GateUnverified || res.GateCode() != GateCodeClustersUnread || !strings.Contains(res.GateReason, "2 clusters publish") {
		t.Fatalf("gate %s %s %q", res.Gate, res.GateCode(), res.GateReason)
	}
}

func TestPotentialBreakingIgnoresUnreadClusters(t *testing.T) {
	r := Result{Classification: "POTENTIAL_BREAKING", Completeness: fleet.CompletenessComplete, UnreadClusters: 3}
	if gate, _ := r.gate(); gate != GateClear || r.GateCode() != GateCodePotentialBreaking {
		t.Fatalf("gate %s %s", gate, r.GateCode())
	}
}

func TestImpactClustersMatchProductMeta(t *testing.T) {
	full := fleet.NewQuery(euUS(t, 0))
	for name, q := range map[string]*fleet.Query{"full": full, "eu only": full.Restrict(func(c, _ string) bool { return c != usID }, nil)} {
		res := breakPayments(q.SnapshotForSerialization())
		want := fleet.NewQuery(q.SnapshotForSerialization()).ProductMeta().Clusters
		if len(want) < 2 {
			want = nil
		}
		got, _ := json.Marshal(res.Clusters)
		exp, _ := json.Marshal(want)
		if string(got) != string(exp) {
			t.Errorf("%s: clusters %s, want %s", name, got, exp)
		}
	}
}

// TestSingleClusterImpactJSONUnchanged compares a single-cluster answer with
// the bytes recorded before clusters were attributed.
func TestSingleClusterImpactJSONUnchanged(t *testing.T) {
	snap := clusterFleet(t, paymentsRevs(), clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "checkout", "ledger", "payments"))
	got, err := json.MarshalIndent(breakPayments(snap), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const golden = "testdata/single-cluster-impact.json"
	if *updateGolden {
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(append(got, '\n')) != string(want) {
		t.Errorf("single-cluster answer changed:\n%s", got)
	}
}

// serviceEvent asks what happens to the consumers of payments.
func serviceEvent(snap *fleet.FleetSnapshot, kind string) *Result {
	return Analyze(context.Background(), nil, nil, nil, nil, snap, Options{Event: &Event{Kind: kind, Service: "payments"}})
}

func TestServiceEventNamesConsumerClusters(t *testing.T) {
	res := serviceEvent(euUS(t, 0), EventRemoved)
	if len(res.Clusters) != 2 {
		t.Fatalf("clusters %+v", res.Clusters)
	}
	if c := rowOf(t, res, "ledger"); !slices.Equal(c.Clusters, []string{usID}) {
		t.Errorf("ledger clusters %v", c.Clusters)
	}
}

func TestOfferingEventNamesConsumerClusters(t *testing.T) {
	pg := offeringRev("payments", "pay", "payments")
	snap := clusterFleet(t, []fleet.RawRevision{pg, clusterRev("ledger", onPayments(">=1.0.0"))},
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "payments"),
		clusterSource(usID, "us-prod", fleet.ClusterNameFromContext, evidenceNow, "ledger"))
	res := offeringEvent(snap, EventUnavailable, "pay")
	if c := rowOf(t, res, "ledger"); len(res.Clusters) != 2 || !slices.Equal(c.Clusters, []string{usID}) {
		t.Fatalf("clusters %+v ledger %v", res.Clusters, c.Clusters)
	}
}

func TestEventWithUnreadClustersNeverClears(t *testing.T) {
	snap := clusterFleet(t, []fleet.RawRevision{clusterRev("payments")},
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "payments"), unreadCarrier(2))
	res := serviceEvent(snap, EventRemoved)
	if res.UnreadClusters != 2 || res.Gate != GateUnverified || !strings.Contains(res.GateReason, "2 clusters publish to this hub") {
		t.Fatalf("unread %d gate %s %q", res.UnreadClusters, res.Gate, res.GateReason)
	}
	// Without unread clusters, nobody visible is still a clear event answer.
	snap = clusterFleet(t, []fleet.RawRevision{clusterRev("payments")},
		clusterSource(euID, "eu-prod", fleet.ClusterNameFromApproval, evidenceNow, "payments"))
	if res := serviceEvent(snap, EventRemoved); res.Gate != GateClear {
		t.Fatalf("gate %s %q", res.Gate, res.GateReason)
	}
}
