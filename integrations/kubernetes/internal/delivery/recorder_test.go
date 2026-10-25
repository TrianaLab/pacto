/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// testRegistry is an in-process OCI registry with the Referrers API, seeded
// with a deploy artifact pair (to names its contract) and that contract.
type testRegistry struct {
	srv           *httptest.Server
	host          string
	to, from, ctr string
	unsupported   bool
	// fault, when set, refuses the requests it matches (403: never retried).
	fault func(*http.Request) bool
}

func newTestRegistry(t *testing.T) *testRegistry {
	t.Helper()
	r := &testRegistry{}
	inner := registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0)))
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.fault != nil && r.fault(req) {
			http.Error(w, `{"errors":[{"code":"DENIED"}]}`, http.StatusForbidden)
			return
		}
		if r.unsupported && strings.Contains(req.URL.Path, "/referrers/") {
			http.Error(w, `{"errors":[{"code":"UNSUPPORTED"}]}`, http.StatusNotFound)
			return
		}
		inner.ServeHTTP(w, req)
	}))
	t.Cleanup(r.srv.Close)
	r.host = strings.TrimPrefix(r.srv.URL, "http://")
	ctr := r.push(t, "pactos/payments", "contract", nil)
	r.ctr = ctr.Digest.String()
	r.to = r.push(t, "apps/payments", "v2", map[string]string{delivery.ContractAnnotation: "oci://" + r.host + "/pactos/payments@" + r.ctr}).Digest.String()
	r.from = r.push(t, "apps/payments", "v1", nil).Digest.String()
	return r
}

func (r *testRegistry) push(t *testing.T, path, payload string, ann map[string]string) ocispec.Descriptor {
	t.Helper()
	repo, err := ocireferrer.Open(r.host+"/"+path, ocireferrer.Options{})
	if err != nil {
		t.Fatal(err)
	}
	art := ocireferrer.Build("application/x-test", "application/x-test", []byte(payload), nil, ann)
	if err := ocireferrer.Push(context.Background(), repo, art); err != nil {
		t.Fatal(err)
	}
	return art.ManifestDesc
}

// referrers counts the records attached to path@digest.
func (r *testRegistry) referrers(t *testing.T, path, digest string) int {
	t.Helper()
	repo, _ := ocireferrer.Open(r.host+"/"+path, ocireferrer.Options{})
	desc, err := repo.Resolve(context.Background(), digest)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	_ = repo.Referrers(context.Background(), desc, "", func(page []ocispec.Descriptor) error {
		n += len(page)
		return nil
	})
	return n
}

type recEnv struct {
	*env
	reg *testRegistry
	rec *Recorder
	key ed25519.PrivateKey
}

func newRecEnv(t *testing.T, objs ...any) *recEnv {
	t.Helper()
	reg := newTestRegistry(t)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	e := newEnv(t)
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": "payments"},
		"spec":     map[string]any{"url": "oci://" + reg.host + "/apps/payments", "insecure": true},
	}}
	u.SetGroupVersionKind(gvkOCI)
	e.create(u)
	re := &recEnv{env: e, reg: reg, key: key}
	re.rec = &Recorder{Client: e.c, APIReader: e.c, Cluster: e.op.Cluster, ClusterID: "cid",
		Store:  &RecordStore{Streams: reg.host + "/delivery/streams", Verify: edVerifier(key.Public())},
		Signer: edSigner(key), Now: func() time.Time { return e.now }, Poll: time.Second, GapGrace: time.Minute}
	return re
}

// concluded is a concluded operation of the Stage with seq, carrying the
// record finalizer.
func (re *recEnv) concluded(name string, seq int64, outcome delivery.Outcome, edit ...func(*dv1.Operation)) *dv1.Operation {
	re.t.Helper()
	o := opObj(name, delivery.TypePromote, re.reg.from, re.reg.to, bot)
	for _, f := range edit {
		f(o)
	}
	re.create(o)
	o.Status = delivery.OperationStatus{Phase: delivery.PhaseConcluded, Outcome: outcome, Seq: seq, ActorClass: delivery.ClassAgent,
		Pointer: delivery.PointerChange{Before: re.reg.from, After: re.reg.to}, Times: delivery.Times{Finished: "2026-10-20T10:00:00Z"}}
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		re.t.Fatal(err)
	}
	return o
}

func (re *recEnv) stage(seq int64) {
	re.t.Helper()
	s := stageObj(re.reg.from)
	s.Status.Stream.Seq = seq
	re.create(s)
}

