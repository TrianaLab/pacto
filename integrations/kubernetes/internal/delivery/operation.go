/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// immediately asks to run again right away: the next step needs no wait.
const immediately = time.Millisecond

// reasonWaiting marks an operation parked on the Stage: a lock, a freeze.
const reasonWaiting = "Waiting"

// reasonRecheckChanged holds an approved operation whose gates changed since the hold.
const reasonRecheckChanged = "RecheckChanged"

var (
	errWait         = errors.New("wait")
	errPointerMoved = errors.New("pointer moved")
	errLockLost     = errors.New("lock lost")
	errConcluded    = errors.New("concluded")
)

// OperationReconciler carries one Operation through its lifecycle: admit,
// pre-gates, Held, lock, pointer, Flux confirmation, verify, conclude. Every
// step stores its progress in status and is idempotent, so a crashed promoter
// resumes from etcd.
type OperationReconciler struct {
	client.Client
	// APIReader reads Stages uncached for the compare-and-swap updates.
	APIReader client.Reader
	Cluster   Cluster
	Policy    delivery.ClassPolicy
	// Workers is how many reconciles run at once (0 is one), so one slow
	// health provider does not hold up every other Stage.
	Workers int
	// Gates are the gates a Stage may name (NewGates).
	Gates map[string]delivery.Gate
	Now   func() time.Time
	// Poll is how often an operation waiting on the outside world is looked at.
	Poll time.Duration
	// Records says the recorder runs and owns the record finalizer of every
	// operation that got a seq.
	Records bool
	// RecordedTTL deletes a recorded operation this long after it concluded;
	// zero keeps every operation.
	RecordedTTL time.Duration
	// Freshness is how old a login may be when its approval is given; zero
	// means delivery.AuthFreshness.
	Freshness time.Duration
	// AdmissionPolicies names the ValidatingAdmissionPolicies (each bound by a
	// binding of the same name) that check an operation's actor and an
	// approval's identity. While one is missing the promoter admits nothing
	// and counts no approval: the spec says only what its writer claimed.
	AdmissionPolicies []string
	// Events receives the Events it emits on hold, conclusion and rollback;
	// nil emits none.
	Events record.EventRecorder
	// heldChecks is when the verify gates last ran for each operation held on
	// a running version, by uid.
	// ponytail: in memory, so a restart runs them once early.
	heldChecks *sync.Map
}

// conclusion is how an operation ends.
type conclusion struct {
	outcome      delivery.Outcome
	verification delivery.Verification
	reason       string // "Code: message"
	freeze       bool
	// cause is the verdict that failed the operation, cited by its rollback.
	cause *delivery.Verdict
	// preempted says a rollback took the lock and failed the operation
	// (preemption): its step-down is raised, its rollback already ran.
	preempted bool
}

// Reconcile implements reconcile.Reconciler.
func (r *OperationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	op := &dv1.Operation{}
	if err := r.Get(ctx, req.NamespacedName, op); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if err := r.adopt(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	if op.Status.Phase == delivery.PhaseConcluded {
		if err := r.releaseFinalizer(ctx, op); err != nil {
			return reconcile.Result{}, err
		}
		return r.expire(ctx, op)
	}
	stage := &dv1.Stage{}
	key := types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.Stage}
	err := r.Get(ctx, key, stage)
	if apierrors.IsNotFound(err) {
		// NoStage is final, so a cache that has not seen a new Stage yet
		// never decides it: only the apiserver's NotFound does.
		err = r.APIReader.Get(ctx, key, stage)
	}
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		// ponytail: an applied operation whose Stage is gone concludes with no
		// seq, so no record; a Stage finalizer held until its changes are
		// recorded closes that, if deleting a Stage mid-change matters.
		c := conclusion{outcome: delivery.Rejected, reason: "NoStage: there is no Stage named " + op.Spec.Stage}
		if op.Status.Applied {
			c.outcome = delivery.Errored
		}
		return r.finish(ctx, op, nil, c)
	}
	if concludedOn(stage.Status, op.UID) != nil {
		return r.finish(ctx, op, stage, conclusion{}) // concluded, but its status write failed
	}
	switch {
	case op.Status.Phase == "":
		return r.admit(ctx, op)
	case op.Status.Phase == delivery.PhasePending:
		return r.pending(ctx, op, stage)
	case op.Status.Phase == delivery.PhaseHeld && !op.Status.Applied:
		return r.held(ctx, op, stage)
	case op.Status.Phase == delivery.PhaseRunning:
		return r.running(ctx, op, stage)
	case op.Status.Phase == delivery.PhaseVerifying:
		return r.verifying(ctx, op, stage)
	default: // Held after apply
		return r.heldAfterApply(ctx, op, stage)
	}
}

