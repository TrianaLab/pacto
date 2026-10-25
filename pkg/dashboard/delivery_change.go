package dashboard

import (
	"context"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// ── one change: its view and the actions offered on it ─────────────────

type operationInput struct {
	Namespace string `path:"namespace"`
	Name      string `path:"name"`
}

type operationOutput struct {
	Body struct {
		Explanation *delivery.Explanation     `json:"explanation"`
		Operation   *delivery.OperationObject `json:"operation"`
		Actions     []string                  `json:"actions" doc:"What the caller may ask for: approve, reject, accept-unknown, cancel, rollback"`
		Labels
	}
}

func (s *Server) deliveryOperation(ctx context.Context, in *operationInput) (*operationOutput, error) {
	if err := s.inScope(in.Namespace); err != nil {
		return nil, err
	}
	api, err := s.deliveryFor(ctx)
	if err != nil {
		return nil, err
	}
	e, err := api.Explain(ctx, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	op, err := api.Get(ctx, in.Namespace, in.Name)
	if err != nil {
		return nil, err
	}
	caller, err := api.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	out := &operationOutput{}
	out.Body.Explanation, out.Body.Operation = e, op
	out.Body.Actions = s.actions(ctx, op, e.Stage, caller)
	out.Body.Versions = stageVersions(ctx, api, in.Namespace, op.Spec.Stage, op.Spec.Params.From, op.Spec.Params.To,
		op.Status.Pointer.Before, op.Status.Pointer.After, e.Stage.Pointer)
	people := []string{op.Spec.Actor.User}
	if op.Spec.OnBehalfOf != nil {
		people = append(people, op.Spec.OnBehalfOf.User)
	}
	for _, a := range op.Status.Approvals {
		people = append(people, a.By)
	}
	out.Body.People = s.named(people...)
	return out, nil
}

// actions are what the UI offers on an operation. They only hide what the
// caller cannot do; the apiserver and the promoter still decide.
func (s *Server) actions(ctx context.Context, op *delivery.OperationObject, stage delivery.StageNow, caller delivery.Caller) []string {
	acts := []string{}
	st := op.Status
	if d := inboxItem(*op).Decision; st.Phase == delivery.PhaseHeld && s.decisionsServed(ctx) && mayOffer(op, caller, d) {
		acts = append(acts, string(d), string(delivery.Reject))
	}
	if st.Phase != delivery.PhaseConcluded && !st.Applied {
		acts = append(acts, "cancel")
	}
	if st.Phase == delivery.PhaseConcluded && st.Applied && st.Pointer.After != "" && st.Pointer.After == stage.Pointer &&
		delivery.MayRaise(delivery.TypeRollback, caller.Class, false) {
		acts = append(acts, "rollback")
	}
	return acts
}

// mayOffer: the promoter would count caller's decision on op, apart from how
// old their login is (deciding asks for a fresh one). A cluster that reports
// no login time (userInfo.extra[pacto.dev/auth-time]) counts nobody's, so it
// offers nothing.
func mayOffer(op *delivery.OperationObject, caller delivery.Caller, decision delivery.Decision) bool {
	return caller.AuthTime != "" && answerRefusal(op, caller, decision) == ""
}

// answerRefusal is why the promoter would not count caller's decision on op
// as a code, apart from how old their login is; "" when it would.
func answerRefusal(op *delivery.OperationObject, caller delivery.Caller, decision delivery.Decision) string {
	now := time.Now()
	a := approvalOf(op, caller, decision)
	a.AuthTime = now.UTC().Format(time.RFC3339)
	code, _ := delivery.ApprovalRefusal(op.Spec, op.Status, op.Metadata.UID, a, now, caller.Policy, delivery.AuthFreshness)
	return code
}

// decisionsServed: decisions only in shared mode, from a signed-in browser.
func (s *Server) decisionsServed(ctx context.Context) bool {
	a := s.opts.Auth
	return a != nil && !a.Local && a.Login && s.authState(ctx).SignedIn
}