func (re *recEnv) reconcile() reconcile.Result {
	re.t.Helper()
	res, err := re.rec.Reconcile(context.Background(), reconcile.Request{NamespacedName: keyOf("payments-prod")})
	if err != nil {
		re.t.Fatalf("record: %v", err)
	}
	return res
}

func (re *recEnv) records() []stored {
	re.t.Helper()
	a, err := re.rec.Store.anchor(context.Background(), "cid/prod/payments-prod")
	if err != nil {
		re.t.Fatal(err)
	}
	recs, _, err := re.rec.Store.list(context.Background(), a)
	if err != nil {
		re.t.Fatal(err)
	}
	return recs
}

func condition(s *dv1.Stage) delivery.Condition {
	for _, c := range s.Status.Conditions {
		if c.Type == ConditionRecordStoreReady {
			return c
		}
	}
	return delivery.Condition{}
}

func TestRecorderWritesChainedRecords(t *testing.T) {
	re := newRecEnv(t)
	re.stage(2)
	first := re.concluded("ship", 1, delivery.Succeeded)
	second := re.concluded("ship-again", 2, delivery.Rejected)
	re.approve(second, "ok", "alice", delivery.Approve)
	second.Status.Approvals = []delivery.ApprovalRecord{{Name: "ok", By: "alice", Decision: delivery.Approve}}
	if err := re.c.Status().Update(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	pending := func() float64 { return promtest.ToFloat64(RecordsPending.WithLabelValues("cid/prod/payments-prod")) }
	re.rec.Signer = failingSigner{}
	re.reconcile()
	if got := pending(); got != 2 {
		t.Fatalf("pacto_delivery_records_pending = %v with two operations unrecorded, want 2", got)
	}
	re.rec.Signer = edSigner(re.key)
	re.reconcile()
	if got := pending(); got != 0 {
		t.Fatalf("pacto_delivery_records_pending = %v once both are recorded, want 0", got)
	}

	recs := re.records()
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	byUID := map[string]stored{}
	for _, r := range recs {
		byUID[r.Statement.Predicate.Intent.UID] = r
	}
	r1, r2 := byUID[string(first.UID)], byUID[string(second.UID)]
	if r1.Statement.Predicate.Chain.Seq != 1 || r2.Statement.Predicate.Chain.Seq != 2 || r2.Statement.Predicate.Chain.Parent != r1.ID {
		t.Fatalf("chain: %+v / %+v", r1.Statement.Predicate.Chain, r2.Statement.Predicate.Chain)
	}
	if len(r1.Statement.Subject) != 4 || r2.Statement.Predicate.Approvals[0].IntentDigest != second.Spec.IntentDigest {
		t.Fatalf("subjects %+v approvals %+v", r1.Statement.Subject, r2.Statement.Predicate.Approvals)
	}
	for path, d := range map[string]string{"apps/payments": re.reg.to, "pactos/payments": re.reg.ctr} {
		if n := re.reg.referrers(t, path, d); n != 2 {
			t.Errorf("%s@%s has %d referrers, want 2", path, d, n)
		}
	}
	s := re.getStage()
	if s.Status.Stream.RecordedSeq != 2 || s.Status.Stream.Head != r2.ID || condition(s).Status != "True" {
		t.Fatalf("stream %+v condition %+v", s.Status.Stream, condition(s))
	}
	for _, name := range []string{"ship", "ship-again"} {
		o := re.getOp(name)
		if controllerutil.ContainsFinalizer(o, dv1.RecordFinalizer) || o.Status.Record == nil || o.Status.Record.Kind != delivery.RecordOperation {
			t.Fatalf("%s: finalizers %v record %+v", name, o.Finalizers, o.Status.Record)
		}
	}
	// Nothing owed: no registry round trip, nothing new.
	re.reg.srv.Close()
	if res := re.reconcile(); res.RequeueAfter != 0 {
		t.Fatalf("idle pass requeued: %+v", res)
	}
}

func TestRecorderUnrecordable(t *testing.T) {
	for name, edit := range map[string]func(*recEnv, *dv1.Operation){
		"secret": func(re *recEnv, o *dv1.Operation) {
			o.Status.Message = "Bearer abcdefghijklmnopqrstuvwxyz0123"
		},
		"approval gone": func(re *recEnv, o *dv1.Operation) {
			o.Status.Approvals = []delivery.ApprovalRecord{{Name: "deleted", By: "alice", Decision: delivery.Approve}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			re := newRecEnv(t)
			re.stage(1)
			o := re.concluded("ship", 1, delivery.Failed)
			edit(re, o)
			if err := re.c.Status().Update(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			re.reconcile()
			recs := re.records()
			if len(recs) != 1 || recs[0].Statement.Predicate.Kind != delivery.RecordUnrecordable || recs[0].Statement.Predicate.Intent.Params != nil {
				t.Fatalf("records = %+v", recs)
			}
			if n := re.reg.referrers(t, "apps/payments", re.reg.to); n != 0 {
				t.Fatalf("an unrecordable record went on to: %d", n)
			}
			if got := re.getOp("ship").Status.Record; got == nil || got.Kind != delivery.RecordUnrecordable || got.Refused == "" {
				t.Fatalf("record ref = %+v", got)
			}
		})
	}
}

// TestRecorderTooLargeIsUnrecordable: a record larger than a reader fetches
// is signed as unrecordable, so the stream stays readable.
func TestRecorderTooLargeIsUnrecordable(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded, func(o *dv1.Operation) {
		o.Spec.Reason = strings.Repeat("x", int(deliveryrecord.MaxBytes))
	})
	re.reconcile()
	recs := re.records()
	if len(recs) != 1 || recs[0].Statement.Predicate.Unrecordable == nil || recs[0].Statement.Predicate.Unrecordable.Check != delivery.CheckSize {
		t.Fatalf("records = %+v", recs)
	}
	if got := re.getOp("ship").Status.Record; got == nil || !strings.HasPrefix(got.Refused, "size: ") {
		t.Fatalf("record ref = %+v", got)
	}
}

func TestRecorderGapWaitsThenRecords(t *testing.T) {
	re := newRecEnv(t)
	re.stage(2)
	re.concluded("ship", 2, delivery.Succeeded)
	if res := re.reconcile(); res.RequeueAfter != time.Minute {
		t.Fatalf("gap inside grace: %+v", res)
	}
	if len(re.records()) != 0 {
		t.Fatal("a gap was recorded inside its grace")
	}
	re.now = re.now.Add(2 * time.Minute)
	re.reconcile()
	recs := re.records()
	if len(recs) != 2 {
		t.Fatalf("records = %d", len(recs))
	}
	for _, r := range recs {
		p := r.Statement.Predicate
		if (p.Kind == delivery.RecordGap) != (p.Chain.Seq == 1) || (p.Gap != nil && p.Gap.Seq != 1) {
			t.Fatalf("record %+v", p)
		}
	}
	if s := re.getStage(); s.Status.Stream.RecordedSeq != 2 {
		t.Fatalf("recordedSeq = %d", s.Status.Stream.RecordedSeq)
	}
}

func TestRecorderAdoptsAndFastForwards(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	head := re.getStage().Status.Stream.Head

	// An etcd restore: the Stage and the operation are back as they were
	// before the record was written.
	re.setStage(func(s *delivery.StageStatus) { s.Stream.RecordedSeq, s.Stream.Head, s.Stream.Seq = 0, "", 2 })
	o := re.getOp("ship")
	controllerutil.AddFinalizer(o, dv1.RecordFinalizer)
	if err := re.c.Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	// A rollback concluded after the restore undoes it, citing it as cause.
	rb := re.concluded("rollback", 2, delivery.Succeeded, func(rb *dv1.Operation) {
		rb.Spec.Params.Undoes, rb.Spec.Cause = string(o.UID), &delivery.Cause{Operation: string(o.UID)}
	})
	re.reconcile()

	recs := re.records()
	if len(recs) != 2 {
		t.Fatalf("the recorded operation was written twice: %d records", len(recs))
	}
	for _, r := range recs {
		if p := r.Statement.Predicate; p.Intent.UID == string(rb.UID) &&
			(p.Chain.Seq != 2 || p.Chain.Parent != head || p.Chain.Undoes != head || p.Chain.Cause != head) {
			t.Fatalf("rollback chain = %+v, want parent, undoes and cause %s", p.Chain, head)
		}
	}
	if o := re.getOp("ship"); controllerutil.ContainsFinalizer(o, dv1.RecordFinalizer) {
		t.Fatal("the adopted operation kept its finalizer")
	}
}

// TestRecorderRecordsAReconclusion: after an etcd restore an operation that
// was recorded concludes again, differently. Its old record is not adopted for
// the new conclusion, which gets its own.
func TestRecorderRecordsAReconclusion(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	old := re.getOp("ship").Status.Record.Digest

	re.setStage(func(s *delivery.StageStatus) { s.Stream.RecordedSeq, s.Stream.Head = 0, "" })
	o := re.getOp("ship")
	controllerutil.AddFinalizer(o, dv1.RecordFinalizer)
	if err := re.c.Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	o.Status.Outcome, o.Status.Times.Finished, o.Status.Record = delivery.Failed, "2026-10-20T11:00:00Z", nil
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	re.reconcile()
	if recs := re.records(); len(recs) != 2 {
		t.Fatalf("records = %d, want the old and the new conclusion", len(recs))
	}
	got := re.getOp("ship").Status.Record
	if got == nil || got.Digest == old || got.Seq != 2 {
		t.Fatalf("record = %+v, want a new one at seq 2 (old %s)", got, old)
	}
}

// TestRecorderGapOfARecreatedStage: a Stage deleted and recreated with the
// same name reuses the stream, and its seqs start over. The old Stage's gap
// record of a seq is not this Stage's.
func TestRecorderGapOfARecreatedStage(t *testing.T) {
	re := newRecEnv(t)
	re.stage(2)
	ctx := context.Background()
	a, _ := re.rec.Store.anchor(ctx, "cid/prod/payments-prod")
	st := delivery.NewGap("cid", delivery.ObjectMeta{Name: "payments-prod", Namespace: ns, UID: "old-stage"}, 1,
		delivery.Chain{Stream: "cid/prod/payments-prod", Seq: 1}, a.desc.Digest.String(), "t")
	bundle, _, _ := delivery.SignRecord(ctx, re.rec.Signer, st)
	if err := re.rec.Store.write(ctx, bundle, st, locations{anchor: a}); err != nil {
		t.Fatal(err)
	}
	re.concluded("ship", 2, delivery.Succeeded)
	re.reconcile()
	re.now = re.now.Add(2 * time.Minute)
	re.reconcile()
	if recs := re.records(); len(recs) != 3 {
		t.Fatalf("records = %d, want the old gap, a new gap and the operation", len(recs))
	}
}

// TestRecorderUsesThePolicyOfTheConclusion: the record embeds the Stage spec
// the operation concluded under, not the one the Stage has when the record
// is written.
func TestRecorderUsesThePolicyOfTheConclusion(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	o := re.concluded("ship", 1, delivery.Succeeded)
	policy := stageObj("").Spec
	policy.Environment = "prod-then"
	o.Status.Policy = &policy
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	re.reconcile()
	if p := re.records()[0].Statement.Predicate.Policy; p == nil || p.Environment != "prod-then" {
		t.Fatalf("policy = %+v", p)
	}
}

func TestRecorderRefusesToFork(t *testing.T) {
	t.Run("head not in the registry", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.setStage(func(s *delivery.StageStatus) { s.Stream.Head = "sha256:gone" })
		re.concluded("ship", 1, delivery.Succeeded)
		re.reconcile()
		if c := condition(re.getStage()); c.Reason != ReasonStreamConflict || len(re.records()) != 0 {
			t.Fatalf("condition %+v", c)
		}
	})
	t.Run("signed by another key", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.reconcile()
		_, other, _ := ed25519.GenerateKey(rand.Reader)
		re.rec.Store.Verify = edVerifier(other.Public())
		re.rec.Signer = edSigner(other)
		re.setStage(func(s *delivery.StageStatus) { s.Stream.Seq = 2 })
		re.concluded("next", 2, delivery.Succeeded)
		re.reconcile()
		if c := condition(re.getStage()); c.Reason != ReasonStreamConflict {
			t.Fatalf("condition %+v", c)
		}
	})
	t.Run("a record of another stream on the anchor is ignored", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.reconcile()
		// Validly signed, chained from this stream's head, but naming
		// another stream: a key trusted only there must not extend this one.
		ctx := context.Background()
		a, _ := re.rec.Store.anchor(ctx, "cid/prod/payments-prod")
		head := re.getStage().Status.Stream.Head
		st := delivery.NewGap("cid", delivery.ObjectMeta{Name: "x"}, 9, delivery.Chain{Stream: "cid/dev/x", Seq: 2, Parent: head}, a.desc.Digest.String(), "t")
		bundle, _, _ := delivery.SignRecord(ctx, re.rec.Signer, st)
		if err := re.rec.Store.write(ctx, bundle, st, locations{anchor: a}); err != nil {
			t.Fatal(err)
		}
		re.setStage(func(s *delivery.StageStatus) { s.Stream.Seq = 2 })
		re.concluded("next", 2, delivery.Succeeded)
		re.reconcile()
		if c := condition(re.getStage()); c.Status != "True" || !strings.Contains(c.Message, `names stream "cid/dev/x"`) {
			t.Fatalf("condition %+v", c)
		}
		if recs := re.records(); len(recs) != 2 || re.getStage().Status.Stream.RecordedSeq != 2 {
			t.Fatalf("records = %+v", recs)
		}
	})
	// Anyone with push access to the streams repository can attach a
	// referrer. One no trusted key signed is not the stream's history, so it
	// neither freezes the Stage nor stops the recorder.
	t.Run("a record another key signed is ignored", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.reconcile()
		ctx := context.Background()
		a, _ := re.rec.Store.anchor(ctx, "cid/prod/payments-prod")
		_, mallory, _ := ed25519.GenerateKey(rand.Reader)
		st := delivery.NewGap("cid", delivery.ObjectMeta{Name: "x"}, 9, delivery.Chain{Stream: "cid/prod/payments-prod", Seq: 2,
			Parent: re.getStage().Status.Stream.Head}, a.desc.Digest.String(), "t")
		bundle, _, _ := delivery.SignRecord(ctx, edSigner(mallory), st)
		if err := re.rec.Store.write(ctx, bundle, st, locations{anchor: a}); err != nil {
			t.Fatal(err)
		}
		re.setStage(func(s *delivery.StageStatus) { s.Stream.Seq = 2 })
		re.concluded("next", 2, delivery.Succeeded)
		re.reconcile()
		if c := condition(re.getStage()); c.Status != "True" || !strings.Contains(c.Message, "1 record(s) on the anchor are ignored") {
			t.Fatalf("condition %+v", c)
		}
		if recs := re.records(); len(recs) != 2 || re.getStage().Status.Stream.RecordedSeq != 2 {
			t.Fatalf("records = %+v", recs)
		}
	})
}

