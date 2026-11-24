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
	Cluster string `query:"cluster" doc:"The cluster to act in; empty for the dashboard's own, the only one it acts in: another is refused with RemoteClusterReadOnly"`
	Body    delivery.Request
}

type planOutput struct {
	Body delivery.PlanAnswer
}

func (s *Server) deliveryPlan(ctx context.Context, in *planInput) (*planOutput, error) {
	if err := s.inScope(in.Body.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.refuseRemote(ctx, in.Cluster, in.Body.Namespace, ""); err != nil {
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
		Plan      delivery.PlanAnswer       `json:"plan"`
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
	if err := s.refuseRemote(ctx, in.Cluster, in.Body.Namespace, ""); err != nil {
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
	Cluster   string `query:"cluster" doc:"The cluster of the operation; empty for the dashboard's own, the only one it acts in: another is refused with RemoteClusterReadOnly"`
	Body      struct {
		Decision delivery.Decision `json:"decision" enum:"approve,reject,accept-unknown"`
		Note     string            `json:"note,omitempty"`
	}
}

type decideOutput struct {
	Body struct {
		Approval string `json:"approval" doc:"The Approval created; the promoter consumes it"`
		Signing  string `json:"signing,omitempty" enum:"recorder" doc:"recorder when the recorder wrote the operation's intent record, so it signs the decision into the registry on the person's behalf; empty otherwise"`
	}
}

// decisionSession refuses a decision this dashboard's sign-in cannot carry:
// a local or sign-in-free dashboard or no session. cli is the command that
// decides instead. A token session decides like any session: mayDecide
// refuses ServiceAccounts as agents, and a sign-in link session never gets
// here because its delivery API refuses it first.
func (s *Server) decisionSession(ctx context.Context, cli string) error {
	a := s.authSummary(ctx)
	switch a.Mode {
	case "none", "local":
		return Refuse(http.StatusForbidden, "ApproveFromCLI",
			"a local dashboard never decides: any process on this machine could do it as you. Run: "+cli)
	case "open":
		return Refuse(http.StatusForbidden, "LoginNotConfigured", "this dashboard has no sign-in, so it serves no decisions")
	}
	if !a.State.SignedIn { // defence in depth: the sign-in gate turns a signed-out request away first
		return Refuse(http.StatusUnauthorized, "LoginRequired", "sign in to decide")
	}
	return nil
}

// deliveryDecide is the decision flow, the same for an identity provider and
// a person's token session: local mode never decides; on a Stage
// that publishes a login freshness a decision needs an auth-time under
// DecisionFreshnessFor it; and
// the decision must be one the promoter counts (delivery.ApprovalRefusal):
// never the asker's, never an agent's, only from the teams the question
// names. The promoter checks it all again.
func (s *Server) deliveryDecide(ctx context.Context, in *decideInput) (*decideOutput, error) {
	cli := fmt.Sprintf("pacto delivery approve -n %s %s --decision %s", in.Namespace, in.Name, in.Body.Decision)
	if err := s.decisionSession(ctx, cli); err != nil {
		return nil, err
	}
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.refuseRemote(ctx, in.Cluster, in.Namespace, in.Name); err != nil {
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
	// The promoter's login freshness, as the Stage publishes it. A Stage the
	// caller cannot read leaves it unknown, so the decision is refused rather
	// than recorded and then not counted.
	stage, err := api.Stage(ctx, in.Namespace, op.Spec.Stage)
	if err != nil {
		return nil, err
	}
	published := stage.Status.AuthFreshness
	if err := s.mayDecide(caller, published, s.authSummary(ctx).State.Method == "token"); err != nil {
		return nil, err
	}
	// The promoter's own rule: the dashboard records only a decision it counts.
	approval := approvalOf(op, caller, in.Body.Decision)
	access := delivery.AccessMemo([]delivery.StageObject{*stage}, api.WorkloadAccess)(ctx, in.Namespace, op.Spec.Stage)
	if code, why := delivery.ApprovalRefusal(op.Spec, op.Status, op.Metadata.UID, approval, time.Now(), caller.Policy, delivery.PublishedFreshness(published), access); code != "" {
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
	// The recorder wrote this operation's intent record, so it is running for
	// it and signs the decision too. An anchor alone could be left over from
	// a recorder since turned off.
	if op.Status.IntentRecord != nil {
		out.Body.Signing = "recorder"
	}
	return out, nil
}

// approvalOf is the Approval caller would create for decision on op.
func approvalOf(op *delivery.OperationObject, caller delivery.Caller, decision delivery.Decision) delivery.ApprovalSpec {
	return delivery.ApprovalSpec{Operation: op.Metadata.Name, OperationUID: op.Metadata.UID, IntentDigest: op.Spec.IntentDigest,
		Decision: decision, By: caller.User, Groups: caller.Groups, AuthTime: caller.AuthTime}
}

// mayDecide refuses a caller the apiserver names an agent. On a Stage that
// publishes a login freshness it also refuses a login that is unknown or
// older than two thirds of that freshness (delivery.DecisionFreshnessFor), so
// the promoter still counts it. An unknown login time of a pasted token is
// the token's (static, EKS and kubeconfig tokens carry none), never proof
// the cluster maps none.
func (s *Server) mayDecide(caller delivery.Caller, published string, token bool) error {
	at, ok := delivery.ParseAuthTime(caller.AuthTime)
	window := delivery.DecisionFreshnessFor(published)
	switch {
	case !caller.Policy.IsHuman(caller.Identity):
		return Refuse(http.StatusForbidden, "AgentsMayNotApprove", "agents cannot approve: the apiserver says "+caller.User+" is an agent")
	case window == 0:
		return nil
	case caller.AuthTime == "" && token:
		return Refuse(http.StatusForbidden, "AuthTimeUnavailable",
			"this token does not say when you signed in, so it cannot approve on a Stage that asks for a recent sign-in: sign in with your identity provider")
	case caller.AuthTime == "":
		return Refuse(http.StatusForbidden, "AuthTimeUnavailable",
			"this cluster does not map auth_time to userInfo.extra[delivery.pacto.trianalab.io/auth-time]; nobody can approve here")
	case !ok || time.Since(at) > window:
		return Refuse(http.StatusUnauthorized, "FreshLoginRequired", fmt.Sprintf("your login is older than %s: sign in again", window))
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
	if err := s.refuseRemote(ctx, in.Cluster, in.Namespace, in.Name); err != nil {
		return nil, err
	}
	if err := api.Cancel(ctx, in.Namespace, in.Name); err != nil {
		return nil, err
	}
	out := &cancelOutput{}
	out.Body.Cancelled = delivery.StageKey(delivery.ObjectMeta{Namespace: in.Namespace}, in.Name)
	return out, nil
}