func (r *OperationReconciler) now() time.Time { return r.Now().UTC() }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// passed reports whether the RFC 3339 deadline d has passed.
func (r *OperationReconciler) passed(d string) bool {
	t, err := time.Parse(time.RFC3339, d)
	return err == nil && r.now().After(t)
}

func (r *OperationReconciler) requeue() (reconcile.Result, error) {
	return reconcile.Result{RequeueAfter: r.Poll}, nil
}

// saveHeld writes an operation that now waits on a person, and says so in an Event.
func (r *OperationReconciler) saveHeld(ctx context.Context, op *dv1.Operation) (reconcile.Result, error) {
	event(r.Events, op, true, "Held", op.Status.Reason+": "+op.Status.Question)
	return r.save(ctx, op)
}

// save writes the operation's status and polls again.
func (r *OperationReconciler) save(ctx context.Context, op *dv1.Operation) (reconcile.Result, error) {
	if err := r.Status().Update(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	return r.requeue()
}

// pending runs the pre-gates.
func (r *OperationReconciler) pending(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if res, done, err := r.preApplyExits(ctx, op, stage); done {
		return res, err
	}
	if op.Spec.Type == delivery.TypePromote {
		newer, err := r.newerPromote(ctx, op)
		if err != nil {
			return reconcile.Result{}, err
		}
		if newer != "" {
			return r.finish(ctx, op, stage, conclusion{outcome: delivery.Superseded, reason: "Superseded: " + newer + " is a newer promote on this Stage"})
		}
	}
	if op.Spec.Type == delivery.TypeFreeze {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified, reason: "Frozen: automation stops moving the Stage until a person unfreezes it"})
	}
	if op.Spec.Facet == delivery.FacetFreeze {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified, reason: "Unfrozen: automation may move the Stage again"})
	}
	if op.Spec.Facet == delivery.FacetUpstream {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified,
			reason: "Acknowledged: the upstream stand-in " + op.Spec.Params.Record + " no longer stops " + stage.Name + " offering versions"})
	}
	plan, err := r.plan(ctx, op, stage)
	if err != nil {
		return reconcile.Result{}, err
	}
	op.Status.Plan, op.Status.Verdicts = &plan, mergeVerdicts(op.Status.Verdicts, plan.Verdicts)
	switch plan.Predicted {
	case delivery.PredictRejected:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: planRefusal(plan)})
	case delivery.PredictHeld:
		op.Status.Phase, op.Status.Question, op.Status.Times.Held = delivery.PhaseHeld, plan.Question, ts(r.now())
		op.Status.ApprovedTeams = nil // a question asked again is answered again
		op.Status.Reason, op.Status.Message = reasonAwaitingHuman, "a person must "+string(plan.Floor.Decision)
		return r.saveHeld(ctx, op)
	case delivery.PredictWaits:
		op.Status.Reason, op.Status.Message = reasonWaiting, strings.Join(plan.Conflicts, "; ")
		return r.save(ctx, op)
	}
	return r.lock(ctx, op, stage, false)
}

// preApplyExits handles the ways an operation that has not moved anything can
// end on its own: cancel (delete) and the approval deadline.
func (r *OperationReconciler) preApplyExits(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, bool, error) {
	if !op.DeletionTimestamp.IsZero() {
		res, err := r.finish(ctx, op, stage, conclusion{outcome: delivery.Cancelled, reason: "Cancelled: cancelled before the pointer moved"})
		return res, true, err
	}
	if r.passed(op.Status.Times.ApprovalDeadline) {
		res, err := r.finish(ctx, op, stage, conclusion{outcome: delivery.Expired, reason: "Expired: nobody decided before the deadline"})
		return res, true, err
	}
	return reconcile.Result{}, false, nil
}

