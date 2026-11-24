package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// MemberPlanOptions are how a hub plans a promote over its members.
type MemberPlanOptions struct {
	// Repository is the <registry>/pacto-targets repository the members
	// publish to; Hub is the hub cluster's UID.
	Repository, Hub string
	// Delivery reads the hub's approver signers and the PublisherConfig
	// whose delivery policy the signed placement policy sits beside.
	Delivery *Delivery
	// Authorize reviews, in the hub, who may approve members (ClusterJoins).
	Authorize authn.Review
	// Restrict, when set, says whether the reader may get stages in
	// namespace on member cluster; nil is the unrestricted reader.
	Restrict func(cluster, namespace string) bool
	// Reviewable says whether a live channel reviews for member uid: a
	// restricted reader never sees a member without one; nil is none.
	Reviewable func(uid string) bool
}

func (o MemberPlanOptions) joins() JoinOptions {
	return JoinOptions{Repository: o.Repository, Hub: o.Hub, Authorize: o.Authorize, Delivery: o.Delivery}
}

// memberRead is what one member's request stream says about a promote: the
// requests for it that verified (newest last), the newest one's furthest
// outcome, and what was not read.
type memberRead struct {
	requests    []delivery.VerifiedRequest
	outcome     *delivery.RequestOutcome
	unread      string
	unverified  int
	badOutcomes int
	requestsCut bool
	outcomesCut bool
}

// MemberPlan plans req, a member request, over the approved members of the
// hub opts names: where the promote goes (delivery.MemberPlacement), and per
// placed member what happened to the newest request a person signed for it.
// A request counts only when it verifies under the hub's approver signers as
// a person's record naming its requester, and names the member whose stream
// it is on; an outcome only when the member's approved key signed it for
// that request. What cannot be read or does not verify is counted in Notes;
// what could change the answer makes it partial. With no req.To the newest
// request for the service and environment is read, whatever its digest; a
// req.To that is not a sha256 digest is DigestRequired.
func (s *Service) MemberPlan(ctx context.Context, req delivery.Request, opts MemberPlanOptions) (delivery.MemberPlan, error) {
	if req.To != "" && !strings.HasPrefix(req.To, "sha256:") {
		return delivery.MemberPlan{}, delivery.HubRefusal(delivery.RefusalDigestRequired, "member mode takes the digest of what to promote, never a version or tag ("+req.To+")", req, opts.Repository)
	}
	return s.memberPlan(ctx, req, opts, s.ClusterJoins(ctx, opts.joins())), nil
}

// MemberPlans are the member plans, with no target digest, of service in
// each environment the hub lists (envs, in their order) or an approved
// member takes requests for (delivery.MemberEnvironments), read over one
// list of the members: only those that place it on a member or hide one
// from a restricted reader.
func (s *Service) MemberPlans(ctx context.Context, service string, envs []string, opts MemberPlanOptions) []delivery.MemberPlan {
	list := s.ClusterJoins(ctx, opts.joins())
	out := []delivery.MemberPlan{}
	for _, env := range delivery.MemberEnvironments(envs, list.Clusters) {
		if p := s.memberPlan(ctx, delivery.Request{Service: service, Environment: env}, opts, list); len(p.Placement.Placed) > 0 || p.Hidden > 0 {
			out = append(out, p)
		}
	}
	return out
}

