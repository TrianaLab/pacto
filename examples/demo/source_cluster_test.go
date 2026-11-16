package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoFleet builds the browser demo's fleet from the same bundles the wasm binary embeds.
func demoFleet(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	partners, err := NewEmbedSource(os.DirFS("partners"))
	if err != nil {
		t.Fatal(err)
	}
	snap, _, err := buildDemoFleet(bundlesFS(t), partners)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// demoClusterID is the kube-system UID the demo's one cluster reports.
const demoClusterID = "5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11"

// TestDemoTargetsCarryOneCluster: the demo reads one cluster, so every target
// carries it, every key keeps its 3-part form, the snapshot lists one cluster and
// the limitations are the ones the demo had before targets knew their cluster.
func TestDemoTargetsCarryOneCluster(t *testing.T) {
	snap := demoFleet(t)
	if len(snap.Targets) == 0 {
		t.Fatal("the demo fleet has no targets")
	}
	for _, tg := range snap.Targets {
		if tg.Cluster != demoClusterID || tg.ClusterName != "production" || tg.ClusterNameSource != fleet.ClusterNameFromContext {
			t.Errorf("%s: cluster %q name %q source %q, want the demo cluster", tg.Key, tg.Cluster, tg.ClusterName, tg.ClusterNameSource)
		}
		if _, _, _, _, ok := fleet.ParseTargetKey(tg.Key); !ok || strings.Count(string(tg.Key), "/") != 2 {
			t.Errorf("%s: want a 3-part key in a single-cluster snapshot", tg.Key)
		}
	}
	if len(snap.Clusters) != 1 || snap.Clusters[0].ID != demoClusterID {
		t.Errorf("clusters = %+v, want the one demo cluster", snap.Clusters)
	}
	// Pinned from feat/452-adoption-w1, before any source reported a cluster.
	want := []fleet.Limitation{
		{Code: "OWNER_CONFLICT", Source: "fleet", Message: "targets of orders-service are linked by tag (inferred) to revisions that declare different owners"},
		{Code: "SOURCE_PARTIAL", Source: "registry-mirror", Message: "source registry-mirror returned a partial result"},
		{Code: "SOURCE_PARTIAL", Source: "registry-mirror", Message: "tag listing truncated by the mirror"},
	}
	got := make([]fleet.Limitation, 0, len(snap.Limitations))
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationClusterUnknown {
			t.Errorf("the demo carries %s: %s", l.Code, l.Message)
		}
		got = append(got, fleet.Limitation{Code: l.Code, Source: l.Source, Message: l.Message})
	}
	if !slices.Equal(got, want) {
		t.Errorf("limitations changed:\n got %+v\nwant %+v", got, want)
	}
}
