/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// baseEnv is a prod Stage running A, its OCIRepository pinned to A and applied,
// and the payments workload, with op created.
func baseEnv(t *testing.T, op *dv1Op) *env {
	t.Helper()
	e := newEnv(t, stageObj(dA), ociRepo("payments", dA, "latest@"+dA, true),
		kustomization("payments", "payments", "True", "ReconciliationSucceeded", "latest@"+dA, "latest@"+dA),
		deployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	if op != nil {
		e.create(op)
	}
	return e
}

// TestPromoteShipsAndVerifies is acceptance items 1 and 3 at unit level: a
// change nothing depends on moves, Flux applies it, the contract check passes
// for exactly that version and the Stage remembers it as verified.
func TestPromoteShipsAndVerifies(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseRunning || !o.Status.Applied || e.ociDigest("payments") != dB {
		t.Fatalf("want Running with the digest written, got %s applied=%v digest=%s (%s)", o.Status.Phase, o.Status.Applied, e.ociDigest("payments"), o.Status.Message)
	}
	if e.reg.hosts[0] != "reg.example" {
		t.Fatalf("an insecure OCIRepository must reach its host over HTTP: %v", e.reg.hosts)
	}
	if s := e.getStage(); s.Status.Pointer != dB || s.Status.Locks["pointer"] != "uid-p1" || s.Status.LastOp != "uid-p1" {
		t.Fatalf("stage after the pointer CAS: %+v", s.Status)
	}
	e.apply(dB)
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseVerifying {
		t.Fatalf("want Verifying once Flux applied B, got %s (%s)", o.Status.Phase, o.Status.Message)
	}
	// Item 8: the Pacto still reports the OLD contract, so its Compliant says nothing about B.
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseVerifying || !strings.Contains(o.Status.Message, "last evaluated contract") {
		t.Fatalf("a result for the old version must not verify the new one: %s %s", o.Status.Phase, o.Status.Message)
	}
	e.evaluate(c2, "Compliant")
	o = e.drive("p1")
	wantOutcome(t, o, delivery.Succeeded, "Verified")
	if o.Status.Verification != delivery.Verified || o.Status.Seq != 1 {
		t.Fatalf("verification %s seq %d", o.Status.Verification, o.Status.Seq)
	}
	s := e.getStage()
	if s.Status.Locks["pointer"] != "" || s.Status.Verified[len(s.Status.Verified)-1] != dB || s.Status.Stream.Seq != 1 || s.Status.AutoHold {
		t.Fatalf("stage after conclusion: %+v", s.Status)
	}
	// A concluded operation keeps its record finalizer until it is deleted.
	if err := e.c.Delete(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	e.reconcileOp("p1")
	if err := e.c.Get(context.Background(), keyOf("p1"), o); err == nil {
		t.Fatal("a deleted concluded operation must lose its finalizer and go")
	}
}

// TestSquattedRollbackNameFreezes: an operation someone else created under the
// name of the rollback a failed promote raises never stands in for it. The
// failed version is not left running unwatched: the Stage freezes, naming the
// squatter.
func TestSquattedRollbackNameFreezes(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	squat := opObj("p1-rollback", delivery.TypePromote, dC, dC, bot)
	e.create(squat)
	wantOutcome(t, e.drive("p1-rollback"), delivery.Rejected, "")
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	wantOutcome(t, e.drive("p1"), delivery.Failed, "VerifyFailed")
	s := e.getStage()
	if !s.Status.Frozen || !strings.Contains(s.Status.FrozenReason, "p1-rollback is taken by a "+delivery.TypePromote+" "+bot) {
		t.Fatalf("a squatted rollback name must freeze the Stage: %+v", s.Status)
	}
}

// TestFailedVerifyRollsBackOnceAndNeverRetries is acceptance item 2: the
// broken version fails its check, is remembered as failed, and exactly one
// bound rollback returns the Stage to the version it ran before.
func TestFailedVerifyRollsBackOnceAndNeverRetries(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Failed, "VerifyFailed")
	s := e.getStage()
	if s.Status.FailedDigests[0] != dB || s.Status.AutoRollbacks["uid-p1"] != 1 || s.Status.Frozen {
		t.Fatalf("stage after the failure: %+v", s.Status)
	}
	rb := e.getOp("p1-rollback")
	if rb.Spec.Type != delivery.TypeRollback || rb.Spec.Params.From != dB || rb.Spec.Params.To != dA || rb.Spec.Params.Undoes != "uid-p1" ||
		rb.Spec.Cause.Operation != "uid-p1" || rb.Spec.Actor.User != promoterSA {
		t.Fatalf("rollback spec: %+v", rb.Spec)
	}
	// Raising again is a no-op: one rollback per cause.
	if squat, err := raiseRollback(context.Background(), e.c, e.op.Policy, o, s, "again", nil); err != nil || squat != "" {
		t.Fatal(squat, err)
	}
	rb = e.drive("p1-rollback")
	if rb.Status.Phase != delivery.PhaseRunning || e.ociDigest("payments") != dA {
		t.Fatalf("rollback should be running with A written: %s %s", rb.Status.Phase, rb.Status.Message)
	}
	e.apply(dA)
	e.evaluate(c1, "Compliant")
	rb = e.drive("p1-rollback")
	wantOutcome(t, rb, delivery.Succeeded, "Verified")
	if s := e.getStage(); s.Status.Pointer != dA || s.Status.AutoHold || s.Status.Frozen {
		t.Fatalf("an automatic rollback leaves no hold: %+v", s.Status)
	}
	// Item 2, "never retried": a new promote of B is refused.
	e.create(opObj("p2", delivery.TypePromote, dA, dB, bot))
	wantOutcome(t, e.drive("p2"), delivery.Rejected, "FailedDigest")
}

// TestFailedPromoteOverAFailedVersionFreezes: when the version a failed
// promote would roll back to failed here too, no rollback is raised and the
// Stage freezes.
func TestFailedPromoteOverAFailedVersionFreezes(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.setStage(func(s *delivery.StageStatus) { s.FailedDigests = []string{dA} })
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	wantOutcome(t, e.drive("p1"), delivery.Failed, "VerifyFailed")
	if s := e.getStage(); !s.Status.Frozen || !strings.Contains(s.Status.FrozenReason, "nothing safe to roll back to") || len(s.Status.AutoRollbacks) != 0 {
		t.Fatalf("want frozen with no rollback: %+v", s.Status)
	}
	if err := e.c.Get(context.Background(), keyOf("p1-rollback"), &dv1Op{}); err == nil {
		t.Fatal("no rollback may target a failed version")
	}
}

// TestRefusedAutomaticRollbackFreezes: a rollback the promoter raised that is
// refused freezes the Stage rather than leave the failed version running.
func TestRefusedAutomaticRollbackFreezes(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	e.drive("p1")
	e.setStage(func(s *delivery.StageStatus) { s.Pointer = dC }) // something moved it first
	wantOutcome(t, e.drive("p1-rollback"), delivery.Rejected, "PointerMoved")
	if s := e.getStage(); !s.Status.Frozen || !strings.Contains(s.Status.FrozenReason, "automatic rollback was refused") {
		t.Fatalf("a refused automatic rollback must freeze: %+v", s.Status)
	}
}

// TestFailedRollbackFreezes: a second Fail for the same cause freezes instead of
// rolling back again.
func TestFailedRollbackFreezes(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	e.drive("p1")
	e.drive("p1-rollback")
	e.apply(dA)
	e.evaluate(c1, "Invalid")
	wantOutcome(t, e.drive("p1-rollback"), delivery.Failed, "VerifyFailed")
	if s := e.getStage(); !s.Status.Frozen || !strings.Contains(s.Status.FrozenReason, "rollback failed") {
		t.Fatalf("a failed rollback must freeze: %+v", s.Status)
	}
}

// TestBreakingChangeWaitsForTheConsumersTeam is acceptance item 4: a change a
// deployed consumer does not accept is held with a question routed to that
// consumer's owners, and moves once a person approves.
func TestBreakingChangeWaitsForTheConsumersTeam(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dC, bot))
	e.create(pactoObj("checkout", "checkout", c3, "Compliant", 1,
		map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "required": true, "compatibility": "^1.0.0"}))
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Floor.Decision != delivery.Approve ||
		strings.Join(o.Status.Plan.Floor.Owners, ",") != "team-checkout" || !strings.Contains(o.Status.Question, "team-checkout") {
		t.Fatalf("want Held for team-checkout, got %s %+v %q", o.Status.Phase, o.Status.Plan.Floor, o.Status.Question)
	}
	// Approvals that do not count: the actor itself, an agent, an unbound uid,
	// a stale login, and accept-unknown where an approval is needed.
	e.approve(o, "self", bot, delivery.Approve)
	e.approve(o, "agent", "system:serviceaccount:agents:other", delivery.Approve)
	stale := &dv1Approval{}
	stale.Namespace, stale.Name = ns, "stale"
	stale.CreationTimestamp = metaNow(e.now)
	stale.Spec = delivery.ApprovalSpec{Operation: o.Name, OperationUID: string(o.UID), IntentDigest: o.Spec.IntentDigest,
		Decision: delivery.Approve, By: "alice", Groups: []string{"team-checkout"}, AuthTime: e.now.Add(-time.Hour).Format(time.RFC3339)}
	e.create(stale)
	e.approve(o, "wronguid", "alice", delivery.Approve)
	wrong := e.getApproval("wronguid")
	wrong.Spec.OperationUID = "other"
	_ = e.c.Delete(context.Background(), wrong)
	wrong.ResourceVersion = ""
	e.create(wrong)
	e.approve(o, "au", "alice", delivery.AcceptUnknown)
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || !strings.Contains(o.Status.Message, "needs an approval") {
		t.Fatalf("none of those may move it: %s %s", o.Status.Phase, o.Status.Message)
	}
	// Nor may a person outside the named team, or one in an agent group.
	e.approveAs(o, "notteam", "dave", []string{"team-payments"}, delivery.Approve)
	e.approveAs(o, "agentgroup", "erin", []string{"team-checkout", "pacto:agents"}, delivery.Approve)
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseHeld {
		t.Fatalf("only team-checkout may answer: %s %+v", o.Status.Phase, o.Status.Approvals)
	}
	e.approveAs(o, "ok", "bob", []string{"team-checkout"}, delivery.Approve)
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseRunning || len(o.Status.Approvals) != 2 || o.Status.Approvals[1].By != "bob" {
		t.Fatalf("an owner's approval must move it: %s %+v", o.Status.Phase, o.Status.Approvals)
	}
}

