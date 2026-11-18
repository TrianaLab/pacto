/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// ApprovalRecorder writes, on each Stage's record stream, the intent record
// of every operation the promoter admitted and an approval record, signed on
// the person's behalf, of every Approval admission bound to them. It signs
// on behalf only while the Recorder's admission policies (the -approval
// policy) guard Approvals, and only for Approvals created after it first saw
// them guarding, so turning it on never vouches for an Approval admission
// did not check. A failure backs off and writes nothing: the Approval then
// does not count yet.
type ApprovalRecorder struct {
	*Recorder

	mu sync.Mutex
	// guardSince is when the admission policies were first seen guarding,
	// zero while they do not.
	guardSince time.Time
}

// SetupWithManager registers the approval recorder: an Approval wakes the
// operation it answers.
func (r *ApprovalRecorder) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("delivery-approval-recorder").For(&dv1.Operation{}).
		Watches(&dv1.Approval{}, handler.EnqueueRequestsFromMapFunc(approvalToOperation)).
		Complete(r)
}

// Reconcile implements reconcile.Reconciler.
func (r *ApprovalRecorder) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, passTimeout)
	defer cancel()
	op := &dv1.Operation{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, op); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	// Every operation event looks at the guard, so the moment it was first
	// seen is not the moment the first Approval arrives.
	since, err := r.guarded(ctx)
	if err != nil {
		return r.backoff(ctx, op, err)
	}
	if !op.Status.Guarded || op.Status.Phase == delivery.PhaseConcluded {
		return reconcile.Result{}, nil
	}
	stage := &dv1.Stage{}
	if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.Stage}, stage); err != nil {
		return r.backoff(ctx, op, client.IgnoreNotFound(err))
	}
	if stage.Status.Stream.Anchor == "" { // the recorder has not made the stream ready yet
		return reconcile.Result{RequeueAfter: r.Poll}, nil
	}
	anchor, err := r.anchor(ctx, stage.Status.Stream)
	if err != nil {
		return r.backoff(ctx, op, err)
	}
	s := &onStage{ApprovalRecorder: r, stage: stage, anchor: anchor, op: op}
	if err := s.intent(ctx); err != nil {
		return r.backoff(ctx, op, err)
	}
	switch {
	case op.Status.Phase != delivery.PhaseHeld:
		return reconcile.Result{}, nil
	case since.IsZero(): // unguarded: nothing is signed on behalf, and the guard is looked at again
		return reconcile.Result{RequeueAfter: r.Poll}, nil
	}
	return s.approvals(ctx, since)
}

// backoff logs a failure and tries again after Poll; a nil err is a
// reconcile with nothing to do.
func (r *ApprovalRecorder) backoff(ctx context.Context, op *dv1.Operation, err error) (reconcile.Result, error) {
	if err == nil {
		return reconcile.Result{}, nil
	}
	ctrl.LoggerFrom(ctx).Info("not recording the intent or approvals", "operation", op.Name, "error", err.Error())
	return reconcile.Result{RequeueAfter: r.Poll}, nil
}

// guarded says whether the admission policies guard Approvals now, and
// since when they have without a break.
func (r *ApprovalRecorder) guarded(ctx context.Context) (time.Time, error) {
	why, err := unguardedBy(ctx, r.APIReader, r.AdmissionPolicies)
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case err != nil:
		return time.Time{}, err
	case why != "":
		r.guardSince = time.Time{}
	case r.guardSince.IsZero():
		r.guardSince = r.Now()
	}
	return r.guardSince, nil
}

// onStage records one operation's intent and approvals on its Stage's stream.
type onStage struct {
	*ApprovalRecorder
	stage  *dv1.Stage
	anchor *place
	op     *dv1.Operation
}

func (s *onStage) recordStage() delivery.RecordStage {
	return delivery.RecordStage{Cluster: s.ClusterID, Namespace: s.stage.Namespace, Name: s.stage.Name,
		UID: cmp.Or(s.op.Status.StageUID, string(s.stage.UID))}
}

func (s *onStage) object() delivery.OperationObject {
	op := s.op
	return delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: op.Name, Namespace: op.Namespace, UID: string(op.UID)},
		Spec: op.Spec, Status: op.Status}
}

// intent writes the operation's intent record once and names it on the
// operation. The admitted time makes the payload the same on every retry.
func (s *onStage) intent(ctx context.Context) error {
	if s.op.Status.IntentRecord != nil {
		return nil
	}
	st := delivery.NewIntentRecord(s.recordStage(), s.anchor.desc.Digest.String(), s.stage.Status.Stream.Approvals,
		s.object(), s.op.Status.Times.Admitted)
	bundle, id, err := delivery.SignIntent(ctx, s.Signer, st)
	if err != nil {
		return err
	}
	ref, err := s.Store.Push(ctx, s.anchor, delivery.PredicateIntent, bundle, string(s.op.UID))
	if err != nil {
		return err
	}
	s.op.Status.IntentRecord = &delivery.ApprovalRecordRef{Digest: id, Ref: ref}
	return s.Status().Update(ctx, s.op)
}

// approvals signs, on the person's behalf, every Approval of the held
// operation that has no record yet and that admission checked: created
// since the policies guarded without a break, and not a view of a keyless record (spec.record,
// whose own signature is the person's).
func (s *onStage) approvals(ctx context.Context, since time.Time) (reconcile.Result, error) {
	list := &dv1.ApprovalList{}
	if err := s.APIReader.List(ctx, list, client.InNamespace(s.op.Namespace)); err != nil {
		return s.backoff(ctx, s.op, err)
	}
	for i := range list.Items {
		a := &list.Items[i]
		// ponytail: creation times are whole seconds, so an Approval created
		// in the second the guard was first seen waits for a new one.
		if a.Spec.OperationUID != string(s.op.UID) || a.Status.Record != nil || a.Spec.Record != "" || a.CreationTimestamp.Time.Before(since) {
			continue
		}
		if err := s.approval(ctx, a); err != nil {
			return s.backoff(ctx, s.op, err)
		}
	}
	return reconcile.Result{}, nil
}

func (s *onStage) approval(ctx context.Context, a *dv1.Approval) error {
	signer := delivery.ApprovalSigner{Kind: delivery.SignerRecorder,
		OnBehalfOf: &delivery.ApprovalBehalf{User: a.Spec.By, Verified: delivery.VerifiedByAdmission}}
	st := delivery.NewApprovalRecord(s.recordStage(), s.anchor.desc.Digest.String(), s.object(), a.Spec, signer,
		a.CreationTimestamp.UTC().Format(time.RFC3339))
	bundle, id, err := delivery.SignApproval(ctx, s.Signer, st)
	if err != nil {
		return err
	}
	ref, err := s.Store.Push(ctx, s.anchor, delivery.PredicateApproval, bundle, string(s.op.UID))
	if err != nil {
		return err
	}
	a.Status.Record = &delivery.ApprovalRecordRef{Digest: id, Ref: ref}
	return s.Status().Update(ctx, a)
}
