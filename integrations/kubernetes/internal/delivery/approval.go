/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// held consumes approvals for an operation waiting before it moved anything.
func (r *OperationReconciler) held(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if res, done, err := r.preApplyExits(ctx, op, stage); done {
		return res, err
	}
	if res, done, err := r.unguarded(ctx, op); done {
		return res, err
	}
	// The order is refreshed before any approval is read, so an approval is
	// judged against the teams the change still breaks, not the ones it broke
	// when it was admitted. A Reject needs no plan, so a re-plan that fails
	// still lets one through.
	refreshed, err := r.refreshOrder(ctx, op, stage)
	a, skipped := r.approval(ctx, op)
	newSkip := skipped != "" && skipped != op.Status.Message
	if newSkip {
		op.Status.Message = skipped
	}
	switch {
	case a != nil && a.Decision == delivery.Reject:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: "RejectedByHuman: " + a.By + ": " + a.Note})
	case err != nil:
		return reconcile.Result{}, err
	case a == nil && (refreshed || newSkip):
		return r.save(ctx, op)
	case a == nil:
		return r.requeue()
	}
	if waiting := delivery.TeamsWaiting(ownersOf(op), op.Status.ApprovedTeams); len(waiting) > 0 {
		op.Status.Reason = reasonAwaitingHuman
		op.Status.Message = a.By + " answered for " + strings.Join(op.Status.ApprovedTeams, ", ") +
			"; the change still waits for " + strings.Join(waiting, ", ") + ", since it is asked of every one of them"
		return r.save(ctx, op)
	}
	return r.lock(ctx, op, stage, true)
}

// ownersOf are the teams op's question is asked of.
func ownersOf(op *dv1.Operation) []string {
	if op.Status.Plan == nil {
		return nil
	}
	return op.Status.Plan.Floor.Owners
}

// refreshOrder plans a change held on its order again while it waits, so the
// order, the question and who is asked say what must move now, not what had
// to at admission. A consumer that moved first drops out and its team is no
// longer asked: once checkout runs a version that accepts the change, only
// the teams it still breaks may approve it. A consumer moves out of the
// snapshot because each Pacto adds the one contract its status holds, and the
// operator updates that status when Flux applies the consumer's new version.
// A team the change now breaks is asked too. A read that timed out is tried
// again. It reports whether it changed op's plan; the caller saves it.
//
// A re-plan that rejects, or that holds on something Pacto cannot see rather
// than on a consumer, changes nothing: approving re-checks the plan and
// rejects or asks that. One with nothing left to hold the change keeps the
// people already asked and says the question is moot.
// ponytail: re-plans every poll while a change waits on its order (registry
// reads are bounded); throttle it if many changes wait at once.
func (r *OperationReconciler) refreshOrder(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (bool, error) {
	prev := op.Status.Plan
	if prev == nil || prev.Order == nil {
		return false, nil
	}
	cur, err := r.plan(ctx, op, stage)
	if err != nil {
		return false, err
	}
	next := cur
	switch {
	case cur.Predicted == delivery.PredictRejected:
		return false, nil
	case !cur.Floor.Human:
		next = *prev
		next.Order, next.Question = nil, "Nothing Pacto sees holds this change any more: what it waited for has moved. Approve it to ship it, or cancel it and propose it again."
	case cur.Order == nil:
		return false, nil
	}
	if next.Question == op.Status.Question && equality.Semantic.DeepEqual(next.Order, prev.Order) && equality.Semantic.DeepEqual(next.Floor, prev.Floor) {
		return false, nil
	}
	op.Status.Plan, op.Status.Question = &next, next.Question
	op.Status.Verdicts = mergeVerdicts(op.Status.Verdicts, next.Verdicts)
	return true, nil
}

// approval consumes the first Approval for op that delivery.ApprovalEligible
// counts and op has not consumed yet. The groups are the ones admission bound
// to the user. It also says why the newest Approval for op it passed over does
// not count, so the person who gave it is not left waiting on nothing.
// ponytail: times are whole seconds, so an approval created in the same
// second as a new hold still counts.
func (r *OperationReconciler) approval(ctx context.Context, op *dv1.Operation) (*delivery.ApprovalRecord, string) {
	approvals, err := r.approvalsOf(ctx, op)
	if err != nil {
		return nil, ""
	}
	skipped := ""
	for _, a := range approvals {
		s := a.Spec
		if slices.ContainsFunc(op.Status.Approvals, func(x delivery.ApprovalRecord) bool { return x.Name == a.Name }) {
			continue
		}
		ok, why := delivery.ApprovalEligible(op.Spec, op.Status, string(op.UID), s, a.CreationTimestamp.Time, r.Policy, r.Freshness)
		if !ok {
			skipped = "Approval " + a.Name + " by " + s.By + " does not count: " + why
			continue
		}
		rec := delivery.ApprovalRecord{Name: a.Name, By: s.By, Decision: s.Decision, Note: s.Note, AuthTime: s.AuthTime}
		op.Status.Approvals = append(op.Status.Approvals, rec)
		op.Status.ApprovedTeams = delivery.AnsweredTeams(r.Policy, ownersOf(op), s.Groups, op.Status.ApprovedTeams)
		return &rec, ""
	}
	return nil, skipped
}

// approvalsOf lists the Approvals that answer op, oldest first.
func (r *OperationReconciler) approvalsOf(ctx context.Context, op *dv1.Operation) ([]dv1.Approval, error) {
	list := &dv1.ApprovalList{}
	if err := r.List(ctx, list, client.InNamespace(op.Namespace), client.MatchingFields{approvalOperationUID: string(op.UID)}); err != nil {
		return nil, err
	}
	slices.SortFunc(list.Items, func(a, b dv1.Approval) int { return a.CreationTimestamp.Compare(b.CreationTimestamp.Time) })
	return list.Items, nil
}

// adopt makes op the owner of every Approval that answers it, so they are
// garbage collected with it instead of piling up. The CRD keeps an
// Approval's spec as it was given, so owning one changes nothing it says.
// ponytail: an Approval naming no operation that exists is never adopted;
// delete it by hand.
func (r *OperationReconciler) adopt(ctx context.Context, op *dv1.Operation) error {
	approvals, _ := r.approvalsOf(ctx, op) // an unlistable Approval is adopted on a later reconcile
	for i := range approvals {
		a := &approvals[i]
		if slices.ContainsFunc(a.OwnerReferences, func(o metav1.OwnerReference) bool { return o.UID == op.UID }) {
			continue
		}
		patch := client.MergeFrom(a.DeepCopy())
		a.OwnerReferences = append(a.OwnerReferences, metav1.OwnerReference{APIVersion: dv1.GroupVersion.String(), Kind: "Operation", Name: op.Name, UID: op.UID})
		if err := r.Patch(ctx, a, patch); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}
