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
	"strconv"
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

// immediately asks to run again right away: the next step needs no wait.
const immediately = time.Millisecond

// reasonWaiting marks an operation parked on the Stage: a lock, a freeze.
const reasonWaiting = "Waiting"

// authFreshness is how old a login may be when its owner approves.
const authFreshness = 15 * time.Minute

var (
	errWait         = errors.New("wait")
	errPointerMoved = errors.New("pointer moved")
	errLockLost     = errors.New("lock lost")
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
	Now       func() time.Time
	// Poll is how often an operation waiting on the outside world is looked at.
	Poll time.Duration
}

// conclusion is how an operation ends.
type conclusion struct {
	outcome      delivery.Outcome
	verification delivery.Verification
	reason       string // "Code: message"
	freeze       bool
}

// Reconcile implements reconcile.Reconciler.
func (r *OperationReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	op := &dv1.Operation{}
	if err := r.Get(ctx, req.NamespacedName, op); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if op.Status.Phase == delivery.PhaseConcluded {
		return reconcile.Result{}, r.releaseFinalizer(ctx, op)
	}
	stage := &dv1.Stage{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.Stage}, stage); err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		c := conclusion{outcome: delivery.Rejected, reason: "NoStage: there is no Stage named " + op.Spec.Stage}
		if op.Status.Applied {
			c.outcome = delivery.Errored
		}
		return r.finish(ctx, op, nil, c)
	}
	switch {
	case op.Status.Phase == "":
		return r.admit(ctx, op, stage)
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

// save writes the operation's status and polls again.
func (r *OperationReconciler) save(ctx context.Context, op *dv1.Operation) (reconcile.Result, error) {
	if err := r.Status().Update(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	return r.requeue()
}

// admit validates the intent once. Everything checked here is a property of
// the request, so a refusal is final.
func (r *OperationReconciler) admit(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	now := r.now()
	class := r.Policy.Classify(delivery.Identity{User: op.Spec.Actor.User, Groups: op.Spec.Actor.Groups})
	op.Status.ActorClass = class
	op.Status.Times.Admitted = ts(now)
	t, known := delivery.TypeByID(op.Spec.Type)
	approval, _ := t.Deadlines(stage.Spec.Deadlines)
	op.Status.Times.ApprovalDeadline = ts(now.Add(approval))
	if reason := r.refusal(ctx, op, stage, t, known); reason != "" {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: reason})
	}
	op.Status.Phase = delivery.PhasePending
	if err := r.Status().Update(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: immediately}, nil
}

func (r *OperationReconciler) refusal(ctx context.Context, op *dv1.Operation, stage *dv1.Stage, t delivery.OperationType, known bool) string {
	s := op.Spec
	switch {
	case !known:
		return "UnknownType: " + s.Type + " is not an operation type"
	case s.Facet != t.Facet:
		return fmt.Sprintf("WrongFacet: %s changes the %s facet, not %s", s.Type, t.Facet, s.Facet)
	case !delivery.MayRaise(s.Type, op.Status.ActorClass, s.Witnessed):
		return fmt.Sprintf("NotPermitted: a %s actor may not raise %s", op.Status.ActorClass, s.Type)
	case s.IntentDigest != delivery.IntentDigest(s, stage.Spec):
		return "PlanStale: the intent digest does not match this Stage's policy; plan again"
	}
	if s.Facet != delivery.FacetPointer {
		return ""
	}
	if _, err := delivery.ParseDigest(s.Params.To); err != nil {
		return "InvalidDigest: to must be an OCI manifest digest, not " + strconv.Quote(s.Params.To)
	}
	if _, err := delivery.ParseDigest(s.Params.From); s.Params.From != "" && err != nil {
		return "InvalidDigest: from must be an OCI manifest digest, not " + strconv.Quote(s.Params.From)
	}
	if s.Params.From != stage.Status.Pointer {
		return fmt.Sprintf("PointerMoved: from is %q but the Stage runs %q", s.Params.From, stage.Status.Pointer)
	}
	if s.Type != delivery.TypeRollback {
		return ""
	}
	return delivery.AdmitRollback(s, stage.Status, r.cited(ctx, op), op.Status.ActorClass)
}

