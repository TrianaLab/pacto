/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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
	if err := r.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.Stage}, stage); err != nil {
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

// admit validates the intent once. Everything checked here is a property of
// the request, so a refusal is final.
func (r *OperationReconciler) admit(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if res, done, err := r.unguarded(ctx, op); done {
		return res, err
	}
	// A refusal is final, so admission never judges a cached Stage that may
	// lag a pointer move or a policy edit: it reads the Stage uncached, once.
	stage = &dv1.Stage{}
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
		op.Status.Reason, op.Status.Message = "AwaitingHuman", "a person must "+string(plan.Floor.Decision)
		return r.saveHeld(ctx, op)
	case delivery.PredictWaits:
		op.Status.Reason, op.Status.Message = reasonWaiting, strings.Join(plan.Conflicts, "; ")
		return r.save(ctx, op)
	}
	return r.lock(ctx, op, stage, false)
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
		switch {
		case v.Result == delivery.Fail && v.Gate == delivery.GatePublished:
			return "NotPublished: " + v.Message
		case v.Result == delivery.Fail:
			return "GateFailed: " + v.Gate + ": " + v.Message
		}
	}
	return p.Conflicts[0]
}

// newerPromote returns the name of a newer promote on the same Stage that has
// not moved the pointer, ordered by (creationTimestamp, uid).
func (r *OperationReconciler) newerPromote(ctx context.Context, op *dv1.Operation) (string, error) {
	ops, err := stageOps(ctx, r.Client, op.Namespace, op.Spec.Stage)
	if err != nil {
		return "", err
	}
	for _, o := range ops {
		if o.Spec.Type != delivery.TypePromote || o.Status.Applied || o.Status.Phase == delivery.PhaseConcluded || o.UID == op.UID {
			continue
		}
		if c := o.CreationTimestamp.Compare(op.CreationTimestamp.Time); c > 0 || c == 0 && o.UID > op.UID {
			return o.Name, nil
		}
	}
	return "", nil
}