func TestConflict(t *testing.T) {
	rec := func(id string, seq int64, parent string) stored {
		return stored{ID: id, Statement: delivery.Statement{Predicate: delivery.RecordPredicate{Chain: delivery.Chain{Seq: seq, Parent: parent}}}}
	}
	cases := map[string]struct {
		recs []stored
		head string
		want bool
	}{
		"empty":            {nil, "", false},
		"chained":          {[]stored{rec("a", 1, ""), rec("b", 2, "a")}, "a", false},
		"history trimmed":  {[]stored{rec("c", 3, "b")}, "", false},
		"duplicate seq":    {[]stored{rec("a", 1, ""), rec("b", 1, "")}, "", true},
		"broken chain":     {[]stored{rec("a", 1, ""), rec("b", 2, "x")}, "", true},
		"head is not here": {[]stored{rec("a", 1, "")}, "z", true},
	}
	for name, tc := range cases {
		if got := conflict(tc.recs, tc.head) != ""; got != tc.want {
			t.Errorf("%s: conflict = %v", name, got)
		}
	}
}

func TestRecorderStoreFailures(t *testing.T) {
	t.Run("no referrers API", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.reg.unsupported = true
		if res := re.reconcile(); res.RequeueAfter != time.Second {
			t.Fatalf("res = %+v", res)
		}
		if c := condition(re.getStage()); c.Status != "False" || c.Reason != ReasonReferrersUnsupported {
			t.Fatalf("condition %+v", c)
		}
		if !controllerutil.ContainsFinalizer(re.getOp("ship"), dv1.RecordFinalizer) {
			t.Fatal("the finalizer went with no record")
		}
	})
	t.Run("registry down", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.reg.srv.Close()
		re.reconcile()
		if c := condition(re.getStage()); c.Reason != ReasonRegistryUnavailable {
			t.Fatalf("condition %+v", c)
		}
	})
	t.Run("signer down", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.rec.Signer = failingSigner{}
		re.reconcile()
		if c := condition(re.getStage()); c.Reason != ReasonSignerUnavailable || !strings.Contains(c.Message, "kms") {
			t.Fatalf("condition %+v", c)
		}
	})
}

