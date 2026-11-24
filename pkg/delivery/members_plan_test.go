package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

const (
	hubUID = "hub00000-0000-4000-8000-000000000000"
	euUID  = "eu000000-0000-4000-8000-00000000000e"
	usUID  = "us000000-0000-4000-8000-00000000000u"
	apUID  = "ap000000-0000-4000-8000-00000000000a"
)

var hubCluster = &fleet.Cluster{ID: hubUID, Name: "hub"}

// ledgerPacto is a consumer of payments with its own contract digest.
func ledgerPacto(rng string) PactoView {
	p := consumerPacto("prod", "ledger", "1.0.0", rng)
	p.Status.Contract.ResolvedRef = "oci://reg.example/pactos/ledger@sha256:" + strings.Repeat("4", 64)
	return p
}

// member is one cluster of a join list: its state, its snapshot's age and
// the Pactos it publishes.
type member struct {
	uid, name string
	state     JoinState
	age       time.Duration
	pactos    []PactoView
}

// membersOf are the members of a hub over ms; partial reads the list partly.
func membersOf(partial bool, ms ...member) Members {
	l := ClusterJoinList{Repository: "ghcr.io/acme/pacto-targets", Completeness: fleet.CompletenessComplete}
	if partial {
		l.Completeness = fleet.CompletenessPartial
		l.Limitations = []fleet.Limitation{{Code: fleet.LimitationSourceUnavailable, Message: "the member clusters were not read: registry down"}}
	}
	snaps := map[string]TargetSnapshot{}
	for _, m := range ms {
		l.Clusters = append(l.Clusters, ClusterJoinState{UID: m.uid, Name: m.name, State: m.state, NameSource: fleet.ClusterNameFromApproval})
		snaps[m.uid] = TargetSnapshot{Cluster: m.uid, AsOf: now.Add(-m.age).Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Pactos: m.pactos}
	}
	return MembersOf(l, func(uid string) (TargetSnapshot, error) { return snaps[uid], nil }, "prod", now, hubCluster)
}

func memberSnapshot(t *testing.T, from, to Revision, local []PactoView, m Members) *fleet.FleetSnapshot {
	t.Helper()
	snap, err := Snapshot(context.Background(), local, nil, []Revision{from, to}, now, m)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func planMembers(t *testing.T, from, to Revision, local []PactoView, m Members) Plan {
	t.Helper()
	return autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stageSpec(), Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}},
		From: from, To: to, Snapshot: memberSnapshot(t, from, to, local, m), Class: ClassAgent})
}

func floorRank(f Floor) int {
	switch {
	case !f.Human:
		return 0
	case f.Decision == AcceptUnknown:
		return 1
	}
	return 2
}

// TestMemberConsumerIncompatibleAsksItsOwners: an incompatible consumer in
// an approved member breaks like a local one and asks its owners.
func TestMemberConsumerIncompatibleAsksItsOwners(t *testing.T) {
	from, to := breakingSides()
	p := planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")},
		membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, pactos: []PactoView{ledgerPacto("^1.0.0")}}))
	if p.Floor.Decision != Approve || !slices.Contains(p.Floor.Owners, "team-ledger") || !slices.Contains(p.Floor.Consumers, "ledger") {
		t.Fatalf("an incompatible member consumer asks its owners: %+v", p.Floor)
	}
}

// TestMemberOnlyConsumerKeepsNoConsumerDoubt: a consumer seen only in a
// member never removes the doubt a service with no contract leaves.
func TestMemberOnlyConsumerKeepsNoConsumerDoubt(t *testing.T) {
	from, to := breakingSides()
	p := planMembers(t, from, to, nil,
		membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, pactos: []PactoView{ledgerPacto(">=1.0.0")}}))
	if v := verdict(t, p, GateImpact); p.Floor.Decision != AcceptUnknown || !strings.Contains(v.Message, "no Pacto depends on payments") {
		t.Fatalf("a member-only consumer keeps the no-consumer doubt: %+v %+v", p.Floor, v)
	}
	p = planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")},
		membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, pactos: []PactoView{ledgerPacto(">=1.0.0")}}))
	if p.Floor.Human {
		t.Fatalf("a hub consumer removes it: %+v %+v", p.Floor, p.Verdicts)
	}
}

