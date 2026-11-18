/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"oras.land/oras-go/v2/content"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
)

// heldRecEnv is a recorder whose Stage's stream is ready, with a held,
// admitted operation p1, and its approval recorder guarded by pd-operation.
func heldRecEnv(t *testing.T) (*recEnv, *ApprovalRecorder, *dv1.Operation) {
	t.Helper()
	re := newRecEnv(t)
	re.rec.AdmissionPolicies = []string{"pd-operation"}
	re.stage(0)
	re.reconcile()
	o := opObj("p1", delivery.TypePromote, re.reg.from, re.reg.to, bot)
	re.create(o)
	o.Status = delivery.OperationStatus{Phase: delivery.PhaseHeld, Guarded: true, ActorClass: delivery.ClassAgent,
		Times: delivery.Times{Admitted: "2026-10-20T09:00:00Z"}}
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	return re, &ApprovalRecorder{Recorder: re.rec}, o
}

func (re *recEnv) recordApprovals(ar *ApprovalRecorder) reconcile.Result {
	re.t.Helper()
	res, err := ar.Reconcile(context.Background(), reconcile.Request{NamespacedName: keyOf("p1")})
	if err != nil {
		re.t.Fatal(err)
	}
	return res
}

// statementAt fetches the record at ref and decodes its signed statement.
func statementAt(t *testing.T, ref string, st any) {
	t.Helper()
	path, dgst, _ := strings.Cut(ref, "@")
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	desc, err := repo.Resolve(context.Background(), dgst)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := content.FetchAll(context.Background(), repo, desc)
	if err != nil {
		t.Fatal(err)
	}
	var m ocispec.Manifest
	_ = json.Unmarshal(raw, &m)
	bundle, err := content.FetchAll(context.Background(), repo, m.Layers[0])
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		DSSEEnvelope struct {
			Payload []byte `json:"payload"`
		} `json:"dsseEnvelope"`
	}
	_ = json.Unmarshal(bundle, &b)
	if err := json.Unmarshal(b.DSSEEnvelope.Payload, st); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalRecorderSignsOnBehalf(t *testing.T) {
	re, ar, o := heldRecEnv(t)
	re.approve(o, "ok", carol, delivery.Approve)
	re.recordApprovals(ar)
	a := re.getApproval("ok")
	if a.Status.Record == nil || !strings.HasPrefix(a.Status.Record.Ref, re.rec.Store.Streams+"@sha256:") {
		t.Fatalf("approval status = %+v", a.Status)
	}
	var st delivery.ApprovalStatement
	statementAt(t, a.Status.Record.Ref, &st)
	p := st.Predicate
	if st.PredicateType != delivery.PredicateApproval || p.Operation.UID != "uid-p1" || p.Decision != delivery.Approve ||
		p.Signer.Kind != delivery.SignerRecorder || p.Signer.OnBehalfOf.User != carol ||
		p.Signer.OnBehalfOf.Verified != delivery.VerifiedByAdmission || p.Operation.Stage.Cluster != "cid" {
		t.Fatalf("approval record = %+v", st)
	}
	// Signing again writes nothing new: the payload is the same, and so is
	// the record.
	anchor := deliveryrecord.Anchor("cid/prod/payments-prod").ManifestDesc.Digest.String()
	before := re.reg.referrers(t, "delivery/streams", anchor)
	a.Status.Record = nil
	if err := re.c.Status().Update(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	re.recordApprovals(ar)
	if after := re.reg.referrers(t, "delivery/streams", anchor); after != before || before != 2 {
		t.Fatalf("referrers %d then %d, want the intent and the approval once", before, after)
	}
	if len(re.records()) != 0 {
		t.Fatal("an approval or intent record joined the operation chain")
	}
}

// TestApprovalRecorderSkipsViewApprovals: an Approval naming a keyless record
// carries the person's own signature and is never signed on behalf, whether
// or not its status was written yet.
func TestApprovalRecorderSkipsViewApprovals(t *testing.T) {
	re, ar, o := heldRecEnv(t)
	re.create(&dv1.Approval{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "view", CreationTimestamp: metav1.NewTime(re.now)},
		Spec: delivery.ApprovalSpec{Operation: o.Name, OperationUID: string(o.UID), IntentDigest: o.Spec.IntentDigest,
			Decision: delivery.Approve, By: carol, Record: "reg/approvals@sha256:ab"}})
	re.recordApprovals(ar)
	if a := re.getApproval("view"); a.Status.Record != nil {
		t.Fatalf("a view was signed on behalf: %+v", a.Status)
	}
}