// cited finds the operation a rollback undoes, by uid, on the same Stage.
func (r *OperationReconciler) cited(ctx context.Context, op *dv1.Operation) *delivery.CitedOp {
	for _, o := range r.stageOps(ctx, op.Namespace, op.Spec.Stage) {
		if string(o.UID) == op.Spec.Params.Undoes {
			return &delivery.CitedOp{UID: string(o.UID), Type: o.Spec.Type, Applied: o.Status.Applied, Pointer: o.Status.Pointer}
		}
	}
	return nil
}

func (r *OperationReconciler) stageOps(ctx context.Context, ns, stage string) []dv1.Operation {
	return stageOps(ctx, r.Client, ns, stage)
}

func stageOps(ctx context.Context, c client.Reader, ns, stage string) []dv1.Operation {
	list := &dv1.OperationList{}
	if err := c.List(ctx, list, client.InNamespace(ns)); err != nil {
		return nil
	}
	return slices.DeleteFunc(list.Items, func(o dv1.Operation) bool { return o.Spec.Stage != stage })
}

// pending runs the pre-gates.
func (r *OperationReconciler) pending(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if res, done, err := r.preApplyExits(ctx, op, stage); done {
		return res, err
	}
	if op.Spec.Type == delivery.TypePromote {
		if newer := r.newerPromote(ctx, op); newer != "" {
			return r.finish(ctx, op, stage, conclusion{outcome: delivery.Superseded, reason: "Superseded: " + newer + " is a newer promote on this Stage"})
		}
	}
	if op.Spec.Facet == delivery.FacetFreeze {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified, reason: "Unfrozen: automation may move the Stage again"})
	}
	plan, err := r.plan(ctx, op, stage)
	if err != nil {
		return reconcile.Result{}, err
	}
	op.Status.Plan, op.Status.Verdicts = &plan, plan.Verdicts
	switch plan.Predicted {
	case delivery.PredictRejected:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: planRefusal(plan)})
	case delivery.PredictHeld:
		op.Status.Phase, op.Status.Question = delivery.PhaseHeld, plan.Question
		op.Status.Reason, op.Status.Message = "AwaitingHuman", "a person must "+string(plan.Floor.Decision)
		return r.save(ctx, op)
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

func planRefusal(p delivery.Plan) string {
	for _, v := range p.Verdicts {
		if v.Result == delivery.Fail {
			return "GateFailed: " + v.Gate + ": " + v.Message
		}
	}
	return p.Conflicts[0]
}

// newerPromote returns the name of a newer promote on the same Stage that has
// not moved the pointer, ordered by (creationTimestamp, uid).
func (r *OperationReconciler) newerPromote(ctx context.Context, op *dv1.Operation) string {
	for _, o := range r.stageOps(ctx, op.Namespace, op.Spec.Stage) {
		if o.Spec.Type != delivery.TypePromote || o.Status.Applied || o.Status.Phase == delivery.PhaseConcluded || o.UID == op.UID {
			continue
		}
		if c := o.CreationTimestamp.Compare(op.CreationTimestamp.Time); c > 0 || c == 0 && o.UID > op.UID {
			return o.Name
		}
	}
	return ""
}