// TestApprovedHeldWaitsWithoutASecondApproval: an approved change that finds
// the Stage frozen keeps its approval and moves once the freeze lifts.
func TestApprovedHeldWaitsWithoutASecondApproval(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dC, bot))
	e.create(pactoObj("checkout", "checkout", c3, "Compliant", 1,
		map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "required": true, "compatibility": "^1.0.0"}))
	o := e.drive("p1")
	e.setStage(func(s *delivery.StageStatus) { s.Frozen, s.FrozenReason = true, "a person froze it" })
	e.approveAs(o, "ok", "bob", []string{"team-checkout"}, delivery.Approve)
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Reason != "Waiting" || len(o.Status.Approvals) != 0 || !strings.Contains(o.Status.Message, "bob approved") {
		t.Fatalf("want waiting with the approval kept: %s %s %q %+v", o.Status.Phase, o.Status.Reason, o.Status.Message, o.Status.Approvals)
	}
	e.setStage(func(s *delivery.StageStatus) { s.Frozen, s.FrozenReason = false, "" })
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseRunning || len(o.Status.Approvals) != 1 || o.Status.Approvals[0].By != "bob" {
		t.Fatalf("the one approval moves it: %s %+v", o.Status.Phase, o.Status.Approvals)
	}
}

// TestUnknownWaits is acceptance item 5: a contract Pacto cannot read holds the
// change for accept-unknown; it never ships on its own.
func TestUnknownWaits(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	delete(e.reg.bundles, contractRef(c2))
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Floor.Decision != delivery.AcceptUnknown {
		t.Fatalf("want Held for accept-unknown: %s %+v", o.Status.Phase, o.Status.Plan)
	}
	e.approve(o, "no", "alice", delivery.Reject)
	wantOutcome(t, e.drive("p1"), delivery.Rejected, "RejectedByHuman")
	if e.getStage().Status.Pointer != dA {
		t.Fatal("a rejected operation must not move anything")
	}
}

// TestApprovedButRecheckChanged: between the hold and the approval a gate
// stopped passing, so the approval does not carry over.
func TestApprovedButRecheckChanged(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dC, bot))
	e.create(pactoObj("checkout", "checkout", c3, "Compliant", 1,
		map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "required": true, "compatibility": "^1.0.0"}))
	o := e.drive("p1")
	e.approve(o, "ok", "alice", delivery.Approve)
	e.approve(o, "dup", "carol", delivery.Approve) // a second click, never consumed
	delete(e.reg.contracts, dA)                    // the running contract can no longer be read
	e.now = e.now.Add(time.Minute)
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Reason != "RecheckChanged" {
		t.Fatalf("want back to Held: %s %s", o.Status.Phase, o.Status.Reason)
	}
	// The leftover approval answered the old question, not this one.
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || len(o.Status.Approvals) != 1 {
		t.Fatalf("a leftover approval must not clear the new question: %s %+v", o.Status.Phase, o.Status.Approvals)
	}
	// Now the to contract names another service: the re-check is a Fail.
	e.reg.contracts[dA] = contractRef(c1)
	e.approve(o, "ok2", "alice", delivery.Approve)
	e.reg.bundles[contractRef(c3)].Contract.Service.Name = "orders"
	wantOutcome(t, e.drive("p1"), delivery.Rejected, "GateFailed")
}

// TestTwoAgentsOneWins is acceptance item 6: two promotes raised at once; the
// older is superseded, and a promote that loses the pointer CAS is refused.
func TestTwoAgentsOneWins(t *testing.T) {
	older, newer := opObj("p1", delivery.TypePromote, dA, dB, bot), opObj("p2", delivery.TypePromote, dA, dC, "system:serviceaccount:agents:two")
	newer.CreationTimestamp = metaNow(older.CreationTimestamp.Add(time.Second))
	e := baseEnv(t, older)
	e.create(newer)
	e.create(pactoObj("orders", "orders", c3, "Compliant", 1))
	e.reconcileOp("p1") // admit
	e.reconcileOp("p2") // admit
	wantOutcome(t, e.drive("p1"), delivery.Superseded, "Superseded")
	if o := e.drive("p2"); o.Status.Phase != delivery.PhaseRunning {
		t.Fatalf("the newer promote must win: %s %s", o.Status.Phase, o.Status.Message)
	}
	// A third promote planned against A loses: the pointer is C now.
	late := opObj("p3", delivery.TypePromote, dA, dB, bot)
	e.create(late)
	wantOutcome(t, e.drive("p3"), delivery.Rejected, "PointerMoved")
}

