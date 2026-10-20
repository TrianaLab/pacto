/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
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

// StageReconciler raises the operations nobody asked for: the automatic
// promote of the newest eligible candidate, and the witnessed override that
// records an edit of the pointer the promoter did not make. It never moves the
// pointer itself; only the operation reconciler does.
type StageReconciler struct {
	client.Client
	APIReader client.Reader
	Cluster   Cluster
	Policy    delivery.ClassPolicy
	// ClusterID is the kube-system namespace uid, the cluster half of a stream id.
	ClusterID string
	// Gates are the gates a Stage may name (NewGates).
	Gates map[string]delivery.Gate
	Now   func() time.Time
	Poll  time.Duration
}

// Reconcile implements reconcile.Reconciler.
func (r *StageReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	stage := &dv1.Stage{}
	if err := r.Get(ctx, req.NamespacedName, stage); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	if stage.Status.Stream.ID == "" {
		return reconcile.Result{RequeueAfter: immediately}, r.updateStatus(ctx, stage, func(s *dv1.Stage) {
			s.Status.Stream.ID = r.ClusterID + "/" + s.Namespace + "/" + s.Name
		})
	}
	if err := r.witness(ctx, stage); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.recheck(ctx, stage); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.autoPromote(ctx, stage); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: r.Poll}, nil
}

func (r *StageReconciler) updateStatus(ctx context.Context, stage *dv1.Stage, fn func(*dv1.Stage)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &dv1.Stage{}
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(stage), fresh); err != nil {
			return err
		}
		fn(fresh)
		return r.Status().Update(ctx, fresh)
	})
}

// witness raises a witnessed override when the OCIRepository's digest differs
// from the pointer while no operation holds the pointer lock: someone with
// break-glass rights edited it directly. The edit is never undone; it is
// recorded, adopted and the Stage freezes until a person unfreezes it.
func (r *StageReconciler) witness(ctx context.Context, stage *dv1.Stage) error {
	if stage.Status.Locks[delivery.FacetPointer] != "" || stage.Status.Pointer == "" {
		return nil
	}
	a, err := r.Cluster.ociRepository(ctx, stage.Namespace, stage.Spec.ApplyRef.Name)
	if err != nil {
		return client.IgnoreNotFound(err)
	}
	if _, err := delivery.ParseDigest(a.digest); err != nil || a.digest == stage.Status.Pointer {
		return nil
	}
	spec := delivery.OperationSpec{
		Type: delivery.TypeOverride, Stage: stage.Name, Facet: delivery.FacetPointer,
		Params:    delivery.Params{From: stage.Status.Pointer, To: a.digest},
		Reason:    "witnessed: spec.ref.digest of OCIRepository " + a.obj.GetName() + " was edited outside Pacto",
		Actor:     delivery.Actor{User: r.Policy.ControllerUser},
		Cause:     &delivery.Cause{Reason: "pointer edited outside Pacto (editor: unknown)"},
		Witnessed: true,
	}
	return r.raise(ctx, stage, "witnessed", spec)
}

// autoPromote raises a promote of the newest eligible candidate, unless the
// Stage is frozen, on auto-hold or already has an operation in flight.
func (r *StageReconciler) autoPromote(ctx context.Context, stage *dv1.Stage) error {
	if !stage.Spec.Auto || stage.Status.Frozen || stage.Status.AutoHold {
		return nil
	}
	for _, o := range stageOps(ctx, r.Client, stage.Namespace, stage.Name) {
		if o.Status.Phase != delivery.PhaseConcluded {
			return nil
		}
	}
	cand, err := r.candidate(ctx, stage)
	if err != nil || cand == "" {
		return err
	}
	spec := delivery.OperationSpec{
		Type: delivery.TypePromote, Stage: stage.Name, Facet: delivery.FacetPointer,
		Params: delivery.Params{From: stage.Status.Pointer, To: cand},
		Reason: "automatic promote of the newest eligible candidate",
		Actor:  delivery.Actor{User: r.Policy.ControllerUser},
	}
	return r.raise(ctx, stage, "auto", spec)
}

func (r *StageReconciler) candidate(ctx context.Context, stage *dv1.Stage) (string, error) {
	c := stage.Spec.Candidates
	switch {
	case c.OCIRepositoryRef != nil:
		a, err := r.Cluster.ociRepository(ctx, stage.Namespace, c.OCIRepositoryRef.Name)
		if err != nil {
			return "", client.IgnoreNotFound(err)
		}
		return delivery.NewestCandidate(stage.Status, a.revision, nil), nil
	case c.UpstreamStage != nil:
		up, ns := &dv1.Stage{}, c.UpstreamStage.Namespace
		if ns == "" {
			ns = stage.Namespace
		}
		if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: c.UpstreamStage.Name}, up); err != nil {
			return "", client.IgnoreNotFound(err)
		}
		return delivery.NewestCandidate(stage.Status, "", &up.Status), nil
	}
	return "", nil
}

