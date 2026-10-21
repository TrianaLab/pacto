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
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
)

// ConditionRecordStoreReady is the recorder's Stage condition.
const ConditionRecordStoreReady = "RecordStoreReady"

// Reasons RecordStoreReady is False.
const (
	ReasonReferrersUnsupported = "ReferrersUnsupported"
	ReasonRegistryUnavailable  = "RegistryUnavailable"
	ReasonStreamConflict       = "StreamConflict"
)

// RecordsPending is how many concluded operations of each stream wait for a
// record: the lag behind the promoter.
var RecordsPending = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "pacto_delivery_records_pending",
	Help: "Concluded operations per stream that have no record yet.",
}, []string{"stream"})

func init() { metrics.Registry.MustRegister(RecordsPending) }

// Recorder writes one signed record per concluded operation, per stream in
// seq order, and only then lets the operation go. It is the only holder of the
// signing key and the registry push credentials; it writes nothing in the
// cluster but status.record, status.stream.{recordedSeq,head}, its own Stage
// condition and the record finalizer.
type Recorder struct {
	client.Client
	// APIReader reads operations and Stages uncached: a seq the promoter just
	// assigned must not look like a gap because the cache is behind.
	APIReader client.Reader
	Cluster   Cluster
	Store     *RecordStore
	Signer    delivery.Signer
	// Trust is the digest of the trust config Store verifies with; every
	// record cites it.
	Trust string
	// Events, when set, receives a CDEvent for every record written.
	Events    *Events
	ClusterID string
	Now       func() time.Time
	// Poll is how long a transient failure backs off.
	Poll time.Duration
	// GapGrace is how long a seq may have no operation before it is
	// recorded as a gap. The promoter assigns the seq a moment before it
	// writes the operation.
	GapGrace time.Duration

	mu      sync.Mutex
	missing map[string]time.Time
}

// SetupWithManager registers the recorder. An Operation wakes its Stage.
func (r *Recorder) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("delivery-recorder").For(&dv1.Stage{}).
		Watches(&dv1.Operation{}, handler.EnqueueRequestsFromMapFunc(operationToStage)).
		Complete(r)
}

// item is one local seq to record: an operation, or a gap where none is.
type item struct {
	seq int64
	op  *dv1.Operation
}

// passTimeout bounds one pass over a Stage: a registry or signer that stops
// answering must not hold the worker forever.
const passTimeout = 5 * time.Minute

// Reconcile implements reconcile.Reconciler.
func (r *Recorder) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, passTimeout)
	defer cancel()
	stage := &dv1.Stage{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, stage); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	stream := stage.Status.Stream
	if stream.ID == "" {
		return reconcile.Result{}, nil
	}
	items, err := r.pending(ctx, stage)
	if err != nil {
		return reconcile.Result{}, err
	}
	RecordsPending.WithLabelValues(stream.ID).Set(float64(len(items)))
	if len(items) == 0 && hasCondition(stage, ConditionRecordStoreReady, "True") {
		return reconcile.Result{}, nil
	}
	anchor, err := r.Store.anchor(ctx, stream.ID)
	if err != nil {
		return r.notReady(ctx, stage, storeReason(err), err)
	}
	recs, err := r.Store.list(ctx, anchor)
	if err != nil {
		if errors.Is(err, errUnverifiable) {
			return r.notReady(ctx, stage, ReasonStreamConflict, err)
		}
		return r.notReady(ctx, stage, storeReason(err), err)
	}
	if msg := conflict(recs, stream.Head); msg != "" {
		return r.notReady(ctx, stage, ReasonStreamConflict, errors.New(msg))
	}
	if err := r.ready(ctx, stage); err != nil {
		return reconcile.Result{}, err
	}
	w := &writer{Recorder: r, stage: stage, anchor: anchor, recs: recs}
	for _, it := range items {
		wait, err := w.record(ctx, it)
		if err != nil {
			return r.notReady(ctx, stage, storeReason(err), err)
		}
		if wait > 0 {
			return reconcile.Result{RequeueAfter: wait}, nil
		}
	}
	return reconcile.Result{}, nil
}

