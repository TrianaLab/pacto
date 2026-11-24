package delivery_test

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func joinList(states ...delivery.JoinState) delivery.ClusterJoinList {
	l := delivery.ClusterJoinList{Repository: "ghcr.io/acme/pacto-targets"}
	for i, s := range states {
		l.Clusters = append(l.Clusters, delivery.ClusterJoinState{UID: "uid-" + string(rune('a'+i)), Name: "cluster-" + string(rune('a'+i)), State: s})
	}
	return l
}

func TestUnreadClustersNeedsHubRole(t *testing.T) {
	for name, l := range map[string]delivery.ClusterJoinList{
		"empty":        joinList(),
		"pending only": joinList(delivery.JoinPending, delivery.JoinPending),
		"no pending":   joinList(delivery.JoinApproved, delivery.JoinRemoved),
	} {
		if src := delivery.UnreadClusters(l); src != nil {
			t.Errorf("%s: carrier %+v", name, src)
		}
	}
	for _, role := range []delivery.JoinState{delivery.JoinApproved, delivery.JoinRemoved} {
		if delivery.UnreadClusters(joinList(role, delivery.JoinPending)) == nil {
			t.Errorf("%s with a pending cluster: no carrier", role)
		}
	}
}

func TestUnreadClustersCountsPendingNamesNone(t *testing.T) {
	for n, want := range map[int]string{1: "1 cluster publishes to ghcr.io/acme/pacto-targets and is not a member", 2: "2 clusters publish to ghcr.io/acme/pacto-targets and are not members"} {
		states := []delivery.JoinState{delivery.JoinApproved, delivery.JoinRemoved}
		for range n {
			states = append(states, delivery.JoinPending)
		}
		l := joinList(states...)
		src := delivery.UnreadClusters(l)
		if src.ID() != delivery.UnreadClustersSourceID || src.Kind() != fleet.SourceKindClusterJoins {
			t.Fatalf("%s %s", src.ID(), src.Kind())
		}
		col, err := src.Collect(t.Context())
		if err != nil || col.UnreadClusters != n || col.State != nil || len(col.Limitations) != 1 {
			t.Fatalf("%v %+v", err, col)
		}
		lim := col.Limitations[0]
		if lim.Code != fleet.LimitationClustersUnread || lim.Source != delivery.UnreadClustersSourceID || !strings.HasPrefix(lim.Message, want) {
			t.Fatalf("%+v", lim)
		}
		for _, st := range l.Clusters {
			if strings.Contains(lim.Message, st.UID) || strings.Contains(lim.Message, st.Name) {
				t.Fatalf("names %s: %s", st.Name, lim.Message)
			}
		}
	}
}