func TestApprovalRecorderSignsNothingWithoutApprovalPolicy(t *testing.T) {
	re, ar, o := heldRecEnv(t)
	unguard(re.env)
	re.approve(o, "ok", carol, delivery.Approve)
	if res := re.recordApprovals(ar); res.RequeueAfter != re.rec.Poll {
		t.Fatalf("an unguarded pass does not look again: %+v", res)
	}
	if a := re.getApproval("ok"); a.Status.Record != nil {
		t.Fatalf("an unguarded Approval was signed: %+v", a.Status)
	}
	if re.getOp("p1").Status.IntentRecord == nil {
		t.Fatal("the intent record waits for the approval policy")
	}
	// The guard comes back: an Approval created while it was away is
	// still not signed, a new one is.
	re.create(guardPolicy(), guardBinding())
	re.now = re.now.Add(time.Minute)
	re.recordApprovals(ar)
	re.approve(o, "later", carol, delivery.Approve)
	re.recordApprovals(ar)
	if re.getApproval("ok").Status.Record != nil || re.getApproval("later").Status.Record == nil {
		t.Fatal("only an Approval created since the guard holds is signed")
	}
}

func TestApprovalRecorderSkipsConcludedOperations(t *testing.T) {
	re, ar, _ := heldRecEnv(t)
	o := re.getOp("p1")
	o.Status.Phase = delivery.PhaseConcluded
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	re.approve(o, "ok", carol, delivery.Approve)
	re.recordApprovals(ar)
	if re.getApproval("ok").Status.Record != nil || re.getOp("p1").Status.IntentRecord != nil {
		t.Fatal("a concluded operation got records")
	}
	// An Approval older than the moment the guard was first seen.
	re2, ar2, o2 := heldRecEnv(t)
	re2.recordApprovals(ar2)
	re2.now = re2.now.Add(-time.Hour)
	re2.approve(o2, "old", carol, delivery.Approve)
	re2.recordApprovals(ar2)
	if re2.getApproval("old").Status.Record != nil {
		t.Fatal("an Approval created before the guard was seen was signed")
	}
}

func TestIntentRecordWrittenOnceAfterAdmission(t *testing.T) {
	re, ar, _ := heldRecEnv(t)
	o := re.getOp("p1")
	o.Status.Guarded = false
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	re.recordApprovals(ar)
	if re.getOp("p1").Status.IntentRecord != nil {
		t.Fatal("an operation the promoter has not admitted got an intent record")
	}
	o = re.getOp("p1")
	o.Status.Guarded, o.Status.Phase = true, delivery.PhasePending
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	re.recordApprovals(ar)
	ref := re.getOp("p1").Status.IntentRecord
	if ref == nil {
		t.Fatal("an admitted operation has no intent record")
	}
	re.recordApprovals(ar)
	if got := re.getOp("p1").Status.IntentRecord; *got != *ref {
		t.Fatalf("the intent record moved: %+v then %+v", ref, got)
	}
	var st delivery.IntentStatement
	statementAt(t, ref.Ref, &st)
	p := st.Predicate
	if st.PredicateType != delivery.PredicateIntent || p.Actor.User != bot || !p.Guarded || p.Intent.UID != "uid-p1" ||
		p.Approvals != re.getStage().Status.Stream.Approvals || p.Approvals == "" || p.Time != "2026-10-20T09:00:00Z" {
		t.Fatalf("intent record = %+v", st)
	}
}

func TestRecorderAnchorsApprovalsRepository(t *testing.T) {
	re := newRecEnv(t)
	re.stage(0)
	re.reconcile()
	want := deliveryrecord.AnchorRef(re.rec.Store.Approvals, "cid/prod/payments-prod")
	if got := re.getStage().Status.Stream.Approvals; got != want {
		t.Fatalf("approvals = %q, want %q", got, want)
	}
	repo, _ := ocireferrer.Open(re.rec.Store.Approvals, ocireferrer.Options{})
	if _, err := repo.Resolve(context.Background(), deliveryrecord.AnchorTag("cid/prod/payments-prod")); err != nil {
		t.Fatalf("the approvals anchor is missing: %v", err)
	}
	// Set once: a new repository later never moves it.
	re.rec.Store.Approvals = re.reg.host + "/elsewhere"
	re.setStage(func(s *delivery.StageStatus) { s.Conditions = nil })
	re.reconcile()
	if got := re.getStage().Status.Stream.Approvals; got != want {
		t.Fatalf("approvals moved to %q", got)
	}
	// An approvals repository that cannot be anchored keeps the stream
	// not ready.
	re2 := newRecEnv(t)
	re2.stage(0)
	re2.reg.fault = func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/delivery/approvals/") }
	re2.reconcile()
	if c := condition(re2.getStage()); c.Status != "False" || c.Reason != ReasonRegistryUnavailable {
		t.Fatalf("condition %+v", c)
	}
	// No approvals repository: nothing to anchor.
	re3 := newRecEnv(t)
	re3.rec.Store.Approvals = ""
	re3.stage(0)
	re3.reconcile()
	if s := re3.getStage(); s.Status.Stream.Approvals != "" || condition(s).Status != "True" {
		t.Fatalf("stream = %+v", s.Status.Stream)
	}
}