// plan gathers plan()'s inputs from the cluster and the registry and runs it.
func (r *OperationReconciler) plan(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (delivery.Plan, error) {
	in, err := delivery.AssemblePlanInput(ctx, planSources{r}, op.Spec, stageObject(stage), op.Status.ActorClass, r.now())
	if err != nil {
		return delivery.Plan{}, err
	}
	return delivery.PlanFor(ctx, in), nil
}

func stageObject(s *dv1.Stage) delivery.StageObject {
	return delivery.StageObject{Metadata: delivery.ObjectMeta{Name: s.Name, Namespace: s.Namespace, UID: string(s.UID)}, Spec: s.Spec, Status: s.Status}
}

// planSources is delivery.PlanSources over the promoter's own clients.
type planSources struct{ r *OperationReconciler }

func (p planSources) Revisions(ctx context.Context, stage delivery.StageObject, from, to string) (delivery.Revision, delivery.Revision) {
	a, err := p.r.Cluster.applier(ctx, stage.Metadata.Namespace, stage.Spec.ApplyRef)
	if err != nil {
		why := "the apply OCIRepository: " + err.Error()
		return delivery.Revision{Digest: from, Err: why}, delivery.Revision{Digest: to, Err: why}
	}
	reg := p.r.Cluster.registryFor(a)
	return delivery.ResolveRevision(ctx, reg, a.repo, from), delivery.ResolveRevision(ctx, reg, a.repo, to)
}

func (p planSources) Pactos(ctx context.Context) ([]delivery.PactoView, error) { return p.r.Cluster.pactos(ctx) }

func (p planSources) Kustomizations(ctx context.Context) ([]delivery.KustomizationView, error) {
	return p.r.Cluster.kustomizations(ctx)
}

func (p planSources) Describe(ctx context.Context, stage delivery.StageObject, digest string) delivery.Revision {
	return p.r.Cluster.describe(ctx, stage, digest)
}

// Stages lists every Stage, so a blocked plan can place its consumers.
func (p planSources) Stages(ctx context.Context) ([]delivery.StageObject, error) {
	list := &dv1.StageList{}
	if err := p.r.List(ctx, list); err != nil {
		return nil, err
	}
	out := make([]delivery.StageObject, 0, len(list.Items))
	for i := range list.Items {
		s := stageObject(&list.Items[i])
		s.Metadata.UID = ""
		out = append(out, s)
	}
	return out, nil
}

// Upstream reads the status of the Stage this one takes its candidates from.
// It is nil when there is none, and nil with why when it does not exist or
// the promoter may not read it: the published gate then refuses, as nothing
// it offers is known. Any other read error is returned, so the operation is
// planned again. A Stage in another cluster is read from its signed records,
// and why says why that gave nothing.
func (p planSources) Upstream(ctx context.Context, stage delivery.StageObject) (*delivery.StageStatus, string, error) {
	if rs := stage.Spec.Candidates.UpstreamRecords; rs != nil {
		s, why := p.r.Cluster.remote(ctx, *rs)
		return s, why, nil
	}
	up := stage.Spec.Candidates.UpstreamStage
	if up == nil {
		return nil, "", nil
	}
	ns := cmp.Or(up.Namespace, stage.Metadata.Namespace)
	s := &dv1.Stage{}
	if err := p.r.Get(ctx, types.NamespacedName{Namespace: ns, Name: up.Name}, s); err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
			return nil, "the upstream Stage " + ns + "/" + up.Name + " cannot be read: " + err.Error(), nil
		}
		return nil, "", err
	}
	return &s.Status, "", nil
}

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
	return r.lock(ctx, op, stage, true)
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
		ok, why := delivery.ApprovalEligible(op.Spec, op.Status, string(op.UID), s, a.CreationTimestamp.Time, r.Policy,
			cmp.Or(r.Freshness, delivery.AuthFreshness))
		if !ok {
			skipped = "Approval " + a.Name + " by " + s.By + " does not count: " + why
			continue
		}
		rec := delivery.ApprovalRecord{Name: a.Name, By: s.By, Decision: s.Decision, Note: s.Note, AuthTime: s.AuthTime}
		op.Status.Approvals = append(op.Status.Approvals, rec)
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
// garbage collected with it instead of piling up. Admission keeps an
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
			op.Status.Plan, op.Status.Verdicts = &cur, mergeVerdicts(op.Status.Verdicts, cur.Verdicts)
			op.Status.Phase, op.Status.Question, op.Status.Times.Held = delivery.PhaseHeld, cur.Question, ts(r.now())
			op.Status.Reason, op.Status.Message = "RecheckChanged", "a gate no longer passes, so a person must decide again"
			return r.saveHeld(ctx, op)
		}
		op.Status.Verdicts = mergeVerdicts(op.Status.Verdicts, cur.Verdicts)
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

// stillPasses reports whether an approval given on prev still covers cur:
// every gate that passed still passes (a Pass that comes back empty is Unknown,
// and Unknown never passes), and cur asks for nothing the approval did not
// answer: it is routed to no team and names no consumer the approval was not
// asked about, and the impact gate is unknown for the reason the person saw.
func stillPasses(prev, cur delivery.Plan) bool {
	for _, v := range prev.Verdicts {
		if v.Result != delivery.Pass {
			continue
		}
		if !slices.ContainsFunc(cur.Verdicts, func(c delivery.Verdict) bool { return c.Gate == v.Gate && c.Result == delivery.Pass }) {
			return false
		}
	}
	if !cur.Floor.Human {
		return true
	}
	if !subset(cur.Floor.Owners, prev.Floor.Owners) || !subset(cur.Floor.Consumers, prev.Floor.Consumers) {
		return false
	}
	// ponytail: only the impact gate's message is compared, as it lists what
	// Pacto cannot see; other gates' Unknown messages carry error text that
	// changes from one read to the next.
	for _, v := range cur.Verdicts {
		if v.Gate == delivery.GateImpact && v.Result == delivery.Unknown &&
			!slices.ContainsFunc(prev.Verdicts, func(p delivery.Verdict) bool {
				return p.Gate == v.Gate && p.Result == v.Result && p.Message == v.Message
			}) {
			return false
		}
	}
	return cur.Floor.Decision == prev.Floor.Decision || prev.Floor.Decision == delivery.Approve
}

// subset reports whether every element of a is in b.
func subset(a, b []string) bool {
	return !slices.ContainsFunc(a, func(x string) bool { return !slices.Contains(b, x) })
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

// preemptedBy is how an applied operation a rollback or override preempted
// concludes.
var preemptedBy = conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified, reason: "Preempted: a rollback or override took the Stage"}