// TestLockWaitsAndPointerMovedAtLock covers the lock CAS: a held lock makes a
// promote wait, a frozen Stage makes it wait, and a pointer that moved between
// plan and lock refuses it.
func TestLockWaitsAndPointerMovedAtLock(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.create(opObj("holder", delivery.TypePromote, dA, dC, bot))
	holder := e.getOp("holder")
	holder.Status.Phase = delivery.PhaseRunning
	if err := e.c.Status().Update(context.Background(), holder); err != nil {
		t.Fatal(err)
	}
	e.setStage(func(s *delivery.StageStatus) { s.Locks = map[string]string{"pointer": "uid-holder"} })
	e.reconcileOp("p1")
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhasePending || !strings.Contains(o.Status.Message, "uid-holder") {
		t.Fatalf("want waiting for the lock: %s %s", o.Status.Phase, o.Status.Message)
	}
	// The holder concluded without releasing: its lock is stale and free.
	holder = e.getOp("holder")
	holder.Status.Phase = delivery.PhaseConcluded
	if err := e.c.Status().Update(context.Background(), holder); err != nil {
		t.Fatal(err)
	}
	e.setStage(func(s *delivery.StageStatus) { s.Frozen, s.FrozenReason = true, "manual" })
	if o = e.drive("p1"); o.Status.Phase != delivery.PhasePending || !strings.Contains(o.Status.Message, "StageFrozen") {
		t.Fatalf("want waiting on the freeze: %s %s", o.Status.Phase, o.Status.Message)
	}
	// A freeze that lands between the plan and the lock is caught by the CAS.
	if _, err := e.op.lock(context.Background(), o, e.getStage(), false); err != nil {
		t.Fatal(err)
	}
	if o = e.getOp("p1"); !strings.Contains(o.Status.Message, "the Stage is frozen") {
		t.Fatalf("want the lock to wait on the freeze: %s", o.Status.Message)
	}
	e.setStage(func(s *delivery.StageStatus) { s.Frozen = false; s.Pointer = dC; s.Verified = []string{dC} })
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseConcluded {
		t.Fatalf("want refused: %+v", o.Status)
	}
}

// TestHumanRollbackPreemptsAndHolds is acceptance item 7: a person's rollback
// preempts a verifying promote and puts an auto-hold on the Stage, so
// automation does not undo it.
func TestHumanRollbackPreemptsAndHolds(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.drive("p1")
	e.apply(dB)
	e.drive("p1") // verifying, still on the old evaluation
	rb := opObj("undo", delivery.TypeRollback, dB, dA, "alice")
	rb.Spec.Params.Undoes = "uid-p1"
	rb.Spec.IntentDigest = delivery.IntentDigest(rb.Spec, stageObj("").Spec)
	e.create(rb)
	if o := e.drive("undo"); o.Status.Phase != delivery.PhaseRunning {
		t.Fatalf("a rollback preempts a verifying promote: %s %s", o.Status.Phase, o.Status.Message)
	}
	wantOutcome(t, e.drive("p1"), delivery.Succeeded, "Preempted")
	e.apply(dA)
	e.evaluate(c1, "Compliant")
	wantOutcome(t, e.drive("undo"), delivery.Succeeded, "Verified")
	if s := e.getStage(); !s.Status.AutoHold || s.Status.Pointer != dA {
		t.Fatalf("a person's rollback must hold automation: %+v", s.Status)
	}
	// A person promoting a new version again lifts the hold.
	e.create(opObj("again", delivery.TypePromote, dA, dC, "alice"))
	e.create(pactoObj("orders", "orders", c3, "Compliant", 1))
	e.drive("again")
	e.apply(dC)
	e.drive("again")
	e.evaluate(c3, "Compliant")
	wantOutcome(t, e.drive("again"), delivery.Succeeded, "Verified")
	if e.getStage().Status.AutoHold {
		t.Fatal("a human promote lifts the auto-hold")
	}
}

// TestAdmissionRefusals covers every admit-time refusal.
func TestAdmissionRefusals(t *testing.T) {
	stale := opObj("stale", delivery.TypePromote, dA, dB, bot)
	stale.Spec.IntentDigest = "sha256:0"
	facet := opObj("facet", delivery.TypePromote, dA, dB, bot)
	facet.Spec.Facet = delivery.FacetFreeze
	facet.Spec.IntentDigest = delivery.IntentDigest(facet.Spec, stageObj("").Spec)
	unknown := opObj("unknown", "pacto.dev/scale", dA, dB, bot)
	badTo := opObj("badto", delivery.TypePromote, dA, "latest", bot)
	badFrom := opObj("badfrom", delivery.TypePromote, "v1", dB, bot)
	moved := opObj("moved", delivery.TypePromote, dC, dB, bot)
	override := opObj("override", delivery.TypeOverride, dA, dB, bot)
	unbound := opObj("unbound", delivery.TypeRollback, dA, dB, bot)
	nostage := opObj("nostage", delivery.TypePromote, dA, dB, bot)
	nostage.Spec.Stage = "missing"
	cases := map[string]string{"stale": "PlanStale", "facet": "WrongFacet", "unknown": "UnknownType", "badto": "InvalidDigest",
		"badfrom": "InvalidDigest", "moved": "PointerMoved", "override": "NotPermitted", "unbound": "UnboundRollback", "nostage": "NoStage"}
	e := baseEnv(t, nil)
	e.create(stale, facet, unknown, badTo, badFrom, moved, override, unbound, nostage)
	for name, reason := range cases {
		wantOutcome(t, e.drive(name), delivery.Rejected, reason)
	}
	if e.getStage().Status.Stream.Seq != 8 {
		t.Fatalf("every refusal but NoStage takes a seq: %d", e.getStage().Status.Stream.Seq)
	}
}

// TestDeletedMidChangeStillConcludes: an operation deleted after it moved
// the pointer keeps the record finalizer and runs to its conclusion, so its
// seq is taken and the recorder is owed a record, never a seq that waits
// forever.
func TestDeletedMidChangeStillConcludes(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.op.Records = true
	s := e.getStage()
	s.UID = "stage-1"
	if err := e.c.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	e.drive("p1")
	e.apply(dB)
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseVerifying {
		t.Fatalf("want Verifying, got %s", o.Status.Phase)
	}
	if err := e.c.Delete(context.Background(), e.getOp("p1")); err != nil {
		t.Fatal(err)
	}
	e.evaluate(c2, "Compliant")
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Succeeded, "Verified")
	if o.Status.Seq != 1 || !controllerutil.ContainsFinalizer(o, dv1.RecordFinalizer) {
		t.Fatalf("seq %d finalizers %v", o.Status.Seq, o.Finalizers)
	}
	if o.Status.StageUID != "stage-1" {
		t.Fatalf("the seq is not bound to the Stage that gave it: %q", o.Status.StageUID)
	}
}

// TestCancelAndExpire: deleting a pending operation cancels it; a held one
// expires at its deadline.
func TestCancelAndExpire(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	delete(e.reg.bundles, contractRef(c2))
	e.drive("p1") // held
	if err := e.c.Delete(context.Background(), e.getOp("p1")); err != nil {
		t.Fatal(err)
	}
	e.reconcileOp("p1")
	e.reconcileOp("p1")
	if err := e.c.Get(context.Background(), keyOf("p1"), &dv1Op{}); err == nil {
		t.Fatal("a cancelled operation must go once concluded")
	}
	e.create(opObj("p2", delivery.TypePromote, dA, dB, bot))
	e.drive("p2")
	e.now = e.now.Add(25 * time.Hour)
	wantOutcome(t, e.drive("p2"), delivery.Expired, "Expired")
}