// TestMembersKeepHubPactosInHubCluster: with members, the hub's Pactos sit in
// the hub's cluster, so nothing is clusterless and the graph stays complete.
func TestMembersKeepHubPactosInHubCluster(t *testing.T) {
	from, to := breakingSides()
	snap := memberSnapshot(t, from, to, []PactoView{checkoutPacto(">=1.0.0")},
		membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, pactos: []PactoView{ledgerPacto(">=1.0.0")}}))
	if snap.Completeness != fleet.CompletenessComplete || slices.ContainsFunc(snap.Limitations, func(l fleet.Limitation) bool { return l.Code == fleet.LimitationClusterUnknown }) {
		t.Fatalf("the hub's Pactos are clusterless: %s %+v", snap.Completeness, snap.Limitations)
	}
	for tk, tg := range snap.Targets {
		if want := map[string]string{"checkout": hubUID, "ledger": usUID}[tg.Name]; tg.Cluster != want {
			t.Fatalf("%s sits in %q, want %q", tk, tg.Cluster, want)
		}
	}
	if m := memberStates(snap); len(m) != 1 || m[usUID].ID == "" {
		t.Fatalf("members %+v", m)
	}
	res := impact.Analyze(context.Background(), from.Bundle.Contract, to.Bundle.Contract, nil, nil, snap, impact.Options{Domain: contract.OCIDomain(to.ContractRef)})
	if len(res.Consumers) != 2 || localConsumers(snap, res) != 1 {
		t.Fatalf("one consumer in the hub of %+v", res.Consumers)
	}
}

// TestStaleMemberConsumerIsUnseen: a consumer in a stale member is named,
// with the member and its snapshot's age.
func TestStaleMemberConsumerIsUnseen(t *testing.T) {
	from, to := breakingSides()
	p := planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")},
		membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, age: 20 * time.Minute, pactos: []PactoView{ledgerPacto(">=1.0.0")}}))
	if v := verdict(t, p, GateImpact); p.Floor.Decision != AcceptUnknown || !strings.Contains(v.Message, "ledger runs in cluster us-prod, whose snapshot is 20m old") {
		t.Fatalf("a stale member consumer is unseen: %+v", v)
	}
}

// TestUnreadClustersHoldBreakingPromote: a cluster that is not a member yet
// holds a breaking change for a person.
func TestUnreadClustersHoldBreakingPromote(t *testing.T) {
	from, to := breakingSides()
	m := membersOf(false, member{uid: euUID, name: "eu-prod", state: JoinApproved}, member{uid: apUID, name: "ap-dev", state: JoinPending},
		member{uid: usUID, name: "us-dev", state: JoinPending})
	p := planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")}, m)
	v := verdict(t, p, GateImpact)
	if p.Floor.Decision != AcceptUnknown || !strings.Contains(v.Message, "2 clusters publish to this hub and are not members") || strings.Contains(v.Message, "ap-dev") {
		t.Fatalf("unread clusters hold a breaking change and are never named: %+v", v)
	}
	m = membersOf(false, member{uid: euUID, name: "eu-prod", state: JoinApproved}, member{uid: apUID, name: "ap-dev", state: JoinPending})
	if v := verdict(t, planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")}, m), GateImpact); !strings.Contains(v.Message, "1 cluster publishes to this hub and is not a member") {
		t.Fatalf("one unread cluster: %+v", v)
	}
}

// TestUnreadClustersIgnoredForNonBreaking: a plainly non-breaking change
// breaks no one wherever they run.
func TestUnreadClustersIgnoredForNonBreaking(t *testing.T) {
	from, to := minorSides()
	p := planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")},
		membersOf(false, member{uid: euUID, name: "eu-prod", state: JoinApproved}, member{uid: apUID, name: "ap-dev", state: JoinPending}))
	if p.Floor.Human {
		t.Fatalf("a non-breaking change ignores unread clusters: %+v %+v", p.Floor, p.Verdicts)
	}
}