// plan gathers plan()'s inputs from the cluster and the registry and runs it.
func (r *OperationReconciler) plan(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (delivery.Plan, error) {
	in := delivery.PlanInput{Spec: op.Spec, Stage: stage.Spec, Status: stage.Status, Class: op.Status.ActorClass,
		From: delivery.Revision{Digest: op.Spec.Params.From}, To: delivery.Revision{Digest: op.Spec.Params.To}}
	if op.Spec.Type == delivery.TypePromote {
		a, err := r.Cluster.ociRepository(ctx, op.Namespace, stage.Spec.ApplyRef.Name)
		if err != nil {
			in.From.Err, in.To.Err = "the apply OCIRepository: "+err.Error(), "the apply OCIRepository: "+err.Error()
		} else {
			reg := r.Cluster.registryFor(a)
			in.From = delivery.ResolveRevision(ctx, reg, a.repo, op.Spec.Params.From)
			in.To = delivery.ResolveRevision(ctx, reg, a.repo, op.Spec.Params.To)
		}
		pactos, listErr := r.Cluster.pactos(ctx)
		snap, err := delivery.Snapshot(ctx, pactos, listErr, []delivery.Revision{in.From, in.To}, r.now())
		if err != nil {
			return delivery.Plan{}, err
		}
		in.Snapshot = snap
	}
	return delivery.PlanFor(ctx, in), nil
}

// held consumes approvals for an operation waiting before it moved anything.
func (r *OperationReconciler) held(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if res, done, err := r.preApplyExits(ctx, op, stage); done {
		return res, err
	}
	a := r.approval(ctx, op)
	if a == nil {
		return r.requeue()
	}
	switch {
	case a.Decision == delivery.Reject:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: "RejectedByHuman: " + a.By + ": " + a.Note})
	case a.Decision == delivery.AcceptUnknown && op.Status.Plan != nil && op.Status.Plan.Floor.Decision == delivery.Approve:
		op.Status.Message = a.By + " accepted the unknown, but this operation needs an approval"
		return r.save(ctx, op)
	}
	return r.lock(ctx, op, stage, true)
}

// approval consumes the first valid, not yet consumed Approval for op. A valid
// one is bound to this uid and intent digest, comes from a person who is
// neither the actor nor whoever the actor acts for, and was given within
// authFreshness of that person's login.
func (r *OperationReconciler) approval(ctx context.Context, op *dv1.Operation) *delivery.ApprovalRecord {
	list := &dv1.ApprovalList{}
	if err := r.List(ctx, list, client.InNamespace(op.Namespace)); err != nil {
		return nil
	}
	slices.SortFunc(list.Items, func(a, b dv1.Approval) int { return a.CreationTimestamp.Compare(b.CreationTimestamp.Time) })
	for _, a := range list.Items {
		s := a.Spec
		consumed := slices.ContainsFunc(op.Status.Approvals, func(x delivery.ApprovalRecord) bool { return x.Name == a.Name })
		if consumed || s.OperationUID != string(op.UID) || s.IntentDigest != op.Spec.IntentDigest ||
			s.By == op.Spec.Actor.User || op.Spec.OnBehalfOf != nil && s.By == op.Spec.OnBehalfOf.User ||
			!r.Policy.IsHuman(delivery.Identity{User: s.By}) || !fresh(s.AuthTime, a.CreationTimestamp.Time) {
			continue
		}
		rec := delivery.ApprovalRecord{Name: a.Name, By: s.By, Decision: s.Decision, Note: s.Note, AuthTime: s.AuthTime}
		op.Status.Approvals = append(op.Status.Approvals, rec)
		return &rec
	}
	return nil
}

// fresh reports whether a login at authTime (RFC 3339 or Unix seconds) was at
// most authFreshness before at.
func fresh(authTime string, at time.Time) bool {
	t, err := time.Parse(time.RFC3339, authTime)
	if err != nil {
		secs, perr := strconv.ParseInt(authTime, 10, 64)
		if perr != nil {
			return false
		}
		t = time.Unix(secs, 0)
	}
	age := at.Sub(t)
	return age >= -time.Minute && age <= authFreshness
}

