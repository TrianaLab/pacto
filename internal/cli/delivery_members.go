package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// memberPlanHub is how the hub, the current kubeconfig context, plans over the
// members that publish to --registry, asking with the person's own
// credentials.
func memberPlanHub(cmd *cobra.Command, d deps) (app.MemberPlanOptions, error) {
	registry, _ := cmd.Flags().GetString("registry")
	if registry == "" {
		return app.MemberPlanOptions{}, errors.New("a member request reads the members from the registry: pass --registry")
	}
	ns, _ := cmd.Flags().GetString("delivery-namespace")
	_, opts, err := hubJoins(cmd.Context(), d, "", registry, "", ns)
	if err != nil {
		return app.MemberPlanOptions{}, err
	}
	return app.MemberPlanOptions{Repository: opts.Repository, Hub: opts.Hub, Delivery: opts.Delivery, Authorize: opts.Authorize}, nil
}

// planMembers plans req, a member request, over the hub's members.
func planMembers(cmd *cobra.Command, d deps, svc *app.Service, req delivery.Request) (delivery.PlanAnswer, app.MemberPlanOptions, error) {
	opts, err := memberPlanHub(cmd, d)
	if err != nil {
		return delivery.PlanAnswer{}, opts, err
	}
	mp, err := svc.MemberPlan(cmd.Context(), req, opts)
	return delivery.PlanAnswer{Members: &mp}, opts, err
}

// placementWords say where a member placement came from.
func placementWords(p delivery.MemberPlacementPlan) string {
	switch {
	case p.Source == delivery.PlacementFromPolicy && len(p.Placed) > 0:
		return "(" + p.Placed[0].Evidence + ")"
	case p.Source == delivery.RequestPlacedAdvertised:
		return "(confirmed by " + p.ConfirmedBy + ")"
	}
	return "(proposed from the environments they take requests for)"
}

// printMemberPlan prints a plan over the members: the placement, one line
// per member, what was hidden or not read and the confirming command.
func printMemberPlan(w io.Writer, p delivery.MemberPlan) {
	var names []string
	for _, c := range p.Placement.Placed {
		names = append(names, c.Name)
	}
	_, _ = fmt.Fprintf(w, "placement: %s %s\n", orNone([]string{strings.Join(names, ", ")}), placementWords(p.Placement))
	for _, m := range p.Members {
		line := m.Name + " (" + shortUID(m.UID) + "): "
		if m.Stale {
			line += "stale, "
		}
		line += strings.Join(slices.DeleteFunc([]string{m.State, short(m.Digest), m.Operation}, func(s string) bool { return s == "" }), " ")
		if m.Reason != "" {
			line += " (" + m.Reason + ")"
		}
		if m.AsOf != "" {
			line += " as of " + m.AsOf
		}
		_, _ = fmt.Fprintln(w, line)
		if m.Next != "" {
			_, _ = fmt.Fprintf(w, "  next: %s\n", m.Next)
		}
	}
	for _, u := range p.Placement.Unplaced {
		_, _ = fmt.Fprintf(w, "not placed: %s (%s): %s\n", u.Name, shortUID(u.UID), u.Reason)
	}
	if p.Hidden > 0 {
		_, _ = fmt.Fprintf(w, "%s hidden: %s\n", plural(p.Hidden, "member"), p.HiddenReason)
	}
	for _, n := range p.Notes {
		_, _ = fmt.Fprintf(w, "note: %s\n", n)
	}
	_, _ = fmt.Fprintf(w, "as of %s (%s)\n", p.AsOf, p.Completeness)
	if p.Next != "" {
		_, _ = fmt.Fprintf(w, "confirm with: %s\n", p.Next)
	}
}

// short is a digest as a member line shows it.
func short(d string) string {
	if len(d) <= len("sha256:")+12 {
		return d
	}
	return d[:len("sha256:")+12]
}

