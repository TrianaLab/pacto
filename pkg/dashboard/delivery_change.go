package dashboard

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"golang.org/x/sync/errgroup"
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
		Level       string                    `json:"level,omitempty" doc:"The Stage's level now: observe, advise, auto-nonbreaking or autonomous"`
		UndoneBy    *delivery.HistoryEntry    `json:"undoneBy,omitempty" doc:"The rollback that undid this change, read from its Stage's history"`
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
	out.Body.Actions = s.actions(ctx, api, op, e.Stage, caller)
	stage, err := api.Stage(ctx, in.Namespace, op.Spec.Stage)
	if err == nil {
		out.Body.Level = ladderOf(*stage).Level
	} else {
		stage = nil
	}
	if st := op.Status; st.Phase == delivery.PhaseConcluded && st.Pointer.After != "" {
		// ponytail: one history read per concluded change shown; the live
		// host's record stream read is what it costs.
		if h, err := api.History(ctx, in.Namespace, op.Spec.Stage, stage); err == nil {
			out.Body.UndoneBy = undoneBy(h.Entries, in.Name, st.Pointer.After, st.Times.Finished)
		}
	}
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

// undoneBy is the rollback that undid the change name, which moved its Stage
// to after: the first change after it (entries are oldest first) that moved
// the Stage off after. One that did not move it, failed or still running,
// undid nothing. That is the undo only if it is a rollback; anything else
// means the Stage moved on, and a later rollback from after undid that, not
// this.
// ponytail: matched by version, order and time, because history lines do not
// name what a rollback undoes; read params.undoes if they ever do.
func undoneBy(entries []delivery.HistoryEntry, name, after, finished string) *delivery.HistoryEntry {
	i := slices.IndexFunc(entries, func(e delivery.HistoryEntry) bool { return e.Operation == name })
	for _, e := range entries[i+1:] {
		if e.Operation == "" || e.From != after || e.Pointer == nil || e.Pointer.After == "" || finished != "" && e.Finished != "" && e.Finished < finished {
			continue
		}
		if e.Type == delivery.TypeRollback {
			return &e
		}
		return nil
	}
	return nil
}

// actions are what the UI offers on an operation. They only hide what the
// caller cannot do, as the promoter's rules and the caller's own RBAC say;
// the apiserver and the promoter still decide.
func (s *Server) actions(ctx context.Context, api DeliveryAPI, op *delivery.OperationObject, stage delivery.StageNow, caller delivery.Caller) []string {
	acts := []string{}
	st := op.Status
	may := func(verb, resource string) bool { return mayIn(ctx, api, verb, resource)(op.Metadata.Namespace) }
	if d := inboxItem(*op).Decision; st.Phase == delivery.PhaseHeld && s.decisionsServed(ctx) && mayOffer(op, caller, d) && may("create", "approvals") {
		acts = append(acts, string(d), string(delivery.Reject))
	}
	if cancellable(op, caller) && may("delete", "operations") {
		acts = append(acts, "cancel")
	}
	if rollbackable(op, stage, caller) && may("create", "operations") {
		acts = append(acts, "rollback")
	}
	return acts
}

// cancellable: op has not concluded or applied, and caller may cancel it.
// Admission lets an agent cancel only what it proposed itself.
func cancellable(op *delivery.OperationObject, caller delivery.Caller) bool {
	st := op.Status
	return st.Phase != delivery.PhaseConcluded && !st.Applied && (caller.Class != delivery.ClassAgent || op.Spec.Actor.User == caller.User)
}

// rollbackable: op applied the version the Stage runs now, and caller's
// class may raise a rollback.
func rollbackable(op *delivery.OperationObject, stage delivery.StageNow, caller delivery.Caller) bool {
	st := op.Status
	return st.Phase == delivery.PhaseConcluded && st.Applied && st.Pointer.After != "" && st.Pointer.After == stage.Pointer &&
		delivery.MayRaise(delivery.TypeRollback, caller.Class, false)
}

// mayIn asks the apiserver, once per namespace, whether the caller may verb
// resource there. A review that fails says no: nothing is offered that the
// caller may not do. Not safe for concurrent use.
func mayIn(ctx context.Context, api DeliveryAPI, verb, resource string) func(ns string) bool {
	answers := map[string]bool{}
	return func(ns string) bool {
		ok, asked := answers[ns]
		if !asked {
			can, err := api.Can(ctx, verb, resource, ns)
			ok = err == nil && can
			answers[ns] = ok
		}
		return ok
	}
}

// mayRaise drops the actions of each row whose namespace the caller's RBAC
// refuses an Operation in. Only namespaces with something to offer are
// asked, describeParallel at once, so a reader's board costs no review; a
// review that fails says no.
func mayRaise(ctx context.Context, api DeliveryAPI, rows []EnvironmentRow) {
	var asked []string
	for _, r := range rows {
		if len(r.Actions) > 0 && !slices.Contains(asked, r.Namespace) {
			asked = append(asked, r.Namespace)
		}
	}
	allowed := map[string]bool{}
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(describeParallel)
	for _, ns := range asked {
		g.Go(func() error {
			ok, err := api.Can(ctx, "create", "operations", ns)
			mu.Lock()
			allowed[ns] = err == nil && ok
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	for i := range rows {
		if len(rows[i].Actions) > 0 && !allowed[rows[i].Namespace] {
			rows[i].Actions = []string{}
		}
	}
}

// mayOffer: the promoter would count caller's decision on op, apart from how
// old their login is (deciding asks for a fresh one). A cluster that reports
// no login time (userInfo.extra[delivery.pacto.trianalab.io/auth-time]) counts nobody's, so it
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
