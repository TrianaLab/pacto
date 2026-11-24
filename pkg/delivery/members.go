package delivery

import (
	"cmp"
	"slices"
	"strconv"
	"time"

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

// Members are what a promote reads beyond the hub's own Pactos: the sources
// of the clusters that joined this hub and the hub's own cluster, which the
// hub's Pactos sit in whenever Sources is not empty, so they are never
// clusterless next to a member's.
type Members struct {
	Home    *fleet.Cluster
	Sources []fleet.Source
}

// membersListID names the source that says the member list was read partly.
const membersListID = "cluster-joins"

// MembersOf are the members of list a promote in environment reads, as of
// now, the hub being home. Only approved members are read, each through read
// (which verifies the snapshot under the member's approved key): its Pactos
// whose environment label names another environment are dropped, and a
// snapshot past TargetSnapshotFreshness reads stale. A member read fails is
// unavailable, so its consumers are unknown. A list read partly adds one
// partial source, and pending clusters of a hub add the unread count
// ([UnreadClusters]). Advertised environments are never read: they are
// inferred, and inferred never clears a gate.
func MembersOf(list ClusterJoinList, read func(uid string) (TargetSnapshot, error), environment string, now time.Time, home *fleet.Cluster) Members {
	m := Members{Home: home}
	if list.Completeness != fleet.CompletenessComplete {
		m.Sources = append(m.Sources, fleet.NewMemorySource(membersListID, fleet.SourceKindTargetSnapshot, &fleet.Collection{
			State: &fleet.SourceState{Status: fleet.SourcePartial}, Limitations: sourced(membersListID, list.Limitations)}))
	}
	for _, st := range list.Clusters {
		if st.State != JoinApproved {
			continue
		}
		cl := &fleet.Cluster{ID: st.UID, Name: st.Name, NameSource: st.NameSource}
		snap, err := read(st.UID)
		m.Sources = append(m.Sources, memberSource(cl, snap, err, environment, now))
	}
	if u := UnreadClusters(list); u != nil {
		m.Sources = append(m.Sources, u)
	}
	return m
}

// memberSource is the source of member cl's snapshot snap (err when it was
// not read), with the Pactos that may run in environment.
func memberSource(cl *fleet.Cluster, snap TargetSnapshot, err error, environment string, now time.Time) fleet.Source {
	id := "member-" + cl.ID
	name := cmp.Or(cl.Name, cl.ID)
	if err != nil {
		return fleet.NewMemorySource(id, fleet.SourceKindTargetSnapshot, &fleet.Collection{Cluster: cl,
			State: &fleet.SourceState{Status: fleet.SourceUnavailable, Cluster: cl},
			Limitations: []fleet.Limitation{{Code: fleet.LimitationSourceUnavailable, Source: id,
				Message: "the snapshot of member " + name + " was not read, so consumers there are unknown: " + err.Error()}}})
	}
	views := slices.DeleteFunc(slices.Clone(snap.Pactos), func(p PactoView) bool {
		env := p.Metadata.Labels[EnvironmentLabel]
		return env != "" && env != environment
	})
	col := LinkedCollection(id, views, now)
	col.Limitations = append(col.Limitations, sourced(id, snap.Limitations)...)
	status := fleet.SourceAvailable
	if len(col.Limitations) > 0 || snap.Completeness != fleet.CompletenessComplete {
		status = fleet.SourcePartial
	}
	asOf, _ := time.Parse(time.RFC3339, snap.AsOf) // the reader verified it
	if age := now.Sub(asOf); age > TargetSnapshotFreshness {
		status = fleet.SourceStale
		col.Limitations = append(col.Limitations, fleet.Limitation{Code: fleet.LimitationTargetSnapshotStale, Source: id,
			Message: "member " + name + " last published " + ageWords(age) + " ago, so Pacto cannot tell what runs there now"})
	}
	col.Cluster = cl
	col.State = &fleet.SourceState{Status: status, Cluster: cl, LastSuccessfulSync: &asOf}
	return fleet.NewMemorySource(id, fleet.SourceKindTargetSnapshot, col)
}

// sourced is lims, each naming source id.
func sourced(id string, lims []fleet.Limitation) []fleet.Limitation {
	out := make([]fleet.Limitation, len(lims))
	for i, l := range lims {
		l.Source = id
		out[i] = l
	}
	return out
}

// ageWords is d as a person reads an age: minutes under an hour, hours
// under two days, else days.
func ageWords(d time.Duration) string {
	switch {
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}
