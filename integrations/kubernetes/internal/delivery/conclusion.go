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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// finish concludes op: one Stage compare-and-swap releases the lock (only if
// this operation holds it), assigns the stream seq and applies the freeze,
// auto-hold and digest memories; then the operation's status is written. A
// failed promote raises its one bound rollback first, so nothing can slip in
// between the failure and the rollback.
func (r *OperationReconciler) finish(ctx context.Context, op *dv1.Operation, stage *dv1.Stage, c conclusion) (reconcile.Result, error) {
	code, msg, _ := strings.Cut(c.reason, ": ")
	// A stale cached read can show an operation that already concluded as
	// still running: concluding it again would take a second seq. Decide on
	// the stored operation only.
	fresh := &dv1.Operation{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(op), fresh); err != nil {
		return reconcile.Result{}, err
	}
	if fresh.ResourceVersion != op.ResourceVersion {
		return reconcile.Result{RequeueAfter: immediately}, nil
	}
	if stage != nil {
		squat, err := r.raiseOnFailure(ctx, op, stage, c, code+": "+msg)
		if err != nil {
			return reconcile.Result{}, err
		}
		if err := r.casStage(ctx, stage, func(s *dv1.Stage) error {
			// Decided on the fresh Stage: a verdict reached on a cached one
			// never concludes an operation whose lock was taken meanwhile.
			taken := op.Status.Applied && preempted(op, s)
			if s.Status.Locks[op.Spec.Facet] == string(op.UID) {
				delete(s.Status.Locks, op.Spec.Facet)
			}
			// ponytail: a re-conclusion snapshots the policy again, so a Stage
			// edit inside the seconds between a failed write and its retry
			// lands in the record; store the spec in Concluded if that matters.
			spec := s.Spec
			op.Status.Policy, op.Status.StageUID = &spec, string(s.UID)
			if done := concludedOn(s.Status, op.UID); done != nil {
				// This operation took its seq and its status write failed.
				op.Status.Seq, c = done.Seq, conclusion{outcome: done.Outcome, verification: done.Verification, reason: done.Reason}
				return nil
			}
			if taken && !c.preempted {
				c = preemptedBy
			}
			if op.Spec.Type == delivery.TypeAutonomy && c.outcome == delivery.Succeeded {
				if why := delivery.LevelRefusal(op.Spec, s.Spec, s.Status); why != "" {
					c = conclusion{outcome: delivery.Rejected, reason: why}
				}
			}
			before := s.Status.Pointer
			s.Status.Stream.Seq++
			op.Status.Seq = s.Status.Stream.Seq
			done := delivery.Concluded{UID: string(op.UID), Seq: op.Status.Seq, Outcome: c.outcome, Verification: c.verification, Reason: c.reason,
				Type: op.Spec.Type, Person: len(op.Status.Approvals) > 0 || op.Status.ActorClass == delivery.ClassHuman,
				ClearedBreaking: delivery.ClearedBreaking(op.Status.Plan)}
			if op.Status.ActorClass == delivery.ClassController {
				done.Cause = causeOp(op) // only the promoter's own cause excuses what follows
			}
			s.Status.Stream.Concluded = append(s.Status.Stream.Concluded, done)
			s.Status.Stream.Concluded = s.Status.Stream.Concluded[max(0, len(s.Status.Stream.Concluded)-delivery.MaxConcluded):]
			delivery.ApplyEffects(&s.Status, op.Name, string(op.UID), op.Spec, op.Status,
				delivery.Conclusion{Outcome: c.outcome, Verification: c.verification, Reason: c.reason, Freeze: c.freeze})
			if op.Spec.Type == delivery.TypeAutonomy {
				// Leaving observe adopts the pointer; the record shows it.
				op.Status.Pointer = delivery.PointerChange{}
				if s.Status.Pointer != before {
					op.Status.Pointer = delivery.PointerChange{Before: before, After: s.Status.Pointer}
				}
			}
			if squat != "" {
				s.Status.Frozen, s.Status.FrozenReason = true, op.Name+": "+squat
			}
			return nil
		}); err != nil {
			return reconcile.Result{}, err
		}
		code, msg, _ = strings.Cut(c.reason, ": ")
	}
	op.Status.Phase, op.Status.Outcome, op.Status.Verification = delivery.PhaseConcluded, c.outcome, c.verification
	op.Status.Reason, op.Status.Message, op.Status.Question = code, msg, ""
	op.Status.Times.Finished = ts(r.now())
	if err := r.writeConcluded(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	countConcluded(op)
	event(r.Events, op, c.outcome != delivery.Succeeded && c.outcome != delivery.Cancelled, string(c.outcome), c.reason)
	r.heldChecks.Delete(op.UID)
	return reconcile.Result{RequeueAfter: immediately}, nil
}

// raiseOnFailure raises what a failed promote owes before it concludes: its
// step-down and its one bound rollback. A non-empty squat says someone took
// the name of one, and the caller freezes the Stage. Anything else raises
// nothing.
func (r *OperationReconciler) raiseOnFailure(ctx context.Context, op *dv1.Operation, stage *dv1.Stage, c conclusion, why string) (string, error) {
	if c.outcome != delivery.Failed || op.Spec.Type != delivery.TypePromote {
		return "", nil
	}
	// Never on a stale decision: an operation the Stage already concluded,
	// maybe as a success, raises no rollback or step-down.
	cur := &dv1.Stage{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(stage), cur); err != nil {
		return "", err
	}
	// Nor when a rollback or override took the Stage while the gates ran:
	// the compare-and-swap concludes it Preempted.
	// ponytail: a lock taken between this read and that swap still gets the
	// rollback raised, which is then refused and freezes.
	open := concludedOn(cur.Status, op.UID) == nil
	live := open && !preempted(op, cur)
	squat := ""
	if live || open && c.preempted { // a preempting rollback already ran: only the step-down is owed
		var err error
		if squat, err = raiseStepDown(ctx, r.Client, r.Policy, op, cur, why, c.cause); err != nil {
			return "", err
		}
	}
	if !live || op.Status.Pointer.Before == "" || slices.Contains(cur.Status.FailedDigests, op.Status.Pointer.Before) {
		return squat, nil
	}
	rb, err := raiseRollback(ctx, r.Client, r.Policy, op, stage, why, c.cause)
	if err != nil {
		return "", err
	}
	if rb == "" {
		event(r.Events, stage, true, "RollbackRaised", op.Name+" failed ("+why+"), so it is rolled back to "+op.Status.Pointer.Before)
	}
	return strings.Join(slices.DeleteFunc([]string{squat, rb}, func(x string) bool { return x == "" }), "; "), nil
}

// concludedOn is the seq and conclusion the Stage gave uid, or nil.
func concludedOn(s delivery.StageStatus, uid types.UID) *delivery.Concluded {
	i := slices.IndexFunc(s.Stream.Concluded, func(c delivery.Concluded) bool { return c.UID == string(uid) })
	if i < 0 {
		return nil
	}
	return &s.Stream.Concluded[i]
}

// writeConcluded writes op's conclusion, retrying a conflict on a fresh read:
// the seq is already taken on the Stage, so running finish again would take
// another and leave a gap in the stream. Only this reconciler writes the
// status of an operation that has not concluded, so ours is the one to keep.
func (r *OperationReconciler) writeConcluded(ctx context.Context, op *dv1.Operation) error {
	status := op.Status
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.Status().Update(ctx, op)
		if !apierrors.IsConflict(err) {
			return err
		}
		if gerr := r.APIReader.Get(ctx, client.ObjectKeyFromObject(op), op); gerr != nil {
			return gerr
		}
		op.Status = status
		return err
	})
}