func (r *OperationReconciler) verifying(ctx context.Context, op *dv1.Operation, stage *dv1.Stage) (reconcile.Result, error) {
	if preempted(op, stage) {
		return r.finish(ctx, op, stage, preemptedBy)
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
		return r.finish(ctx, op, stage, preemptedBy)
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
		squat := ""
		if c.outcome == delivery.Failed && op.Spec.Type == delivery.TypePromote && op.Status.Pointer.Before != "" {
			// Never on a stale decision: an operation the Stage already
			// concluded, maybe as a success, raises no rollback.
			cur := &dv1.Stage{}
			if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(stage), cur); err != nil {
				return reconcile.Result{}, err
			}
			// Nor when a rollback or override took the Stage while the gates
			// ran: the compare-and-swap below concludes it Preempted.
			// ponytail: a lock taken between this read and that swap still
			// gets the rollback raised, which is then refused and freezes.
			if concludedOn(cur.Status, op.UID) == nil && !preempted(op, cur) && !slices.Contains(cur.Status.FailedDigests, op.Status.Pointer.Before) {
				var err error
				if squat, err = raiseRollback(ctx, r.Client, r.Policy, op, stage, code+": "+msg, c.cause); err != nil {
					return reconcile.Result{}, err
				}
				if squat == "" {
					event(r.Events, stage, true, "RollbackRaised", op.Name+" failed ("+code+": "+msg+"), so it is rolled back to "+op.Status.Pointer.Before)
				}
			}
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
			if taken {
				c = preemptedBy
			}
			s.Status.Stream.Seq++
			op.Status.Seq = s.Status.Stream.Seq
			done := delivery.Concluded{UID: string(op.UID), Seq: op.Status.Seq, Outcome: c.outcome, Verification: c.verification, Reason: c.reason}
			s.Status.Stream.Concluded = append(s.Status.Stream.Concluded, done)
			s.Status.Stream.Concluded = s.Status.Stream.Concluded[max(0, len(s.Status.Stream.Concluded)-delivery.MaxConcluded):]
			applyEffects(&s.Status, op, c)
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

// passedPublished reports whether pacto.published passed: a version a person
// accepted on an unknown published verdict is never offered downstream.
func passedPublished(vs []delivery.Verdict) bool {
	return slices.ContainsFunc(vs, func(v delivery.Verdict) bool {
		return v.Gate == delivery.GatePublished && v.Result == delivery.Pass
	})
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
			if op.Spec.Type == delivery.TypePromote && passedPublished(op.Status.Verdicts) {
				s.Offered = delivery.AddDigest(s.Offered, to, delivery.MaxOffered)
			}
		}
		switch {
		case op.Spec.Type == delivery.TypeUnfreeze:
			s.Frozen, s.FrozenReason, s.AutoHold = false, "", false
		case op.Spec.Type == delivery.TypeFreeze && !s.Frozen:
			// A freeze a failed re-check raised finds the Stage frozen already,
			// with that check's reason, which it keeps.
			s.Frozen, s.FrozenReason = true, op.Name+": frozen: "+op.Spec.Reason
		}
	case delivery.Rejected:
		// A refused automatic rollback must not leave the failed version
		// running unwatched: a person decides.
		if op.Spec.Type == delivery.TypeRollback && op.Status.ActorClass == delivery.ClassController {
			c.freeze, c.reason = true, "the automatic rollback was refused: "+c.reason
		}
	case delivery.Failed:
		s.FailedDigests = delivery.AddDigest(s.FailedDigests, to, delivery.MaxFailedDigests)
		s.Verified = delivery.RemoveDigest(s.Verified, to)
		switch {
		case op.Spec.Type != delivery.TypePromote:
			c.freeze, c.reason = true, "a "+op.Spec.Type+" failed: "+c.reason
		case op.Status.Pointer.Before == "":
			c.freeze, c.reason = true, "the first version failed, so there is nothing to roll back to"
		case slices.Contains(s.FailedDigests, op.Status.Pointer.Before):
			c.freeze, c.reason = true, "the version before it failed here too, so there is nothing safe to roll back to"
		default:
			s.RolledBack = delivery.AddDigest(s.RolledBack, string(op.UID), delivery.MaxRolledBack)
		}
	}
	if c.freeze {
		s.Frozen, s.FrozenReason = true, op.Name+": "+c.reason
	}
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

// StageLabel names the Stage an operation acts on, for selection.
const StageLabel = "delivery.pacto.trianalab.io/stage"