type failingSigner struct{}

func (failingSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("kms down")
}

func TestRecorderClusterReads(t *testing.T) {
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: keyOf("payments-prod")}

	re := newRecEnv(t)
	if _, err := re.rec.Reconcile(ctx, req); err != nil {
		t.Fatalf("a missing Stage: %v", err)
	}
	s := stageObj("")
	s.Status.Stream.ID = ""
	re.create(s)
	if res, err := re.rec.Reconcile(ctx, req); err != nil || res.RequeueAfter != 0 {
		t.Fatalf("a Stage with no stream: %+v %v", res, err)
	}
	re.setStage(func(s *delivery.StageStatus) { s.Stream = delivery.Stream{ID: "cid/prod/payments-prod", Seq: 1} })
	re.concluded("ship", 1, delivery.Succeeded)
	for _, f := range []string{"list/Operation", "get/OCIRepository", "get/Approval"} {
		re.fails = fails{f: true}
		o := re.getOp("ship")
		o.Status.Approvals = []delivery.ApprovalRecord{{Name: "ok"}}
		_ = re.c.Status().Update(ctx, o)
		res, err := re.rec.Reconcile(ctx, req)
		if f == "list/Operation" {
			if err == nil {
				t.Fatal("a failed operation list was not an error")
			}
			continue
		}
		if err != nil || res.RequeueAfter != time.Second {
			t.Fatalf("%s: %+v %v", f, res, err)
		}
	}
	re.fails = fails{"status/Stage": true}
	if _, err := re.rec.Reconcile(ctx, req); err == nil {
		t.Fatal("a failed condition write was not an error")
	}
	re.fails = fails{}
	re.reg.unsupported = true
	re.fails = fails{"status/Stage": true}
	if _, err := re.rec.Reconcile(ctx, req); err == nil {
		t.Fatal("a failed not-ready write was not an error")
	}
}