// plural is "1 word" or "n words".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// proposeMembers plans req over the members and, with --confirm, logs the
// person in and signs one request per placed member, or per --member; it
// signs nothing without --confirm.
func proposeMembers(cmd *cobra.Command, v *viper.Viper, d deps, svc *app.Service, req delivery.Request) error {
	plan, opts, err := planMembers(cmd, d, svc, req)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if confirm, _ := cmd.Flags().GetBool("confirm"); !confirm {
		printMemberPlan(w, *plan.Members)
		return delivery.HubRefusal(delivery.RefusalMemberRequestNeedsPerson, "nothing was signed: a person confirms a member request by signing it", req, opts.Repository)
	}
	mr := &memberRequests{cmd: cmd, v: v, d: d, svc: svc, req: req, opts: opts, plan: *plan.Members}
	reqs, err := mr.build()
	if err != nil {
		return err
	}
	refs, mode, err := svc.WriteOperationRequests(cmd.Context(), reqs, mr.signer, opts)
	if err != nil {
		return err
	}
	for i, ref := range refs {
		_, _ = fmt.Fprintf(w, "requested %s on %s as %s; recorded %s\n", short(req.To), mr.names[i], mr.user, ref)
		if mode == capabilities.ReferrersWriteOnceTags {
			_, dg, _ := strings.Cut(ref, "@")
			_, _ = fmt.Fprintf(w, "pushed %s:%s (write-once tag; registry has no Referrers)\n", opts.Repository,
				deliveryrecord.KindTag(delivery.ClusterRequestStream(reqs[i].Cluster), deliveryrecord.KindRequest, digest.Digest(dg)))
		}
	}
	return nil
}

// memberRequests is a person's confirmation on its way to signed requests.
type memberRequests struct {
	cmd    *cobra.Command
	v      *viper.Viper
	d      deps
	svc    *app.Service
	req    delivery.Request
	opts   app.MemberPlanOptions
	plan   delivery.MemberPlan
	signer delivery.Signer
	user   string
	names  []string
}

// build resolves the members, refuses what a member would not take, logs
// the person in and returns one request per member: every check that can
// refuse runs before the login.
func (mr *memberRequests) build() ([]delivery.OperationRequest, error) {
	members, err := mr.targets()
	if err != nil {
		return nil, err
	}
	policy, _ := mr.opts.Delivery.ClassPolicy(mr.cmd.Context()) // the members were approved under it: unread, it lists no signer
	issuer, _ := mr.cmd.Flags().GetString("issuer")
	signer, ok := pickSigner(policy, issuer)
	if !ok {
		return nil, errors.New("pick the approver signer with --issuer; the hub's identity policy lists " + plural(len(policy.Signers), "signer"))
	}
	for _, m := range members {
		if !slices.Contains(m.Requests.Issuers, signer.Issuer) {
			return nil, delivery.HubRefusal(delivery.RefusalIssuerNotAccepted, m.Name+" takes requests signed through "+strings.Join(m.Requests.Issuers, ", ")+", not "+signer.Issuer, mr.req, mr.opts.Repository)
		}
	}
	dyn, err := mr.d.newDynamic()
	if err == nil {
		var me authn.Person
		me, _, err = authn.ReviewSelf(mr.cmd.Context(), dyn)
		mr.user = me.User
	}
	if err != nil {
		return nil, fmt.Errorf("asking the hub who you are: %w", err)
	}
	if mr.signer, _, err = personLogin(mr.cmd, mr.v, mr.d, signer, policy, mr.user, "a member request", mr.req.Service+" in "+mr.req.Environment); err != nil {
		return nil, err
	}
	return mr.requests(members), nil
}

