package delivery

import (
	"strconv"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// UnreadClustersSourceID is the id of the carrier [UnreadClusters] returns.
const UnreadClustersSourceID = "cluster-joins-unread"

// UnreadClusters is the carrier of the count of clusters that publish to
// list's repository and are not members of this hub, so consumers there are
// unknown to every gate. It is nil unless this installation acts as a hub (a
// person approved or removed a member: the list holds an approved or removed
// state) and some cluster is pending. Removed clusters are not counted: a
// person decided. The count and its limitation never name a cluster.
func UnreadClusters(list ClusterJoinList) fleet.Source {
	hub, n := false, 0
	for _, st := range list.Clusters {
		switch st.State {
		case JoinApproved, JoinRemoved:
			hub = true
		case JoinPending:
			n++
		}
	}
	if !hub || n == 0 {
		return nil
	}
	msg := strconv.Itoa(n) + " cluster publishes to " + list.Repository + " and is not a member of this hub, so consumers there are unknown: approve or remove it (pacto cluster list)"
	if n > 1 {
		msg = strconv.Itoa(n) + " clusters publish to " + list.Repository + " and are not members of this hub, so consumers there are unknown: approve or remove them (pacto cluster list)"
	}
	return fleet.NewMemorySource(UnreadClustersSourceID, fleet.SourceKindClusterJoins, &fleet.Collection{UnreadClusters: n,
		Limitations: []fleet.Limitation{{Code: fleet.LimitationClustersUnread, Source: UnreadClustersSourceID, Message: msg}}})
}
