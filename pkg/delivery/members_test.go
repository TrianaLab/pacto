package delivery_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

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
	// A member whose key changed is pending again, and this is still a hub.
	rotated := joinList(delivery.JoinPending)
	rotated.Clusters[0].ApprovedKeyID = "key-old"
	if c := delivery.UnreadClusters(rotated); c == nil {
		t.Error("a pending cluster a person approved under another key: no carrier")
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

var membersNow = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// memberPacto is a Pacto of name, labelled for env when env is set.
func memberPacto(name, env string) delivery.PactoView {
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = "prod", name
	if env != "" {
		p.Metadata.Labels = map[string]string{delivery.EnvironmentLabel: env}
	}
	p.Status.ContractStatus, p.Status.LastReconciledAt = "Compliant", membersNow.Format(time.RFC3339)
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: name, Version: "1.0.0",
		ResolvedRef: "oci://reg.example/acme/" + name + "@sha256:" + strings.Repeat("a", 64)}
	return p
}

// readerOf answers each uid's snapshot taken age ago with pactos, and
// records the uids read.
func readerOf(read *[]string, age time.Duration, pactos ...delivery.PactoView) func(string) (delivery.TargetSnapshot, error) {
	return func(uid string) (delivery.TargetSnapshot, error) {
		*read = append(*read, uid)
		return delivery.TargetSnapshot{Cluster: uid, AsOf: membersNow.Add(-age).Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Pactos: pactos}, nil
	}
}

func collect(t *testing.T, src fleet.Source) *fleet.Collection {
	t.Helper()
	col, err := src.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return col
}

var hub = &fleet.Cluster{ID: "hub-uid"}

// TestMembersOfApprovedOnly: only approved members are read, each on its own
// cluster; pending and removed clusters are never read.
func TestMembersOfApprovedOnly(t *testing.T) {
	var read []string
	l := joinList(delivery.JoinApproved, delivery.JoinPending, delivery.JoinRemoved)
	l.Completeness = fleet.CompletenessComplete
	m := delivery.MembersOf(l, readerOf(&read, 0, memberPacto("ledger", "")), "prod", nil, membersNow, hub)
	if m.Home != hub || len(read) != 1 || read[0] != "uid-a" || len(m.Sources) != 2 {
		t.Fatalf("read %v, %d sources", read, len(m.Sources))
	}
	src := m.Sources[0]
	col := collect(t, src)
	if src.ID() != "member-uid-a" || src.Kind() != fleet.SourceKindTargetSnapshot || col.Cluster.ID != "uid-a" || col.Cluster.Name != "cluster-a" ||
		col.State.Status != fleet.SourceAvailable || col.State.Cluster != col.Cluster || len(col.Targets) != 1 {
		t.Fatalf("%s %+v", src.ID(), col)
	}
	if m.Sources[1].ID() != delivery.UnreadClustersSourceID {
		t.Fatalf("the carrier: %s", m.Sources[1].ID())
	}
}