func TestRecorderWithoutApplyRepository(t *testing.T) {
	re := newRecEnv(t)
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCI)
	u.SetNamespace(ns)
	u.SetName("payments")
	if err := re.c.Delete(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	recs := re.records()
	if len(recs) != 1 || len(recs[0].Statement.Subject) != 1 {
		t.Fatalf("records = %+v", recs)
	}
}

func TestRecorderReleaseFailures(t *testing.T) {
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: keyOf("payments-prod")}
	for _, f := range []string{"status/Operation", "update/Operation"} {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.fails = fails{f: true}
		if _, err := re.rec.Reconcile(ctx, req); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if c := condition(re.getStage()); c.Reason != ReasonRegistryUnavailable {
			t.Fatalf("%s: condition %+v", f, c)
		}
		re.fails = fails{}
		re.reconcile()
		if o := re.getOp("ship"); controllerutil.ContainsFinalizer(o, dv1.RecordFinalizer) || len(re.records()) != 1 {
			t.Fatalf("%s: the retry did not finish the record", f)
		}
	}
}

func TestPromoterKeepsTheFinalizerForTheRecorder(t *testing.T) {
	e := newEnv(t, stageObj(""))
	e.op.Records = true
	for name, seq := range map[string]int64{"recorded": 3, "no-stream": 0} {
		o := opObj(name, delivery.TypePromote, "", dA, bot)
		e.create(o)
		o.Status = delivery.OperationStatus{Phase: delivery.PhaseConcluded, Seq: seq}
		if err := e.c.Status().Update(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		if err := e.c.Delete(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		e.reconcileOp(name)
	}
	if o := e.getOp("recorded"); !controllerutil.ContainsFinalizer(o, dv1.RecordFinalizer) {
		t.Fatal("the promoter took the recorder's finalizer")
	}
	o := &dv1.Operation{}
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "no-stream"}, o); err == nil {
		t.Fatalf("an operation with no stream kept its finalizer: %v", o.Finalizers)
	}
}

