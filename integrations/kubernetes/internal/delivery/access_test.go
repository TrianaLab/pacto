/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/tools/record"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// carol is a person no test operation names as its asker.
const carol = "carol@example.com"

// heldEnv holds a promote that breaks checkout, asked of team-checkout.
func heldEnv(t *testing.T) (*env, *dv1.Operation) {
	t.Helper()
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dC, bot))
	e.create(pactoObj("checkout", "checkout", c3, "Compliant", 1,
		map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "required": true, "compatibility": "^1.0.0"}))
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld {
		t.Fatalf("want held: %s %s", o.Status.Phase, o.Status.Message)
	}
	return e, o
}

func TestHeldApprovalDeniedBySAR(t *testing.T) {
	e, o := heldEnv(t)
	e.rights[carol] = false
	e.approveAs(o, "ok", carol, []string{"team-checkout"}, delivery.Approve)
	o = e.drive("p1")
	want := "Approval ok by " + carol + " does not count: " + carol + " cannot update deployments/payments in prod, "
	if o.Status.Phase != delivery.PhaseHeld || len(o.Status.Approvals) != 0 || !strings.HasPrefix(o.Status.Message, want) {
		t.Fatalf("a denied review never counts: %s %q %+v", o.Status.Phase, o.Status.Message, o.Status.Approvals)
	}
}

func TestHeldApprovalAllowedRecordsAccess(t *testing.T) {
	e, o := heldEnv(t)
	e.approveAs(o, "ok", carol, []string{"team-checkout"}, delivery.Approve)
	o = e.drive("p1")
	if len(o.Status.Approvals) != 1 {
		t.Fatalf("an allowed review counts: %s %q", o.Status.Phase, o.Status.Message)
	}
	a := o.Status.Approvals[0]
	if a.ClassifiedBy != delivery.ClassByUser || a.Access != "update deployments/payments in prod" ||
		a.AccessSource != delivery.AccessBySubjectReview || a.AccessAt != ts(e.now) {
		t.Fatalf("record: %+v", a)
	}
	r := e.reviews[len(e.reviews)-1]
	ra := r.ResourceAttributes
	if r.User != carol || len(r.Groups) != 1 || r.Groups[0] != "team-checkout" || ra.Verb != "update" || ra.Group != "apps" ||
		ra.Resource != "deployments" || ra.Name != "payments" || ra.Namespace != ns {
		t.Fatalf("review: %+v %+v", r, ra)
	}
}

func TestHeldApprovalReviewErrorUnverified(t *testing.T) {
	e, o := heldEnv(t)
	e.fails["create/SubjectAccessReview"] = true
	e.approveAs(o, "ok", carol, []string{"team-checkout"}, delivery.Approve)
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "Pacto could not ask whether "+carol) {
		t.Fatalf("a failed review never counts: %s %q", o.Status.Phase, o.Status.Message)
	}
}

func TestAccessUnknownKindUnverified(t *testing.T) {
	st := stageObj(dA)
	st.Spec.Workload = &delivery.WorkloadRef{Kind: "Rollout", Name: "payments"}
	e := baseEnv(t, nil)
	if got, err := e.op.access(context.Background(), delivery.Identity{User: carol}, st); got.Unverified == "" || got.Allowed || err != nil || len(e.reviews) != 0 {
		t.Fatalf("an unknown kind is unverified and never asked: %+v", got)
	}
}

func TestHeldAfterApplyAcceptUnknownNeedsUpdateRights(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.now = e.now.Add(2 * time.Minute)
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || !o.Status.Applied {
		t.Fatalf("want held after apply: %s", o.Status.Phase)
	}
	e.rights[carol] = false
	e.approve(e.getOp("p1"), "au", carol, delivery.AcceptUnknown)
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "cannot update") {
		t.Fatalf("an accept-unknown without update rights does not count: %s %q", o.Status.Phase, o.Status.Message)
	}
}

func TestHeldEmitsTeamNarrowingOff(t *testing.T) {
	for _, narrowing := range []bool{false, true} {
		e := baseEnv(t, nil)
		rec := record.NewFakeRecorder(100)
		e.op.Events, e.op.Policy.TeamNarrowing = rec, narrowing
		e.create(opObj("p1", delivery.TypePromote, dA, dC, bot))
		e.create(pactoObj("checkout", "checkout", c3, "Compliant", 1,
			map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "required": true, "compatibility": "^1.0.0"}))
		if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld {
			t.Fatalf("want held: %s", o.Status.Phase)
		}
		n := 0
		for _, ev := range drain(rec) {
			if strings.HasPrefix(ev, "Normal TeamNarrowingOff owner teams do not narrow who may answer: whoever may update deployments/payments in prod decides") {
				n++
			}
		}
		if want := map[bool]int{false: 1, true: 0}[narrowing]; n != want {
			t.Errorf("narrowing %v: %d TeamNarrowingOff Events, want %d", narrowing, n, want)
		}
	}
}