// TestMembersOfFiltersEnvironmentLabel: as on the hub, a member Pacto
// labelled for another environment a Stage declares does not run here; an
// unlabelled one, or one labelled for an environment no Stage declares
// (production on a prod promote), cannot be ruled out.
func TestMembersOfFiltersEnvironmentLabel(t *testing.T) {
	var read []string
	l := joinList(delivery.JoinApproved)
	l.Completeness = fleet.CompletenessComplete
	known := map[string]bool{"prod": true, "staging": true}
	m := delivery.MembersOf(l, readerOf(&read, 0, memberPacto("ledger", "prod"), memberPacto("cron", "staging"), memberPacto("audit", ""),
		memberPacto("checkout", "production")), "prod", known, membersNow, hub)
	var names []string
	for _, tg := range collect(t, m.Sources[0]).Targets {
		names = append(names, tg.Name)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "audit,checkout,ledger" {
		t.Fatalf("targets %v", names)
	}
}

// TestMembersOfIgnoresAdvertisedEnvironments: the environments a member
// seems to host are inferred, so they never place its Pactos.
func TestMembersOfIgnoresAdvertisedEnvironments(t *testing.T) {
	var read []string
	l := joinList(delivery.JoinApproved)
	l.Completeness = fleet.CompletenessComplete
	l.Clusters[0].Environments = []delivery.AdvertisedEnvironment{{Name: "staging", Source: "the cluster name"}}
	m := delivery.MembersOf(l, readerOf(&read, 0, memberPacto("ledger", "")), "prod", nil, membersNow, hub)
	if len(collect(t, m.Sources[0]).Targets) != 1 {
		t.Fatal("an advertised environment placed a Pacto")
	}
}

// TestMembersOfStaleSource: a snapshot past its freshness reads stale and
// says how old it is; one its publisher calls partial reads partial.
func TestMembersOfStaleSource(t *testing.T) {
	var read []string
	l := joinList(delivery.JoinApproved)
	l.Completeness = fleet.CompletenessComplete
	col := collect(t, delivery.MembersOf(l, readerOf(&read, 20*time.Minute, memberPacto("ledger", "")), "prod", nil, membersNow, hub).Sources[0])
	if col.State.Status != fleet.SourceStale || !col.State.LastSuccessfulSync.Equal(membersNow.Add(-20*time.Minute)) ||
		len(col.Limitations) != 1 || col.Limitations[0].Code != fleet.LimitationTargetSnapshotStale || !strings.Contains(col.Limitations[0].Message, "cluster-a last published 20m ago") {
		t.Fatalf("%+v %+v", col.State, col.Limitations)
	}
	partial := func(string) (delivery.TargetSnapshot, error) {
		return delivery.TargetSnapshot{AsOf: membersNow.Format(time.RFC3339), Completeness: fleet.CompletenessPartial,
			Limitations: []fleet.Limitation{{Code: fleet.LimitationSourceUnavailable, Source: "publisher", Message: "workloads not read"}}}, nil
	}
	col = collect(t, delivery.MembersOf(l, partial, "prod", nil, membersNow, hub).Sources[0])
	if col.State.Status != fleet.SourcePartial || col.Limitations[0].Source != "member-uid-a" {
		t.Fatalf("%+v %+v", col.State, col.Limitations)
	}
}

// TestMembersOfReadErrorUnavailable: a member whose snapshot was not read is
// unavailable, named, and its consumers unknown.
func TestMembersOfReadErrorUnavailable(t *testing.T) {
	l := joinList(delivery.JoinApproved)
	l.Completeness = fleet.CompletenessComplete
	col := collect(t, delivery.MembersOf(l, func(string) (delivery.TargetSnapshot, error) {
		return delivery.TargetSnapshot{}, errors.New("registry down")
	},
		"prod", nil, membersNow, hub).Sources[0])
	if col.State.Status != fleet.SourceUnavailable || col.State.Cluster.ID != "uid-a" ||
		!strings.Contains(col.Limitations[0].Message, "member cluster-a was not read, so consumers there are unknown: registry down") {
		t.Fatalf("%+v %+v", col.State, col.Limitations)
	}
}

// TestMembersOfCountsPendingNamesNone: pending clusters of a hub add only
// the count, which names none of them.
func TestMembersOfCountsPendingNamesNone(t *testing.T) {
	var read []string
	l := joinList(delivery.JoinRemoved, delivery.JoinPending, delivery.JoinPending)
	l.Completeness = fleet.CompletenessComplete
	m := delivery.MembersOf(l, readerOf(&read, 0), "prod", nil, membersNow, hub)
	if len(read) != 0 || len(m.Sources) != 1 {
		t.Fatalf("read %v, %d sources", read, len(m.Sources))
	}
	col := collect(t, m.Sources[0])
	if col.UnreadClusters != 2 || strings.Contains(col.Limitations[0].Message, "cluster-b") || strings.Contains(col.Limitations[0].Message, "uid-c") {
		t.Fatalf("%+v", col)
	}
}

// TestMembersOfPartialListKeepsGraphPartial: a member list read partly keeps
// the plan's graph partial, so no gate clears on it.
func TestMembersOfPartialListKeepsGraphPartial(t *testing.T) {
	l := delivery.ClusterJoinList{Completeness: fleet.CompletenessPartial,
		Limitations: []fleet.Limitation{{Code: fleet.LimitationSourceUnavailable, Source: "cluster-joins", Message: "the member clusters were not read: registry down"}}}
	m := delivery.MembersOf(l, nil, "prod", nil, membersNow, hub)
	snap, err := delivery.Snapshot(t.Context(), nil, nil, nil, membersNow, m)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != fleet.CompletenessPartial || !slices.ContainsFunc(snap.Limitations, func(lim fleet.Limitation) bool {
		return lim.Source == "cluster-joins" && strings.Contains(lim.Message, "registry down")
	}) {
		t.Fatalf("%s %+v", snap.Completeness, snap.Limitations)
	}
}
