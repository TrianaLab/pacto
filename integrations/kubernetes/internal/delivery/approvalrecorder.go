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

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
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
	// logged are the refusals already logged, by manifest digest, so a pass
	// every Poll logs each once. Records that do not verify are rejected in
	// reader instead, and never read again.
	// ponytail: emptied once it holds maxLogged keys, so a refusal may be
	// logged again after that.
	logged map[string]bool
	// reader remembers what each approvals repository manifest names.
	reader deliveryrecord.Reader
}

// Bounds of the keyless approval scan: the approval records of one
// operation read per pass, and the views created per operation.
var (
	MaxApprovalScan  = 200
	MaxApprovalViews = 20
)

// UnverifiedApprovalRecords counts the approval records found in an
// approvals repository that do not verify, each once: they are never viewed.
var UnverifiedApprovalRecords = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "pacto_delivery_unverified_approval_records",
	Help: "Approval records in an approvals repository that do not verify, so they are never viewed.",
})

func init() { metrics.Registry.MustRegister(UnverifiedApprovalRecords) }

// maxLogged bounds logged.
const maxLogged = 4096

// once reports whether key is seen for the first time.
func (r *ApprovalRecorder) once(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logged[key] {
		return false
	}
	if r.logged == nil || len(r.logged) >= maxLogged {
		r.logged = map[string]bool{}
	}
	r.logged[key] = true
	return true
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
	if op.Status.Phase != delivery.PhaseHeld {
		return reconcile.Result{}, nil
	}
	// A person's client pushes a signed approval to the registry, which
	// wakes nothing here: a held operation is looked at again every Poll.
	// A failing approvals repository never stops the on-behalf signing of
	// the operation's cluster Approvals: the scan is logged and tried again.
	scan := len(r.Policy.Signers) > 0 && stage.Status.Stream.Approvals != ""
	if scan {
		if err := s.scanSigned(ctx); err != nil {
			ctrl.LoggerFrom(ctx).Info("not viewing signed approvals", "operation", op.Name, "error", err.Error())
		}
	}
	if since.IsZero() { // unguarded: nothing is signed on behalf, and the guard is looked at again
		return reconcile.Result{RequeueAfter: r.Poll}, nil
	}
	res, err := s.approvals(ctx, since)
	if scan {
		res.RequeueAfter = r.Poll
	}
	return res, err
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

// scanSigned views the approval records people signed keyless for the held
// operation, found in the Stage's approvals repository: each that verifies
// under an approver signer and that the promoter would count becomes an
// Approval naming it (spec.record) and its verified signing time
// (spec.signedAt), owned by the operation. A record that does not verify,
// or whose signer the promoter would refuse, gets no view.
func (s *onStage) scanSigned(ctx context.Context) error {
	stream := s.stage.Status.Stream
	path, _, err := deliveryrecord.ParseAnchorRef(stream.Approvals, stream.ID)
	if err != nil {
		return err
	}
	repo, err := s.Store.open(path)
	if err != nil {
		return err
	}
	fetched, cut, err := s.reader.ListPredicate(ctx, repo, stream.ID, delivery.PredicateApproval, string(s.op.UID), MaxApprovalScan)
	if err != nil {
		return err
	}
	if cut {
		ctrl.LoggerFrom(ctx).Info("the approvals repository holds more approval records of this operation than one pass reads; the rest wait",
			"operation", s.op.Name, "max", MaxApprovalScan)
	}
	viewed, err := s.viewed(ctx)
	if err != nil {
		return err
	}
	for _, f := range fetched {
		if ref := path + "@" + f.Manifest; !viewed[ref] {
			if stop, err := s.viewOne(ctx, f, ref, viewed); stop || err != nil {
				return err
			}
		}
	}
	return nil
}

// viewed are the records the operation's views already name.
func (s *onStage) viewed(ctx context.Context) (map[string]bool, error) {
	list := &dv1.ApprovalList{}
	if err := s.APIReader.List(ctx, list, client.InNamespace(s.op.Namespace)); err != nil {
		return nil, err
	}
	viewed := map[string]bool{}
	for _, a := range list.Items {
		if a.Spec.OperationUID == string(s.op.UID) && a.Spec.Record != "" {
			viewed[a.Spec.Record] = true
		}
	}
	return viewed, nil
}

// viewOne creates the view of the record f at ref when it verifies, answers
// the operation and would count, and adds it to viewed. stop says the
// operation has as many views as the recorder creates.
func (s *onStage) viewOne(ctx context.Context, f deliveryrecord.Fetched, ref string, viewed map[string]bool) (stop bool, err error) {
	log := ctrl.LoggerFrom(ctx)
	signed, err := openSigned(f.Bundle, s.Policy)
	switch {
	case err != nil:
		// Rejected, it never takes the scan's bound again.
		s.reader.Reject(s.stage.Status.Stream.ID, delivery.PredicateApproval, f.Manifest)
		UnverifiedApprovalRecords.Inc()
		log.Info("an approval record does not verify, so it is never viewed", "record", ref, "error", err.Error())
		return false, nil
	case signed.st.Predicate.Operation.UID != string(s.op.UID):
		s.reader.Reject(s.stage.Status.Stream.ID, delivery.PredicateApproval, f.Manifest)
		return false, nil
	case len(viewed) >= MaxApprovalViews:
		if s.once("views/" + string(s.op.UID)) {
			log.Info("the operation has as many views of signed approvals as the recorder creates; the rest wait",
				"operation", s.op.Name, "max", MaxApprovalViews)
		}
		return true, nil
	}
	view := s.view(signed, ref)
	if why := s.refusal(ctx, signed, view.Spec); why != "" {
		if s.once(f.Manifest) {
			log.Info("a signed approval would not count, so it is not viewed", "record", ref, "reason", why)
		}
		return false, nil
	}
	if err := s.Create(ctx, view); err != nil {
		return false, err
	}
	view.Status.Record = &delivery.ApprovalRecordRef{Digest: signed.id, Ref: ref}
	viewed[ref] = true
	return false, s.Status().Update(ctx, view)
}

// view is the Approval that stands for signed, the record at ref.
func (s *onStage) view(signed signedApproval, ref string) *dv1.Approval {
	p, op := signed.st.Predicate, s.op
	// A generated name: a predictable one could be taken first by anyone who
	// may create Approvals, and the apiserver shortens a long operation name.
	return &dv1.Approval{
		ObjectMeta: metav1.ObjectMeta{Namespace: op.Namespace, GenerateName: op.Name + "-signed-",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: dv1.GroupVersion.String(), Kind: "Operation", Name: op.Name, UID: op.UID}}},
		Spec: delivery.ApprovalSpec{Operation: op.Name, OperationUID: string(op.UID), IntentDigest: p.Operation.IntentDigest,
			Decision: p.Decision, Note: p.Note, By: signed.identity.User, Record: ref, SignedAt: ts(signed.at)},
	}
}

// refusal is why the promoter would not count a view of signed: the same
// proposal and keyless guards and approver checks, with the workload review asked as the
// identity the certificate names.
func (s *onStage) refusal(ctx context.Context, signed signedApproval, a delivery.ApprovalSpec) string {
	if why := signed.proposal(); why != "" {
		return why
	}
	if code, why := delivery.KeylessRefusal(signed.identity, signed.source); code != "" {
		return why
	}
	_, why, _ := delivery.ApprovalRefusalAsking(s.op.Spec, s.op.Status, string(s.op.UID), a, signed.at, s.Policy,
		delivery.PublishedFreshness(s.stage.Status.AuthFreshness),
		func() delivery.WorkloadAccess {
			access, _ := reviewAccess(ctx, s.Client, s.Now().UTC(), signed.identity, s.stage)
			return access
		})
	return why
}