// TestFreeze: a person freezes the Stage on the version it runs, and a
// promote then waits; an agent may not freeze, and a freeze that names a
// version the Stage no longer runs is refused.
func TestFreeze(t *testing.T) {
	e := baseEnv(t, opObj("f1", delivery.TypeFreeze, dA, "", bot))
	wantOutcome(t, e.drive("f1"), delivery.Rejected, "NotPermitted")
	e.create(opObj("f2", delivery.TypeFreeze, dB, "", "alice"))
	wantOutcome(t, e.drive("f2"), delivery.Rejected, "PointerMoved")
	f3 := opObj("f3", delivery.TypeFreeze, dA, "", "alice")
	f3.Spec.Reason = "payments is under investigation"
	f3.Spec.IntentDigest = delivery.IntentDigest(f3.Spec, stageObj("").Spec)
	e.create(f3)
	wantOutcome(t, e.drive("f3"), delivery.Succeeded, "Frozen")
	if s := e.getStage(); !s.Status.Frozen || s.Status.FrozenReason != "f3: frozen: payments is under investigation" || s.Status.Pointer != dA {
		t.Fatalf("a freeze freezes on the running version: %+v", s.Status)
	}
	e.create(opObj("p1", delivery.TypePromote, dA, dB, bot))
	if o := e.drive("p1"); o.Status.Phase != delivery.PhasePending || o.Status.Reason != "Waiting" {
		t.Fatalf("a promote waits on a frozen Stage: %s %s", o.Status.Phase, o.Status.Reason)
	}
}

// TestFrozenPromoteWaitsAndUnfreeze: a frozen Stage parks promotes; an agent
// may not unfreeze, a person may.
func TestFrozenPromoteWaitsAndUnfreeze(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.setStage(func(s *delivery.StageStatus) { s.Frozen, s.FrozenReason, s.AutoHold = true, "Errored", true })
	if o := e.drive("p1"); o.Status.Phase != delivery.PhasePending || o.Status.Reason != "Waiting" {
		t.Fatalf("want waiting: %s %s", o.Status.Phase, o.Status.Reason)
	}
	e.create(opObj("u1", delivery.TypeUnfreeze, "", "", bot))
	wantOutcome(t, e.drive("u1"), delivery.Rejected, "NotPermitted")
	e.create(opObj("u2", delivery.TypeUnfreeze, "", "", "alice"))
	u2 := e.drive("u2")
	wantOutcome(t, u2, delivery.Succeeded, "Unfrozen")
	if u2.Status.Verification != delivery.Unverified {
		t.Fatalf("an unfreeze concludes Unverified: %s", u2.Status.Verification)
	}
	if s := e.getStage(); s.Status.Frozen || s.Status.AutoHold {
		t.Fatalf("unfreeze clears the freeze and the hold: %+v", s.Status)
	}
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseRunning {
		t.Fatalf("the parked promote moves after unfreeze: %s %s", o.Status.Phase, o.Status.Message)
	}
}

// TestBreakGlassOverrideFreezes: an override applies at once and freezes.
func TestBreakGlassOverrideFreezes(t *testing.T) {
	e := baseEnv(t, opObj("o1", delivery.TypeOverride, dA, dB, "ops", "pacto:break-glass"))
	e.drive("o1")
	e.apply(dB)
	o := e.drive("o1")
	wantOutcome(t, o, delivery.Succeeded, "Overridden")
	if s := e.getStage(); !s.Status.Frozen || s.Status.Pointer != dB {
		t.Fatalf("override freezes: %+v", s.Status)
	}
}

// TestRunningFailures covers the ways Running ends badly.
func TestRunningFailures(t *testing.T) {
	t.Run("flux failure is Failed", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.drive("p1")
		e.apply(dB)
		ks := kustomization("payments", "payments", "False", "HealthCheckFailed", "latest@"+dA, "latest@"+dB)
		_ = e.c.Delete(context.Background(), ks)
		e.create(ks)
		wantOutcome(t, e.drive("p1"), delivery.Failed, "ApplyFailed")
	})
	t.Run("a foreign writer freezes", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.drive("p1")
		u := ociRepo("payments", dC, "latest@"+dC, true)
		_ = e.c.Delete(context.Background(), u)
		e.create(u)
		wantOutcome(t, e.drive("p1"), delivery.Errored, "ForeignWriter")
		if !e.getStage().Status.Frozen || e.ociDigest("payments") != dC {
			t.Fatal("Errored freezes and never overwrites the foreign edit")
		}
	})
	t.Run("execution deadline", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.drive("p1")
		e.now = e.now.Add(time.Hour)
		wantOutcome(t, e.drive("p1"), delivery.Errored, "ExecutionDeadline")
	})
	t.Run("waits for flux and for a missing source", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.drive("p1")
		u := ociRepo("payments", dB, "latest@"+dB, true)
		_ = e.c.Delete(context.Background(), u)
		e.create(u)
		_ = e.c.Delete(context.Background(), kustomization("payments", "payments", "", "", "", ""))
		e.create(kustomization("other", "elsewhere", "True", "", "", ""))
		if o := e.drive("p1"); !strings.Contains(o.Status.Message, "no Kustomization applies") {
			t.Fatalf("message %q", o.Status.Message)
		}
		e.create(kustomization("payments", "payments", "False", "Progressing", "latest@"+dA, "latest@"+dB))
		if o := e.drive("p1"); !strings.Contains(o.Status.Message, "has not applied") {
			t.Fatalf("message %q", o.Status.Message)
		}
		_ = e.c.Delete(context.Background(), u)
		if o := e.drive("p1"); !strings.Contains(o.Status.Message, "reading the apply OCIRepository") {
			t.Fatalf("message %q", o.Status.Message)
		}
	})
	t.Run("lock lost", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.reconcileOp("p1")
		e.reconcileOp("p1") // locked, Running, not yet applied
		e.setStage(func(s *delivery.StageStatus) { s.Locks = map[string]string{"pointer": "someone"} })
		wantOutcome(t, e.drive("p1"), delivery.Errored, "LockLost")
	})
	t.Run("stage deleted after apply", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.drive("p1")
		_ = e.c.Delete(context.Background(), e.getStage())
		wantOutcome(t, e.drive("p1"), delivery.Errored, "NoStage")
	})
}

// TestVerifyUnknownHoldsAfterApply covers Held after apply and its exits.
func TestVerifyUnknownHoldsAfterApply(t *testing.T) {
	setup := func(t *testing.T) *env {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.drive("p1")
		e.apply(dB)
		e.drive("p1")
		e.now = e.now.Add(2 * time.Minute)
		o := e.drive("p1")
		if o.Status.Phase != delivery.PhaseHeld || !o.Status.Applied || !strings.Contains(o.Status.Question, "Accept the unknown") {
			t.Fatalf("want held after apply: %s %q", o.Status.Phase, o.Status.Question)
		}
		return e
	}
	t.Run("accept-unknown", func(t *testing.T) {
		e := setup(t)
		e.approve(e.getOp("p1"), "au", "alice", delivery.AcceptUnknown)
		o := e.drive("p1")
		wantOutcome(t, o, delivery.Succeeded, "AcceptedUnknown")
		if o.Status.Verification != delivery.AcceptedUnknown {
			t.Fatal(o.Status.Verification)
		}
	})
	t.Run("only a later accept or reject answers", func(t *testing.T) {
		e := setup(t)
		e.now = e.now.Add(-time.Hour)
		e.approve(e.getOp("p1"), "early", "alice", delivery.Reject)
		e.now = e.now.Add(time.Hour)
		e.approve(e.getOp("p1"), "approve", "alice", delivery.Approve)
		if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || len(o.Status.Approvals) != 0 {
			t.Fatalf("an earlier reject and an approve do not answer: %s %+v", o.Status.Phase, o.Status.Approvals)
		}
		e.approve(e.getOp("p1"), "au", "bob", delivery.AcceptUnknown)
		wantOutcome(t, e.drive("p1"), delivery.Succeeded, "AcceptedUnknown")
	})
	t.Run("reject rolls back", func(t *testing.T) {
		e := setup(t)
		if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld {
			t.Fatal("waits for a person")
		}
		e.approve(e.getOp("p1"), "no", "alice", delivery.Reject)
		wantOutcome(t, e.drive("p1"), delivery.Failed, "RejectedByHuman")
		e.getOp("p1-rollback")
	})
	t.Run("deadline errors", func(t *testing.T) {
		e := setup(t)
		e.now = e.now.Add(25 * time.Hour)
		wantOutcome(t, e.drive("p1"), delivery.Errored, "VerifyUndecided")
	})
	t.Run("preempted", func(t *testing.T) {
		e := setup(t)
		e.setStage(func(s *delivery.StageStatus) { s.Locks = map[string]string{"pointer": "x"} })
		wantOutcome(t, e.drive("p1"), delivery.Succeeded, "Preempted")
	})
}