// raise creates a controller-class operation. Its name is derived from the
// Stage and the pointer move, so raising the same decision twice is a no-op.
func (r *StageReconciler) raise(ctx context.Context, stage *dv1.Stage, kind string, spec delivery.OperationSpec) error {
	sum := sha256.Sum256([]byte(spec.Params.From + ">" + spec.Params.To))
	op := &dv1.Operation{}
	op.Namespace, op.Name = stage.Namespace, stage.Name+"-"+kind+"-"+hex.EncodeToString(sum[:])[:10]
	op.Labels = map[string]string{StageLabel: stage.Name}
	controllerutil.AddFinalizer(op, dv1.RecordFinalizer)
	spec.IntentDigest = delivery.IntentDigest(spec, stage.Spec)
	op.Spec = spec
	if err := r.Create(ctx, op); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// recheck runs the verify gates again on the version the Stage runs, every
// recheck interval, once the operation that shipped it succeeded. Checks keep
// running after release: a confirmed Fail marks the version failed and raises
// its one bound rollback, or freezes the Stage when that cause already had its
// rollback, there is nothing to roll back to or a person or agent chose the
// version (a rollback, or a promote on auto-hold): automation never undoes that
// choice. Unknown never rolls back. A verdict about a Stage that moved on while
// the gates ran is dropped.
func (r *StageReconciler) recheck(ctx context.Context, stage *dv1.Stage) error {
	s, now := stage.Status, r.Now().UTC()
	if s.Frozen || s.LastOp == "" || s.Locks[delivery.FacetPointer] != "" || slices.Contains(s.FailedDigests, s.Pointer) ||
		s.LastCheck != nil && s.LastCheck.Operation == s.LastOp && now.Before(parseOr(s.LastCheck.At).Add(delivery.RecheckInterval(stage.Spec))) {
		return nil
	}
	var shipped *dv1.Operation
	for _, o := range stageOps(ctx, r.Client, stage.Namespace, stage.Name) {
		if o.Status.Phase != delivery.PhaseConcluded {
			return nil // something is in flight; it owns the Stage
		}
		if string(o.UID) == s.LastOp {
			shipped = &o
		}
	}
	if shipped == nil {
		gone := &delivery.Recheck{Operation: s.LastOp, Digest: s.Pointer, At: ts(now), Verdicts: []delivery.Verdict{{
			Gate: "recheck", Phase: "verify", Result: delivery.Unknown,
			Message: "the operation that shipped the running version is gone, so nothing is re-checked",
		}}}
		return r.updateStatus(ctx, stage, func(st *dv1.Stage) {
			if !moved(gone, st.Status) {
				st.Status.LastCheck = gone
			}
		})
	}
	if shipped.Status.Outcome != delivery.Succeeded || shipped.Status.Verification == delivery.Unverified ||
		shipped.Spec.Params.To != s.Pointer {
		return nil
	}
	from := now.Add(-delivery.VerifyWindow(stage.Spec))
	if applied := parseOr(shipped.Status.Times.Applied); applied.After(from) {
		from = applied
	}
	vs := delivery.RunChecks(ctx, r.Gates, verifyChecks(stage), gateInput(shipped, stage, from, now))
	check := &delivery.Recheck{Operation: s.LastOp, Digest: s.Pointer, At: ts(now), Verdicts: vs}
	f := failing(vs)
	if f == nil {
		return r.updateStatus(ctx, stage, func(st *dv1.Stage) {
			if !moved(check, st.Status) {
				st.Status.LastCheck = check
			}
		})
	}
	return r.recheckFailed(ctx, stage, shipped, check, f)
}

// moved reports whether the Stage is no longer the one a check is about.
func moved(check *delivery.Recheck, st delivery.StageStatus) bool {
	return st.LastOp != check.Operation || st.Pointer != check.Digest || st.Locks[delivery.FacetPointer] != ""
}

// recheckFailed acts on a re-check's confirmed Fail f: it marks the version
// failed and raises its one bound rollback, or freezes the Stage.
func (r *StageReconciler) recheckFailed(ctx context.Context, stage *dv1.Stage, shipped *dv1.Operation, check *delivery.Recheck, f *delivery.Verdict) error {
	// The gates can take minutes; drop the verdict if the Stage moved meanwhile.
	fresh := &dv1.Stage{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(stage), fresh); err != nil {
		return err
	}
	if moved(check, fresh.Status) {
		return nil
	}
	why := "RecheckFailed: " + f.Gate + ": " + f.Message
	cause, before := string(shipped.UID), shipped.Status.Pointer.Before
	chosen := shipped.Spec.Type != delivery.TypePromote || fresh.Status.AutoHold
	rollback := !chosen && fresh.Status.AutoRollbacks[cause] == 0 && before != "" && !slices.Contains(fresh.Status.FailedDigests, before)
	if rollback {
		if err := raiseRollback(ctx, r.Client, r.Policy, shipped, stage, why, f); err != nil {
			return err
		}
	}
	return r.updateStatus(ctx, stage, func(st *dv1.Stage) {
		if moved(check, st.Status) {
			return
		}
		st.Status.LastCheck = check
		st.Status.FailedDigests = delivery.AddDigest(st.Status.FailedDigests, check.Digest, delivery.MaxFailedDigests)
		st.Status.Verified = delivery.RemoveDigest(st.Status.Verified, check.Digest)
		switch {
		case chosen:
			st.Status.Frozen, st.Status.FrozenReason = true, shipped.Name+": failed after release, and a person or agent chose this version, so a person must decide: "+why
		case !rollback:
			st.Status.Frozen, st.Status.FrozenReason = true, shipped.Name+": failed after release and cannot be rolled back automatically: "+why
		default:
			if st.Status.AutoRollbacks == nil {
				st.Status.AutoRollbacks = map[string]int{}
			}
			st.Status.AutoRollbacks[cause]++
		}
	})
}

// parseOr parses an RFC 3339 time, zero when it does not parse.
func parseOr(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
