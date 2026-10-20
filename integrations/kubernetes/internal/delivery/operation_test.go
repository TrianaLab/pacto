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
	if err := raiseRollback(context.Background(), e.c, e.op.Policy, o, s, "again", nil); err != nil {
		t.Fatal(err)
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
		Decision: delivery.Approve, By: "alice", AuthTime: e.now.Add(-time.Hour).Format(time.RFC3339)}
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
	e.approve(o, "ok", "bob", delivery.Approve)
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseRunning || len(o.Status.Approvals) != 2 || o.Status.Approvals[1].By != "bob" {
		t.Fatalf("an owner's approval must move it: %s %+v", o.Status.Phase, o.Status.Approvals)
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
	delete(e.reg.contracts, dA) // the running contract can no longer be read
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Reason != "RecheckChanged" {
		t.Fatalf("want back to Held: %s %s", o.Status.Phase, o.Status.Reason)
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
	wantOutcome(t, e.drive("u2"), delivery.Succeeded, "Unfrozen")
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
	if !fresh("999990", at) || fresh("990000", at) || fresh("nope", at) || !fresh(at.Format(time.RFC3339), at) || fresh(at.Add(time.Hour).Format(time.RFC3339), at) {
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

// TestPublishedFromUpstream: an upstream Stage's published and verified
// versions pass the published gate. A version it only verified (before the
// published rule existed) and an upstream Stage that does not exist are
// refused, never left for a person to accept; a failed read is planned again.
func TestPublishedFromUpstream(t *testing.T) {
	withUpstream := func(name string) *env {
		up := stageObj(dB)
		up.Name, up.Status.Verified, up.Status.Published = "payments-test", []string{dA, dB, dC}, []string{dA, dB}
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
}

// TestVerifiedPromoteIsPublished: a promote verified here passed the published
// gate here, so a downstream Stage may take it; nothing else is added.
func TestVerifiedPromoteIsPublished(t *testing.T) {
	s := delivery.StageStatus{}
	applyEffects(&s, opObj("p", delivery.TypePromote, dA, dB, bot), conclusion{outcome: delivery.Succeeded, verification: delivery.Verified})
	applyEffects(&s, opObj("o", delivery.TypeOverride, dB, dC, bot), conclusion{outcome: delivery.Succeeded, verification: delivery.Verified})
	if len(s.Published) != 1 || s.Published[0] != dB {
		t.Fatalf("published %v", s.Published)
	}
}