// pending lists what this Stage still owes a record, in local seq order: every
// concluded operation that keeps the record finalizer, and every seq after
// recordedSeq that no operation holds.
// ponytail: one uncached list of the namespace's operations per event; add a
// field index on spec.stage if that ever shows on the apiserver.
func (r *Recorder) pending(ctx context.Context, stage *dv1.Stage) ([]item, error) {
	list := &dv1.OperationList{}
	if err := r.APIReader.List(ctx, list, client.InNamespace(stage.Namespace)); err != nil {
		return nil, err
	}
	held := map[int64]bool{}
	var items []item
	for i := range list.Items {
		op := &list.Items[i]
		if op.Spec.Stage != stage.Name || op.Status.Phase != delivery.PhaseConcluded || op.Status.Seq < 1 {
			continue
		}
		held[op.Status.Seq] = true
		if controllerutil.ContainsFinalizer(op, dv1.RecordFinalizer) {
			items = append(items, item{seq: op.Status.Seq, op: op})
		}
	}
	for seq := stage.Status.Stream.RecordedSeq + 1; seq <= stage.Status.Stream.Seq; seq++ {
		if !held[seq] {
			items = append(items, item{seq: seq})
		}
	}
	slices.SortStableFunc(items, func(a, b item) int { return int(a.seq - b.seq) })
	return items, nil
}

// conflict says why the recorder must not extend this stream, or "". Two
// records with one seq or a record whose parent is not its predecessor is a
// fork; a head the Stage names that the registry does not hold means the
// registry lost history the cluster saw. A registry that is ahead of the
// Stage (an etcd restore) is not a conflict: the recorder chains from it.
func conflict(recs []stored, head string) string {
	bySeq := map[int64]stored{}
	for _, r := range recs {
		seq := r.Statement.Predicate.Chain.Seq
		if prev, dup := bySeq[seq]; dup {
			return fmt.Sprintf("records %s and %s both claim seq %d", prev.ID, r.ID, seq)
		}
		bySeq[seq] = r
	}
	ids := map[string]bool{}
	for seq, r := range bySeq {
		ids[r.ID] = true
		if prev, ok := bySeq[seq-1]; ok && r.Statement.Predicate.Chain.Parent != prev.ID {
			return fmt.Sprintf("record %s at seq %d does not chain from %s", r.ID, seq, prev.ID)
		}
	}
	if head != "" && !ids[head] {
		return "the stream head " + head + " the Stage names is not in the registry"
	}
	return ""
}

// storeReason names a registry failure for the condition.
func storeReason(err error) string {
	if errors.Is(err, errUnsupported) {
		return ReasonReferrersUnsupported
	}
	return ReasonRegistryUnavailable
}

// notReady records why the stream is not being written and backs off.
func (r *Recorder) notReady(ctx context.Context, stage *dv1.Stage, reason string, cause error) (reconcile.Result, error) {
	ctrl.LoggerFrom(ctx).Info("not recording", "stage", stage.Name, "reason", reason, "error", cause.Error())
	c := delivery.Condition{Type: ConditionRecordStoreReady, Status: "False", Reason: reason, Message: cause.Error()}
	if err := r.setCondition(ctx, stage, c); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: r.Poll}, nil
}

func hasCondition(stage *dv1.Stage, typ, status string) bool {
	return slices.ContainsFunc(stage.Status.Conditions, func(c delivery.Condition) bool { return c.Type == typ && c.Status == status })
}

// ready marks the stream writable and names where its records are listed.
func (r *Recorder) ready(ctx context.Context, stage *dv1.Stage) error {
	want := delivery.Condition{Type: ConditionRecordStoreReady, Status: "True",
		Reason: "ReferrersSupported", Message: "records are written to " + r.Store.Streams}
	ref := deliveryrecord.AnchorRef(r.Store.Streams, stage.Status.Stream.ID)
	if slices.Contains(stage.Status.Conditions, want) && stage.Status.Stream.Anchor == ref {
		return nil
	}
	return r.casStage(ctx, stage, func(s *dv1.Stage) {
		s.Status.Stream.Anchor = ref
		s.Status.Conditions = append(slices.DeleteFunc(s.Status.Conditions, func(x delivery.Condition) bool { return x.Type == want.Type }), want)
	})
}