// TestApprovalRecorderBacksOff: every failure writes nothing and looks again
// after Poll; a missing operation or Stage is nothing to do.
func TestApprovalRecorderBacksOff(t *testing.T) {
	ctx := context.Background()
	req := reconcile.Request{NamespacedName: keyOf("p1")}
	re := newRecEnv(t)
	ar := &ApprovalRecorder{Recorder: re.rec}
	if res, err := ar.Reconcile(ctx, req); err != nil || res.RequeueAfter != 0 {
		t.Fatalf("a missing operation: %+v %v", res, err)
	}
	for name, tc := range map[string]struct {
		setup func(*recEnv, *ApprovalRecorder)
		want  time.Duration
	}{
		"guard unreadable": {func(re *recEnv, _ *ApprovalRecorder) { re.fails = fails{"get/*v1.ValidatingAdmissionPolicy": true} }, time.Second},
		"stage missing":    {func(re *recEnv, _ *ApprovalRecorder) { _ = re.c.Delete(ctx, re.getStage()) }, 0},
		"stage unreadable": {func(re *recEnv, _ *ApprovalRecorder) { re.fails = fails{"get/Stage": true} }, time.Second},
		"stream not ready": {func(re *recEnv, _ *ApprovalRecorder) {
			re.setStage(func(s *delivery.StageStatus) { s.Stream.Anchor = "" })
		}, time.Second},
		"registry down": {func(re *recEnv, _ *ApprovalRecorder) { re.reg.srv.Close() }, time.Second},
		"signer down":   {func(re *recEnv, _ *ApprovalRecorder) { re.rec.Signer = failingSigner{} }, time.Second},
		"push refused": {func(re *recEnv, _ *ApprovalRecorder) {
			re.reg.fault = from(1, http.MethodPut, "/delivery/streams/manifests/")
		}, time.Second},
		"intent not stored": {func(re *recEnv, _ *ApprovalRecorder) { re.fails = fails{"status/Operation": true} }, time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			re, ar, _ := heldRecEnv(t)
			tc.setup(re, ar)
			res, err := ar.Reconcile(ctx, req)
			if err != nil || res.RequeueAfter != tc.want {
				t.Fatalf("%+v %v", res, err)
			}
			if o := re.getOp("p1"); o.Status.IntentRecord != nil {
				t.Fatalf("a failed pass wrote %+v", o.Status.IntentRecord)
			}
		})
	}
	for name, setup := range map[string]func(*recEnv, *ApprovalRecorder){
		"approvals unlistable": func(re *recEnv, _ *ApprovalRecorder) { re.fails = fails{"list/Approval": true} },
		"approval signer down": func(re *recEnv, _ *ApprovalRecorder) { re.rec.Signer = failingSigner{} },
		"approval push refused": func(re *recEnv, _ *ApprovalRecorder) {
			re.reg.fault = from(1, http.MethodPut, "/delivery/streams/manifests/")
		},
		"approval not stored": func(re *recEnv, _ *ApprovalRecorder) { re.fails = fails{"status/Approval": true} },
	} {
		t.Run(name, func(t *testing.T) {
			re, ar, o := heldRecEnv(t)
			re.recordApprovals(ar) // the intent is recorded first
			re.approve(o, "ok", carol, delivery.Approve)
			setup(re, ar)
			if res, err := ar.Reconcile(ctx, req); err != nil || res.RequeueAfter != time.Second {
				t.Fatalf("%+v %v", res, err)
			}
			if a := re.getApproval("ok"); a.Status.Record != nil {
				t.Fatalf("a failed pass wrote %+v", a.Status)
			}
		})
	}
}

// TestApprovalToOperation: an Approval wakes the operation it names.
func TestApprovalToOperation(t *testing.T) {
	got := approvalToOperation(context.Background(), &dv1.Approval{ObjectMeta: metav1.ObjectMeta{Namespace: ns},
		Spec: delivery.ApprovalSpec{Operation: "p1"}})
	if len(got) != 1 || got[0].NamespacedName != keyOf("p1") {
		t.Fatalf("requests = %+v", got)
	}
}

