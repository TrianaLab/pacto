/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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
	// Workers is how many reconciles run at once (0 is one), so one slow
	// health provider does not hold up every other Stage.
	Workers int
	// ClusterID is the kube-system namespace uid, the cluster half of a stream id.
	ClusterID string
	// Gates are the gates a Stage may name (NewGates).
	Gates map[string]delivery.Gate
	Now   func() time.Time
	Poll  time.Duration
	// AdmissionPolicies are the policies the operation reconciler waits on
	// (OperationReconciler.AdmissionPolicies); while they do not guard, the
	// Stage is not Ready.
	AdmissionPolicies []string
	// Freshness is the operation reconciler's login freshness, published on
	// every Stage so clients ask for a login it will still count; zero means
	// delivery.AuthFreshness.
	Freshness time.Duration
	// Events receives the Events it emits when a Stage freezes, stops being
	// watched or raises a rollback; nil emits none.
	Events record.EventRecorder
	// seen is what each Stage last showed (frozen, unwatched), so an Event
	// marks a change, not every pass. Nil emits none.
	// ponytail: in memory, so a restart says each once more.
	seen *sync.Map

	mu          sync.Mutex
	reads       map[string]upstreamRead // remote upstream reads by Stage and stream
	shadowTried map[string]time.Time    // last shadow verify by Stage and digest
}

// ConditionWatched is True while the newest re-check of the running version
// passed. False says nothing watches it for Pacto: the re-check failed, or it
// came back Unknown (a health provider down, too many spans, no coverage).
const ConditionWatched = "Watched"

// watched is the Watched condition check sets.
func watched(check *delivery.Recheck, gen int64) delivery.Condition {
	c := delivery.Condition{Type: ConditionWatched, Status: statusTrue, Reason: "Rechecked",
		Message: "the running version passed its re-check at " + check.At, ObservedGeneration: gen}
	var why []string
	for _, v := range check.Verdicts {
		if v.Result != delivery.Pass {
			why = append(why, v.Gate+": "+v.Message)
		}
	}
	switch delivery.Worst(check.Verdicts) {
	case delivery.Fail:
		c.Status, c.Reason, c.Message = statusFalse, "RecheckFailed", "the running version failed its re-check: "+strings.Join(why, "; ")
	case delivery.Unknown:
		c.Status, c.Reason, c.Message = statusFalse, "RecheckUnknown", "nothing watches the running version: its re-check is Unknown: "+strings.Join(why, "; ")
	}
	return c
}