// flakyReader fails every Get after the first n.
type flakyReader struct {
	client.Reader
	n int
}

func (f *flakyReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if f.n--; f.n < 0 {
		return errors.New("apiserver gone")
	}
	return f.Reader.Get(ctx, key, obj, opts...)
}

func TestRecorderEdgeCases(t *testing.T) {
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: keyOf("payments-prod")}

	t.Run("other operations are not owed", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded, func(o *dv1.Operation) { o.Spec.Params.Undoes = "uid-unknown" })
		re.concluded("elsewhere", 1, delivery.Succeeded, func(o *dv1.Operation) { o.Spec.Stage = "other" })
		re.create(opObj("running", delivery.TypePromote, "", dA, bot))
		re.reconcile()
		recs := re.records()
		if len(recs) != 1 || recs[0].Statement.Predicate.Chain.Undoes != "" {
			t.Fatalf("records = %+v", recs)
		}
	})
	for name, fault := range map[string]func(*http.Request) bool{
		"listing": from(2, http.MethodGet, "/referrers/"),
		"locate":  from(1, http.MethodHead, "/apps/payments/manifests/"),
		"write":   from(1, http.MethodPut, "/apps/payments/manifests/"),
	} {
		t.Run(name, func(t *testing.T) {
			re := newRecEnv(t)
			re.stage(1)
			re.concluded("ship", 1, delivery.Succeeded)
			re.reg.fault = fault
			re.reconcile()
			if c := condition(re.getStage()); c.Reason != ReasonRegistryUnavailable {
				t.Fatalf("condition %+v", c)
			}
		})
	}
	t.Run("advance fails", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.setStage(func(s *delivery.StageStatus) {
			s.Conditions = []delivery.Condition{{Type: ConditionRecordStoreReady, Status: "True", Reason: "ReferrersSupported",
				Message: "records are written to " + re.rec.Store.Streams}}
		})
		re.concluded("ship", 1, delivery.Succeeded)
		re.fails = fails{"status/Stage": true}
		if _, err := re.rec.Reconcile(ctx, req); err == nil {
			t.Fatal("a failed stream advance was not an error")
		}
	})
	t.Run("stage read fails mid-write", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.concluded("ship", 1, delivery.Succeeded)
		re.rec.APIReader = &flakyReader{Reader: re.c, n: 1}
		if _, err := re.rec.Reconcile(ctx, req); err == nil {
			t.Fatal("a failed Stage read was not an error")
		}
	})
	t.Run("gap adopted and gap signer down", func(t *testing.T) {
		re := newRecEnv(t)
		re.stage(1)
		re.rec.GapGrace = 0
		re.reconcile()
		re.setStage(func(s *delivery.StageStatus) { s.Stream.RecordedSeq, s.Stream.Head = 0, "" })
		re.reconcile()
		if n := len(re.records()); n != 1 {
			t.Fatalf("the gap was written %d times", n)
		}
		re.setStage(func(s *delivery.StageStatus) { s.Stream.Seq = 2 })
		re.rec.Signer = failingSigner{}
		re.reconcile()
		if c := condition(re.getStage()); c.Reason != ReasonSignerUnavailable {
			t.Fatalf("condition %+v", c)
		}
	})
}