// memberPlan is MemberPlan over list, the hub's members.
func (s *Service) memberPlan(ctx context.Context, req delivery.Request, opts MemberPlanOptions, list delivery.ClusterJoinList) delivery.MemberPlan {
	now := s.env().now()
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(s.sys.snapshotTimeout, delivery.TargetSnapshotReadTimeout))
	defer cancel()
	svc := delivery.ServiceRef{Name: req.Service}
	plan := delivery.MemberPlan{Service: svc, Environment: req.Environment, Digest: req.To, Members: []delivery.MemberState{},
		Completeness: fleet.CompletenessComplete, AsOf: now.UTC().Format(time.RFC3339)}
	mp := &memberPlanning{s: s, req: req, opts: opts, now: now, plan: &plan, list: list, trust: delivery.JoinTrust(list.Clusters)}
	for _, l := range list.Limitations {
		mp.note(l.Message, list.Completeness != fleet.CompletenessComplete)
	}
	policy, digest, err := opts.Delivery.placementPolicy(ctx)
	if err != nil {
		mp.note("the placement policy could not be verified, so no placement rule was read: "+err.Error(), true)
	}
	reads := mp.read(ctx, delivery.MemberPlacement(policy, list.Clusters, svc, req.Environment, nil))
	var verified []delivery.VerifiedRequest
	for _, r := range reads {
		verified = append(verified, r.requests...)
	}
	plan.Placement = delivery.MemberPlacement(policy, list.Clusters, svc, req.Environment, verified)
	if plan.Placement.Source == delivery.PlacementFromPolicy {
		plan.Placement.Policy = digest
	}
	mp.states(reads)
	if req.To != "" && len(plan.Placement.Placed) > 0 {
		plan.Next = delivery.ConfirmCommand(req, opts.Repository)
	}
	return plan
}

// memberPlanning is one MemberPlan call on its way to its answer.
type memberPlanning struct {
	s     *Service
	req   delivery.Request
	opts  MemberPlanOptions
	now   time.Time
	plan  *delivery.MemberPlan
	list  delivery.ClusterJoinList
	trust *delivery.TrustConfig
}

// note adds a note, and makes the plan partial when uncertain.
func (mp *memberPlanning) note(n string, uncertain bool) {
	mp.plan.Notes = append(mp.plan.Notes, n)
	if uncertain {
		mp.plan.Completeness = fleet.CompletenessPartial
	}
}

// placementPolicy is the hub's signed placement policy and its digest, read
// beside the delivery policy its published PublisherConfig names, through
// the ambient registry credentials; none when no delivery namespace is given
// or no delivery policy is configured.
func (d *Delivery) placementPolicy(ctx context.Context) (*delivery.PlacementPolicy, string, error) {
	if d.PolicyNamespace == "" {
		return nil, "", nil
	}
	cfg, err := d.publisherConfig(ctx)
	if err != nil || cfg.Policy == "" {
		return nil, "", err
	}
	return verifiedPlacement(ctx, publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), oci.EnvInsecureRegistries()...), cfg)
}

