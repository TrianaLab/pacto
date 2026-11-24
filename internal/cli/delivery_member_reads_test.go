package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// hubMembers reads the members of e's hub as the dashboard does.
func (e *clusterEnv) hubMembers() *memberDelivery {
	return newMemberDelivery(app.NewService(nil, nil), &app.JoinOptions{Resolve: func(ctx context.Context) (app.JoinOptions, error) {
		return joinsOn(ctx, e.hub(), e.repo, "", delivery.PolicyNamespace)
	}})
}

// failingMembers are members whose joins cannot be resolved (err) or name
// no registry (err nil).
func failingMembers(err error) *memberDelivery {
	return newMemberDelivery(app.NewService(nil, nil), &app.JoinOptions{Resolve: func(context.Context) (app.JoinOptions, error) {
		return app.JoinOptions{}, err
	}})
}

// code is the dashboard refusal code of err, "" for none.
func code(err error) string {
	var de *dashboard.DeliveryError
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

// TestDashDeliveryPlanMemberMode: the dashboard's plan route plans a member
// request over the hub's members, refuses one that names a Stage too or a
// version, and a host that reads no members says so; the environments
// answer reads the members with no target.
func TestDashDeliveryPlanMemberMode(t *testing.T) {
	eu, us := newJoinMember(euProd, "eu-prod"), newJoinMember(usProd, "us-prod")
	e := newRequestHub(t, eu, us)
	a := dashDelivery{id: &dashboardIdentity{members: e.hubMembers(), now: time.Now}}
	ctx := t.Context()
	req := delivery.Request{Service: "billing", Environment: "production", To: memberDigest}
	p, err := a.Plan(ctx, req)
	if err != nil || p.Members == nil || len(p.Members.Placement.Placed) != 2 || !strings.Contains(p.Members.Next, "--confirm") {
		t.Fatalf("%v %+v", err, p.Members)
	}
	plans := a.Members(ctx, "billing", []string{"production"})
	if len(plans) != 1 || plans[0].Environment != "production" || plans[0].Digest != "" || len(plans[0].Placement.Placed) != 2 {
		t.Fatalf("%+v", plans)
	}
	for c, r := range map[string]delivery.Request{
		delivery.RefusalDigestRequired: {Service: "billing", Environment: "production", To: "1.5.0"},
		delivery.RefusalStageOrMembers: {Stage: "billing-prod", Service: "billing", Environment: "production", To: memberDigest},
	} {
		if _, err := a.Plan(ctx, r); code(err) != c {
			t.Errorf("%s: %v", c, err)
		}
	}
	// The MCP plan tool plans the same over stdio, and refuses without members.
	if p, err := (mcpDelivery{members: e.hubMembers()}).Plan(ctx, req); err != nil || p.Members == nil {
		t.Fatalf("mcp: %v", err)
	}
	if _, err := (mcpDelivery{}).Plan(ctx, req); !errors.Is(err, error(errNoMembers)) {
		t.Fatalf("mcp, no members: %v", err)
	}
}

// TestDashDeliveryMembersWithoutMembers: a host that reads no members says
// so to a member request and answers no member plans; members that cannot
// be read answer one partial plan saying why.
func TestDashDeliveryMembersWithoutMembers(t *testing.T) {
	ctx := t.Context()
	req := delivery.Request{Service: "billing", Environment: "production", To: memberDigest}
	for _, none := range []dashDelivery{{}, {id: &dashboardIdentity{}}} {
		if _, err := none.Plan(ctx, req); code(err) != "NoMembers" {
			t.Errorf("no members: %v", err)
		}
		if none.Members(ctx, "billing", nil) != nil {
			t.Error("no members, no plans")
		}
	}
	if _, err := (dashDelivery{id: &dashboardIdentity{members: failingMembers(nil)}}).Plan(ctx, req); err == nil || !strings.Contains(err.Error(), errNoMemberRegistry.Error()) {
		t.Errorf("no registry: %v", err)
	}
	broken := dashDelivery{id: &dashboardIdentity{members: failingMembers(errors.New("hub down")), now: time.Now}}
	plans := broken.Members(ctx, "billing", nil)
	if len(plans) != 1 || plans[0].Completeness != fleet.CompletenessPartial || !strings.Contains(plans[0].Notes[0], "hub down") {
		t.Fatalf("%+v", plans)
	}
}

// refusedHub is a hub whose member us-prod refused alice's request at
// intake and whose member eu-prod raised it and its gates rejected it.
func refusedHub(t *testing.T) *clusterEnv {
	t.Helper()
	eu, us := newJoinMember(euProd, "eu-prod"), newJoinMember(usProd, "us-prod")
	e := newRequestHub(t, eu, us)
	e.answer(t, us, delivery.RequestOutcome{Request: e.ask(t, us.uid), State: delivery.RequestRefused, Code: "NoStage",
		Reason: "no Stage of billing for production", Next: "create the Stage"})
	op := &delivery.RequestedOperation{Namespace: "billing", Name: "billing-prod-req-0123456789", Stage: "billing-prod"}
	asked := e.ask(t, eu.uid)
	e.answer(t, eu, delivery.RequestOutcome{Request: asked, State: delivery.RequestRaised, Operation: op})
	e.answer(t, eu, delivery.RequestOutcome{Request: asked, State: delivery.RequestConcluded, Outcome: delivery.Rejected,
		Published: "pacto.published: no publisher signature for billing in billing", Operation: op})
	return e
}

// rowOf is the refusal row of member name.
func rowOf(t *testing.T, r *dashboard.PublishRefusals, name string) dashboard.PublishRefusal {
	t.Helper()
	for _, row := range r.Refusals {
		if row.Cluster == name {
			return row
		}
	}
	t.Fatalf("no row of %s in %+v", name, r.Refusals)
	return dashboard.PublishRefusal{}
}

// TestRefusalsListMemberRefusals: a hub lists the requests its members
// refused and those their gates rejected, as check member rows naming the
// member; a hub whose members cannot be read is partial.
func TestRefusalsListMemberRefusals(t *testing.T) {
	e := refusedHub(t)
	a := dashDelivery{d: &app.Delivery{}, id: &dashboardIdentity{members: e.hubMembers()}}
	r, err := a.PublishRefusals(t.Context(), "")
	if err != nil || r == nil || len(r.Refusals) != 2 || r.Completeness != "complete" || !strings.Contains(r.Note, "members") {
		t.Fatalf("%v %+v", err, r)
	}
	us, eu := rowOf(t, r, "us-prod"), rowOf(t, r, "eu-prod")
	us.At, eu.At = "", ""
	if want := (dashboard.PublishRefusal{Service: "billing", Check: "member", Cluster: "us-prod", Digest: memberDigest, Code: "NoStage",
		Reason: "no Stage of billing for production", Actor: alice, Verified: true}); us != want {
		t.Errorf("%+v", us)
	}
	if want := (dashboard.PublishRefusal{Service: "billing", Check: "member", Cluster: "eu-prod", Digest: memberDigest, Code: "Rejected", Namespace: "billing", Stage: "billing-prod",
		Reason: "pacto.published: no publisher signature for billing in billing", Actor: alice, Verified: true}); eu != want {
		t.Errorf("%+v", eu)
	}
	// pacto delivery refusals --registry reads them on the hub too.
	if out, _, err := runRoot(t, e.d, nil, "delivery", "refusals", "--registry", e.repo); err != nil || !strings.Contains(out, `"cluster": "us-prod"`) {
		t.Errorf("%v %s", err, out)
	}
	// So does pacto delivery environments --registry, per environment.
	if out, _, err := runRoot(t, e.d, nil, "delivery", "environments", "--service", "billing", "--registry", e.repo); err != nil || !strings.Contains(out, `"members": [`) {
		t.Errorf("%v %s", err, out)
	}
}

// TestRefusalsMembersUnread: no records and no members is no answer;
// members that cannot be resolved or read make the answer partial.
func TestRefusalsMembersUnread(t *testing.T) {
	if r, _ := (dashDelivery{d: &app.Delivery{}, id: &dashboardIdentity{}}).PublishRefusals(t.Context(), ""); r != nil {
		t.Errorf("no records, no members: %+v", r)
	}
	broken := dashDelivery{d: &app.Delivery{}, id: &dashboardIdentity{members: failingMembers(errors.New("hub down"))}}
	if r, _ := broken.PublishRefusals(t.Context(), ""); r.Completeness != "partial" || !strings.Contains(strings.Join(r.Unreadable, ";"), "hub down") {
		t.Errorf("%+v", r)
	}
	// A registry that does not answer leaves the members unread.
	down := dashDelivery{d: &app.Delivery{}, id: &dashboardIdentity{members: newMemberDelivery(app.NewService(nil, nil), &app.JoinOptions{
		Repository: "127.0.0.1:1/acme/" + delivery.TargetSnapshotRepository, Hub: clusterHubUID, Delivery: &app.Delivery{}})}}
	if r, _ := down.PublishRefusals(t.Context(), ""); r.Completeness != "partial" || len(r.Unreadable) == 0 || !strings.HasPrefix(r.Unreadable[0], "members: ") {
		t.Errorf("%+v", r)
	}
}

// TestRefusalsMemberRowsRestricted: a signed-in reader does not see a
// member no live channel reviews for (counted in hidden), and sees a row of
// a member whose namespace they may not read without that namespace; a
// reader the hub cannot name sees none.
func TestRefusalsMemberRowsRestricted(t *testing.T) {
	e := refusedHub(t)
	id, _, _ := operationAccessWorld(t)
	id.viewer = authn.NewViewer(func(context.Context) map[string]authn.Review {
		return map[string]authn.Review{euProd: func(_ context.Context, _ authn.Person, ns string, _ authn.ResourceAttributes) (bool, error) {
			return ns != "billing", nil
		}}
	})
	id.members = e.hubMembers()
	a := dashDelivery{d: &app.Delivery{}, id: id}
	r, err := a.PublishRefusals(t.Context(), "")
	if err != nil || len(r.Refusals) != 1 || r.Hidden != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	eu := rowOf(t, r, "eu-prod")
	if eu.Namespace != "" || eu.Stage != "" || strings.Contains(eu.Reason, " billing in billing") || !strings.Contains(eu.Reason, "a namespace you may not read") {
		t.Errorf("namespace kept: %+v", eu)
	}
	id.cluster = nil
	if r, _ := a.PublishRefusals(t.Context(), ""); len(r.Refusals) != 0 || r.Hidden != 2 {
		t.Errorf("nobody named: %+v", r)
	}
}

// TestMemberReadsOverRemoteMCPUseTheReviewedCaller: on the dashboard's /mcp
// there is no session: a member read is reviewed as the caller the
// apiserver named for the request, a request naming nobody sees nothing,
// and the plan tool reads the dashboard's members narrowed to that caller.
func TestMemberReadsOverRemoteMCPUseTheReviewedCaller(t *testing.T) {
	e := refusedHub(t)
	id, _, _ := operationAccessWorld(t)
	id.viewer = authn.NewViewer(func(context.Context) map[string]authn.Review {
		return map[string]authn.Review{euProd: func(_ context.Context, p authn.Person, _ string, _ authn.ResourceAttributes) (bool, error) {
			return p.User == "bob", nil
		}}
	})
	id.members = e.hubMembers()
	a := dashDelivery{id: id}
	for user, want := range map[string]bool{"bob": true, "carol": false, "": false} {
		ctx := context.WithValue(t.Context(), reviewedKey{}, reviewed{person: authn.Person{User: user}})
		var o app.MemberPlanOptions
		a.restrictMembers(ctx, &o)
		if got := o.Restrict(euProd, "billing"); got != want {
			t.Errorf("%q: %v", user, got)
		}
	}
	api := remoteMCP{id: id}.deliveryAPI(&app.Delivery{})
	if api.members != id.members || api.scope == nil {
		t.Fatalf("%+v", api)
	}
	if api := (remoteMCP{}).deliveryAPI(&app.Delivery{}); api.members != nil || api.scope != nil {
		t.Fatalf("%+v", api)
	}
	ctx := context.WithValue(t.Context(), reviewedKey{}, reviewed{person: authn.Person{User: "bob"}})
	if p, err := api.Plan(ctx, delivery.Request{Service: "billing", Environment: "production", To: memberDigest}); err != nil || p.Members == nil {
		t.Fatalf("%v %+v", err, p)
	}
}
