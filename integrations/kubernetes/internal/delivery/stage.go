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
	Poll      time.Duration
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
		up := &dv1.Stage{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: stage.Namespace, Name: c.UpstreamStage.Name}, up); err != nil {
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