// TestRecorderRecordsAnOperationWithoutTheFinalizer: admission off, or the
// finalizer removed, does not let a concluded operation's seq be skipped.
func TestRecorderRecordsAnOperationWithoutTheFinalizer(t *testing.T) {
	re := newRecEnv(t)
	re.stage(2)
	re.concluded("ship", 1, delivery.Succeeded, func(o *dv1.Operation) { o.Finalizers = nil })
	re.concluded("ship-again", 2, delivery.Succeeded)
	re.reconcile()
	if recs := re.records(); len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	if got := re.getOp("ship").Status.Record; got == nil || got.Seq != 1 {
		t.Fatalf("record = %+v, want seq 1", got)
	}
}

// TestRecorderForgetsAMissedSeq: a seq missed for a moment and then held
// starts its grace over the next time it is missed, here by a Stage recreated
// with the same name.
func TestRecorderForgetsAMissedSeq(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.reconcile()
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	ctx := context.Background()
	if err := re.c.Delete(ctx, re.getStage()); err != nil {
		t.Fatal(err)
	}
	if err := re.c.Delete(ctx, re.getOp("ship")); err != nil {
		t.Fatal(err)
	}
	s := stageObj(re.reg.from)
	s.UID, s.Status.Stream.Seq = "new-stage", 1
	re.create(s)
	re.now = re.now.Add(2 * time.Minute)
	if res := re.reconcile(); res.RequeueAfter != time.Minute {
		t.Fatalf("a stale miss skipped the grace: %+v", res)
	}
	if recs := re.records(); len(recs) != 1 {
		t.Fatalf("records = %d, want only the first operation", len(recs))
	}
}