// lock takes the Stage's facet lock with one compare-and-swap. An approved
// promote or rung change re-checks its gates first: every gate that passed
// must still pass, and a floor the approval did not cover sends it back to a
// person. A rung change takes no lock: it concludes at once, and the
// compare-and-swap that takes its seq moves the rung (RungRefusal).
func (r *OperationReconciler) lock(ctx context.Context, op *dv1.Operation, stage *dv1.Stage, recheck bool) (reconcile.Result, error) {
	if recheck && (op.Spec.Type == delivery.TypePromote || op.Spec.Type == delivery.TypeAutonomy) {
		cur, err := r.plan(ctx, op, stage)
		if err != nil {
			return reconcile.Result{}, err
		}
		if cur.Predicted == delivery.PredictRejected {
			return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: planRefusal(cur)})
		}
		if !delivery.StillPasses(*op.Status.Plan, cur) {
			op.Status.Plan, op.Status.Verdicts = &cur, mergeVerdicts(op.Status.Verdicts, cur.Verdicts)
			op.Status.Phase, op.Status.Question, op.Status.Times.Held = delivery.PhaseHeld, cur.Question, ts(r.now())
			op.Status.ApprovedTeams = nil // a question asked again is answered again
			op.Status.Reason, op.Status.Message = reasonRecheckChanged, "a gate no longer passes, so a person must decide again"
			return r.saveHeld(ctx, op)
		}
		op.Status.Verdicts = mergeVerdicts(op.Status.Verdicts, cur.Verdicts)
	}
	if op.Spec.Facet == delivery.FacetRung {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified,
			reason: "RungChanged: " + op.Spec.Stage + " moved from " + op.Spec.Params.From + " to " + op.Spec.Params.To})
	}
	facet, uid := op.Spec.Facet, string(op.UID)
	waiting := ""
	err := r.casStage(ctx, stage, func(s *dv1.Stage) error {
		if concludedOn(s.Status, op.UID) != nil {
			return errConcluded // a stale read: this operation concluded already
		}
		if holder := s.Status.Locks[facet]; holder != "" && holder != uid {
			free, err := r.lockFree(ctx, op, holder)
			if err != nil {
				return err
			}
			if !free {
				waiting = "waiting for operation " + holder + " to release the " + facet + " lock"
				return errWait
			}
		}
		if s.Status.Frozen && op.Spec.Type == delivery.TypePromote {
			waiting = "the Stage is frozen: " + s.Status.FrozenReason
			return errWait
		}
		if s.Status.Pointer != op.Spec.Params.From {
			return errPointerMoved
		}
		if s.Status.Locks == nil {
			s.Status.Locks = map[string]string{}
		}
		s.Status.Locks[facet] = uid
		return nil
	})
	switch {
	case errors.Is(err, errConcluded):
		return r.finish(ctx, op, stage, conclusion{})
	case errors.Is(err, errWait):
		if recheck {
			// The approval held() just took stays unconsumed, so it moves the
			// operation once the lock or the freeze lets go, re-checked then
			// against the people the question is routed to at that moment.
			a := op.Status.Approvals[len(op.Status.Approvals)-1]
			op.Status.Approvals = op.Status.Approvals[:len(op.Status.Approvals)-1]
			waiting = a.By + " approved; " + waiting
		}
		op.Status.Reason, op.Status.Message = reasonWaiting, waiting
		return r.save(ctx, op)
	case errors.Is(err, errPointerMoved):
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: "PointerMoved: another operation moved the pointer first"})
	case err != nil:
		return reconcile.Result{}, err
	}
	now := r.now()
	t, _ := delivery.TypeByID(op.Spec.Type)
	_, exec := t.Deadlines(stage.Spec.Deadlines)
	op.Status.Phase, op.Status.Question, op.Status.Reason, op.Status.Message = delivery.PhaseRunning, "", "", ""
	op.Status.Times.Started, op.Status.Times.ExecutionDeadline = ts(now), ts(now.Add(exec))
	if err := r.Status().Update(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: immediately}, nil
}