// raiseRollback creates the one rollback bound to a failed promote, citing
// the failed operation and, when a gate failed it, that verdict. Its name is
// derived from the failed operation, so raising it twice is a no-op: at most
// one automatic rollback per cause. A non-empty squat says someone else took
// that name, and the caller freezes the Stage.
func raiseRollback(ctx context.Context, c client.Client, policy delivery.ClassPolicy, failed *dv1.Operation, stage *dv1.Stage, why string, cause *delivery.Verdict) (squat string, _ error) {
	rb := &dv1.Operation{}
	rb.Namespace, rb.Name = failed.Namespace, failed.Name+"-rollback"
	rb.Labels = map[string]string{StageLabel: stage.Name}
	controllerutil.AddFinalizer(rb, dv1.RecordFinalizer)
	rb.Spec = delivery.OperationSpec{
		Type: delivery.TypeRollback, Stage: stage.Name, Facet: delivery.FacetPointer,
		Params: delivery.Params{From: failed.Spec.Params.To, To: failed.Status.Pointer.Before, Undoes: string(failed.UID)},
		Reason: "automatic rollback: " + failed.Name + " failed (" + why + ")",
		Actor:  delivery.Actor{User: policy.ControllerUser},
		Cause:  &delivery.Cause{Operation: string(failed.UID), Verdict: cause},
	}
	rb.Spec.IntentDigest = delivery.IntentDigest(rb.Spec, stage.Spec)
	return createRaised(ctx, c, rb)
}

