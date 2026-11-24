package cli

import (
	"cmp"
	"context"
	"errors"
	"time"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// memberDelivery is how a host on a hub reads its members' requests: svc
// over the members joins resolves at each read.
type memberDelivery struct {
	svc   *app.Service
	joins *app.JoinOptions
}

// newMemberDelivery is svc reading the members of joins; nil without joins.
func newMemberDelivery(svc *app.Service, joins *app.JoinOptions) *memberDelivery {
	if joins == nil {
		return nil
	}
	return &memberDelivery{svc: svc, joins: joins}
}

// errNoMemberRegistry is a hub whose joins name no registry: it has no
// members to read.
var errNoMemberRegistry = errors.New("this hub names no registry its members publish to")

// options resolves the joins into the options a member read takes.
func (m *memberDelivery) options(ctx context.Context) (app.MemberPlanOptions, error) {
	o := *m.joins
	if o.Resolve != nil {
		r, err := o.Resolve(ctx)
		if err != nil {
			return app.MemberPlanOptions{}, err
		}
		o = r
	}
	if o.Repository == "" {
		return app.MemberPlanOptions{}, errNoMemberRegistry
	}
	return app.MemberPlanOptions{Repository: o.Repository, Hub: o.Hub, Delivery: o.Delivery, Authorize: o.Authorize}, nil
}

// errNoMembers refuses a member request on a host that reads no members.
var errNoMembers = &delivery.HubRefusalError{Code: "NoMembers", Why: "this host reads no members",
	Next: "plan it on the hub: pacto delivery plan --service <service> --environment <environment> --to <sha256 digest> --registry <registry>"}

// plan plans req, a member request, over the hub's members, the options
// narrowed by scope; errNoMembers on a host (m nil) that reads none.
func (m *memberDelivery) plan(ctx context.Context, req delivery.Request, scope func(*app.MemberPlanOptions)) (delivery.PlanAnswer, error) {
	if m == nil {
		return delivery.PlanAnswer{}, errNoMembers
	}
	o, err := m.options(ctx)
	if err == nil {
		err = delivery.CheckRequestMode(req, o.Repository)
	}
	if err != nil {
		return delivery.PlanAnswer{}, err
	}
	scope(&o)
	mp, err := m.svc.MemberPlan(ctx, req, o)
	return delivery.PlanAnswer{Members: &mp}, err
}

// memberOptions are the options a's caller reads the members with, false
// when its host reads none.
func (a dashDelivery) memberOptions(ctx context.Context) (app.MemberPlanOptions, bool, error) {
	if a.id == nil || a.id.members == nil {
		return app.MemberPlanOptions{}, false, nil
	}
	o, err := a.id.members.options(ctx)
	a.restrictMembers(ctx, &o)
	return o, true, err
}

// restrictMembers narrows o to a's caller: behind a viewer they see a
// member only when a live channel reviews for it, and a namespace there
// only when that member's review lets them; a caller the hub cannot name
// sees none.
func (a dashDelivery) restrictMembers(ctx context.Context, o *app.MemberPlanOptions) {
	v := a.id.viewer
	if v == nil {
		return
	}
	who, err := a.id.subject(ctx)
	p := authn.Person{User: who.User, Groups: who.Groups}
	o.Restrict = func(cluster, ns string) bool { return err == nil && v.Sees(ctx, p, cluster, ns) }
	o.Reviewable = func(uid string) bool { return err == nil && v.Reviewable(ctx, p, uid) }
}

// memberPlan plans req, a member request, over the hub's members as a's
// caller may see them.
func (a dashDelivery) memberPlan(ctx context.Context, req delivery.Request) (delivery.PlanAnswer, error) {
	var m *memberDelivery
	if a.id != nil {
		m = a.id.members
	}
	p, err := m.plan(ctx, req, func(o *app.MemberPlanOptions) { a.restrictMembers(ctx, o) })
	return p, refusal(err)
}

// Members are the member plans of service with no target, one per
// environment placed on a member; nil on a host that reads no members, and
// one partial plan saying why when the members cannot be read.
func (a dashDelivery) Members(ctx context.Context, service string, envs []string) []delivery.MemberPlan {
	o, ok, err := a.memberOptions(ctx)
	switch {
	case !ok:
		return nil
	case err != nil:
		return []delivery.MemberPlan{{Service: delivery.ServiceRef{Name: service}, Members: []delivery.MemberState{},
			Placement:    delivery.MemberPlacementPlan{Placed: []delivery.PlacedMember{}, Source: delivery.PlacementUnconfirmed},
			Completeness: fleet.CompletenessPartial, Notes: []string{"the members could not be read: " + err.Error()}, AsOf: a.id.now().UTC().Format(time.RFC3339)}}
	}
	return a.id.members.svc.MemberPlans(ctx, service, envs, o)
}

// memberRefusalsNote says where member rows come from.
const memberRefusalsNote = "; the request streams of the hub's members (requests a member refused or whose promote its gates rejected, verified with each member's approved key)"

// memberRefusals adds to out the hub's member rows, uncapped and unsorted;
// out unchanged on a host that reads no members, and a fresh answer when out
// is nil and it reads some.
func (a dashDelivery) memberRefusals(ctx context.Context, out *dashboard.PublishRefusals) *dashboard.PublishRefusals {
	o, ok, err := a.memberOptions(ctx)
	if !ok {
		return out
	}
	if out == nil {
		out = &dashboard.PublishRefusals{Completeness: "complete", Refusals: []dashboard.PublishRefusal{},
			Note: "this cluster keeps no record of refused publishes or promotes"}
	}
	out.Note += memberRefusalsNote
	if err != nil {
		out.Completeness, out.Unreadable = "partial", append(out.Unreadable, "members: "+err.Error())
		return out
	}
	r := a.id.members.svc.MemberRefusals(ctx, o)
	out.Hidden += r.Hidden
	for _, u := range r.Unreadable {
		out.Completeness, out.Unreadable = "partial", append(out.Unreadable, "members: "+u)
	}
	for _, m := range r.Refusals {
		out.Refusals = append(out.Refusals, memberRefusalRow(m))
	}
	return out
}

// memberRefusalRow is m as a refusals row: check member, the member's name
// and, when the reader may see it, the Stage its Operation was raised on.
func memberRefusalRow(m app.MemberRefusal) dashboard.PublishRefusal {
	o := m.Outcome
	row := dashboard.PublishRefusal{Service: m.Request.Service.Name, Check: "member", Cluster: m.Member.Name, Digest: m.Request.Digest,
		Code: o.Code, Reason: o.Reason, Actor: m.Request.Requester, At: o.At, Verified: true}
	if o.State == delivery.RequestConcluded {
		row.Code, row.Reason = string(o.Outcome), cmp.Or(o.Published, o.Reason)
	}
	if o.Operation != nil {
		row.Namespace, row.Stage = o.Operation.Namespace, o.Operation.Stage
	}
	return row
}

// memberRequest reports a plan request that names members rather than a
// Stage: a service, an environment or member uids.
func memberRequest(req delivery.Request) bool {
	return req.Service != "" || req.Environment != "" || len(req.Members) > 0
}