// lock takes the Stage's facet lock with one compare-and-swap. An approved
// promote re-checks its gates first: every gate that passed must still pass,
// and a floor the approval did not cover sends it back to a person.
func (r *OperationReconciler) lock(ctx context.Context, op *dv1.Operation, stage *dv1.Stage, recheck bool) (reconcile.Result, error) {
	if recheck && op.Spec.Type == delivery.TypePromote {
		cur, err := r.plan(ctx, op, stage)
		if err != nil {
			return reconcile.Result{}, err
		}
		if cur.Predicted == delivery.PredictRejected {
			return r.finish(ctx, op, stage, conclusion{outcome: delivery.Rejected, reason: planRefusal(cur)})
		}
		if !stillPasses(*op.Status.Plan, cur) {
			op.Status.Plan, op.Status.Verdicts = &cur, cur.Verdicts
			op.Status.Phase, op.Status.Question = delivery.PhaseHeld, cur.Question
			op.Status.Reason, op.Status.Message = "RecheckChanged", "a gate no longer passes, so a person must decide again"
			return r.save(ctx, op)
		}
		op.Status.Verdicts = cur.Verdicts
	}
	facet, uid := op.Spec.Facet, string(op.UID)
	waiting := ""
	err := r.casStage(ctx, stage, func(s *dv1.Stage) error {
		if holder := s.Status.Locks[facet]; holder != "" && holder != uid {
			if !r.lockFree(ctx, op, holder) {
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
	case errors.Is(err, errWait):
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

// stillPasses reports whether an approval given on prev still covers cur:
// every gate that passed still passes (a Pass that comes back empty is Unknown,
// and Unknown never passes), and cur asks for nothing the approval did not
// answer.
func stillPasses(prev, cur delivery.Plan) bool {
	for _, v := range prev.Verdicts {
		if v.Result != delivery.Pass {
			continue
		}
		if !slices.ContainsFunc(cur.Verdicts, func(c delivery.Verdict) bool { return c.Gate == v.Gate && c.Result == delivery.Pass }) {
			return false
		}
	}
	return !cur.Floor.Human || cur.Floor.Decision == prev.Floor.Decision || prev.Floor.Decision == delivery.Approve
}

// lockFree reports whether op may take the lock holder has: holder is gone or
// concluded, or op is a rollback or override and holder is verifying or held
// after apply, which priority lets it preempt.
func (r *OperationReconciler) lockFree(ctx context.Context, op *dv1.Operation, holder string) bool {
	o := r.opByUID(ctx, op.Namespace, holder)
	if o == nil || o.Status.Phase == delivery.PhaseConcluded {
		return true
	}
	preempts := op.Spec.Type == delivery.TypeRollback || op.Spec.Type == delivery.TypeOverride
	return preempts && (o.Status.Phase == delivery.PhaseVerifying || o.Status.Phase == delivery.PhaseHeld && o.Status.Applied)
}

func (r *OperationReconciler) opByUID(ctx context.Context, ns, uid string) *dv1.Operation {
	list := &dv1.OperationList{}
	if err := r.List(ctx, list, client.InNamespace(ns)); err != nil {
		return nil
	}
	for i := range list.Items {
		if string(list.Items[i].UID) == uid {
			return &list.Items[i]
		}
	}
	return nil
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
	a, err := r.Cluster.ociRepository(ctx, op.Namespace, stage.Spec.ApplyRef.Name)
	if err != nil {
		op.Status.Message = "reading the apply OCIRepository: " + err.Error()
		return r.save(ctx, op)
	}
	if a.digest != to {
		if a.digest != from {
			return r.finish(ctx, op, stage, conclusion{outcome: delivery.Errored, freeze: true,
				reason: fmt.Sprintf("ForeignWriter: spec.ref.digest is %q, which this operation did not write; it is left as found", a.digest)})
		}
		if err := r.Cluster.setDigest(ctx, a, to, r.now()); err != nil {
			return reconcile.Result{}, err
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

// verdict runs the pacto.contract verify gate against the applied version.
func (r *OperationReconciler) verdict(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) delivery.Verdict {
	to := op.Spec.Params.To
	unknown := delivery.Verdict{Gate: delivery.GateContract, Phase: "verify", Result: delivery.Unknown}
	a, err := r.Cluster.ociRepository(ctx, op.Namespace, stage.Spec.ApplyRef.Name)
	if err != nil {
		unknown.Message = "reading the apply OCIRepository: " + err.Error()
		return unknown
	}
	ann, err := r.Cluster.registryFor(a).Annotations(ctx, a.repo+"@"+to)
	if err != nil {
		unknown.Message = "reading the artifact: " + err.Error()
		return unknown
	}
	ok, _, _, _ := r.Cluster.applied(ctx, op.Namespace, a, to)
	pactos, err := r.Cluster.pactos(ctx)
	if err != nil {
		unknown.Message = "listing Pactos: " + err.Error()
		return unknown
	}
	return delivery.ContractVerdict(delivery.ContractCheck{
		ContractRef: ann[delivery.ContractAnnotation], Applied: ok,
		Pactos:   delivery.PactosFor(pactos, op.Namespace, stage.Spec.Service.Name),
		Workload: r.Cluster.workload(ctx, op.Namespace),
	})
}

// preempted reports whether a rollback or override took this operation's lock.
func preempted(op *dv1.Operation, stage *dv1.Stage) bool {
	return stage.Status.Locks[op.Spec.Facet] != string(op.UID)
}

func (r *OperationReconciler) verifying(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if preempted(op, stage) {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified, reason: "Preempted: a rollback or override took the Stage"})
	}
	v := r.verdict(ctx, op, stage)
	op.Status.Verdicts = append(slices.DeleteFunc(op.Status.Verdicts, func(x delivery.Verdict) bool { return x.Gate == v.Gate }), v)
	switch {
	case v.Result == delivery.Pass:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Verified, reason: "Verified: " + v.Message})
	case v.Result == delivery.Fail:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Failed, reason: "VerifyFailed: " + v.Message})
	case r.passed(op.Status.Times.VerifyDeadline):
		t, _ := delivery.TypeByID(op.Spec.Type)
		approval, _ := t.Deadlines(stage.Spec.Deadlines)
		op.Status.Phase = delivery.PhaseHeld
		op.Status.Times.ApprovalDeadline = ts(r.now().Add(approval))
		op.Status.Reason, op.Status.Message = "VerifyUnknown", v.Message
		op.Status.Question = "The Pacto contract check could not confirm " + op.Spec.Params.To + " in time: " + v.Message +
			". Accept the unknown (the version stays), or reject it (it is rolled back)?"
		return r.save(ctx, op)
	}
	op.Status.Message = v.Message
	return r.save(ctx, op)
}

// heldAfterApply waits for a person on a version that is already running. It
// keeps the lock, but a rollback or override may preempt it.
func (r *OperationReconciler) heldAfterApply(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if preempted(op, stage) {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified, reason: "Preempted: a rollback or override took the Stage"})
	}
	if r.passed(op.Status.Times.ApprovalDeadline) {
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Errored, freeze: true, reason: "VerifyUndecided: nobody accepted or rejected the unknown in time"})
	}
	a := r.approval(ctx, op)
	switch {
	case a == nil:
		return r.requeue()
	case a.Decision == delivery.Reject:
		return r.finish(ctx, op, stage, conclusion{outcome: delivery.Failed, reason: "RejectedByHuman: " + a.By + ": " + a.Note})
	}
	return r.finish(ctx, op, stage, conclusion{outcome: delivery.Succeeded, verification: delivery.AcceptedUnknown, reason: "AcceptedUnknown: " + a.By + " accepted the unknown"})
}