// raiseFreeze creates the freeze a failed re-check raises when it cannot roll
// back. The Stage is already frozen; the freeze is how that failure reaches
// the records, naming the version the Stage stays on and the failed verdict,
// so a Stage in another cluster that follows these records never takes it.
// Its name is derived from the operation that shipped the version, so raising
// it twice is a no-op. A non-empty squat says someone else took that name.
func raiseFreeze(ctx context.Context, c client.Client, policy delivery.ClassPolicy, shipped *dv1.Operation, stage *dv1.Stage, why string, cause *delivery.Verdict) (squat string, _ error) {
	fz := &dv1.Operation{}
	fz.Namespace, fz.Name = shipped.Namespace, shipped.Name+"-freeze"
	fz.Labels = map[string]string{StageLabel: stage.Name}
	controllerutil.AddFinalizer(fz, dv1.RecordFinalizer)
	fz.Spec = delivery.OperationSpec{
		Type: delivery.TypeFreeze, Stage: stage.Name, Facet: delivery.FacetFreeze,
		Params: delivery.Params{From: shipped.Spec.Params.To},
		Reason: shipped.Name + " failed after release (" + why + ")",
		Actor:  delivery.Actor{User: policy.ControllerUser},
		Cause:  &delivery.Cause{Operation: string(shipped.UID), Verdict: cause},
	}
	fz.Spec.IntentDigest = delivery.IntentDigest(fz.Spec, stage.Spec)
	return createRaised(ctx, c, fz)
}

// createRaised creates an operation the promoter raises under a name derived
// from its cause, so raising it twice is a no-op. Anyone who may create
// operations can take that name first, so an operation found under it counts
// only if the promoter raised it for the same intent. Otherwise squat says
// why, and the caller freezes the Stage: a rollback, freeze or promote
// silently skipped would leave nobody deciding.
func createRaised(ctx context.Context, c client.Client, op *dv1.Operation) (squat string, _ error) {
	err := c.Create(ctx, op)
	if !apierrors.IsAlreadyExists(err) {
		return "", err
	}
	got := &dv1.Operation{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(op), got); err != nil {
		return "", err
	}
	if got.Spec.Actor.User == op.Spec.Actor.User && got.Spec.Type == op.Spec.Type &&
		got.Spec.Params == op.Spec.Params && causeOp(got) == causeOp(op) {
		return "", nil
	}
	return "the name " + op.Name + " is taken by a " + got.Spec.Type + " " + got.Spec.Actor.User +
		" created, so the promoter cannot raise its " + op.Spec.Type + "; a person must decide", nil
}

func causeOp(o *dv1.Operation) string {
	if o.Spec.Cause == nil {
		return ""
	}
	return o.Spec.Cause.Operation
}

// releaseFinalizer lets a concluded, deleted operation go when no record is
// owed for it: records are off, or the operation never got a seq (no Stage,
// so no stream). Otherwise the recorder removes the finalizer once the record
// is in the registry.
func (r *OperationReconciler) releaseFinalizer(ctx context.Context, op *dv1.Operation) error {
	if op.DeletionTimestamp.IsZero() || (r.Records && op.Status.Seq > 0) || !controllerutil.RemoveFinalizer(op, dv1.RecordFinalizer) {
		return nil
	}
	return r.Update(ctx, op)
}

// expire deletes a recorded operation once RecordedTTL has passed since it
// concluded: its record holds what happened, and history reads the record.
// The operation the Stage runs is kept, because a rollback must cite it.
func (r *OperationReconciler) expire(ctx context.Context, op *dv1.Operation) (reconcile.Result, error) {
	if r.RecordedTTL <= 0 || op.Status.Record == nil || !op.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}
	finished, err := time.Parse(time.RFC3339, op.Status.Times.Finished)
	if err != nil {
		return reconcile.Result{}, nil
	}
	if wait := finished.Add(r.RecordedTTL).Sub(r.now()); wait > 0 {
		return reconcile.Result{RequeueAfter: wait}, nil
	}
	stage := &dv1.Stage{}
	switch err := r.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.Stage}, stage); {
	case err == nil && stage.Status.LastOp == string(op.UID):
		return reconcile.Result{}, nil
	case err != nil && !apierrors.IsNotFound(err):
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, client.IgnoreNotFound(r.Delete(ctx, op))
}