// lockFree reports whether op may take the lock holder has: holder is gone or
// concluded, or op is a rollback or override and holder is verifying or held
// after apply, which priority lets it preempt. A failed read is an error, so
// an unreadable holder never reads as a free lock.
func (r *OperationReconciler) lockFree(ctx context.Context, op *dv1.Operation, holder string) (bool, error) {
	o, err := r.opByUID(ctx, op.Namespace, holder)
	if err != nil {
		return false, err
	}
	if o == nil || o.Status.Phase == delivery.PhaseConcluded {
		return true, nil
	}
	preempts := op.Spec.Type == delivery.TypeRollback || op.Spec.Type == delivery.TypeOverride
	return preempts && (o.Status.Phase == delivery.PhaseVerifying || o.Status.Phase == delivery.PhaseHeld && o.Status.Applied), nil
}

// opByUID finds an operation by uid; nil with no error means it is gone.
func (r *OperationReconciler) opByUID(ctx context.Context, ns, uid string) (*dv1.Operation, error) {
	list := &dv1.OperationList{}
	if err := r.List(ctx, list, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if string(list.Items[i].UID) == uid {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

// casStage applies fn to a fresh Stage and writes its status with optimistic
// concurrency. A conflict re-reads and re-applies, so fn's checks always run
// against the state it writes over.
func (r *OperationReconciler) casStage(ctx context.Context, stage *dv1.Stage, fn func(*dv1.Stage) error) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &dv1.Stage{}
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(stage), fresh); err != nil {
			return err
		}
		if err := fn(fresh); err != nil {
			return err
		}
		if err := r.Status().Update(ctx, fresh); err != nil {
			return err
		}
		*stage = *fresh
		return nil
	})
}

// running moves the pointer, writes it to the OCIRepository and waits for Flux.
func (r *OperationReconciler) running(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if r.passed(op.Status.Times.ExecutionDeadline) {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Errored, freeze: true, reason: "ExecutionDeadline: Flux did not confirm the version in time"})
	}
	from, to, uid := op.Spec.Params.From, op.Spec.Params.To, string(op.UID)
	if !op.Status.Applied {
		err := r.casStage(ctx, stage, func(s *dv1.Stage) error {
			if s.Status.Locks[delivery.FacetPointer] != uid {
				return errLockLost
			}
			s.Status.Pointer, s.Status.LastOp = to, uid
			return nil
		})
		if errors.Is(err, errLockLost) {
			return r.finish(ctx, op, stage, conclusion{outcome: delivery.Errored, freeze: true, reason: "LockLost: the pointer lock was taken while running"})
		}
		if err != nil {
			return reconcile.Result{}, err
		}
		op.Status.Applied, op.Status.Pointer = true, delivery.PointerChange{Before: from, After: to}
		if err := r.Status().Update(ctx, op); err != nil {
			return reconcile.Result{}, err
		}
	}
	a, err := r.Cluster.applier(ctx, op.Namespace, stage.Spec.ApplyRef)
	if err != nil {
		op.Status.Message = "reading the apply OCIRepository: " + err.Error()
		return r.save(ctx, op)
	}
	if a.digest != to {
		if a.digest != from {
			return r.finish(ctx, op, stage, conclusion{outcome: delivery.Errored, freeze: true,
				reason: fmt.Sprintf("ForeignWriter: %s is %q, which this operation did not write; it is left as found", a.field(), a.digest)})
		}
		if err := r.Cluster.setDigest(ctx, a, to, r.now()); err != nil {
			// A refusal (a missing apply-ref label, RBAC) lasts until a
			// person fixes it, so the operation says what it is.
			op.Status.Message = "writing " + a.field() + ": " + err.Error()
			return r.save(ctx, op)
		}
		return r.requeue()
	}
	ok, failed, msg, err := r.Cluster.applied(ctx, op.Namespace, a, to)
	switch {
	case err != nil:
		return reconcile.Result{}, err
	case failed:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Failed, reason: "ApplyFailed: " + msg})
	case !ok:
		op.Status.Message = msg
		return r.save(ctx, op)
	}
	now := r.now()
	op.Status.Times.Applied = ts(now)
	if op.Spec.Type == delivery.TypeOverride {
		outcome := delivery.Succeeded
		if op.Spec.Witnessed {
			outcome = delivery.Witnessed
		}
		return r.finish(ctx, op, stage, conclusion{outcome: outcome, verification: delivery.Unverified, freeze: true,
			reason: "Overridden: the Stage stays frozen until a person unfreezes it"})
	}
	op.Status.Phase, op.Status.Message = delivery.PhaseVerifying, ""
	op.Status.Times.VerifyDeadline = ts(now.Add(delivery.VerifyWindow(stage.Spec)))
	if err := r.Status().Update(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: immediately}, nil
}