// TestVerdictUnknownReasons covers the built-in gates' own failure paths.
func TestVerdictUnknownReasons(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	in := gateInput(e.getOp("p1"), e.getStage(), e.now, e.now)
	check := func() string { return e.op.Gates[delivery.GateContract].Check(context.Background(), in, nil).Message }
	e.fails["list/Pacto"] = true
	if m := check(); !strings.Contains(m, "listing Pactos") {
		t.Fatal(m)
	}
	e.reg.annErr = errTest
	if m := check(); !strings.Contains(m, "reading the artifact") {
		t.Fatal(m)
	}
	e.fails["get/OCIRepository"] = true
	if m := check(); !strings.Contains(m, "reading the apply OCIRepository") {
		t.Fatal(m)
	}
	e.fails["get/Stage"] = true
	if m := check(); !strings.Contains(m, "reading the Stage") {
		t.Fatal(m)
	}
}

func TestFresh(t *testing.T) {
	at := time.Unix(1_000_000, 0)
	f := delivery.AuthFreshness
	if !fresh("999990", at, f) || fresh("990000", at, f) || fresh("nope", at, f) || !fresh(at.Format(time.RFC3339), at, f) || fresh(at.Add(time.Hour).Format(time.RFC3339), at, f) ||
		fresh("999990", at, time.Second) {
		t.Fatal("fresh")
	}
}

// TestNotPublishedIsRefused: a version pacto publish did not publish (no
// contract annotation, or a digest Flux never verified on the candidate
// source) is refused with NotPublished, before any other gate.
func TestNotPublishedIsRefused(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	delete(e.reg.contracts, dB)
	wantOutcome(t, e.drive("p1"), delivery.Rejected, "NotPublished")
	e.setStage(func(s *delivery.StageStatus) { s.Published = []string{dA} })
	e.create(opObj("p2", delivery.TypePromote, dA, dC, bot))
	o := e.drive("p2")
	wantOutcome(t, o, delivery.Rejected, "NotPublished")
	if !strings.Contains(o.Status.Message, "Flux never verified a signature") || len(o.Status.Verdicts) != 1 {
		t.Fatalf("message %q verdicts %+v", o.Status.Message, o.Status.Verdicts)
	}
}