// observe exports what a Stage shows as metrics, and emits an Event when it
// freezes or stops being watched.
func (r *StageReconciler) observe(stage *dv1.Stage) {
	type shown struct{ frozen, unwatched bool }
	now := shown{frozen: stage.Status.Frozen}
	StageFrozen.WithLabelValues(stage.Namespace, stage.Name).Set(b2f(now.frozen))
	var w *delivery.Condition
	if i := slices.IndexFunc(stage.Status.Conditions, func(c delivery.Condition) bool { return c.Type == ConditionWatched }); i >= 0 {
		w = &stage.Status.Conditions[i]
		now.unwatched = w.Status != statusTrue
		StageWatched.WithLabelValues(stage.Namespace, stage.Name).Set(b2f(!now.unwatched))
	}
	if r.seen == nil {
		return
	}
	prev, _ := r.seen.Swap(stage.Namespace+"/"+stage.Name, now)
	was, _ := prev.(shown)
	if now.frozen && !was.frozen {
		event(r.Events, stage, true, "Frozen", stage.Status.FrozenReason)
	}
	if now.unwatched && !was.unwatched {
		event(r.Events, stage, true, w.Reason, w.Message)
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// upstreamRead is one read of a remote upstream, kept for UpstreamPoll.
type upstreamRead struct {
	at  time.Time
	up  *delivery.StageStatus
	why string
}

// UpstreamPoll is how long the stage reconciler reuses a read of an upstream
// in another cluster before it reads the registry again. The Stage itself is
// polled every few seconds; a registry is not, so a follower lags its
// upstream by up to this long. A promote reads the records afresh when it is
// planned, so it never relies on a read this old.
const UpstreamPoll = 30 * time.Second

// Reconcile implements reconcile.Reconciler.
func (r *StageReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	stage := &dv1.Stage{}
	if err := r.Get(ctx, req.NamespacedName, stage); err != nil {
		if apierrors.IsNotFound(err) {
			StageFrozen.DeleteLabelValues(req.Namespace, req.Name)
			StageWatched.DeleteLabelValues(req.Namespace, req.Name)
		}
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	r.observe(stage)
	if stage.Status.Rung == "" {
		// The one rung write not done through an operation: startRung is
		// observe or advise, so a Stage whose status was lost seeds back to a
		// rung on which Pacto ships nothing without a person.
		return reconcile.Result{RequeueAfter: immediately}, r.updateStatus(ctx, stage, func(s *dv1.Stage) {
			if s.Status.Rung == "" {
				s.Status.Rung = delivery.CurrentRung(s.Spec, s.Status)
			}
		})
	}
	if stage.Status.Stream.ID == "" {
		return reconcile.Result{RequeueAfter: immediately}, r.updateStatus(ctx, stage, func(s *dv1.Stage) {
			s.Status.Stream.ID = r.ClusterID + "/" + s.Namespace + "/" + s.Name
		})
	}
	if err := r.streamConflict(ctx, stage); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.witness(ctx, stage); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.recheck(ctx, stage); err != nil {
		return reconcile.Result{}, err
	}
	cand, up, err := r.ready(ctx, stage)
	if err != nil {
		return reconcile.Result{}, err
	}
	if err := r.recordPublished(ctx, stage, cand); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.showCandidate(ctx, stage, up); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.shadow(ctx, stage); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.propose(ctx, stage); err != nil {
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

// streamConflict freezes a Stage whose records the recorder refuses to
// extend: a stream that forked or broke, or lost the record the Stage names
// as its head. A person unfreezes it once the stream is repaired; while the
// conflict lasts, it freezes again.
func (r *StageReconciler) streamConflict(ctx context.Context, stage *dv1.Stage) error {
	i := slices.IndexFunc(stage.Status.Conditions, func(c delivery.Condition) bool {
		return c.Type == ConditionRecordStoreReady && c.Status == statusFalse && c.Reason == ReasonStreamConflict
	})
	if i < 0 || stage.Status.Frozen {
		return nil
	}
	why := ReasonStreamConflict + ": the recorder will not extend this Stage's records (" + stage.Status.Conditions[i].Message +
		"); repair the stream, then unfreeze"
	return r.updateStatus(ctx, stage, func(s *dv1.Stage) { s.Status.Frozen, s.Status.FrozenReason = true, why })
}

// witness raises a witnessed override when the applier's digest differs
// from the pointer while no operation holds the pointer lock: someone with
// break-glass rights edited it directly. The edit is never undone; it is
// recorded, adopted and the Stage freezes until a person unfreezes it.
func (r *StageReconciler) witness(ctx context.Context, stage *dv1.Stage) error {
	if stage.Status.Locks[delivery.FacetPointer] != "" || stage.Status.Pointer == "" {
		return nil
	}
	a, err := r.Cluster.applier(ctx, stage.Namespace, stage.Spec.ApplyRef)
	switch {
	case apierrors.IsNotFound(err) || apierrors.IsForbidden(err) || meta.IsNoMatchError(err):
		return nil // ready names it (applierReady)
	case err != nil:
		return err
	}
	if delivery.CurrentRung(stage.Spec, stage.Status) == delivery.RungObserve && !labelled(a.obj) {
		return nil // observing an unguarded applyRef: GitOps moves it
	}
	if _, err := delivery.ParseDigest(a.digest); err != nil || a.digest == stage.Status.Pointer {
		return nil
	}
	spec := delivery.OperationSpec{
		Type: delivery.TypeOverride, Stage: stage.Name, Facet: delivery.FacetPointer,
		Params:    delivery.Params{From: stage.Status.Pointer, To: a.digest},
		Reason:    "witnessed: " + a.field() + " was edited outside Pacto",
		Actor:     delivery.Actor{User: r.Policy.ControllerUser},
		Cause:     &delivery.Cause{Reason: "pointer edited outside Pacto (editor: unknown)"},
		Witnessed: true,
	}
	return r.raise(ctx, stage, "witnessed", spec)
}

// autoPromote raises a promote of the newest eligible candidate on an
// automatic rung, unless the Stage is frozen, on auto-hold or already has an
// operation in flight.
func (r *StageReconciler) autoPromote(ctx context.Context, stage *dv1.Stage) error {
	if !delivery.CurrentRung(stage.Spec, stage.Status).Automatic() || stage.Status.Frozen || stage.Status.AutoHold {
		return nil
	}
	ops, err := stageOps(ctx, r.Client, stage.Namespace, stage.Name)
	if err != nil {
		return err
	}
	for _, o := range ops {
		if o.Status.Phase != delivery.PhaseConcluded {
			return nil
		}
	}
	cand := stage.Status.Candidate
	if cand == "" {
		return nil
	}
	spec := delivery.OperationSpec{
		Type: delivery.TypePromote, Stage: stage.Name, Facet: delivery.FacetPointer,
		Params: delivery.Params{From: stage.Status.Pointer, To: cand},
		Reason: "automatic promote of the newest eligible candidate",
		Actor:  delivery.Actor{User: r.Policy.ControllerUser},
	}
	return r.raise(ctx, stage, "auto", spec)
}

// ready sets the Ready condition and returns the candidate OCIRepository, if
// the Stage names one that exists, and what an upstream in another cluster
// offers. Ready is False, naming what is missing, when the Stage cannot
// promote: nothing is published without a candidate OCIRepository whose
// spec.verify names the publisher (design M2), nothing is offered by records
// that cannot be read, nothing is applied through an applier the promoter
// cannot write and nothing is admitted while admission does not guard. It
// also remembers the newest upstream record read, which the next read must
// extend.
func (r *StageReconciler) ready(ctx context.Context, stage *dv1.Stage) (*applyRef, *delivery.StageStatus, error) {
	cand, up, want, err := r.candidateReady(ctx, stage)
	if err != nil {
		return nil, nil, err
	}
	blocked, a, err := r.applierReady(ctx, stage)
	if err != nil {
		return nil, nil, err
	}
	conds := []delivery.Condition{}
	if a != nil {
		med, err := r.Cluster.mediated(ctx, stage.Namespace, a)
		if err != nil {
			return nil, nil, err
		}
		conds = append(conds, med)
	}
	observing := delivery.CurrentRung(stage.Spec, stage.Status) == delivery.RungObserve
	unlabelled := blocked != nil && blocked.Reason == reasonApplierUnlabelled
	if blocked != nil && (!observing || !unlabelled) {
		want = *blocked
	}
	if want.Status == statusTrue {
		why, err := unguardedBy(ctx, r.Client, r.AdmissionPolicies)
		if err != nil {
			return nil, nil, err
		}
		if why != "" {
			want = notReady(ReasonUnguarded, why+", so the promoter admits no operation and counts no approval")
		}
	}
	if observing && want.Status == statusTrue {
		want.Reason, want.Message = delivery.ReasonObservingGuarded, "the Stage observes, and the applyRef carries "+ApplyRefLabel+
			", so GitOps cannot write the pointer until a person removes the label or the Stage leaves observe"
		if unlabelled {
			want.Reason, want.Message = delivery.ReasonObserving, "the Stage observes: GitOps owns the applyRef and Pacto plans what it would do"
		}
	}
	conds = append(conds, want)
	changed, cur := false, stage.Status.Conditions
	for i := range conds {
		conds[i].ObservedGeneration = stage.Generation
		var c bool
		cur, c = delivery.SetCondition(cur, conds[i], r.Now())
		changed = changed || c
	}
	fresh := cmp.Or(r.Freshness, delivery.AuthFreshness).String()
	seen := up != nil && up.Stream.ID != "" && (up.Stream.ID != stage.Status.UpstreamSeen.Stream || up.Stream.Seq > stage.Status.UpstreamSeen.Seq)
	if !changed && !seen && stage.Status.ObservedGeneration == stage.Generation && stage.Status.AuthFreshness == fresh {
		return cand, up, nil
	}
	set := func(s *dv1.Stage) {
		for _, c := range conds {
			s.Status.Conditions, _ = delivery.SetCondition(s.Status.Conditions, c, r.Now())
		}
		s.Status.ObservedGeneration, s.Status.AuthFreshness = stage.Generation, fresh
		if seen {
			s.Status.UpstreamSeen = delivery.RecordMark{Stream: up.Stream.ID, Seq: up.Stream.Seq, Record: up.Stream.Head}
		}
	}
	if err := r.updateStatus(ctx, stage, set); err != nil {
		return nil, nil, err
	}
	set(stage)
	return cand, up, nil
}

// candidateReady reads the Stage's candidate source and says whether it can
// offer anything: the candidate OCIRepository, what the upstream in another
// cluster offers, and the Ready condition.
func (r *StageReconciler) candidateReady(ctx context.Context, stage *dv1.Stage) (*applyRef, *delivery.StageStatus, delivery.Condition, error) {
	want := delivery.Condition{Type: conditionReady, Status: statusTrue, Reason: conditionReady, Message: "the Stage can promote published versions"}
	c := stage.Spec.Candidates
	switch {
	case c.OCIRepositoryRef != nil:
		a, err := r.Cluster.ociRepository(ctx, stage.Namespace, c.OCIRepositoryRef.Name)
		switch {
		case apierrors.IsNotFound(err):
			want = notReady("CandidateNotFound", "the candidate OCIRepository "+c.OCIRepositoryRef.Name+" does not exist")
		case err != nil:
			return nil, nil, want, err
		case !pinsIdentity(a.obj.Object):
			want = notReady("CandidateUnverified", "the candidate OCIRepository "+c.OCIRepositoryRef.Name+
				" sets no spec.verify with a secretRef or matchOIDCIdentity naming the publisher, so no version is published and the Stage never promotes")
		}
		return a, nil, want, nil
	case c.UpstreamRecords != nil && r.Cluster.Upstream == nil:
		return nil, nil, notReady("UpstreamUntrusted", noUpstreamTrust+", so the Stage never promotes"), nil
	case c.UpstreamRecords != nil:
		up, why := r.remote(ctx, stage)
		if up == nil {
			want = notReady("UpstreamUnreadable", "the records of the upstream Stage "+c.UpstreamRecords.Stream+" in "+c.UpstreamRecords.Repository+
				" cannot be read or verified ("+why+"), so the Stage promotes nothing until they can")
		}
		return nil, up, want, nil
	case c.UpstreamStage == nil:
		want = notReady("NoCandidateSource", "the Stage names no candidates.ociRepositoryRef, candidates.upstreamStage or candidates.upstreamRecords, so it never promotes")
	}
	return nil, nil, want, nil
}

// remote reads the Stage's upstream in another cluster, reusing a read
// younger than UpstreamPoll.
func (r *StageReconciler) remote(ctx context.Context, stage *dv1.Stage) (*delivery.StageStatus, string) {
	rs, now := stage.Spec.Candidates.UpstreamRecords, r.Now()
	key := stage.Namespace + "/" + stage.Name + "|" + rs.Repository + "|" + rs.Stream
	r.mu.Lock()
	last, ok := r.reads[key]
	r.mu.Unlock()
	if ok && now.Sub(last.at) < UpstreamPoll {
		return last.up, last.why
	}
	up, why := r.Cluster.remote(ctx, stage)
	r.mu.Lock()
	if r.reads == nil {
		r.reads = map[string]upstreamRead{}
	}
	r.reads[key] = upstreamRead{at: now, up: up, why: why}
	r.mu.Unlock()
	return up, why
}

// applierReady says what keeps the Stage's applier from taking a version, nil
// when nothing does: it cannot be read (missing, forbidden or a kind whose
// CRD the cluster lacks), it lacks ApplyRefLabel (the admission
// policy then refuses every promoter write) or, on Argo CD, it has no single
// OCI spec.source or no automated sync (the promoter never runs a sync).
func (r *StageReconciler) applierReady(ctx context.Context, stage *dv1.Stage) (*delivery.Condition, *applyRef, error) {
	ref := stage.Spec.ApplyRef
	kind := ref.Kind
	if kind == "" {
		kind = delivery.ApplierFlux
	}
	what := "the " + kind + " " + ref.Name
	a, err := r.Cluster.applier(ctx, stage.Namespace, ref)
	switch {
	case apierrors.IsNotFound(err) || apierrors.IsForbidden(err):
		c := notReady("ApplierUnreadable", what+" the Stage applies through cannot be read: "+err.Error())
		return &c, nil, nil
	case meta.IsNoMatchError(err):
		c := notReady("ApplierUnreadable", what+" the Stage applies through cannot be read: its CRD is not installed in this cluster ("+err.Error()+")")
		return &c, nil, nil
	case err != nil:
		return nil, nil, err
	}
	var c delivery.Condition
	_, multi, _ := unstructured.NestedFieldNoCopy(a.obj.Object, "spec", "sources")
	url, _, _ := unstructured.NestedString(a.obj.Object, "spec", "source", "repoURL")
	_, automated, _ := unstructured.NestedFieldNoCopy(a.obj.Object, "spec", "syncPolicy", "automated")
	switch {
	case !labelled(a.obj):
		c = notReady(reasonApplierUnlabelled, what+" has no "+ApplyRefLabel+" label, so admission refuses every version the promoter writes to it")
	case !a.argo:
		return nil, a, nil
	case multi:
		c = notReady("ApplierMultiSource", what+" sets spec.sources; the promoter writes only the one OCI spec.source")
	case !strings.HasPrefix(url, "oci://"):
		c = notReady("ApplierNotOCI", what+" has no OCI spec.source.repoURL (oci://...), so it cannot fetch a version by digest")
	case !automated:
		c = notReady("ApplierNotAutomated", what+" sets no spec.syncPolicy.automated; the promoter never runs a sync, so a version it writes is never applied")
	default:
		return nil, a, nil
	}
	return &c, a, nil
}

// reasonApplierUnlabelled is the Ready reason of an applyRef without
// ApplyRefLabel, which is no fault while the Stage observes.
const reasonApplierUnlabelled = "ApplierUnlabelled"

func labelled(o *unstructured.Unstructured) bool {
	_, ok := o.GetLabels()[ApplyRefLabel]
	return ok
}

func notReady(reason, msg string) delivery.Condition {
	return delivery.Condition{Type: conditionReady, Status: statusFalse, Reason: reason, Message: msg}
}

// recordPublished remembers the candidate OCIRepository's current digest once
// Flux verified its signature: a version pacto publish published. The
// pacto.published gate admits only remembered digests, so a version Flux
// verified earlier can still be proposed after the tag moved on.
func (r *StageReconciler) recordPublished(ctx context.Context, stage *dv1.Stage, a *applyRef) error {
	if a == nil {
		return nil
	}
	d := delivery.RevisionDigest(a.revision)
	if !a.verified || slices.Contains(stage.Status.Published, d) {
		return nil
	}
	if err := r.updateStatus(ctx, stage, func(s *dv1.Stage) {
		s.Status.Published = delivery.AddDigest(s.Status.Published, d, delivery.MaxPublished)
	}); err != nil {
		return err
	}
	stage.Status.Published = delivery.AddDigest(stage.Status.Published, d, delivery.MaxPublished)
	return nil
}

// showCandidate writes the newest eligible candidate to status.candidate, so
// the CLI and the dashboard can offer to promote it on a Stage without auto.
// remote is what the upstream in another cluster offers, nil when nothing.
func (r *StageReconciler) showCandidate(ctx context.Context, stage *dv1.Stage, remote *delivery.StageStatus) error {
	cand, err := r.candidate(ctx, stage, remote)
	if err != nil || cand == stage.Status.Candidate {
		return err
	}
	if err := r.updateStatus(ctx, stage, func(s *dv1.Stage) { s.Status.Candidate = cand }); err != nil {
		return err
	}
	stage.Status.Candidate = cand
	return nil
}

func (r *StageReconciler) candidate(ctx context.Context, stage *dv1.Stage, remote *delivery.StageStatus) (string, error) {
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
	case remote != nil:
		return delivery.NewestCandidate(stage.Status, "", remote), nil
	}
	return "", nil // nothing it cannot verify is ever a candidate
}

// raise creates a controller-class operation. Its name is derived from the
// Stage's stream seq and the pointer move, so raising the same decision twice
// is a no-op, while the same move after an earlier one concluded (expired,
// cancelled or rejected) gets a new name and is proposed again.
func (r *StageReconciler) raise(ctx context.Context, stage *dv1.Stage, kind string, spec delivery.OperationSpec) error {
	op := &dv1.Operation{}
	op.Namespace, op.Name = stage.Namespace, raisedName(stage, kind, spec.Params)
	op.Labels = map[string]string{StageLabel: stage.Name}
	controllerutil.AddFinalizer(op, dv1.RecordFinalizer)
	spec.IntentDigest = delivery.IntentDigest(spec, stage.Spec)
	op.Spec = spec
	squat, err := createRaised(ctx, r.Client, op)
	if err != nil {
		return err
	}
	return r.freeze(ctx, stage, squat)
}

func raisedName(stage *dv1.Stage, kind string, p delivery.Params) string {
	sum := sha256.Sum256([]byte(strconv.FormatInt(stage.Status.Stream.Seq, 10) + ":" + p.From + ">" + p.To))
	return stage.Name + "-" + kind + "-" + hex.EncodeToString(sum[:])[:10]
}

// freeze freezes the Stage for why, unless why is empty or it is frozen.
func (r *StageReconciler) freeze(ctx context.Context, stage *dv1.Stage, why string) error {
	if why == "" {
		return nil
	}
	return r.updateStatus(ctx, stage, func(s *dv1.Stage) {
		if !s.Status.Frozen {
			s.Status.Frozen, s.Status.FrozenReason = true, why
		}
	})
}

// recheck runs the verify gates again on the version the Stage runs, every
// recheck interval, once the operation that shipped it succeeded. Checks keep
// running after release: a confirmed Fail marks the version failed and raises
// its one bound rollback, or freezes the Stage when that cause already had its
// rollback, there is nothing to roll back to, a person or agent chose the
// version (a rollback, or a promote on auto-hold) or it is the promoter's own
// rollback: automation never undoes a choice or rolls back a rollback.
// Unknown never rolls back. A verdict about a Stage that moved on while the
// gates ran is dropped.
func (r *StageReconciler) recheck(ctx context.Context, stage *dv1.Stage) error {
	s, now := stage.Status, r.Now().UTC()
	failed := slices.Contains(s.FailedDigests, s.Pointer)
	if s.Frozen || s.LastOp == "" || s.Locks[delivery.FacetPointer] != "" ||
		!failed && s.LastCheck != nil && s.LastCheck.Operation == s.LastOp && now.Before(parseOr(s.LastCheck.At).Add(delivery.RecheckInterval(stage.Spec))) {
		return nil
	}
	ops, err := stageOps(ctx, r.Client, stage.Namespace, stage.Name)
	if err != nil {
		return err
	}
	var shipped *dv1.Operation
	for _, o := range ops {
		if o.Status.Phase != delivery.PhaseConcluded {
			return nil // something is in flight; it owns the Stage
		}
		if string(o.UID) == s.LastOp {
			shipped = &o
		}
	}
	if failed {
		return r.reraise(ctx, stage, shipped, ops)
	}
	if shipped == nil {
		gone := &delivery.Recheck{Operation: s.LastOp, Digest: s.Pointer, At: ts(now), Verdicts: []delivery.Verdict{{
			Gate: "recheck", Phase: "verify", Result: delivery.Unknown,
			Message: "the operation that shipped the running version is gone, so nothing is re-checked",
		}}}
		return r.updateStatus(ctx, stage, func(st *dv1.Stage) {
			if !moved(gone, st.Status) {
				st.Status.LastCheck = gone
				st.Status.Conditions, _ = delivery.SetCondition(st.Status.Conditions, watched(gone, st.Generation), now)
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
	countVerdicts(vs)
	check := &delivery.Recheck{Operation: s.LastOp, Digest: s.Pointer, At: ts(now), Verdicts: vs}
	f := failing(vs)
	if f == nil {
		return r.updateStatus(ctx, stage, func(st *dv1.Stage) {
			if !moved(check, st.Status) {
				st.Status.LastCheck = check
				st.Status.Conditions, _ = delivery.SetCondition(st.Status.Conditions, watched(check, st.Generation), now)
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
	rollback := !chosen && !slices.Contains(fresh.Status.RolledBack, cause) && before != "" && !slices.Contains(fresh.Status.FailedDigests, before)
	// The failure is recorded before the rollback is raised: raising takes the
	// pointer lock, after which the Stage reads as moved and the record would be
	// dropped. A rollback lost after the record is raised again by reraise.
	// A freeze is raised first instead: it takes no lock, and once the Stage is
	// frozen no re-check runs again to raise it, so a freeze lost here would
	// leave the failure out of the records.
	// The step-down is raised before the failure is recorded, for the same
	// reason as the freeze: once the Stage is frozen or rolled back, no
	// re-check runs again to raise it.
	stepSquat, err := raiseStepDown(ctx, r.Client, r.Policy, shipped, fresh, why, f)
	if err != nil {
		return err
	}
	if !rollback {
		squat, err := raiseFreeze(ctx, r.Client, r.Policy, shipped, stage, why, f)
		if err != nil {
			return err
		}
		if squat != "" {
			why += " (and " + squat + ")"
		}
	}
	wrote := false
	if err := r.updateStatus(ctx, stage, func(st *dv1.Stage) {
		if wrote = !moved(check, st.Status); !wrote {
			return
		}
		st.Status.LastCheck = check
		st.Status.Conditions, _ = delivery.SetCondition(st.Status.Conditions, watched(check, st.Generation), r.Now())
		st.Status.FailedDigests = delivery.AddDigest(st.Status.FailedDigests, check.Digest, delivery.MaxFailedDigests)
		st.Status.Verified = delivery.RemoveDigest(st.Status.Verified, check.Digest)
		switch {
		case shipped.Spec.Type == delivery.TypeRollback && shipped.Status.ActorClass == delivery.ClassController:
			st.Status.Frozen, st.Status.FrozenReason = true, shipped.Name+": failed after release, and it is the promoter's own rollback, which is never undone automatically, so a person must decide: "+why
		case chosen:
			st.Status.Frozen, st.Status.FrozenReason = true, shipped.Name+": failed after release, and a person or agent chose this version, so a person must decide: "+why
		case !rollback:
			st.Status.Frozen, st.Status.FrozenReason = true, shipped.Name+": failed after release and cannot be rolled back automatically: "+why
		default:
			st.Status.RolledBack = delivery.AddDigest(st.Status.RolledBack, cause, delivery.MaxRolledBack)
		}
		if stepSquat != "" {
			// The rung stays where the failure happened: a person decides.
			st.Status.Frozen, st.Status.FrozenReason = true, shipped.Name+": failed after release: "+why+", and "+stepSquat
		}
	}); err != nil || !wrote || !rollback {
		return err
	}
	return r.raiseRollback(ctx, stage, shipped, why, f)
}

// raiseRollback raises shipped's one bound rollback, or freezes the Stage when
// someone else took its name.
func (r *StageReconciler) raiseRollback(ctx context.Context, stage *dv1.Stage, shipped *dv1.Operation, why string, f *delivery.Verdict) error {
	squat, err := raiseRollback(ctx, r.Client, r.Policy, shipped, stage, why, f)
	if err != nil {
		return err
	}
	if squat == "" {
		event(r.Events, stage, true, "RollbackRaised", shipped.Name+" "+why+", so it is rolled back to "+shipped.Status.Pointer.Before)
		return nil
	}
	return r.freeze(ctx, stage, shipped.Name+": failed after release: "+why+", and "+squat)
}

// reraise raises again the rollback a failed re-check recorded but never
// created, because raising it failed after the record was written. It acts only
// when the recorded check is about the running version, its cause has its one
// rollback counted and the promoter raised no rollback for that cause yet.
// ponytail: a person deleting a concluded rollback gets it raised again; keep
// a raised marker in status if that ever matters.
func (r *StageReconciler) reraise(ctx context.Context, stage *dv1.Stage, shipped *dv1.Operation, ops []dv1.Operation) error {
	s, lc := stage.Status, stage.Status.LastCheck
	if shipped == nil || !slices.Contains(s.RolledBack, s.LastOp) || lc == nil || lc.Operation != s.LastOp || lc.Digest != s.Pointer {
		return nil
	}
	f := failing(lc.Verdicts)
	if f == nil || slices.ContainsFunc(ops, func(o dv1.Operation) bool {
		return o.Name == shipped.Name+"-rollback" && o.Spec.Type == delivery.TypeRollback &&
			o.Spec.Actor.User == r.Policy.ControllerUser && causeOp(&o) == string(shipped.UID)
	}) {
		return nil
	}
	// One someone else created under that name freezes the Stage instead.
	return r.raiseRollback(ctx, stage, shipped, "RecheckFailed: "+f.Gate+": "+f.Message, f)
}

// parseOr parses an RFC 3339 time, zero when it does not parse.
func parseOr(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