// read reads the request streams of the placed members, joinReadsAtOnce at
// once, by uid.
func (mp *memberPlanning) read(ctx context.Context, placed delivery.MemberPlacementPlan) map[string]memberRead {
	out := map[string]memberRead{}
	if len(placed.Placed) == 0 {
		return out
	}
	policy, err := mp.opts.Delivery.classPolicy(ctx)
	if err != nil {
		mp.note("the hub's approver signers could not be read, so no request was read: "+err.Error(), true)
		return out
	}
	repo, _ := openTargets(mp.list.Repository) // ClusterJoins opened it
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(joinReadsAtOnce)
	for _, m := range placed.Placed {
		g.Go(func() error {
			r := mp.readMember(gctx, repo, policy, m.UID)
			mu.Lock()
			out[m.UID] = r
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // readMember notes its failures
	return out
}

// readMember reads the requests on member uid's stream that ask for the
// planned promote, and the outcomes of the newest one.
func (mp *memberPlanning) readMember(ctx context.Context, repo *remote.Repository, policy delivery.ClassPolicy, uid string) memberRead {
	var r memberRead
	var err error
	r.requests, r.unverified, r.requestsCut, err = mp.verifiedRequests(ctx, repo, policy, uid, func(q delivery.OperationRequest) bool {
		return q.Service.Name == mp.req.Service && q.Environment == mp.req.Environment && (mp.req.To == "" || q.Digest == mp.req.To)
	})
	if err != nil {
		r.unread = err.Error()
		return r
	}
	if len(r.requests) == 0 {
		return r
	}
	r.outcome, r.badOutcomes, r.outcomesCut, err = mp.outcomeOf(ctx, repo, uid, r.requests[len(r.requests)-1].Manifest)
	if err != nil {
		r.unread = err.Error()
	}
	return r
}

// verifiedRequests are the requests on member uid's stream that verify
// (verifyRequest) and match says to keep, oldest first; those that do not
// verify are counted and leave the read bound.
func (mp *memberPlanning) verifiedRequests(ctx context.Context, repo *remote.Repository, policy delivery.ClassPolicy, uid string,
	match func(delivery.OperationRequest) bool) (reqs []delivery.VerifiedRequest, unverified int, cut bool, err error) {
	fetched, cut, err := mp.s.joinRecords.ListOperationRequests(ctx, repo, uid)
	if err != nil {
		return nil, 0, false, err
	}
	for _, f := range fetched {
		v, ok := verifyRequest(policy, uid, f)
		switch {
		case !ok:
			unverified++
			mp.s.joinRecords.Reject(delivery.ClusterRequestStream(uid), delivery.PredicateOperationRequest, f.Manifest)
		case match(v.Request):
			reqs = append(reqs, v)
		}
	}
	slices.SortFunc(reqs, func(a, b delivery.VerifiedRequest) int { return a.SignedAt.Compare(b.SignedAt) })
	return reqs, unverified, cut, nil
}

// outcomeOf is the furthest outcome of request on member uid's stream that
// the member's approved key signed for it (nil for none yet), with how
// many did not verify and whether the read was cut.
func (mp *memberPlanning) outcomeOf(ctx context.Context, repo *remote.Repository, uid, request string) (*delivery.RequestOutcome, int, bool, error) {
	outcomes, cut, err := mp.s.joinRecords.ListRequestOutcomes(ctx, repo, uid, request)
	if err != nil {
		return nil, 0, false, err
	}
	var furthest *delivery.RequestOutcome
	bad := 0
	for _, f := range outcomes {
		st, err := delivery.OpenRequestOutcome(f.Bundle, mp.trust, uid, signing.OpenKey)
		if err != nil || st.Predicate.Request != request {
			bad++
			continue
		}
		if o, n := st.Predicate, outcomeStage[st.Predicate.State]; n > 0 && (furthest == nil || n > outcomeStage[furthest.State]) {
			furthest = &o
		}
	}
	return furthest, bad, cut, nil
}

// outcomeStage orders outcomes: the furthest one says where a request is.
var outcomeStage = map[delivery.RequestState]int{delivery.RequestRefused: 1, delivery.RequestRaised: 2, delivery.RequestConcluded: 3}

// verifyRequest opens f, a request on member uid's stream, under the hub's
// approver signers: it counts when a person signed it as the requester it
// names and it names uid.
func verifyRequest(policy delivery.ClassPolicy, uid string, f deliveryrecord.Fetched) (delivery.VerifiedRequest, bool) {
	var req delivery.OperationRequest
	_, at, class, why := signing.VerifyPersonRecord(policy, "member request", func(verify func([]byte) error) (string, error) {
		st, err := delivery.OpenOperationRequest(f.Bundle, verify)
		req = st.Predicate
		return req.Requester, err
	})
	return delivery.VerifiedRequest{Request: req, Manifest: f.Manifest, SignedAt: at}, why == "" && class == delivery.ClassHuman && req.Cluster == uid
}

// states fills the plan's member states from reads, hiding from a
// restricted reader the members no live channel reviews for.
func (mp *memberPlanning) states(reads map[string]memberRead) {
	p, restricted := mp.plan, mp.opts.Restrict != nil
	visible := func(uid string) bool { return mp.opts.Reviewable != nil && mp.opts.Reviewable(uid) }
	var placed []delivery.PlacedMember
	for _, m := range p.Placement.Placed {
		m.Reviewable = visible(m.UID)
		if restricted && !m.Reviewable {
			p.Hidden++
			continue
		}
		placed = append(placed, m)
		p.Members = append(p.Members, mp.memberState(m, reads[m.UID]))
	}
	p.Placement.Placed = append([]delivery.PlacedMember{}, placed...)
	p.Placement.Unplaced = slices.DeleteFunc(p.Placement.Unplaced, func(u delivery.UnplacedMember) bool {
		if restricted && !visible(u.UID) {
			p.Hidden++
			return true
		}
		return false
	})
	if p.Hidden > 0 {
		p.HiddenReason = "members no live channel reviews for are hidden from a reader whose access is restricted"
	}
}

// memberState is placed member m's state from what its stream said.
func (mp *memberPlanning) memberState(m delivery.PlacedMember, r memberRead) delivery.MemberState {
	st := delivery.MemberState{UID: m.UID, Name: m.Name, State: delivery.MemberNoRequest, Stale: m.Stale}
	if i := slices.IndexFunc(mp.list.Clusters, func(c delivery.ClusterJoinState) bool { return c.UID == m.UID }); i >= 0 {
		st.AsOf = mp.list.Clusters[i].SnapshotAsOf
	}
	if m.Stale {
		mp.note(m.Name+" is stale: its snapshot is from "+st.AsOf+", so what it did since is unknown", true)
	}
	mp.readNotes(m.Name, r)
	if r.unread != "" {
		st.State, st.Reason = delivery.MemberUnknown, "its request stream could not be read: "+r.unread
		return st
	}
	if len(r.requests) == 0 {
		return st
	}
	newest := r.requests[len(r.requests)-1]
	st.Digest = newest.Request.Digest
	if r.outcome == nil {
		st.State, st.Reason = delivery.MemberWaiting, "requested "+newest.SignedAt.UTC().Format(time.RFC3339)+", no answer yet"
		if m.Stale {
			st.Reason = "stale: its request waits until it reads the registry"
		}
		if mp.now.Sub(newest.SignedAt) > delivery.TargetSnapshotFreshness {
			st.State, st.Reason = delivery.MemberUnknown, "requested "+newest.SignedAt.UTC().Format(time.RFC3339)+", no answer"
			mp.note(m.Name+" has not answered a request signed at "+newest.SignedAt.UTC().Format(time.RFC3339), true)
		}
		return st
	}
	mp.outcomeState(&st, *r.outcome)
	return st
}

// readNotes notes what member name's stream left unread.
func (mp *memberPlanning) readNotes(name string, r memberRead) {
	if r.unread != "" {
		mp.note("the request stream of "+name+" could not be read: "+r.unread, true)
	}
	if r.unverified > 0 {
		mp.note(fmt.Sprintf("%s on the stream of %s did not verify and %s not read", plural(r.unverified, "request"), name, were(r.unverified)), false)
	}
	if r.badOutcomes > 0 {
		mp.note(fmt.Sprintf("%s from %s did not verify and %s not read", plural(r.badOutcomes, "outcome"), name, were(r.badOutcomes)), true)
	}
	if r.requestsCut || r.outcomesCut {
		mp.note("the stream of "+name+" holds more records than a read takes; newer ones are read next time", true)
	}
}

// were is was or were, for n.
func were(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// concludedStates are the member states an Operation's outcome reads as.
var concludedStates = map[delivery.Outcome]string{delivery.Succeeded: delivery.MemberApplied, delivery.Witnessed: delivery.MemberApplied,
	delivery.Rejected: delivery.MemberRefused, delivery.Failed: delivery.MemberFailed, delivery.Errored: delivery.MemberFailed}

// outcomeState sets st from o, the newest request's furthest outcome,
// dropping what a restricted reader may not read in the Operation's
// namespace.
func (mp *memberPlanning) outcomeState(st *delivery.MemberState, o delivery.RequestOutcome) {
	st.Reason, st.Next = o.Reason, o.Next
	switch o.State {
	case delivery.RequestRefused:
		st.State, st.Reason = delivery.MemberRefused, o.Code+": "+o.Reason
	case delivery.RequestRaised:
		st.State = delivery.MemberRaised
		if o.Operation.IntentRecord != "" {
			st.State, st.Next = delivery.MemberHeld, "pacto delivery approve --intent "+o.Operation.IntentRecord+" --registry "+mp.opts.Repository
		}
	default:
		st.State = cmp.Or(concludedStates[o.Outcome], delivery.MemberRejected)
		st.Reason = strings.TrimSuffix(string(o.Outcome)+": "+cmp.Or(o.Published, o.Reason), ": ")
	}
	if o.Operation == nil {
		return
	}
	st.Operation = o.Operation.Namespace + "/" + o.Operation.Name
	if mp.opts.Restrict != nil && !mp.opts.Restrict(st.UID, o.Operation.Namespace) {
		st.Operation, st.Next = "", ""
		st.Reason = strings.ReplaceAll(st.Reason, o.Operation.Namespace, "a namespace you may not read")
	}
}

// WriteOperationRequests signs each of reqs with signer and pushes it on its
// member's request stream, one record per member, through the ambient
// registry credentials. It first reads the members again and refuses every
// request, signing none, when one names a member that is not approved now
// (a changed key reads pending: NotAMember) or that takes no requests
// (MemberTakesNoRequests). It returns where each record is and the form
// they took.
func (s *Service) WriteOperationRequests(ctx context.Context, reqs []delivery.OperationRequest, signer delivery.Signer, opts MemberPlanOptions) ([]string, capabilities.ReferrersMode, error) {
	list := s.ClusterJoins(ctx, opts.joins())
	for _, r := range reqs {
		asked := delivery.Request{Service: r.Service.Name, Environment: r.Environment, To: r.Digest}
		i := slices.IndexFunc(list.Clusters, func(c delivery.ClusterJoinState) bool { return c.UID == r.Cluster })
		switch {
		case r.Check() != nil:
			return nil, "", r.Check()
		case i < 0 || list.Clusters[i].State != delivery.JoinApproved:
			why := "no approved member " + r.Cluster + " publishes to " + list.Repository
			if i >= 0 {
				why = list.Clusters[i].Name + " (" + r.Cluster + ") is " + string(list.Clusters[i].State) + ": " + list.Clusters[i].Reason
			}
			return nil, "", delivery.HubRefusal(delivery.RefusalNotAMember, why, asked, opts.Repository)
		case list.Clusters[i].Requests == nil:
			return nil, "", delivery.HubRefusal(delivery.RefusalMemberTakesNoRequests, list.Clusters[i].Name+" does not take requests", asked, opts.Repository)
		}
	}
	ctx, cancel := delivery.WithRegistryTimeout(ctx)
	defer cancel()
	repo, _ := openTargets(list.Repository) // ClusterJoins opened it
	var refs []string
	var mode capabilities.ReferrersMode
	for _, r := range reqs {
		signed, err := delivery.SignOperationRequest(ctx, signer, delivery.NewOperationRequestStatement(r))
		if err != nil {
			return refs, mode, err
		}
		ref, m, err := deliveryrecord.PushOperationRequest(ctx, repo, signed)
		if err != nil {
			return refs, mode, err
		}
		refs, mode = append(refs, ref), m
	}
	return refs, mode, nil
}

// MemberTrust is the trust config the Stage records of the hub's approved
// members verify with (delivery.MemberRecordTrust), and the members it was
// built from.
func (s *Service) MemberTrust(ctx context.Context, opts MemberPlanOptions) (*delivery.TrustConfig, []delivery.ClusterJoinState) {
	list := s.ClusterJoins(ctx, opts.joins())
	return delivery.MemberRecordTrust(list.Clusters), list.Clusters
}

// MemberRefusal is a request a member refused at intake, or whose promote
// its gates rejected: the member, the request and that outcome.
type MemberRefusal struct {
	Member  delivery.ClusterRef
	Request delivery.OperationRequest
	Outcome delivery.RequestOutcome
}

// MemberRefusals are the refusals of the hub's members, the members a
// restricted reader may not see (counted) and what could not be read.
type MemberRefusals struct {
	Refusals   []MemberRefusal
	Hidden     int
	Unreadable []string
}

// MemberRefusals reads, on the request stream of every approved member that
// takes requests, the requests that verify (as MemberPlan does) and each
// one's furthest outcome, keeping those the member refused or whose
// Operation concluded Rejected. A restricted reader does not see a member no
// live channel reviews for (counted in Hidden), nor the namespace and
// Operation of an outcome its review of that member does not let them read.
// ponytail: every request's outcomes are read on each call, joinReadsAtOnce
// members at once and MaxOperationRequests requests each; keep the answer a
// moment if a Publish view on a large hub reads them too often.
func (s *Service) MemberRefusals(ctx context.Context, opts MemberPlanOptions) MemberRefusals {
	list := s.ClusterJoins(ctx, opts.joins())
	var out MemberRefusals
	if list.Completeness != fleet.CompletenessComplete {
		for _, l := range list.Limitations {
			out.Unreadable = append(out.Unreadable, l.Message)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(s.sys.snapshotTimeout, delivery.TargetSnapshotReadTimeout))
	defer cancel()
	var members []delivery.ClusterJoinState
	for _, c := range list.Clusters {
		switch {
		case c.State != delivery.JoinApproved || c.Requests == nil:
		case opts.Restrict != nil && (opts.Reviewable == nil || !opts.Reviewable(c.UID)):
			out.Hidden++
		default:
			members = append(members, c)
		}
	}
	if len(members) == 0 {
		return out
	}
	policy, err := opts.Delivery.classPolicy(ctx)
	if err != nil {
		out.Unreadable = append(out.Unreadable, "the hub's approver signers could not be read, so no member request was read: "+err.Error())
		return out
	}
	mp := &memberPlanning{s: s, opts: opts, list: list, trust: delivery.JoinTrust(list.Clusters)}
	repo, _ := openTargets(list.Repository) // ClusterJoins opened it
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(joinReadsAtOnce)
	for _, c := range members {
		g.Go(func() error {
			rows, unread := mp.refusalsOf(gctx, repo, policy, c)
			mu.Lock()
			defer mu.Unlock()
			out.Refusals = append(out.Refusals, rows...)
			if unread != "" {
				out.Unreadable = append(out.Unreadable, c.Name+": "+unread)
			}
			return nil
		})
	}
	_ = g.Wait() // refusalsOf returns its failures
	return out
}

// refusalsOf are member c's refusals and what of its stream was not read.
func (mp *memberPlanning) refusalsOf(ctx context.Context, repo *remote.Repository, policy delivery.ClassPolicy, c delivery.ClusterJoinState) ([]MemberRefusal, string) {
	reqs, unverified, cut, err := mp.verifiedRequests(ctx, repo, policy, c.UID, func(delivery.OperationRequest) bool { return true })
	if err != nil {
		return nil, "its request stream could not be read: " + err.Error()
	}
	var rows []MemberRefusal
	var unread []string
	if unverified > 0 {
		unread = append(unread, fmt.Sprintf("%s did not verify and %s not read", plural(unverified, "request"), were(unverified)))
	}
	for _, r := range reqs {
		o, bad, ocut, err := mp.outcomeOf(ctx, repo, c.UID, r.Manifest)
		switch {
		case err != nil:
			unread = append(unread, "the outcomes of a request could not be read: "+err.Error())
			continue
		case bad > 0:
			unread = append(unread, fmt.Sprintf("%s did not verify and %s not read", plural(bad, "outcome"), were(bad)))
		}
		cut = cut || ocut
		if refused(o) {
			rows = append(rows, MemberRefusal{Member: delivery.ClusterRef{UID: c.UID, Name: c.Name}, Request: r.Request, Outcome: mp.restricted(c.UID, *o)})
		}
	}
	if cut {
		unread = append(unread, "its stream holds more records than a read takes; newer ones are read next time")
	}
	return rows, strings.Join(unread, "; ")
}

// refused reports an outcome that refused its request: at intake, or as an
// Operation that concluded Rejected.
func refused(o *delivery.RequestOutcome) bool {
	return o != nil && (o.State == delivery.RequestRefused || o.State == delivery.RequestConcluded && o.Outcome == delivery.Rejected)
}

// restricted is o as a restricted reader sees it on member uid: without the
// Operation, and its namespace out of the reasons, when that member's review
// does not let them read the namespace.
func (mp *memberPlanning) restricted(uid string, o delivery.RequestOutcome) delivery.RequestOutcome {
	if o.Operation == nil || mp.opts.Restrict == nil || mp.opts.Restrict(uid, o.Operation.Namespace) {
		return o
	}
	ns := o.Operation.Namespace
	o.Operation = nil
	o.Reason = strings.ReplaceAll(o.Reason, ns, "a namespace you may not read")
	o.Published = strings.ReplaceAll(o.Published, ns, "a namespace you may not read")
	return o
}