// TestPublishedFromUpstream: the versions an upstream Stage offers and
// verified pass the published gate. A version it only verified (before the
// published rule existed) and an upstream Stage that does not exist are
// refused, never left for a person to accept; a failed read is planned again.
func TestPublishedFromUpstream(t *testing.T) {
	withUpstream := func(name string) *env {
		up := stageObj(dB)
		up.Name, up.Status.Verified, up.Status.Offered = "payments-test", []string{dA, dB, dC}, []string{dA, dB}
		e := baseEnv(t, nil)
		e.create(up)
		s := e.getStage()
		ref := &delivery.StageRef{Name: name} // the Stage's own namespace
		if name == "missing" {
			ref.Namespace = ns
		}
		s.Spec.Candidates = delivery.Candidates{UpstreamStage: ref}
		if err := e.c.Update(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := withUpstream("payments-test")
	e.create(bind(opObj("p1", delivery.TypePromote, dA, dB, bot), e.getStage()))
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseRunning {
		t.Fatalf("a version verified upstream ships: %s %s %+v", o.Status.Phase, o.Status.Reason, o.Status.Verdicts)
	}
	e = withUpstream("payments-test")
	e.create(bind(opObj("p3", delivery.TypePromote, dA, dC, bot), e.getStage()))
	wantOutcome(t, e.drive("p3"), delivery.Rejected, "NotPublished")
	op, stage := opObj("p4", delivery.TypePromote, dA, dB, bot), e.getStage()
	e.fails["get/Stage"] = true
	if _, err := e.op.plan(context.Background(), op, stage); err == nil {
		t.Fatal("a failed read of the upstream Stage must be planned again")
	}
	e = withUpstream("missing")
	e.create(bind(opObj("p1", delivery.TypePromote, dA, dB, bot), e.getStage()))
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Rejected, "NotPublished")
	if !strings.Contains(o.Status.Message, "does not exist or cannot be read") {
		t.Fatalf("message %q", o.Status.Message)
	}
	// An upstream Stage the promoter may not read is refused, not retried.
	e = withUpstream("payments-test")
	r := *e.op
	r.Client = interceptor.NewClient(e.c, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if key.Name == "payments-test" {
				return apierrors.NewForbidden(schema.GroupResource{Group: dv1.GroupVersion.Group, Resource: "stages"}, key.Name, errors.New("no RBAC"))
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	op = opObj("p5", delivery.TypePromote, dA, dB, bot)
	op.Status.ActorClass = delivery.ClassAgent
	p, err := r.plan(context.Background(), op, e.getStage())
	if err != nil || p.Predicted != delivery.PredictRejected || !strings.Contains(planRefusal(p), "does not exist or cannot be read") {
		t.Fatalf("a forbidden upstream Stage must be refused, not retried: %v %+v", err, p)
	}
}

// TestVerifiedPromoteIsOffered: a promote that passed the published gate here
// and verified is offered to a downstream Stage. One a person accepted on an
// unknown published verdict is not, and nothing but a promote is.
func TestVerifiedPromoteIsOffered(t *testing.T) {
	published := func(name, to string, r delivery.Result) *dv1Op {
		o := opObj(name, delivery.TypePromote, dA, to, bot)
		o.Status.Verdicts = []delivery.Verdict{{Gate: delivery.GatePublished, Phase: "pre", Result: r}, {Gate: delivery.GateDiff, Phase: "pre", Result: delivery.Pass}}
		return o
	}
	s := delivery.StageStatus{}
	verified := conclusion{outcome: delivery.Succeeded, verification: delivery.Verified}
	applyEffects(&s, published("p", dB, delivery.Pass), verified)
	applyEffects(&s, published("u", dC, delivery.Unknown), verified)
	applyEffects(&s, opObj("o", delivery.TypeOverride, dB, dC, bot), verified)
	if len(s.Offered) != 1 || s.Offered[0] != dB || len(s.Published) != 0 {
		t.Fatalf("offered %v published %v", s.Offered, s.Published)
	}
}

// TestBlockedPromoteSaysWhatMovesFirst is M4 at unit level: a promote that
// checkout, running in prod, does not accept is held, and status.plan names
// the verified version of checkout to ship first, read through checkout's own
// apply OCIRepository. A checkout Pacto can place in test, where the test
// Stage's Kustomization applies, does not hold it; one it cannot place does.
func TestBlockedPromoteSaysWhatMovesFirst(t *testing.T) {
	const dX = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	world := func(checkoutNS string) *env {
		checkoutProd := stageObj("")
		checkoutProd.Name, checkoutProd.Spec.Service.Name, checkoutProd.Spec.ApplyRef.Name = "checkout-prod", "checkout", "checkout"
		checkoutProd.Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "checkout-test", Namespace: "test"}}
		checkoutTest := stageObj("")
		checkoutTest.Namespace, checkoutTest.Name, checkoutTest.Spec.Service.Name, checkoutTest.Spec.Environment = "test", "checkout-test", "checkout", "test"
		checkoutTest.Status = delivery.StageStatus{Offered: []string{dX}, Verified: []string{dX}}
		checkout := pactoObj("checkout", "checkout", c3, "Compliant", 1,
			map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "compatibility": "^1.0.0"})
		checkout.SetNamespace(checkoutNS)
		e := newEnv(t, stageObj(dA), ociRepo("payments", dA, "latest@"+dA, true), deployment("payments", 2, 2),
			pactoObj("payments", "payments", c1, "Compliant", 2), checkoutProd, checkoutTest, ociRepo("checkout", "", "", false), checkout,
			opObj("p1", delivery.TypePromote, dA, dC, bot))
		e.reg.contracts[dX] = "oci://reg.example/pactos/checkout@" + c2
		b := bundle("1.1.0")
		b.Contract.Service.Name = "checkout"
		b.Contract.Dependencies = append(b.Contract.Dependencies, contract.Dependency{Name: "payments", Ref: "oci://reg.example/pactos/payments", Compatibility: ">=1.0.0 <3.0.0"})
		e.reg.bundles["oci://reg.example/pactos/checkout@"+c2] = b
		return e
	}
	// The test Stage's version runs in namespace test: Flux applies it there.
	placed := kustomization("payments", "payments", "True", "ReconciliationSucceeded", "", "")
	placed.SetNamespace("test")
	_ = unstructured.SetNestedField(placed.Object, "test", "spec", "targetNamespace")
	e := world("test")
	if err := e.c.Create(context.Background(), placed); err != nil {
		t.Fatal(err)
	}
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseRunning {
		t.Fatalf("checkout runs only in test, so prod does not wait for it: %s %q", o.Status.Phase, o.Status.Question)
	}
	// Nothing says where a checkout in a namespace no Stage applies into runs,
	// and with the Kustomizations unlistable nothing says where any runs: it
	// counts, so prod waits.
	if o := world("checkout-team").drive("p1"); o.Status.Phase != delivery.PhaseHeld {
		t.Fatalf("a checkout Pacto cannot place holds prod: %s %q", o.Status.Phase, o.Status.Question)
	}
	e = world("test")
	_ = e.c.Create(context.Background(), placed)
	e.fails["list/Kustomization"] = true
	op := opObj("p1", delivery.TypePromote, dA, dC, bot)
	op.Status.ActorClass = delivery.ClassAgent
	if p, err := e.op.plan(context.Background(), op, e.getStage()); err != nil || p.Predicted != delivery.PredictHeld {
		t.Fatalf("with the Kustomizations unlistable checkout counts: %v %+v", err, p)
	}
	e = world(ns)
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Order == nil || len(o.Status.Plan.Order.MoveFirst) != 1 {
		t.Fatalf("want Held with an order: %s %+v", o.Status.Phase, o.Status.Plan)
	}
	m := o.Status.Plan.Order.MoveFirst[0]
	if m.Service != "checkout" || m.Version != "1.0.0" || m.Candidate == nil || m.Candidate.Digest != dX ||
		*m.Stage != (delivery.StageRef{Name: "checkout-prod", Namespace: ns}) || !strings.Contains(o.Status.Question, "Or ship checkout first") {
		t.Fatalf("move first: %+v question %q", m, o.Status.Question)
	}

	// checkout's apply OCIRepository gone, then the Stages unlistable: the
	// order says it cannot tell, it never guesses.
	_ = e.c.Delete(context.Background(), ociRepo("checkout", "", "", false))
	plan, err := e.op.plan(context.Background(), o, e.getStage())
	if err != nil {
		t.Fatal(err)
	}
	if m := plan.Order.MoveFirst[0]; m.Candidate != nil || !strings.Contains(m.Unknown, "the apply OCIRepository: ") {
		t.Fatalf("unreadable candidate source: %+v", m)
	}
	e.fails["list/Stage"] = true
	plan, err = e.op.plan(context.Background(), o, e.getStage())
	if err != nil {
		t.Fatal(err)
	}
	if m := plan.Order.MoveFirst[0]; !strings.Contains(m.Unknown, "the Stages could not be listed") {
		t.Fatalf("unlistable Stages: %+v", m)
	}
}

// TestHeldOrderIsRefreshed is story beat B6 on Kubernetes: a change held
// because checkout and orders accept only the old major is planned again while
// it waits. Once checkout runs a version that accepts it, only team-orders is
// asked and only team-orders may approve; a team it newly breaks is asked too.
// A re-plan that rejects, errs or holds on something Pacto cannot see leaves
// the hold as it is.
func TestHeldOrderIsRefreshed(t *testing.T) {
	consumer := func(name, digest, rng string) *unstructured.Unstructured {
		return pactoObj(name, name, digest, "Compliant", 1,
			map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "compatibility": rng})
	}
	e := newEnv(t, stageObj(dA), ociRepo("payments", dA, "latest@"+dA, true), deployment("payments", 2, 2),
		pactoObj("payments", "payments", c1, "Compliant", 2), consumer("checkout", c3, "^1.0.0"), consumer("orders", c2, "^1.0.0"),
		opObj("p1", delivery.TypePromote, dA, dC, bot))
	ctx := context.Background()
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Order == nil ||
		strings.Join(o.Status.Plan.Floor.Owners, ",") != "team-checkout,team-orders" {
		t.Fatalf("want Held for checkout and orders: %s %+v", o.Status.Phase, o.Status.Plan)
	}
	held := o.Status.Question
	unchanged := func(stage *dv1.Stage) {
		t.Helper()
		op := e.getOp("p1")
		if changed, err := e.op.refreshOrder(ctx, op, stage); err != nil || changed || op.Status.Question != held {
			t.Fatalf("want no change: %v %v %q", changed, err, op.Status.Question)
		}
	}
	unchanged(e.getStage())
	// A plan that would now reject: approving rejects it.
	rejected := e.getStage()
	rejected.Status.FailedDigests = []string{dC}
	unchanged(rejected)
	// A hold on what Pacto cannot see, not on a consumer: approving asks that.
	delete(e.reg.contracts, dA)
	unchanged(e.getStage())
	e.reg.contracts[dA] = contractRef(c1)
	// An unreadable upstream Stage is an error, retried.
	up := e.getStage()
	up.Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "payments-test"}}
	e.fails["get/Stage"] = true
	if _, err := e.op.held(ctx, e.getOp("p1"), up); err == nil {
		t.Fatal("want the plan error")
	}
	delete(e.fails, "get/Stage")

	// checkout moved first: its Pacto now holds the version that accepts the change.
	move := func(name, digest, rng string) {
		t.Helper()
		_ = e.c.Delete(ctx, consumer(name, digest, rng))
		if err := e.c.Create(ctx, consumer(name, digest, rng)); err != nil {
			t.Fatal(err)
		}
	}
	move("checkout", c3, ">=1.0.0 <3.0.0")
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || strings.Join(o.Status.Plan.Floor.Owners, ",") != "team-orders" ||
		strings.Join(o.Status.Plan.Floor.Consumers, ",") != "orders" || len(o.Status.Plan.Order.MoveFirst) != 1 ||
		o.Status.Plan.Order.MoveFirst[0].Service != "orders" || o.Status.Plan.Question != o.Status.Question ||
		!strings.Contains(o.Status.Question, "Asked of: team-orders") || strings.Contains(o.Status.Question, "checkout") {
		t.Fatalf("after checkout moved only orders is asked: %s %q %+v", o.Status.Phase, o.Status.Question, o.Status.Plan.Floor)
	}
	// A checkout member may no longer approve it.
	e.approveAs(o, "checkout-ok", "bob", []string{"team-checkout"}, delivery.Approve)
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || len(o.Status.Approvals) != 0 {
		t.Fatalf("team-checkout is no longer asked: %s %+v", o.Status.Phase, o.Status.Approvals)
	}
	// ledger breaks too: its team is asked as well, and drops out again.
	if err := e.c.Create(ctx, consumer("ledger", c1, "^1.0.0")); err != nil {
		t.Fatal(err)
	}
	if o = e.drive("p1"); strings.Join(o.Status.Plan.Floor.Owners, ",") != "team-ledger,team-orders" {
		t.Fatalf("a newly broken team is asked: %+v", o.Status.Plan.Floor)
	}
	_ = e.c.Delete(ctx, consumer("ledger", c1, "^1.0.0"))
	if o = e.drive("p1"); strings.Join(o.Status.Plan.Floor.Owners, ",") != "team-orders" {
		t.Fatalf("ledger dropped out: %+v", o.Status.Plan.Floor)
	}
	// carol in team-orders approves: it ships.
	e.approveAs(o, "orders-ok", "carol", []string{"team-orders"}, delivery.Approve)
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseRunning || o.Status.Approvals[0].By != "carol" {
		t.Fatalf("an orders approval ships it: %s %+v", o.Status.Phase, o.Status.Approvals)
	}
}