// finish concludes op: one Stage compare-and-swap releases the lock (only if
// this operation holds it), assigns the stream seq and applies the freeze,
// auto-hold and digest memories; then the operation's status is written. A
// failed promote raises its one bound rollback first, so nothing can slip in
// between the failure and the rollback.
func (r *OperationReconciler) finish(ctx context.Context, op *dv1.Operation, stage *dv1.Stage, c conclusion) (reconcile.Result, error) {
	code, msg, _ := strings.Cut(c.reason, ": ")
	if stage != nil {
		if c.outcome == delivery.Failed && op.Spec.Type == delivery.TypePromote && op.Status.Pointer.Before != "" {
			if err := r.raiseRollback(ctx, op, stage, code+": "+msg); err != nil {
				return reconcile.Result{}, err
			}
		}
		if err := r.casStage(ctx, stage, func(s *dv1.Stage) error {
			if s.Status.Locks[op.Spec.Facet] == string(op.UID) {
				delete(s.Status.Locks, op.Spec.Facet)
			}
			s.Status.Stream.Seq++
			op.Status.Seq = s.Status.Stream.Seq
			applyEffects(&s.Status, op, c)
			return nil
		}); err != nil {
			return reconcile.Result{}, err
		}
	}
	op.Status.Phase, op.Status.Outcome, op.Status.Verification = delivery.PhaseConcluded, c.outcome, c.verification
	op.Status.Reason, op.Status.Message, op.Status.Question = code, msg, ""
	op.Status.Times.Finished = ts(r.now())
	if err := r.Status().Update(ctx, op); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: immediately}, nil
}

