/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// observeStage is a Stage that observes a labelled applyRef whose source
// reports dA, with advise proposed.
func observeStage() *dv1.Stage {
	s := stageObj("")
	s.Status.Rung = delivery.RungObserve
	s.Status.ObservedRevision = &delivery.ObservedRevision{Digest: dA, Since: "2026-10-20T09:00:00Z"}
	s.Status.Conditions = []delivery.Condition{{Type: conditionReady, Status: statusTrue, Reason: delivery.ReasonObservingGuarded}}
	s.Status.Proposal = &delivery.Proposal{Rung: delivery.RungAdvise, Evidence: []string{dA}}
	// The status that earned it: autonomyGate recomputes the proposal.
	for range delivery.DefaultProposeAfter {
		s.Status.Shadow = append(s.Status.Shadow, delivery.Shadow{Candidate: dA, ObservedAt: "2026-10-20T09:00:00Z", Verification: delivery.Verified})
	}
	return s
}

// TestRungSeededOnce: an empty status.rung seeds from spec.startRung, once;
// a wiped status seeds back to that manual rung.
func TestRungSeededOnce(t *testing.T) {
	s := stageObj("")
	s.Status.Rung, s.Spec.StartRung = "", delivery.RungAdvise
	e := newEnv(t, s)
	if err := e.reconcileStage("payments-prod"); err != nil || e.getStage().Status.Rung != delivery.RungAdvise {
		t.Fatalf("seed from startRung: %v %q", err, e.getStage().Status.Rung)
	}
	e.setStage(func(st *delivery.StageStatus) { st.Rung = delivery.RungAutonomous })
	if err := e.reconcileStage("payments-prod"); err != nil || e.getStage().Status.Rung != delivery.RungAutonomous {
		t.Fatalf("a seeded rung is never seeded again: %v %q", err, e.getStage().Status.Rung)
	}
	e.setStage(func(st *delivery.StageStatus) { *st = delivery.StageStatus{} })
	if err := e.reconcileStage("payments-prod"); err != nil || e.getStage().Status.Rung != delivery.RungAdvise {
		t.Fatalf("a wiped status seeds back to startRung: %v %q", err, e.getStage().Status.Rung)
	}
	if delivery.ApplyRefLabel != ApplyRefLabel {
		t.Fatal("the planner and the promoter must name the same label")
	}
}

// TestObserveWitnessAndReady: on observe an unlabelled applyRef is Ready
// (Observing) and GitOps moving it is no edit to witness; a labelled one is
// ObservingGuarded and an edit of it is witnessed.
func TestObserveWitnessAndReady(t *testing.T) {
	unlabelled := ociRepo("payments", dB, "latest@"+dB, false)
	unlabelled.SetLabels(nil)
	s := stageObj(dA)
	s.Status.Rung = delivery.RungObserve
	e := newEnv(t, s, unlabelled, verifiedRepo("payments-candidates", "latest@"+dB))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if c := conditionOf(e.getStage().Status.Conditions, conditionReady); c.Status != statusTrue || c.Reason != delivery.ReasonObserving || len(e.ops()) != 0 {
		t.Fatalf("observing an unlabelled applyRef: %+v, %d ops", c, len(e.ops()))
	}
	e = newEnv(t, s, ociRepo("payments", dB, "latest@"+dB, false), verifiedRepo("payments-candidates", "latest@"+dB))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	ops := e.ops()
	if len(ops) != 1 || !ops[0].Spec.Witnessed {
		t.Fatalf("an edit of a labelled applyRef on observe is witnessed: %+v", ops)
	}
	if c := conditionOf(e.getStage().Status.Conditions, conditionReady); c.Reason != delivery.ReasonObservingGuarded {
		t.Fatalf("a labelled applyRef on observe: %+v", c)
	}
}

// TestAutonomyRaiseNeedsAnotherPerson: leaving observe stays Held with no
// approval and with the raiser's own, concludes on another person's, and the
// swap that moves the rung adopts the observed digest as the pointer.
func TestAutonomyRaiseNeedsAnotherPerson(t *testing.T) {
	e := newEnv(t, observeStage(), ociRepo("payments", dA, "latest@"+dA, false))
	e.create(opObj("up", delivery.TypeAutonomy, "observe", "advise", "alice"))
	o := e.drive("up")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Reason != "AwaitingHuman" || !strings.Contains(o.Status.Question, "team-payments") {
		t.Fatalf("a raise waits for a person: %+v", o.Status)
	}
	e.approve(o, "self", "alice", delivery.Approve)
	if o = e.drive("up"); o.Status.Phase != delivery.PhaseHeld {
		t.Fatalf("the raiser cannot approve their own raise: %+v", o.Status)
	}
	e.approve(o, "other", "bob", delivery.Approve)
	o = e.drive("up")
	wantOutcome(t, o, delivery.Succeeded, "RungChanged")
	st := e.getStage().Status
	if st.Rung != delivery.RungAdvise || st.Pointer != dA || o.Status.Pointer != (delivery.PointerChange{After: dA}) {
		t.Fatalf("rung and adopted pointer: %+v / %+v", st, o.Status.Pointer)
	}
	done := concludedOn(st, o.UID)
	if done == nil || done.Type != delivery.TypeAutonomy || !done.Person || done.ClearedBreaking {
		t.Fatalf("concluded entry: %+v", done)
	}
}

