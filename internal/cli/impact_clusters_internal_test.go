package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

const (
	icEU = "eeee0000-0000-4000-8000-00000000000e"
	icUS = "uuuu0000-0000-4000-8000-00000000000u"
	icAP = "aaaa0000-0000-4000-8000-00000000000a"
)

var clustersAsOf = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// singleClusterResult is a BREAKING answer over one cluster: no cluster is
// named anywhere.
func singleClusterResult() *impact.Result {
	return &impact.Result{
		Service: "payments", OldVersion: "1.0.0", NewVersion: "2.0.0", Classification: "BREAKING",
		AsOf: clustersAsOf, Completeness: fleet.CompletenessComplete, Gate: impact.GateBlocked,
		Consumers: []impact.AffectedConsumer{
			{Service: "checkout", Direct: true, Confidence: "declared", CompatibilityVerdict: impact.CompatibilityIncompatible, Owner: "team:shop",
				Path: []string{"checkout", "payments"}, Provenance: "declared", Via: []string{"dependency"}},
		},
	}
}

// multiClusterResult is the same answer over eu-prod (complete) and us-prod
// (stale 14m), with ledger in us-prod and one cluster not a member yet.
func multiClusterResult() *impact.Result {
	res := singleClusterResult()
	stale := clustersAsOf.Add(-14 * time.Minute)
	res.Clusters = []fleet.ClusterSummary{
		{ID: icEU, Name: "eu-prod", Status: fleet.SourceAvailable, Targets: 1},
		{ID: icUS, Name: "us-prod", Status: fleet.SourceStale, Targets: 1, AsOf: &stale},
	}
	res.UnreadClusters = 1
	res.Consumers[0].Clusters = []string{icEU}
	res.Consumers = append(res.Consumers, impact.AffectedConsumer{Service: "ledger", Direct: true, Confidence: "declared",
		CompatibilityVerdict: impact.CompatibilityUnknown, Owner: "team:ledger", Path: []string{"ledger", "payments"}, Provenance: "declared",
		Via: []string{"dependency"}, Clusters: []string{icUS}})
	return res
}

func impactText(t *testing.T, res *impact.Result) string {
	t.Helper()
	return textOf(t, func(cmd *cobra.Command) error { return printImpactResult(cmd, res, "text") })
}

func TestImpactPrintsClustersRead(t *testing.T) {
	wantLines(t, impactText(t, multiClusterResult()), "Clusters read: eu-prod (complete), us-prod (stale 14m); 1 cluster is not a member yet\n")
	res := singleClusterResult()
	res.UnreadClusters = 2
	wantLines(t, impactText(t, res), "Clusters read: 2 clusters are not members yet\n")
}

func TestImpactCountsClustersPastTheCap(t *testing.T) {
	res := multiClusterResult()
	res.ClustersTruncated = 10
	if got := impactText(t, res); !strings.Contains(got, "; 10 more clusters;") {
		t.Fatalf("no count of the clusters left out:\n%s", got)
	}
	res.ClustersTruncated = 1
	if got := impactText(t, res); !strings.Contains(got, "; 1 more cluster;") {
		t.Fatalf("no count of the cluster left out:\n%s", got)
	}
}

func TestImpactPrintsConsumerClusters(t *testing.T) {
	got := impactText(t, multiClusterResult())
	wantLines(t, got, "    clusters: eu-prod\n", "    clusters: us-prod (stale 14m)\n")
	if strings.Contains(got, icEU) || strings.Contains(got, icUS) {
		t.Fatalf("a uid is printed:\n%s", got)
	}
}

func TestImpactConsumerClusterPastCapNamesNoUid(t *testing.T) {
	res := multiClusterResult()
	res.Consumers[0].Clusters = []string{icAP, icEU}
	res.Consumers[1].Clusters = []string{icAP, "bbbb0000-0000-4000-8000-00000000000b", icUS}
	got := impactText(t, res)
	wantLines(t, got, "    clusters: eu-prod, 1 more cluster\n", "    clusters: us-prod (stale 14m), 2 more clusters\n")
	if strings.Contains(got, icAP) {
		t.Fatalf("a uid is printed:\n%s", got)
	}
}

// TestImpactSingleClusterOutputUnchanged: the text recorded before clusters
// were printed.
func TestImpactSingleClusterOutputUnchanged(t *testing.T) {
	want := "Impact: payments 1.0.0 -> 2.0.0\n" +
		"Classification: BREAKING\n" +
		"Breaking changes: 0\n" +
		"Potentially breaking changes: 0\n" +
		"Affected consumers (1):\n" +
		"  checkout                     direct     confidence=declared     compat=incompatible owner=team:shop\n" +
		"    path: checkout > payments  provenance: declared  via: dependency\n" +
		"Release gate: blocked\n"
	if got := impactText(t, singleClusterResult()); got != want {
		t.Fatalf("got:\n%s", got)
	}
}

func TestClusterAgeWords(t *testing.T) {
	at := func(d time.Duration) *time.Time { v := clustersAsOf.Add(-d); return &v }
	for _, c := range []struct {
		sum  fleet.ClusterSummary
		want string
	}{
		{fleet.ClusterSummary{Status: fleet.SourceAvailable}, "complete"},
		{fleet.ClusterSummary{Status: fleet.SourcePartial}, "partial"},
		{fleet.ClusterSummary{Status: fleet.SourceUnavailable}, "unreachable"},
		{fleet.ClusterSummary{Status: fleet.SourceAvailable, Access: fleet.ClusterNoAccess}, "no access"},
		{fleet.ClusterSummary{Status: fleet.SourceStale}, "stale"},
		{fleet.ClusterSummary{Status: fleet.SourceStale, AsOf: at(14 * time.Minute)}, "stale 14m"},
		{fleet.ClusterSummary{Status: fleet.SourceStale, AsOf: at(5 * time.Hour)}, "stale 5h"},
		{fleet.ClusterSummary{Status: fleet.SourceStale, AsOf: at(72 * time.Hour)}, "stale 3d"},
		{fleet.ClusterSummary{Status: fleet.SourceStale, AsOf: at(-time.Hour)}, "stale 0m"},
	} {
		if got := clusterAgeWords(clustersAsOf, c.sum); got != c.want {
			t.Errorf("%+v: %q, want %q", c.sum, got, c.want)
		}
	}
}