// applyEffects updates the Stage's memories for a concluded operation.
func applyEffects(s *delivery.StageStatus, op *dv1.Operation, c conclusion) {
	to := op.Spec.Params.To
	if op.Status.Applied {
		switch {
		case delivery.SetsAutoHold(op.Spec, op.Status.ActorClass, *s):
			s.AutoHold = true
		case op.Spec.Type == delivery.TypePromote && op.Status.ActorClass == delivery.ClassHuman:
			s.AutoHold = false
		}
	}
	switch c.outcome {
	case delivery.Succeeded:
		if c.verification == delivery.Verified {
			s.Verified = delivery.AddDigest(s.Verified, to, delivery.MaxVerified)
		}
		if op.Spec.Type == delivery.TypeUnfreeze {
			s.Frozen, s.FrozenReason, s.AutoHold = false, "", false
		}
	case delivery.Failed:
		s.FailedDigests = delivery.AddDigest(s.FailedDigests, to, delivery.MaxFailedDigests)
		s.Verified = delivery.RemoveDigest(s.Verified, to)
		switch {
		case op.Spec.Type != delivery.TypePromote:
			c.freeze, c.reason = true, "a "+op.Spec.Type+" failed: "+c.reason
		case op.Status.Pointer.Before == "":
			c.freeze, c.reason = true, "the first version failed, so there is nothing to roll back to"
		default:
			if s.AutoRollbacks == nil {
				s.AutoRollbacks = map[string]int{}
			}
			s.AutoRollbacks[string(op.UID)]++
		}
	}
	if c.freeze {
		s.Frozen, s.FrozenReason = true, op.Name+": "+c.reason
	}
}

// raiseRollback creates the one rollback bound to a failed promote. Its name is
// derived from the failed operation, so raising it twice is a no-op: at most
// one automatic rollback per cause.
func (r *OperationReconciler) raiseRollback(ctx context.Context, failed *dv1.Operation, stage *dv1.Stage, why string) error {
	rb := &dv1.Operation{}
	rb.Namespace, rb.Name = failed.Namespace, failed.Name+"-rollback"
	rb.Labels = map[string]string{StageLabel: stage.Name}
	controllerutil.AddFinalizer(rb, dv1.RecordFinalizer)
	rb.Spec = delivery.OperationSpec{
		Type: delivery.TypeRollback, Stage: stage.Name, Facet: delivery.FacetPointer,
		Params: delivery.Params{From: failed.Spec.Params.To, To: failed.Status.Pointer.Before, Undoes: string(failed.UID)},
		Reason: "automatic rollback: " + failed.Name + " failed (" + why + ")",
		Actor:  delivery.Actor{User: r.Policy.ControllerUser},
		Cause:  &delivery.Cause{Operation: string(failed.UID)},
	}
	rb.Spec.IntentDigest = delivery.IntentDigest(rb.Spec, stage.Spec)
	if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// releaseFinalizer lets a concluded, deleted operation go. In M0 nothing writes
// records yet, so the promoter is the one to remove the record finalizer.
// ponytail: the M3 recorder takes this over once records exist.
func (r *OperationReconciler) releaseFinalizer(ctx context.Context, op *dv1.Operation) error {
	if op.DeletionTimestamp.IsZero() || !controllerutil.RemoveFinalizer(op, dv1.RecordFinalizer) {
		return nil
	}
	return r.Update(ctx, op)
}

// StageLabel names the Stage an operation acts on, for selection.
const StageLabel = "delivery.pacto.trianalab.io/stage"
