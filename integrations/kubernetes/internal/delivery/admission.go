/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// admit validates the intent once. Everything checked here is a property of
// the request, so a refusal is final.
func (r *OperationReconciler) admit(ctx context.Context, op *dv1.Operation) (reconcile.Result, error) {
	if res, done, err := r.unguarded(ctx, op); done {
		return res, err
	}
	// A refusal is final, so admission never judges a cached Stage that may
	// lag a pointer move or a policy edit: it reads the Stage uncached, once.
	stage := &dv1.Stage{}
	if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.Stage}, stage); err != nil {
		return reconcile.Result{}, err
	}
	now := r.now()
	class := r.Policy.Classify(delivery.Identity{User: op.Spec.Actor.User, Groups: op.Spec.Actor.Groups})
	op.Status.ActorClass = class
	op.Status.Guarded = len(r.AdmissionPolicies) > 0 // unguarded() just found them enforcing
	op.Status.Times.Admitted = ts(now)
	t, known := delivery.TypeByID(op.Spec.Type)
	approval, _ := t.Deadlines(stage.Spec.Deadlines)
	op.Status.Times.ApprovalDeadline = ts(now.Add(approval))
	reason, err := r.refusal(ctx, op, stage, t, known)
	if err != nil {
		return reconcile.Result{}, err
	}
	if reason != "" {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: reason})
	}
	op.Status.Phase = delivery.PhasePending
	if err := r.Status().Update(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: immediately}, nil
}

// refusal returns why op is refused, or "". An error is a read that failed:
// the reconcile retries rather than judge on missing operations.
func (r *OperationReconciler) refusal(ctx context.Context, op *dv1.Operation, stage *dv1.Stage, t delivery.OperationType, known bool) (string, error) {
	s := op.Spec
	switch {
	case !known:
		return "UnknownType: " + s.Type + " is not an operation type", nil
	case s.Facet != t.Facet:
		return fmt.Sprintf("WrongFacet: %s changes the %s facet, not %s", s.Type, t.Facet, s.Facet), nil
	case !delivery.MayRaise(s.Type, op.Status.ActorClass, s.Witnessed):
		return fmt.Sprintf("NotPermitted: a %s actor may not raise %s", op.Status.ActorClass, s.Type), nil
	case s.IntentDigest != delivery.IntentDigest(s, stage.Spec):
		return "PlanStale: the intent digest does not match this Stage's policy; plan again", nil
	}
	if reason, err := r.causeRefusal(ctx, op, stage); reason != "" || err != nil {
		return reason, err
	}
	if s.Type == delivery.TypeFreeze && s.Params.From != stage.Status.Pointer {
		return fmt.Sprintf("PointerMoved: a freeze stays on %q but the Stage runs %q", s.Params.From, stage.Status.Pointer), nil
	}
	if s.Facet != delivery.FacetPointer {
		return "", nil
	}
	if _, err := delivery.ParseDigest(s.Params.To); err != nil {
		return "InvalidDigest: to must be an OCI manifest digest, not " + strconv.Quote(s.Params.To), nil
	}
	if _, err := delivery.ParseDigest(s.Params.From); s.Params.From != "" && err != nil {
		return "InvalidDigest: from must be an OCI manifest digest, not " + strconv.Quote(s.Params.From), nil
	}
	if s.Params.From != stage.Status.Pointer {
		return fmt.Sprintf("PointerMoved: from is %q but the Stage runs %q", s.Params.From, stage.Status.Pointer), nil
	}
	if s.Type != delivery.TypeRollback {
		return "", nil
	}
	cited, err := r.cited(ctx, op)
	if err != nil {
		return "", err
	}
	return delivery.AdmitRollback(s, stage.Status, cited, op.Status.ActorClass), nil
}

// causeRefusal re-runs a verdict an actor cites as its cause, against this
// Stage's own gates and the version the Stage runs now. An agent's word counts
// for nothing until it is checked: a re-run that comes back Unknown (an empty
// one included) or with another result is refused as a cause. The promoter's
// own operations cite verdicts it has just computed.
func (r *OperationReconciler) causeRefusal(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (string, error) {
	c := op.Spec.Cause
	if c == nil || c.Verdict == nil || op.Status.ActorClass == delivery.ClassController {
		return "", nil
	}
	// The cited window must fall in the time the running version has run. With
	// no applied time for it (its operation is gone, or Flux never confirmed it)
	// no window can be about it, so nothing is confirmed.
	now, from := r.now(), time.Time{}
	shipped, err := r.opByUID(ctx, op.Namespace, stage.Status.LastOp)
	if err != nil {
		return "", err
	}
	if shipped != nil {
		from = parseOr(shipped.Status.Times.Applied)
	}
	if from.IsZero() {
		return "CauseUnconfirmed: the running version has no applied time, so no cited " + c.Verdict.Gate + " window can be about it", nil
	}
	base := gateInput(op, stage, from, now)
	base.Subjects = []string{stage.Status.Pointer}
	v := delivery.Rerun(ctx, r.Gates, stage.Spec.Gates.Verify.Checks, base, *c.Verdict)
	if v.Result == delivery.Unknown || v.Result != c.Verdict.Result {
		return fmt.Sprintf("CauseUnconfirmed: the cited %s verdict says %s, re-running it now says %s: %s",
			c.Verdict.Gate, c.Verdict.Result, v.Result, v.Message), nil
	}
	v.Phase, v.Message = "pre", "the cited cause re-ran: "+v.Message
	op.Status.Verdicts = append(op.Status.Verdicts, v)
	return "", nil
}

// cited finds the operation a rollback undoes, by uid, on the same Stage.
// A failed list is an error, never "not found": that would reject a valid
// rollback as unbound.
func (r *OperationReconciler) cited(ctx context.Context, op *dv1.Operation) (*delivery.CitedOp, error) {
	ops, err := stageOps(ctx, r.Client, op.Namespace, op.Spec.Stage)
	if err != nil {
		return nil, err
	}
	for _, o := range ops {
		if string(o.UID) == op.Spec.Params.Undoes {
			return &delivery.CitedOp{UID: string(o.UID), Type: o.Spec.Type, Applied: o.Status.Applied, Pointer: o.Status.Pointer}, nil
		}
	}
	return nil, nil
}

// stageOps lists the operations on a Stage.
func stageOps(ctx context.Context, c client.Reader, ns, stage string) ([]dv1.Operation, error) {
	list := &dv1.OperationList{}
	if err := c.List(ctx, list, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(list.Items, func(o dv1.Operation) bool { return o.Spec.Stage != stage }), nil
}

// ReasonUnguarded holds an operation while an admission policy is missing.
const ReasonUnguarded = "Unguarded"

// unguarded parks op while the admission policies in r.AdmissionPolicies do
// not guard (unguardedBy). A failed read is an error, so an unreadable policy
// never reads as installed.
func (r *OperationReconciler) unguarded(ctx context.Context, op *dv1.Operation) (reconcile.Result, bool, error) {
	why, err := unguardedBy(ctx, r.Client, r.AdmissionPolicies)
	if err != nil || why == "" {
		return reconcile.Result{}, err != nil, err
	}
	op.Status.Reason = ReasonUnguarded
	op.Status.Message = why + ", so who wrote an operation or approval cannot be trusted; waiting for it"
	res, err := r.save(ctx, op)
	return res, true, err
}