// setCondition writes the recorder's condition when it changed.
func (r *Recorder) setCondition(ctx context.Context, stage *dv1.Stage, want delivery.Condition) error {
	if slices.Contains(stage.Status.Conditions, want) {
		return nil
	}
	return r.casStage(ctx, stage, func(s *dv1.Stage) {
		s.Status.Conditions = append(slices.DeleteFunc(s.Status.Conditions, func(x delivery.Condition) bool { return x.Type == want.Type }), want)
	})
}

// casStage applies fn to a fresh Stage and writes its status, retrying on a
// conflict with the promoter.
func (r *Recorder) casStage(ctx context.Context, stage *dv1.Stage, fn func(*dv1.Stage)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &dv1.Stage{}
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(stage), fresh); err != nil {
			return err
		}
		fn(fresh)
		if err := r.Status().Update(ctx, fresh); err != nil {
			return err
		}
		*stage = *fresh
		return nil
	})
}

// writer records one Stage's items against the stream as the registry holds it.
type writer struct {
	*Recorder
	stage  *dv1.Stage
	anchor *place
	recs   []stored
}

// head is the newest record in the stream, or the zero value for a new one.
func (w *writer) head() stored {
	var h stored
	for _, r := range w.recs {
		if r.Statement.Predicate.Chain.Seq > h.Statement.Predicate.Chain.Seq {
			h = r
		}
	}
	return h
}

// find returns the first record whose predicate matches.
func (w *writer) find(match func(delivery.RecordPredicate) bool) *stored {
	for i := range w.recs {
		if match(w.recs[i].Statement.Predicate) {
			return &w.recs[i]
		}
	}
	return nil
}

func (w *writer) recordOf(uid string) string {
	if uid == "" {
		return ""
	}
	if s := w.find(func(p delivery.RecordPredicate) bool { return p.Intent.UID == uid }); s != nil {
		return s.ID
	}
	return ""
}

// record writes or adopts the record of one item. It returns how long to wait
// when a gap is still inside its grace period.
func (w *writer) record(ctx context.Context, it item) (time.Duration, error) {
	if it.op == nil {
		return w.gap(ctx, it.seq)
	}
	op := it.op
	rec := w.find(func(p delivery.RecordPredicate) bool { return p.Intent.UID == string(op.UID) })
	if rec == nil {
		written, err := w.write(ctx, op)
		if err != nil {
			return 0, err
		}
		rec = written
	}
	if err := w.advance(ctx, it.seq); err != nil {
		return 0, err
	}
	return 0, w.release(ctx, op, rec)
}

// write builds, checks, signs and pushes the record of op at the stream head.
func (w *writer) write(ctx context.Context, op *dv1.Operation) (*stored, error) {
	in, err := w.input(ctx, op)
	if err != nil {
		return nil, err
	}
	apply, err := w.Cluster.ociRepository(ctx, w.stage.Namespace, w.stage.Spec.ApplyRef.Name)
	switch {
	case apierrors.IsNotFound(err):
		apply = nil // records still go on the anchor
	case err != nil:
		return nil, err
	}
	l, err := w.Store.locate(ctx, w.anchor, apply, op.Spec.Params.From, op.Spec.Params.To)
	if err != nil {
		return nil, err
	}
	in.Subjects = l.subjects()
	st := delivery.NewOperationRecord(in)
	if check, msg := delivery.CheckRecord(in, st); check != "" {
		st, l = delivery.NewUnrecordable(in, check, msg), locations{anchor: w.anchor}
	}
	rec, err := w.sign(ctx, st, l)
	if err == nil {
		w.Events.emit(ctx, w.stage, st, rec.ID, op.Status.Applied)
	}
	return rec, err
}

func (w *writer) sign(ctx context.Context, st delivery.Statement, l locations) (*stored, error) {
	bundle, id, err := delivery.SignRecord(ctx, w.Signer, st)
	if err != nil {
		return nil, err
	}
	if err := w.Store.write(ctx, bundle, st, l); err != nil {
		return nil, err
	}
	w.recs = append(w.recs, stored{ID: id, Statement: st})
	return &w.recs[len(w.recs)-1], nil
}