// verdicts runs the verify gates on the applied version, over the time since
// Flux applied it.
func (r *OperationReconciler) verdicts(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) []delivery.Verdict {
	applied, _ := time.Parse(time.RFC3339, op.Status.Times.Applied)
	return delivery.RunChecks(ctx, r.Gates, verifyChecks(stage), gateInput(op, stage, applied, r.now()))
}

// preempted reports whether a rollback or override took this operation's lock.
// ponytail: the preempted operation concludes itself on its next reconcile,
// not inside the preempting compare-and-swap; seq order can differ from the
// design's until records (M5) depend on it.
func preempted(op *dv1.Operation, stage *dv1.Stage) bool {
	return stage.Status.Locks[op.Spec.Facet] != string(op.UID)
}

// reasonAwaitingHuman is the reason of an operation held for a person.
const reasonAwaitingHuman = "AwaitingHuman"

// preemptedBy is how an applied operation a rollback or override preempted
// concludes.
var preemptedBy = conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified, reason: "Preempted: a rollback or override took the Stage"}

// preemption is how op concludes once a rollback or override took its lock:
// preemptedBy, or Failed when a rollback no person raised undid a promote no
// person approved. Concluded as a success, an agent could pull its own
// failing promote before verify judged it: no step-down, no failed digest, so
// the rung stays automatic and the version can ship again.
func (r *OperationReconciler) preemption(ctx context.Context, op *dv1.Operation) (conclusion, error) {
	if op.Spec.Type != delivery.TypePromote || op.Status.ActorClass == delivery.ClassHuman || len(op.Status.Approvals) > 0 {
		return preemptedBy, nil
	}
	ops, err := stageOps(ctx, r.Client, op.Namespace, op.Spec.Stage)
	if err != nil {
		return conclusion{}, err
	}
	for _, o := range ops {
		if o.Spec.Type == delivery.TypeRollback && o.Spec.Params.Undoes == string(op.UID) &&
			o.Status.ActorClass != delivery.ClassHuman && o.Status.ActorClass != delivery.ClassBreakGlass {
			msg := "preempted by " + string(o.Status.ActorClass) + " rollback " + o.Name + ", which no person raised, before verify judged it"
			return conclusion{outcome: delivery.Failed, reason: "PreemptedByRollback: " + msg, preempted: true,
				cause: &delivery.Verdict{Gate: "pacto.preempted", Phase: "verify", Result: delivery.Fail, Message: msg,
					Citation: map[string]string{"rollback": string(o.UID)}}}, nil
		}
	}
	return preemptedBy, nil
}

func (r *OperationReconciler) verifying(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if preempted(op, stage) {
		c, err := r.preemption(ctx, op)
		if err != nil {
			return reconcile.Result{}, err
		}
		return r.finish(ctx, op, stage, c)
	}
	vs := r.verdicts(ctx, op, stage)
	op.Status.Verdicts = slices.DeleteFunc(op.Status.Verdicts, func(x delivery.Verdict) bool { return x.Phase == "verify" })
	op.Status.Verdicts = append(op.Status.Verdicts, vs...)
	msg := verdictMessages(vs, delivery.Pass)
	switch delivery.Worst(vs) {
	case delivery.Pass:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Verified, reason: "Verified: " + msg})
	case delivery.Fail:
		f := failing(vs)
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Failed, reason: "VerifyFailed: " + f.Gate + ": " + f.Message, cause: f})
	}
	msg = verdictMessages(vs, delivery.Unknown)
	switch {
	case r.passed(op.Status.Times.VerifyDeadline):
		t, _ := delivery.TypeByID(op.Spec.Type)
		approval, _ := t.Deadlines(stage.Spec.Deadlines)
		op.Status.Phase, op.Status.Times.Held = delivery.PhaseHeld, ts(r.now())
		op.Status.ApprovedTeams = nil // a question asked again is answered again
		op.Status.Times.ApprovalDeadline = ts(r.now().Add(approval))
		op.Status.Reason, op.Status.Message = "VerifyUnknown", msg
		op.Status.Question = "The verify gates could not confirm " + op.Spec.Params.To + " in time: " + msg +
			". Accept the unknown (the version stays), or reject it (it is rolled back)?"
		return r.saveHeld(ctx, op)
	}
	op.Status.Message = msg
	return r.save(ctx, op)
}

