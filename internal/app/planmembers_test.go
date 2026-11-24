package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// collected is each source of m by id.
func collected(t *testing.T, m delivery.Members) map[string]*fleet.Collection {
	t.Helper()
	out := map[string]*fleet.Collection{}
	for _, src := range m.Sources {
		col, err := src.Collect(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		out[src.ID()] = col
	}
	return out
}

// TestClientPlanReadsApprovedMembers: a client plan reads the approved
// member's snapshot on its cluster, counts the pending one and puts the
// hub's own Pactos in the hub's cluster.
func TestClientPlanReadsApprovedMembers(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m, p := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e.publish(t, m, m.key, snapNow)
	e.publish(t, p, p.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	opts := e.opts()
	d := &Delivery{Members: e.svc.PlanMembers(&opts)}
	got := planSources{d}.Members(t.Context(), "prod")
	cols := collected(t, got)
	member, unread := cols["member-"+memberA], cols[delivery.UnreadClustersSourceID]
	if got.Home == nil || got.Home.ID != joinHub || len(cols) != 2 || member == nil || unread == nil {
		t.Fatalf("home %+v, sources %v", got.Home, cols)
	}
	if member.State.Status != fleet.SourceAvailable || member.Cluster.Name != "eu-prod" || len(member.Targets) != 1 || unread.UnreadClusters != 1 {
		t.Fatalf("member %+v, unread %+v", member.State, unread)
	}
}

// TestClientPlanWithoutJoinsHasNoMembers: a client with no hub reads no
// members, and a hub whose joins cannot be resolved reads partial.
func TestClientPlanWithoutJoinsHasNoMembers(t *testing.T) {
	if (&Service{}).PlanMembers(nil) != nil {
		t.Fatal("no joins, no member reader")
	}
	if got := (planSources{&Delivery{}}).Members(t.Context(), "prod"); got.Home != nil || got.Sources != nil {
		t.Fatalf("%+v", got)
	}
	e := newJoinEnv(t, capabilities.ReferrersNative)
	got := e.svc.PlanMembers(&JoinOptions{Resolve: func(context.Context) (JoinOptions, error) { return JoinOptions{}, errors.New("no detection") }})(t.Context(), "prod")
	cols := collected(t, got)
	if got.Home != nil || len(cols) != 1 || cols["cluster-joins"] == nil || cols["cluster-joins"].State.Status != fleet.SourcePartial {
		t.Fatalf("an unresolved hub reads partial: %+v %v", got, cols)
	}
}

// TestClientMembersEqualMembersOf: the client's members are MembersOf over
// the list the moved join core reads, as the promoter computes them.
func TestClientMembersEqualMembersOf(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m, p := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e.publish(t, m, m.key, snapNow)
	e.publish(t, p, p.key, snapNow)
	e.decide(t, m, delivery.JoinApprove)
	opts := e.opts()
	got := collected(t, e.svc.PlanMembers(&opts)(t.Context(), "prod"))
	r := &snapshotReader{cache: &targetSnapshotCache{}, now: e.clock}
	list, repo := deliveryrecord.ReadJoins(t.Context(), deliveryrecord.JoinRead{Repository: e.repo, Hub: joinHub, Policy: e.policy, Records: &deliveryrecord.Reader{},
		Authorize: func(ctx context.Context, user, uid string) (bool, error) {
			return (&Service{}).authorizeRecord(ctx, e.authorize, user, uid, e.clock)
		},
		Fetch: func(ctx context.Context, repo *remote.Repository, ref string) ([]byte, string, error) {
			return r.fetch(ctx, repo, e.repo, ref)
		},
		Forbidden: apierrors.IsForbidden, VerifyPerson: verifyPerson, OpenKey: signing.OpenKey, Open: openTargets, Now: e.clock})
	want := collected(t, delivery.MembersOf(list, func(uid string) (delivery.TargetSnapshot, error) {
		return r.member(t.Context(), repo, e.repo, delivery.JoinTrust(list.Clusters), uid)
	}, "prod", e.clock, &fleet.Cluster{ID: joinHub}))
	if len(got) != 2 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestMemberSnapshotReadRefusesWhatMoved: a member's tag that no longer
// holds a snapshot its approved key signed for it is an error, never a
// member read.
func TestMemberSnapshotReadRefusesWhatMoved(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	m, p := newMember(memberA, "eu-prod"), newMember(memberB, "us-prod")
	e.publish(t, p, p.key, snapNow)
	s := ledgerSnapshot(p.uid, snapNow)
	pushAt(t, e.repo, p.key, m.uid, s) // p's snapshot under m's tag
	both := []delivery.ClusterJoinState{{UID: m.uid, Name: m.name, State: delivery.JoinApproved, PublicKey: p.join().PublicKey},
		{UID: p.uid, Name: p.name, State: delivery.JoinApproved, PublicKey: p.join().PublicKey}}
	repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	r := &snapshotReader{cache: &targetSnapshotCache{}, now: e.clock}
	if _, err := r.member(t.Context(), repo, e.repo, delivery.JoinTrust(both), m.uid); err == nil || !strings.Contains(err.Error(), "holds the snapshot of cluster "+p.uid) {
		t.Fatalf("a moved tag: %v", err)
	}
	if _, err := r.member(t.Context(), repo, e.repo, delivery.JoinTrust(both[:1]), m.uid); err == nil {
		t.Fatal("a snapshot no approved key of its cluster signed")
	}
	if _, err := r.member(t.Context(), repo, e.repo, delivery.JoinTrust(both), "cccc0000-0000-4000-8000-00000000000c"); err == nil {
		t.Fatal("a member with no snapshot")
	}
	if st, err := r.member(t.Context(), repo, e.repo, delivery.JoinTrust(both), p.uid); err != nil || st.Cluster != p.uid {
		t.Fatalf("%v %+v", err, st)
	}
}