func TestUnfreezeNeedsUpdateRights(t *testing.T) {
	e := baseEnv(t, nil)
	e.setStage(func(s *delivery.StageStatus) { s.Frozen, s.FrozenReason = true, "Errored" })
	e.rights["frank"] = false
	e.create(opObj("u1", delivery.TypeUnfreeze, "", "", "frank", "team-payments"))
	u1 := e.drive("u1")
	wantOutcome(t, u1, delivery.Rejected, "CannotUpdateWorkload")
	if !strings.Contains(u1.Status.Message, "frank cannot update deployments/payments in prod, so they may not unfreeze it") {
		t.Fatalf("message: %q", u1.Status.Message)
	}
	// A review that failed is retried, never a final rejection.
	e.fails["create/SubjectAccessReview"] = true
	e.create(opObj("u2", delivery.TypeUnfreeze, "", "", "alice", "team-payments"))
	if err := e.reconcileOpErr("u2"); err == nil {
		t.Fatal("a failed review must requeue")
	}
	if o := e.getOp("u2"); o.Status.Outcome != "" {
		t.Fatalf("a failed review concluded the unfreeze: %s %q", o.Status.Outcome, o.Status.Message)
	}
	e.fails["create/SubjectAccessReview"] = false
	wantOutcome(t, e.drive("u2"), delivery.Succeeded, "Unfrozen")
}

func TestAccessEvaluationErrorUnverified(t *testing.T) {
	e, o := heldEnv(t)
	e.rights[carol], e.evalError = false, "webhook authorizer timed out"
	e.approveAs(o, "ok", carol, []string{"team-checkout"}, delivery.Approve)
	o = e.drive("p1")
	if len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "Pacto could not ask whether "+carol+" may update deployments/payments in prod: the SubjectAccessReview could not be evaluated: webhook authorizer timed out") {
		t.Fatalf("a refusal with an evaluation error is no answer: %q", o.Status.Message)
	}
	e.rights[carol] = true
	if a, err := e.op.access(context.Background(), delivery.Identity{User: carol}, e.getStage()); !a.Allowed || a.Unverified != "" || err != nil {
		t.Fatalf("an allow with an evaluation error is an allow: %+v %v", a, err)
	}
}

func TestHeldIneligibleApprovalsCostNoReview(t *testing.T) {
	e, o := heldEnv(t)
	e.approveAs(o, "own", bot, nil, delivery.Approve)
	e.approveAs(o, "wrong", carol, []string{"team-checkout"}, delivery.Decision("maybe"))
	e.reviews = nil
	for range 3 {
		e.reconcileOp("p1")
	}
	if len(e.reviews) != 0 {
		t.Fatalf("approvals that cannot count asked %d reviews", len(e.reviews))
	}
}

func TestHeldUnknownKindWarnsNoApprovalCounts(t *testing.T) {
	e, o := heldEnv(t)
	rec := record.NewFakeRecorder(100)
	e.op.Events = rec
	st := e.getStage()
	st.Spec.Workload = &delivery.WorkloadRef{Kind: "Rollout", Name: "payments"}
	if _, err := e.op.saveHeld(context.Background(), o, st); err != nil {
		t.Fatal(err)
	}
	var warned, off bool
	for _, ev := range drain(rec) {
		warned = warned || strings.HasPrefix(ev, "Warning ApproverUnverified no approval can count: the workload kind Rollout is none Pacto knows how to ask about")
		off = off || strings.Contains(ev, "TeamNarrowingOff")
	}
	if !warned || off {
		t.Fatalf("warned %v, TeamNarrowingOff %v", warned, off)
	}
}

func TestRecordKeepsApprovalAccess(t *testing.T) {
	re := newRecEnv(t)
	re.stage(1)
	o := re.concluded("ship", 1, delivery.Succeeded)
	re.approve(o, "ok", carol, delivery.Approve)
	o.Status.Approvals = []delivery.ApprovalRecord{{Name: "ok", By: carol, Decision: delivery.Approve, ClassifiedBy: delivery.ClassByUser,
		Access: "update deployments/payments in prod", AccessSource: delivery.AccessBySubjectReview, AccessAt: ts(re.now)}}
	if err := re.c.Status().Update(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	re.reconcile()
	recs := re.records()
	if len(recs) != 1 || len(recs[0].Statement.Predicate.Approvals) != 1 {
		t.Fatalf("records: %+v", recs)
	}
	a := recs[0].Statement.Predicate.Approvals[0]
	if a.Access != "update deployments/payments in prod" || a.AccessSource != delivery.AccessBySubjectReview || a.AccessAt == "" || a.ClassifiedBy != delivery.ClassByUser {
		t.Fatalf("approval: %+v", a)
	}
}

func TestAcknowledgeNeedsUpdateRights(t *testing.T) {
	e := baseEnv(t, nil)
	e.rights["frank"] = false
	ack := opObj("a1", delivery.TypeAcknowledge, "", "", "frank", "team-payments")
	ack.Status.ActorClass = delivery.ClassHuman
	why, _ := e.op.unfreezeRefusal(context.Background(), ack, e.getStage())
	if !strings.HasPrefix(why, "CannotUpdateWorkload: frank cannot update deployments/payments in prod, so they may not acknowledge its upstream stand-ins") {
		t.Fatalf("%q", why)
	}
	e.evalError = "webhook authorizer timed out"
	if why, err := e.op.unfreezeRefusal(context.Background(), ack, e.getStage()); !strings.HasPrefix(why, "ApproverUnverified: Pacto could not ask whether frank") || err != nil {
		t.Fatalf("an evaluation error refuses as unverified: %q %v", why, err)
	}
	ack.Spec.Actor.User = carol
	if why, err := e.op.unfreezeRefusal(context.Background(), ack, e.getStage()); why != "" || err != nil {
		t.Fatalf("allowed: %q", why)
	}
}