// TestHeldRejectSurvivesAPlanFailure: a re-plan that cannot read its inputs
// does not hold back a human Reject, which needs no plan.
func TestHeldRejectSurvivesAPlanFailure(t *testing.T) {
	e := newEnv(t, stageObj(dA), ociRepo("payments", dA, "latest@"+dA, true), deployment("payments", 2, 2),
		pactoObj("payments", "payments", c1, "Compliant", 2),
		pactoObj("checkout", "checkout", c3, "Compliant", 1,
			map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "compatibility": "^1.0.0"}),
		opObj("p1", delivery.TypePromote, dA, dC, bot))
	ctx := context.Background()
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Order == nil {
		t.Fatalf("want Held on its order: %s %+v", o.Status.Phase, o.Status.Plan)
	}
	e.approveAs(o, "checkout-no", "bob", []string{"team-checkout"}, delivery.Reject)
	// The upstream Stage cannot be read, so the re-plan cannot be made.
	up := e.getStage()
	up.Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "payments-test"}}
	e.fails["get/Stage/payments-test"] = true
	if _, err := e.op.refreshOrder(ctx, e.getOp("p1"), up); err == nil {
		t.Fatal("want the re-plan to fail")
	}
	if _, err := e.op.held(ctx, e.getOp("p1"), up); err != nil {
		t.Fatalf("a Reject needs no plan: %v", err)
	}
	if o = e.getOp("p1"); o.Status.Outcome != delivery.Rejected {
		t.Fatalf("want Rejected: %s %s", o.Status.Phase, o.Status.Outcome)
	}
}

// TestHeldOrderGoesMoot: once every blocked consumer has moved, nothing holds
// the change. The question says so, the people already asked may still
// approve it, and with no order left it is not planned again.
func TestHeldOrderGoesMoot(t *testing.T) {
	checkout := func(rng string) *unstructured.Unstructured {
		return pactoObj("checkout", "checkout", c3, "Compliant", 1,
			map[string]any{"name": "payments", "ref": "oci://reg.example/pactos/payments", "compatibility": rng})
	}
	e := newEnv(t, stageObj(dA), ociRepo("payments", dA, "latest@"+dA, true), deployment("payments", 2, 2),
		pactoObj("payments", "payments", c1, "Compliant", 2), checkout("^1.0.0"), opObj("p1", delivery.TypePromote, dA, dC, bot))
	ctx := context.Background()
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Order == nil {
		t.Fatalf("want Held with an order: %s %+v", o.Status.Phase, o.Status.Plan)
	}
	_ = e.c.Delete(ctx, checkout("^1.0.0"))
	if err := e.c.Create(ctx, checkout(">=1.0.0 <3.0.0")); err != nil {
		t.Fatal(err)
	}
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Plan.Order != nil || o.Status.Plan.Question != o.Status.Question ||
		strings.Join(o.Status.Plan.Floor.Owners, ",") != "team-checkout" ||
		!strings.HasPrefix(o.Status.Question, "Nothing Pacto sees holds this change any more") {
		t.Fatalf("after checkout moved: %s %q %+v", o.Status.Phase, o.Status.Question, o.Status.Plan)
	}
	if changed, err := e.op.refreshOrder(ctx, o, e.getStage()); err != nil || changed {
		t.Fatalf("no order, no re-plan: %v %v", changed, err)
	}
	e.approveAs(o, "ok", "bob", []string{"team-checkout"}, delivery.Approve)
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseRunning {
		t.Fatalf("the people asked may ship it: %s", o.Status.Phase)
	}
}

// TestConclusionConflictTakesOneSeq: a conflict writing the conclusion is
// retried on a fresh read, not by concluding again, which would take a second
// seq and leave the first a gap the recorder has to sign. A failed re-read
// surfaces.
func TestConclusionConflictTakesOneSeq(t *testing.T) {
	conflictOnce := func(e *env, getErr error) {
		conflicted := false
		r := *e.op
		r.Client = interceptor.NewClient(e.c, interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if o, ok := obj.(*dv1.Operation); ok && o.Status.Phase == delivery.PhaseConcluded && !conflicted {
					conflicted = true
					return apierrors.NewConflict(schema.GroupResource{Group: dv1.GroupVersion.Group, Resource: "operations"}, o.Name, errors.New("modified"))
				}
				return c.Status().Update(ctx, obj, opts...)
			},
		})
		if getErr != nil {
			r.APIReader = interceptor.NewClient(e.c, interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*dv1.Operation); ok && conflicted {
						return getErr
					}
					return c.Get(ctx, key, obj, opts...)
				},
			})
		}
		e.op = &r
	}
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	delete(e.reg.contracts, dB)
	conflictOnce(e, nil)
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Rejected, "NotPublished")
	if o.Status.Seq != 1 || e.getStage().Status.Stream.Seq != 1 {
		t.Fatalf("one conclusion must take one seq: op %d, stage %d", o.Status.Seq, e.getStage().Status.Stream.Seq)
	}
	e = baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	delete(e.reg.contracts, dB)
	conflictOnce(e, errors.New("injected read failure"))
	var err error
	for range 5 {
		if err = e.reconcileOpErr("p1"); err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "injected read failure") {
		t.Fatalf("a failed re-read after a conflict must surface: %v", err)
	}
}