// TestAutonomyRaiseRecheckedBeforeTheSwap: a proposal that lapsed between the
// approval and the swap sends the raise back to a person.
func TestAutonomyRaiseRecheckedBeforeTheSwap(t *testing.T) {
	e := newEnv(t, observeStage(), ociRepo("payments", dA, "latest@"+dA, false))
	e.create(opObj("up", delivery.TypeAutonomy, "observe", "advise", "alice"))
	o := e.drive("up")
	e.setStage(func(st *delivery.StageStatus) {
		st.Proposal.Rung, st.Proposal.Blockers = "", []delivery.Blocker{{Kind: "incident", Message: "a verify failed"}}
	})
	e.approve(o, "other", "bob", delivery.Approve)
	o = e.drive("up")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Reason != reasonRecheckChanged || !strings.Contains(o.Status.Question, "incident") {
		t.Fatalf("a lapsed proposal goes back to a person: %+v", o.Status)
	}
	if e.getStage().Status.Rung != delivery.RungObserve {
		t.Fatal("the rung must not move")
	}
}

// TestAutonomyLowerConcludesWithoutAPerson: lowering needs no approval and
// takes no freeze shortcut: it is planned, and a stale one is refused.
func TestAutonomyLowerConcludesWithoutAPerson(t *testing.T) {
	e := baseEnv(t, opObj("down", delivery.TypeAutonomy, "autonomous", "advise", bot))
	o := e.drive("down")
	wantOutcome(t, o, delivery.Succeeded, "RungChanged")
	if o.Status.Plan == nil || e.getStage().Status.Rung != delivery.RungAdvise || e.getStage().Status.Pointer != dA || o.Status.Pointer != (delivery.PointerChange{}) {
		t.Fatalf("lowered with a plan, pointer kept: %+v %+v", o.Status, e.getStage().Status)
	}
	e.create(opObj("again", delivery.TypeAutonomy, "autonomous", "advise", bot))
	wantOutcome(t, e.drive("again"), delivery.Rejected, "RungMoved")
}

// TestLowerToObserveClearsThePointer: lowered to observe, GitOps owns the
// pointer again and moves it; labelling the applyRef to leave observe is no
// edit outside Pacto, so nothing is witnessed and nothing freezes.
func TestLowerToObserveClearsThePointer(t *testing.T) {
	e := baseEnv(t, opObj("down", delivery.TypeAutonomy, "advise", "observe", "alice"))
	e.setStage(func(st *delivery.StageStatus) { st.Rung = delivery.RungAdvise })
	wantOutcome(t, e.drive("down"), delivery.Succeeded, "RungChanged")
	if st := e.getStage().Status; st.Rung != delivery.RungObserve || st.Pointer != "" {
		t.Fatalf("observe clears the pointer: %+v", st)
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCI)
	_ = e.c.Get(context.Background(), keyOf("payments"), u)
	_ = unstructured.SetNestedField(u.Object, dB, "spec", "ref", "digest")
	if err := e.c.Update(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	e.apply(dB) // GitOps moved it; the label is on again
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if st := e.getStage().Status; st.Frozen || slices.ContainsFunc(e.ops(), func(o dv1.Operation) bool { return o.Spec.Witnessed }) {
		t.Fatalf("no override and no freeze: %+v", st)
	}
}

// TestAutonomyRungMovedAtTheSwap: the compare-and-swap that concludes a rung
// change refuses it when the rung moved after it was planned.
func TestAutonomyRungMovedAtTheSwap(t *testing.T) {
	e := baseEnv(t, opObj("down", delivery.TypeAutonomy, "autonomous", "advise", bot))
	e.reconcileOp("down") // admitted
	e.setStage(func(st *delivery.StageStatus) { st.Rung = delivery.RungAdvise })
	o, stage := e.getOp("down"), e.getStage()
	stage.Status.Rung = delivery.RungAutonomous // what the planner saw
	if _, err := e.op.lock(context.Background(), o, stage, false); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, e.getOp("down"), delivery.Rejected, "RungMoved")
}

// TestAgentPromoteOnAdviseIsHeld: on advise an agent's promote waits for a
// person even when the graph clears it.
func TestAgentPromoteOnAdviseIsHeld(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.setStage(func(st *delivery.StageStatus) { st.Rung = delivery.RungAdvise })
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || !strings.Contains(o.Status.Question, "advises") {
		t.Fatalf("an agent promote on advise: %+v", o.Status)
	}
}