// TestRecorderWaitsForAConcludingOperation: the Stage gave an operation its
// seq and the operation's status write is late. That seq is no gap and
// nothing after it is recorded; once the operation is gone it is a gap.
func TestRecorderWaitsForAConcludingOperation(t *testing.T) {
	re := newRecEnv(t)
	re.stage(2)
	late := opObj("late", delivery.TypePromote, re.reg.from, re.reg.to, bot)
	re.create(late)
	re.setStage(func(s *delivery.StageStatus) {
		s.Stream.Concluded = []delivery.Concluded{{UID: string(late.UID), Seq: 1, Outcome: delivery.Succeeded}}
	})
	re.concluded("ship", 2, delivery.Succeeded)
	for range 2 {
		if res := re.reconcile(); res.RequeueAfter != time.Second {
			t.Fatalf("not waiting: %+v", res)
		}
		re.now = re.now.Add(2 * time.Minute)
	}
	if recs := re.records(); len(recs) != 0 {
		t.Fatalf("records = %d while seq 1 concludes", len(recs))
	}
	gone := re.getOp("late")
	gone.Finalizers = nil
	if err := re.c.Update(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	if err := re.c.Delete(context.Background(), gone); err != nil {
		t.Fatal(err)
	}
	re.reconcile()
	re.now = re.now.Add(2 * time.Minute)
	re.reconcile()
	if recs := re.records(); len(recs) != 2 {
		t.Fatalf("records = %d, want a gap and the operation", len(recs))
	}
}

// TestRecorderLeftoverOfADeletedStage: an operation an earlier Stage of the
// same name concluded is recorded under that Stage and never moves the new
// Stage's recordedSeq, so the new Stage's own seqs are not skipped.
func TestRecorderLeftoverOfADeletedStage(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.concluded("old", 50, delivery.Succeeded)
	old := re.getOp("old")
	old.Status.StageUID = "old-stage"
	if err := re.c.Status().Update(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	recs := re.records()
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	for _, r := range recs {
		p := r.Statement.Predicate
		if want := map[string]string{"old": "old-stage", "ship": "uid-payments-prod"}[p.Intent.Name]; p.Intent.Stage.UID != want {
			t.Fatalf("%s is recorded under %q, want %q", p.Intent.Name, p.Intent.Stage.UID, want)
		}
	}
	if s := re.getStage(); s.Status.Stream.RecordedSeq != 1 {
		t.Fatalf("recordedSeq = %d, want 1", s.Status.Stream.RecordedSeq)
	}
	if o := re.getOp("old"); controllerutil.ContainsFinalizer(o, dv1.RecordFinalizer) {
		t.Fatal("the leftover was not let go")
	}
}

// TestRecorderGapsWaitOneGraceTogether: two missing seqs start their grace
// together, so both are recorded one grace later, not one grace each.
func TestRecorderGapsWaitOneGraceTogether(t *testing.T) {
	re := newRecEnv(t)
	re.stage(3)
	re.concluded("ship", 3, delivery.Succeeded)
	re.reconcile()
	re.now = re.now.Add(time.Minute + time.Second)
	if res := re.reconcile(); res.RequeueAfter != 0 {
		t.Fatalf("the second gap started its grace late: %+v", res)
	}
	if recs := re.records(); len(recs) != 3 {
		t.Fatalf("records = %d, want two gaps and the operation", len(recs))
	}
}

// TestRecorderRevokesAKeyWithoutFreezing: rotating to a new key and revoking
// the old one after the last seq it signed keeps the stream writable and its
// history readable, and a record the revoked key signs later is ignored.
func TestRecorderRevokesAKeyWithoutFreezing(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	re.concluded("ship", 1, delivery.Succeeded)
	re.reconcile()
	oldPEM, _ := delivery.PublicKeyPEM(re.key.Public())
	_, next, _ := ed25519.GenerateKey(rand.Reader)
	nextPEM, _ := delivery.PublicKeyPEM(next.Public())
	v, err := signing.TrustVerifier(delivery.TrustConfig{Keys: []delivery.TrustedKey{
		{Name: "old", PublicKey: oldPEM, Streams: []string{"cid/prod/payments-prod"}, UntilSeq: 1},
		{Name: "next", PublicKey: nextPEM},
	}})
	if err != nil {
		t.Fatal(err)
	}
	re.rec.Store.Verify, re.rec.Signer = v, edSigner(next)
	// The compromised key signs a record after its revocation.
	ctx := context.Background()
	a, _ := re.rec.Store.anchor(ctx, "cid/prod/payments-prod")
	st := delivery.NewGap("cid", delivery.ObjectMeta{Name: "x"}, 9, delivery.Chain{Stream: "cid/prod/payments-prod", Seq: 2,
		Parent: re.getStage().Status.Stream.Head}, a.desc.Digest.String(), "t")
	bundle, _, _ := delivery.SignRecord(ctx, edSigner(re.key), st)
	if err := re.rec.Store.write(ctx, bundle, st, locations{anchor: a}); err != nil {
		t.Fatal(err)
	}
	re.setStage(func(s *delivery.StageStatus) { s.Stream.Seq = 2 })
	re.concluded("next", 2, delivery.Succeeded)
	re.reconcile()
	if c := condition(re.getStage()); c.Status != "True" || !strings.Contains(c.Message, "revoked after seq 1") {
		t.Fatalf("condition %+v", c)
	}
	recs := re.records()
	next2 := slices.ContainsFunc(recs, func(r stored) bool {
		return r.Statement.Predicate.Chain.Seq == 2 && r.Statement.Predicate.Intent.Name == "next"
	})
	if len(recs) != 2 || !next2 || re.getStage().Status.Frozen {
		t.Fatalf("records = %+v", recs)
	}
}