// TestApprovalWaitsForItsRecord: with the recorder on, a person's Approval
// counts only once the recorder wrote its signed record, and the counted
// approval names that record and who signed it.
func TestApprovalWaitsForItsRecord(t *testing.T) {
	e, o := heldEnv(t)
	e.op.Records = true
	e.approveAs(o, "ok", carol, []string{"team-checkout"}, delivery.Approve)
	o = e.drive("p1")
	want := "Approval ok by " + carol + " waits for its signed record: the recorder has not written it yet"
	if o.Status.Phase != delivery.PhaseHeld || len(o.Status.Approvals) != 0 || o.Status.Message != want {
		t.Fatalf("an approval without a record counted: %s %q", o.Status.Phase, o.Status.Message)
	}
	a := e.getApproval("ok")
	a.Status.Record = &delivery.ApprovalRecordRef{Digest: "sha256:ab", Ref: "reg/streams@sha256:cd"}
	if err := e.c.Status().Update(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	o = e.drive("p1")
	if len(o.Status.Approvals) != 1 {
		t.Fatalf("a recorded approval did not count: %s %q", o.Status.Phase, o.Status.Message)
	}
	got := o.Status.Approvals[0]
	if got.Record != "sha256:ab" || got.Signer == nil || got.Signer.Kind != delivery.SignerRecorder ||
		got.Signer.OnBehalfOf.User != carol || got.Signer.OnBehalfOf.Verified != delivery.VerifiedByAdmission {
		t.Fatalf("counted approval = %+v", got)
	}
}

// TestApprovalWithoutRecorderCountsAsBefore: without the recorder an
// Approval counts as it always has, with no record named.
func TestApprovalWithoutRecorderCountsAsBefore(t *testing.T) {
	e, o := heldEnv(t)
	e.approveAs(o, "ok", carol, []string{"team-checkout"}, delivery.Approve)
	o = e.drive("p1")
	if len(o.Status.Approvals) != 1 || o.Status.Approvals[0].Record != "" || o.Status.Approvals[0].Signer != nil {
		t.Fatalf("approvals = %+v (%q)", o.Status.Approvals, o.Status.Message)
	}
}

// TestNoApprovalCountsWithoutRecordsPolicy: with the recorder on, the
// records policy is one the promoter is guarded by; without it nobody can
// tell who wrote status.record, so no approval counts, recorded or not.
func TestNoApprovalCountsWithoutRecordsPolicy(t *testing.T) {
	e, o := heldEnv(t)
	e.op.Records = true
	e.op.AdmissionPolicies = append(e.op.AdmissionPolicies, "pd-records")
	e.approveAs(o, "ok", carol, []string{"team-checkout"}, delivery.Approve)
	a := e.getApproval("ok")
	a.Status.Record = &delivery.ApprovalRecordRef{Digest: "sha256:ab", Ref: "reg/streams@sha256:cd"}
	if err := e.c.Status().Update(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if o = e.drive("p1"); o.Status.Reason != ReasonUnguarded || len(o.Status.Approvals) != 0 {
		t.Fatalf("an approval counted without the records policy: %s %q %+v", o.Status.Reason, o.Status.Message, o.Status.Approvals)
	}
	e.create(&admissionv1.ValidatingAdmissionPolicy{ObjectMeta: metav1.ObjectMeta{Name: "pd-records"}},
		&admissionv1.ValidatingAdmissionPolicyBinding{ObjectMeta: metav1.ObjectMeta{Name: "pd-records"},
			Spec: admissionv1.ValidatingAdmissionPolicyBindingSpec{PolicyName: "pd-records",
				ValidationActions: []admissionv1.ValidationAction{admissionv1.Deny}}})
	if o = e.drive("p1"); len(o.Status.Approvals) != 1 {
		t.Fatalf("a recorded approval did not count once guarded: %s %q", o.Status.Phase, o.Status.Message)
	}
}

// TestOperationRecordNamesTheApprovalRecord: the record of an operation an
// approval let through names that approval's own record and that the
// recorder signed it on the approver's behalf, as admission verified them.
func TestOperationRecordNamesTheApprovalRecord(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	o := re.concluded("ship", 1, delivery.Succeeded)
	re.approve(o, "ok", carol, delivery.Approve)
	signer := &delivery.ApprovalSigner{Kind: delivery.SignerRecorder,
		OnBehalfOf: &delivery.ApprovalBehalf{User: carol, Verified: delivery.VerifiedByAdmission}}
	o = re.getOp("ship")
	o.Status.Approvals = []delivery.ApprovalRecord{{Name: "ok", By: carol, Decision: delivery.Approve, Record: "sha256:ab", Signer: signer}}
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	re.reconcile()
	recs := re.records()
	if len(recs) != 1 || len(recs[0].Statement.Predicate.Approvals) != 1 {
		t.Fatalf("records = %+v", recs)
	}
	a := recs[0].Statement.Predicate.Approvals[0]
	if a.Record != "sha256:ab" || a.Signer.Kind != delivery.SignerRecorder || a.Signer.OnBehalfOf.User != carol ||
		a.Signer.OnBehalfOf.Verified != delivery.VerifiedByAdmission {
		t.Fatalf("record approval = %+v", a)
	}
}
