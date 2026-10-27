package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// ── actions ────────────────────────────────────────────────────────────

type planInput struct {
	Body delivery.Request
}

type planOutput struct {
	Body delivery.Plan
}

func (s *Server) deliveryPlan(ctx context.Context, in *planInput) (*planOutput, error) {
	if err := s.inScope(in.Body.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	p, err := api.Plan(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &planOutput{Body: p}, nil
}

type proposeOutput struct {
	Body struct {
		Operation *delivery.OperationObject `json:"operation"`
		Plan      delivery.Plan             `json:"plan"`
	}
}

func (s *Server) deliveryPropose(ctx context.Context, in *planInput) (*proposeOutput, error) {
	if in.Body.Type == delivery.TypeOverride {
		return nil, Refuse(http.StatusBadRequest, "OverrideFromCLI", "an override is break-glass: raise it with `pacto delivery propose --type override`")
	}
	if err := s.inScope(in.Body.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	op, p, err := api.Propose(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	out := &proposeOutput{}
	out.Body.Operation, out.Body.Plan = op, p
	return out, nil
}

type decideInput struct {
	Namespace string `path:"namespace"`
	Name      string `path:"name"`
	Body      struct {
		Decision delivery.Decision `json:"decision" enum:"approve,reject,accept-unknown"`
		Note     string            `json:"note,omitempty"`
	}
}

type decideOutput struct {
	Body struct {
		Approval string `json:"approval" doc:"The Approval created; the promoter consumes it"`
	}
}

// deliveryDecide is the fresh-login decision flow: local mode never decides,
// a decision needs a session from a login the person repeated with an
// auth-time under DecisionFreshnessFor the Stage's published freshness, and
// the decision must be one the promoter counts (delivery.ApprovalRefusal):
// never the asker's, never an agent's, only from the teams the question
// names. The promoter checks it all again.
func (s *Server) deliveryDecide(ctx context.Context, in *decideInput) (*decideOutput, error) {
	a := s.opts.Auth
	cli := fmt.Sprintf("pacto delivery approve -n %s %s --decision %s", in.Namespace, in.Name, in.Body.Decision)
	switch {
	case a == nil || a.Local:
		return nil, Refuse(http.StatusForbidden, "ApproveFromCLI",
			"a local dashboard never decides: any process on this machine could do it as you. Run: "+cli)
	case !a.Login && !a.TokenLogin:
		return nil, Refuse(http.StatusForbidden, "LoginNotConfigured", "this dashboard has no sign-in, so it serves no decisions")
	}
	st := s.authState(ctx)
	switch {
	case !st.SignedIn:
		return nil, Refuse(http.StatusUnauthorized, "LoginRequired", "sign in to decide")
	case st.Method == "token" && a.Login:
		return nil, Refuse(http.StatusForbidden, "TokenCannotDecide",
			"a pasted token never decides: sign in with your identity provider to decide, or run: "+cli)
	case st.Method == "token":
		return nil, Refuse(http.StatusForbidden, "TokenCannotDecide",
			"a pasted token never decides, and this dashboard offers no identity provider sign-in. Run: "+cli)
	case !st.Fresh:
		return nil, Refuse(http.StatusUnauthorized, "FreshLoginRequired", "deciding needs a fresh login: sign in again")
	}
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	op, err := api.Get(ctx, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	published := "" // the promoter's login freshness, as the Stage publishes it
	if stage, err := api.Stage(ctx, in.Namespace, op.Spec.Stage); err == nil {
		published = stage.Status.AuthFreshness
	}
	if err := s.mayDecide(caller, published); err != nil {
		return nil, err
	}
	// The promoter's own rule: the dashboard records only a decision it counts.
	approval := approvalOf(op, caller, in.Body.Decision)
	if code, why := delivery.ApprovalRefusal(op.Spec, op.Status, op.Metadata.UID, approval, time.Now(), caller.Policy, delivery.PublishedFreshness(published)); code != "" {
		status := http.StatusForbidden
		if code == "FreshLoginRequired" {
			status = http.StatusUnauthorized
		}
		return nil, Refuse(status, code, why)
	}
	name, err := api.Approve(ctx, in.Namespace, in.Name, in.Body.Decision, in.Body.Note)
	if err != nil {
		return nil, err
	}
	out := &decideOutput{}
	out.Body.Approval = name
	return out, nil
}

// approvalOf is the Approval caller would create for decision on op.
func approvalOf(op *delivery.OperationObject, caller delivery.Caller, decision delivery.Decision) delivery.ApprovalSpec {
	return delivery.ApprovalSpec{Operation: op.Metadata.Name, OperationUID: op.Metadata.UID, IntentDigest: op.Spec.IntentDigest,
		Decision: decision, By: caller.User, Groups: caller.Groups, AuthTime: caller.AuthTime}
}

// mayDecide refuses a caller the apiserver names an agent, or whose login is
// unknown or older than two thirds of the login freshness the Stage
// publishes (delivery.DecisionFreshnessFor), so the promoter still counts it.
func (s *Server) mayDecide(caller delivery.Caller, published string) error {
	at, ok := delivery.ParseAuthTime(caller.AuthTime)
	switch {
	case !caller.Policy.IsHuman(caller.Identity):
		return Refuse(http.StatusForbidden, "AgentsMayNotApprove", "the apiserver says "+caller.User+" is an agent; agents may not decide")
	case caller.AuthTime == "":
		return Refuse(http.StatusForbidden, "AuthTimeUnavailable",
			"this cluster does not map auth_time to userInfo.extra[delivery.pacto.trianalab.io/auth-time]; nobody can approve here")
	case !ok || time.Since(at) > delivery.DecisionFreshnessFor(published):
		return Refuse(http.StatusUnauthorized, "FreshLoginRequired",
			fmt.Sprintf("your login is older than %s: sign in again", delivery.DecisionFreshnessFor(published)))
	}
	return nil
}

type cancelOutput struct {
	Body struct {
		Cancelled string `json:"cancelled"`
	}
}

func (s *Server) deliveryCancel(ctx context.Context, in *operationInput) (*cancelOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := api.Cancel(ctx, in.Namespace, in.Name); err != nil {
		return nil, err
	}
	out := &cancelOutput{}
	out.Body.Cancelled = in.Namespace + "/" + in.Name
	return out, nil
}