// TestMembersNeverLowerTheFloor: whatever the members say, the floor is never
// lower than the hub alone sets.
func TestMembersNeverLowerTheFloor(t *testing.T) {
	ledger := func(rng string) []PactoView { return []PactoView{ledgerPacto(rng)} }
	rows := map[string]Members{
		"no members":          {},
		"compatible member":   membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, pactos: ledger(">=1.0.0")}),
		"incompatible member": membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, pactos: ledger("^1.0.0")}),
		"stale member":        membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, age: time.Hour, pactos: ledger(">=1.0.0")}),
		"one of two clusters stale": membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, pactos: ledger(">=1.0.0")},
			member{uid: euUID, name: "eu-prod", state: JoinApproved, age: 3 * time.Hour, pactos: ledger(">=1.0.0")}),
		"unread only": membersOf(false, member{uid: euUID, name: "eu-prod", state: JoinRemoved}, member{uid: apUID, name: "ap-dev", state: JoinPending}),
		"unread plus incompatible": membersOf(false, member{uid: usUID, name: "us-prod", state: JoinApproved, pactos: ledger("^1.0.0")},
			member{uid: apUID, name: "ap-dev", state: JoinPending}),
		"join list unreadable": membersOf(true),
		"member promoter":      MemberOnlyUnread(),
		"member unread": MembersOf(ClusterJoinList{Completeness: fleet.CompletenessComplete, Clusters: []ClusterJoinState{{UID: usUID, State: JoinApproved}}},
			func(string) (TargetSnapshot, error) { return TargetSnapshot{}, errors.New("registry down") }, "prod", now, hubCluster),
	}
	locals := map[string][]PactoView{"none": nil, "compatible": {checkoutPacto(">=1.0.0")}, "incompatible": {checkoutPacto("^1.0.0")}}
	for change, sides := range map[string]func() (Revision, Revision){"breaking": breakingSides, "non-breaking": minorSides} {
		from, to := sides()
		for lname, local := range locals {
			alone := floorRank(planMembers(t, from, to, local, Members{}).Floor)
			for name, m := range rows {
				if got := floorRank(planMembers(t, from, to, local, m).Floor); got < alone {
					t.Errorf("%s, local %s, %s: floor %d below the hub's own %d", change, lname, name, got, alone)
				}
			}
		}
	}
	// A member read that failed is never positive.
	from, to := breakingSides()
	if p := planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")}, rows["member unread"]); !p.Floor.Human {
		t.Fatalf("an unread member clears the gate: %+v", p.Verdicts)
	}
}

// TestMemberOnlyUnreadCounts: a member's promoter counts one unread cluster,
// names none, and asks a person for a breaking change with what to do next.
func TestMemberOnlyUnreadCounts(t *testing.T) {
	m := MemberOnlyUnread()
	if m.Home != nil || len(m.Sources) != 1 || m.Sources[0].ID() != MemberUnreadID || m.Sources[0].Kind() != fleet.SourceKindClusterJoins {
		t.Fatalf("%+v", m)
	}
	from, to := breakingSides()
	p := planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")}, m)
	if v := verdict(t, p, GateImpact); p.Floor.Decision != AcceptUnknown || !strings.Contains(v.Message, "this cluster is a member of a hub") ||
		!strings.Contains(v.Message, "pacto delivery approve") || strings.Contains(v.Message, "pacto cluster list") {
		t.Fatalf("a member holds a breaking change: %+v %+v", p.Floor, v)
	}
	from, to = minorSides()
	if p := planMembers(t, from, to, []PactoView{checkoutPacto(">=1.0.0")}, m); p.Floor.Human {
		t.Fatalf("a non-breaking change on a member: %+v", p.Verdicts)
	}
}

// TestNoMembersPlanUnchanged: with no members a plan is byte for byte the
// plan before members existed.
func TestNoMembersPlanUnchanged(t *testing.T) {
	from, to := breakingSides()
	got := planMembers(t, from, to, []PactoView{checkoutPacto("^1.0.0"), ledgerPacto(">=1.0.0")}, Members{})
	assertGolden(t, "testdata/no-members-plan.json", got)
}

func TestAgeWords(t *testing.T) {
	for d, want := range map[time.Duration]string{14 * time.Minute: "14m", 3 * time.Hour: "3h", 72 * time.Hour: "3d"} {
		if got := ageWords(d); got != want {
			t.Errorf("%s: %q", d, got)
		}
	}
}

// assertGolden compares v as indented JSON with the file at path.
func assertGolden(t *testing.T, path string, v any) {
	t.Helper()
	got, _ := json.MarshalIndent(v, "", "  ")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("%s changed:\n%s", path, got)
	}
}

// TestTargetClusterUnknownTarget: a target the snapshot does not hold has
// no cluster Pacto can tell, so it never counts as a member's.
func TestTargetClusterUnknownTarget(t *testing.T) {
	if got := targetCluster(&fleet.FleetSnapshot{}, "eu/prod/kubernetes/ledger"); got != "" {
		t.Fatal(got)
	}
}