// targets are the members each --member names, each approved and taking
// requests, or else every placed member.
func (mr *memberRequests) targets() ([]delivery.ClusterJoinState, error) {
	list := mr.svc.ClusterJoins(mr.cmd.Context(), app.JoinOptions{Repository: mr.opts.Repository, Hub: mr.opts.Hub, Authorize: mr.opts.Authorize, Delivery: mr.opts.Delivery})
	uids := mr.req.Members
	for _, c := range mr.plan.Placement.Placed {
		if len(mr.req.Members) == 0 {
			uids = append(uids, c.UID)
		}
	}
	if len(uids) == 0 {
		return nil, delivery.HubRefusal(delivery.RefusalNotAMember, "no member is placed for "+mr.req.Service+" in "+mr.req.Environment, mr.req, mr.opts.Repository)
	}
	var out []delivery.ClusterJoinState
	for _, uid := range uids {
		m, err := findCluster(list, uid)
		switch {
		case err != nil:
			return nil, delivery.HubRefusal(delivery.RefusalNotAMember, err.Error(), mr.req, mr.opts.Repository)
		case m.State != delivery.JoinApproved:
			return nil, delivery.HubRefusal(delivery.RefusalNotAMember, clusterName(m)+" ("+shortUID(m.UID)+") is "+string(m.State), mr.req, mr.opts.Repository)
		case m.Requests == nil:
			return nil, delivery.HubRefusal(delivery.RefusalMemberTakesNoRequests, clusterName(m)+" does not take requests", mr.req, mr.opts.Repository)
		}
		out = append(out, m)
		mr.names = append(mr.names, clusterName(m)+" ("+shortUID(m.UID)+")")
	}
	return out, nil
}

// requests are the signed-to-be requests, one per member, carrying the
// placement the person confirms.
func (mr *memberRequests) requests(members []delivery.ClusterJoinState) []delivery.OperationRequest {
	pl := delivery.RequestPlacement{Source: delivery.RequestPlacedAdvertised, ConfirmedBy: mr.user}
	if p := mr.plan.Placement; p.Source == delivery.PlacementFromPolicy {
		pl = delivery.RequestPlacement{Source: delivery.RequestPlacedPolicy, Policy: p.Policy}
	}
	for _, m := range members {
		pl.Clusters = append(pl.Clusters, m.UID)
	}
	now := time.Now().UTC()
	var out []delivery.OperationRequest
	for _, m := range members {
		out = append(out, delivery.OperationRequest{Cluster: m.UID, Hub: mr.opts.Hub, Service: delivery.ServiceRef{Name: mr.req.Service},
			Environment: mr.req.Environment, Digest: mr.req.To, Reason: mr.req.Reason, CorrelationID: mr.req.CorrelationID, Requester: mr.user,
			Placement: pl, At: now.Format(time.RFC3339), Expires: now.Add(delivery.DefaultRequestLifetime).Format(time.RFC3339)})
	}
	return out
}

// memberIntentTrust, with --registry and no --trust, makes c verify intent
// records with the recorder keys of the hub's approved members, and
// refuses an intent of a member that advertises no recorder key. Without
// --signers the hub's approver signers sign the decision.
func memberIntentTrust(cmd *cobra.Command, d deps, svc *app.Service, c *app.Delivery, ref string) error {
	opts, err := memberPlanHub(cmd, d)
	if err != nil {
		return err
	}
	trust, states := svc.MemberTrust(cmd.Context(), opts)
	c.Trust = trust
	if signers, _ := cmd.Flags().GetString("signers"); signers == "" {
		if c.Policy, err = opts.Delivery.ClassPolicy(cmd.Context()); err != nil {
			return fmt.Errorf("the hub's approver signers could not be read: %w", err)
		}
	}
	at, err := c.RecordAt(cmd.Context(), ref)
	if err != nil || at.Intent == nil || at.Verified {
		return nil // IntentAt says why
	}
	uid, _, _ := strings.Cut(at.Intent.Predicate.Intent.Stage.Stream(), "/")
	i := slices.IndexFunc(states, func(s delivery.ClusterJoinState) bool { return s.UID == uid && s.State == delivery.JoinApproved })
	if i >= 0 && states[i].RecorderKey == nil {
		return delivery.HubRefusal(delivery.RefusalMemberRecorderUnknown, states[i].Name+" advertises no recorder key, so its intent records verify under no key; nothing was signed",
			delivery.Request{}, opts.Repository)
	}
	return nil
}