// input gathers what the record of op is built from.
func (w *writer) input(ctx context.Context, op *dv1.Operation) (delivery.RecordInput, error) {
	h := w.head()
	in := delivery.RecordInput{
		ClusterID: w.ClusterID,
		Stage: delivery.StageObject{Metadata: delivery.ObjectMeta{Name: w.stage.Name, Namespace: w.stage.Namespace,
			UID: string(w.stage.UID)}, Spec: w.stage.Spec},
		Operation: delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: op.Name, Namespace: op.Namespace,
			UID: string(op.UID)}, Spec: op.Spec, Status: op.Status},
		Approvals: map[string]delivery.ApprovalSpec{},
		Trust:     w.Trust,
		Chain: delivery.Chain{Stream: w.stage.Status.Stream.ID, Seq: h.Statement.Predicate.Chain.Seq + 1, Parent: h.ID,
			Undoes: w.recordOf(op.Spec.Params.Undoes)},
	}
	if op.Spec.Cause != nil {
		in.Chain.Cause = w.recordOf(op.Spec.Cause.Operation)
	}
	for _, a := range op.Status.Approvals {
		obj := &dv1.Approval{}
		err := w.APIReader.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: a.Name}, obj)
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			return in, err
		default:
			in.Approvals[a.Name] = obj.Spec
		}
	}
	return in, nil
}

// gap records a local seq no operation holds, once its grace has passed.
func (w *writer) gap(ctx context.Context, seq int64) (time.Duration, error) {
	key := fmt.Sprintf("%s/%d", w.stage.Status.Stream.ID, seq)
	w.mu.Lock()
	if w.missing == nil {
		w.missing = map[string]time.Time{}
	}
	first, seen := w.missing[key]
	if !seen {
		first = w.Now()
		w.missing[key] = first
	}
	w.mu.Unlock()
	if wait := w.GapGrace - w.Now().Sub(first); wait > 0 {
		return wait, nil
	}
	if w.find(func(p delivery.RecordPredicate) bool { return p.Gap != nil && p.Gap.Seq == seq }) == nil {
		h := w.head()
		meta := delivery.ObjectMeta{Name: w.stage.Name, Namespace: w.stage.Namespace, UID: string(w.stage.UID)}
		chain := delivery.Chain{Stream: w.stage.Status.Stream.ID, Seq: h.Statement.Predicate.Chain.Seq + 1, Parent: h.ID}
		st := delivery.NewGap(w.ClusterID, meta, seq, chain, w.anchor.desc.Digest.String(), w.Now().UTC().Format(time.RFC3339))
		st.Predicate.Trust = w.Trust
		if _, err := w.sign(ctx, st, locations{anchor: w.anchor}); err != nil {
			return 0, err
		}
	}
	w.mu.Lock()
	delete(w.missing, key)
	w.mu.Unlock()
	return 0, w.advance(ctx, seq)
}

// advance moves recordedSeq up to seq and the head to the registry's newest
// record, in one compare-and-swap: the head only ever fast-forwards.
func (w *writer) advance(ctx context.Context, seq int64) error {
	head := w.head().ID
	return w.casStage(ctx, w.stage, func(s *dv1.Stage) {
		s.Status.Stream.RecordedSeq = max(s.Status.Stream.RecordedSeq, seq)
		s.Status.Stream.Head = head
	})
}

// release names the record on the operation and drops the record finalizer.
func (w *writer) release(ctx context.Context, op *dv1.Operation, rec *stored) error {
	p := rec.Statement.Predicate
	ref := &delivery.RecordRef{Digest: rec.ID, Seq: p.Chain.Seq, Kind: p.Kind}
	if p.Unrecordable != nil {
		ref.Refused = p.Unrecordable.Check + ": " + p.Unrecordable.Message
	}
	if op.Status.Record == nil || *op.Status.Record != *ref {
		op.Status.Record = ref
		if err := w.Status().Update(ctx, op); err != nil {
			return err
		}
	}
	controllerutil.RemoveFinalizer(op, dv1.RecordFinalizer)
	return w.Update(ctx, op)
}