// mergeVerdicts replaces the verdicts of the gates fresh answers for, phase by
// phase, and keeps the rest (a re-run cause stays beside the plan's verdicts).
func mergeVerdicts(old, fresh []delivery.Verdict) []delivery.Verdict {
	out := slices.DeleteFunc(slices.Clone(old), func(o delivery.Verdict) bool {
		return slices.ContainsFunc(fresh, func(f delivery.Verdict) bool { return f.Gate == o.Gate && f.Phase == o.Phase })
	})
	return append(out, fresh...)
}

// verdictMessages joins the messages of the verdicts with result r, each led
// by its gate.
func verdictMessages(vs []delivery.Verdict, r delivery.Result) string {
	var msgs []string
	for _, v := range vs {
		if v.Result == r {
			msgs = append(msgs, v.Gate+": "+v.Message)
		}
	}
	return strings.Join(msgs, "; ")
}

// heldAfterApply waits for a person on a version that is already running. It
// keeps the lock, but a rollback or override may preempt it.
func (r *OperationReconciler) heldAfterApply(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if preempted(op, stage) {
		c, err := r.preemption(ctx, op)
		if err != nil {
			return reconcile.Result{}, err
		}
		return r.finish(ctx, op, stage, c)
	}
	if r.passed(op.Status.Times.ApprovalDeadline) {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Errored, freeze: true, reason: "VerifyUndecided: nobody accepted or rejected the unknown in time"})
	}
	if res, done, err := r.unguarded(ctx, op); done {
		return res, err
	}
	// An Approve answers the question asked before the version shipped, not
	// this one, so only an accept or a reject counts here.
	a, skipped := r.approval(ctx, op)
	switch {
	case a == nil:
		return r.recheckHeld(ctx, op, stage, skipped)
	case a.Decision == delivery.Reject:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Failed, reason: "RejectedByHuman: " + a.By + ": " + a.Note})
	}
	return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.AcceptedUnknown, reason: "AcceptedUnknown: " + a.By + " accepted the unknown"})
}

// recheckHeld runs the verify gates again, every recheck interval, on a
// version held for a person after it shipped: a Fail rolls it back as a
// verify Fail does, all Pass verifies it, and Unknown keeps asking with the
// fresh verdicts and why an Approval passed over does not count (skipped).
func (r *OperationReconciler) recheckHeld(ctx context.Context, op *dv1.Operation, stage *dv1.Stage, skipped string) (reconcile.Result, error) {
	now := r.now()
	if last, ok := r.heldChecks.Load(op.UID); ok && now.Before(last.(time.Time).Add(delivery.RecheckInterval(stage.Spec))) {
		if msg := heldMessage(op.Status.Verdicts, skipped); skipped != "" && msg != op.Status.Message {
			op.Status.Message = msg
			return r.save(ctx, op)
		}
		return r.requeue()
	}
	r.heldChecks.Store(op.UID, now)
	vs := r.verdicts(ctx, op, stage)
	op.Status.Verdicts = slices.DeleteFunc(op.Status.Verdicts, func(x delivery.Verdict) bool { return x.Phase == "verify" })
	op.Status.Verdicts = append(op.Status.Verdicts, vs...)
	switch delivery.Worst(vs) {
	case delivery.Pass:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Verified, reason: "Verified: " + verdictMessages(vs, delivery.Pass)})
	case delivery.Fail:
		f := failing(vs)
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Failed, reason: "VerifyFailed: " + f.Gate + ": " + f.Message, cause: f})
	}
	op.Status.Message = heldMessage(vs, skipped)
	return r.save(ctx, op)
}

// heldMessage is what a version held on an unknown says: the Unknown
// verdicts and why an Approval passed over does not count.
func heldMessage(vs []delivery.Verdict, skipped string) string {
	return strings.Join(slices.DeleteFunc([]string{verdictMessages(vs, delivery.Unknown), skipped}, func(m string) bool { return m == "" }), "; ")
}

// StageLabel names the Stage an operation acts on, for selection.
const StageLabel = "delivery.pacto.trianalab.io/stage"