// TestConclusionWriteFailureTakesOneSeq: the Stage took the seq, then the
// operation's status write failed for a reason that is not a conflict. The
// next reconcile finds the lock released, but the operation concludes from
// what the Stage holds: the same seq and outcome, one failed digest and one
// rollback, never a second seq that would leave the first a gap.
func TestConclusionWriteFailureTakesOneSeq(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	failed := false
	r := *e.op
	r.Client = interceptor.NewClient(e.c, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if o, ok := obj.(*dv1.Operation); ok && o.Status.Phase == delivery.PhaseConcluded && !failed {
				failed = true
				return apierrors.NewInternalError(errors.New("etcd timeout"))
			}
			return c.Status().Update(ctx, obj, opts...)
		},
	})
	e.op = &r
	if err := e.reconcileOpErr("p1"); err == nil || !failed {
		t.Fatalf("the injected write failure did not surface: %v", err)
	}
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Failed, "VerifyFailed")
	s := e.getStage()
	if o.Status.Seq != 1 || s.Status.Stream.Seq != 1 || len(s.Status.FailedDigests) != 1 || s.Status.AutoRollbacks["uid-p1"] != 1 ||
		o.Status.Policy == nil || o.Status.Policy.Environment != "prod" {
		t.Fatalf("one conclusion must take one seq: op %+v stage %+v", o.Status, s.Status)
	}

	// Decided again on a stale read, even as a failure, it raises no
	// rollback: the Stage already concluded it.
	rb := e.getOp("p1-rollback")
	controllerutil.RemoveFinalizer(rb, dv1.RecordFinalizer)
	if err := e.c.Update(context.Background(), rb); err != nil {
		t.Fatal(err)
	}
	if err := e.c.Delete(context.Background(), rb); err != nil {
		t.Fatal(err)
	}
	stale := e.getOp("p1")
	stale.Status.Phase = delivery.PhaseVerifying
	if err := e.c.Status().Update(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	stale = e.getOp("p1")
	c := conclusion{outcome: delivery.Failed, reason: "VerifyFailed: again"}
	if _, err := e.op.finish(context.Background(), stale, e.getStage(), c); err != nil {
		t.Fatal(err)
	}
	if err := e.c.Get(context.Background(), keyOf("p1-rollback"), rb); err == nil {
		t.Fatal("a stale failure raised a second rollback")
	}
	if o := e.getOp("p1"); o.Status.Seq != 1 || o.Status.Reason != "VerifyFailed" || e.getStage().Status.Stream.Seq != 1 {
		t.Fatalf("re-concluded as %+v", o.Status)
	}
	e.fails["get/Stage"] = true
	if _, err := e.op.finish(context.Background(), e.getOp("p1"), s, c); err == nil {
		t.Fatal("a failed Stage read before a rollback must surface")
	}
}

// TestLockRefusesAConcludedOperation: a stale read shows an operation the
// Stage already concluded as still pending; it must not take the lock.
func TestLockRefusesAConcludedOperation(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.setStage(func(s *delivery.StageStatus) {
		s.Stream.Seq = 4
		s.Stream.Concluded = []delivery.Concluded{{UID: "uid-p1", Seq: 4, Outcome: delivery.Rejected, Reason: "PointerMoved: another operation moved the pointer first"}}
	})
	o := e.getOp("p1")
	if _, err := e.op.lock(context.Background(), o, stageObj(dA), false); err != nil {
		t.Fatal(err)
	}
	o = e.getOp("p1")
	wantOutcome(t, o, delivery.Rejected, "PointerMoved")
	if s := e.getStage(); o.Status.Seq != 4 || s.Status.Stream.Seq != 4 || s.Status.Locks["pointer"] != "" {
		t.Fatalf("op %+v stage %+v", o.Status, s.Status)
	}
}

// TestStaleOperationIsNotConcludedAgain: a cached read older than the stored
// operation, such as one that shows a concluded operation still verifying, is
// decided again on the next reconcile and takes no seq. A failed fresh read
// surfaces.
func TestStaleOperationIsNotConcludedAgain(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	stale := e.getOp("p1")
	moved := e.getOp("p1")
	moved.Labels = map[string]string{"touched": "yes"}
	if err := e.c.Update(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	c := conclusion{outcome: delivery.Succeeded, verification: delivery.Unverified, reason: "Preempted: a rollback or override took the Stage"}
	res, err := e.op.finish(context.Background(), stale, e.getStage(), c)
	if err != nil || res.RequeueAfter != immediately || e.getStage().Status.Stream.Seq != 0 || e.getOp("p1").Status.Phase == delivery.PhaseConcluded {
		t.Fatalf("a stale operation must be decided again, not concluded: %v %+v seq %d", err, res, e.getStage().Status.Stream.Seq)
	}
	current, stage := e.getOp("p1"), e.getStage()
	e.fails["get/Operation"] = true
	if _, err := e.op.finish(context.Background(), current, stage, c); err == nil {
		t.Fatal("a failed fresh read must surface")
	}
}

// TestUnguardedAdmitsNothingAndCountsNoApproval: while the admission policies
// that check who wrote a spec are missing, no operation is admitted and no
// approval counts; once they are installed the operation goes on.
func TestUnguardedAdmitsNothingAndCountsNoApproval(t *testing.T) {
	guard := func(e *env) {
		e.create(&admissionv1.ValidatingAdmissionPolicy{ObjectMeta: metav1.ObjectMeta{Name: "pd-operation"}},
			&admissionv1.ValidatingAdmissionPolicyBinding{ObjectMeta: metav1.ObjectMeta{Name: "pd-operation"}})
	}
	t.Run("admission waits for the policy and its binding", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.op.AdmissionPolicies = []string{"pd-operation"}
		if o := e.drive("p1"); o.Status.Phase != "" || o.Status.Reason != ReasonUnguarded || o.Status.ActorClass != "" {
			t.Fatalf("want parked before admission: %q %s %s", o.Status.Phase, o.Status.Reason, o.Status.ActorClass)
		}
		e.create(&admissionv1.ValidatingAdmissionPolicy{ObjectMeta: metav1.ObjectMeta{Name: "pd-operation"}})
		if o := e.drive("p1"); o.Status.Reason != ReasonUnguarded {
			t.Fatalf("a policy with no binding guards nothing: %s", o.Status.Reason)
		}
		e.create(&admissionv1.ValidatingAdmissionPolicyBinding{ObjectMeta: metav1.ObjectMeta{Name: "pd-operation"}})
		if o := e.drive("p1"); o.Status.Phase != delivery.PhaseRunning {
			t.Fatalf("guarded, it moves: %s %s", o.Status.Phase, o.Status.Message)
		}
	})
	t.Run("a held operation counts no approval", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		delete(e.reg.bundles, contractRef(c2))
		o := e.drive("p1")
		e.op.AdmissionPolicies = []string{"pd-operation"}
		e.approve(o, "no", "alice", delivery.Reject)
		if o = e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || o.Status.Reason != ReasonUnguarded || len(o.Status.Approvals) != 0 {
			t.Fatalf("an unguarded approval must not count: %s %s %+v", o.Status.Phase, o.Status.Reason, o.Status.Approvals)
		}
		guard(e)
		wantOutcome(t, e.drive("p1"), delivery.Rejected, "RejectedByHuman")
	})
	t.Run("held after apply counts no approval", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.drive("p1")
		e.apply(dB)
		e.drive("p1")
		e.now = e.now.Add(2 * time.Minute)
		e.drive("p1")
		e.op.AdmissionPolicies = []string{"pd-operation"}
		e.approve(e.getOp("p1"), "au", "alice", delivery.AcceptUnknown)
		if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || o.Status.Reason != ReasonUnguarded {
			t.Fatalf("want parked: %s %s", o.Status.Phase, o.Status.Reason)
		}
		guard(e)
		wantOutcome(t, e.drive("p1"), delivery.Succeeded, "AcceptedUnknown")
	})
	t.Run("an unreadable policy is not installed", func(t *testing.T) {
		e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.op.AdmissionPolicies = []string{"pd-operation"}
		e.fails["get/*v1.ValidatingAdmissionPolicy"] = true
		if err := e.reconcileOpErr("p1"); err == nil {
			t.Fatal("a failed policy read must surface")
		}
	})
}
