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
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// observeEnv is a Stage that observes an unlabelled applyRef whose source
// reports dA while spec.ref names dC, with dB published on the candidate
// OCIRepository.
func observeEnv(t *testing.T) *env {
	t.Helper()
	apply := ociRepo("payments", dC, "latest@"+dA, false)
	apply.SetLabels(nil)
	s := stageObj("")
	s.Status.Rung = delivery.RungObserve
	return newEnv(t, s, apply, verifiedRepo("payments-candidates", "latest@"+dB),
		kustomization("payments", "payments", "True", "ReconciliationSucceeded", "latest@"+dA, "latest@"+dA),
		deployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
}

func shadowOf(st delivery.StageStatus, d string) *delivery.Shadow {
	i := slices.IndexFunc(st.Shadow, func(s delivery.Shadow) bool { return s.Candidate == d })
	if i < 0 {
		return nil
	}
	return &st.Shadow[i]
}

// TestShadowPlansWhatGitOpsApplies: on observe the Stage reads what the
// source reports (status.artifact.revision, never spec.ref), plans each new
// candidate once as a fully automatic Stage would, stamps the entry when
// GitOps applies it and raises nothing.
func TestShadowPlansWhatGitOpsApplies(t *testing.T) {
	e := observeEnv(t)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	st := e.getStage().Status
	if st.ObservedRevision == nil || st.ObservedRevision.Digest != dA || st.ObservedRevision.Since != ts(e.now) {
		t.Fatalf("observed revision: %+v", st.ObservedRevision)
	}
	b, a := shadowOf(st, dB), shadowOf(st, dA)
	// A rollback target never verified here is an unknown a fully automatic
	// Stage would ask its team to accept.
	if b == nil || b.From != dA || b.ObservedAt != "" || b.Predicted != delivery.PredictHeld || b.Decision != delivery.AcceptUnknown ||
		!slices.Equal(b.Owners, []string{"team-payments"}) || b.IntentDigest == "" || b.ImpactDigest == "" ||
		a == nil || a.From != "" || a.ObservedAt != ts(e.now) || len(st.Shadow) != 2 {
		t.Fatalf("shadow entries: %+v", st.Shadow)
	}
	if st.Proposal == nil || st.Proposal.Rung != "" || st.Proposal.Blockers[2].Kind != delivery.BlockerUnlabelledApplyRef {
		t.Fatalf("proposal: %+v", st.Proposal)
	}
	stale := stageObj("")
	stale.Status.Rung = delivery.RungObserve
	stale.ResourceVersion = ""
	if err := e.st.shadow(context.Background(), stale); err != nil || len(e.getStage().Status.Shadow) != 2 {
		t.Fatalf("a reconcile of a stale read adds no second entry per candidate: %v %+v", err, e.getStage().Status.Shadow)
	}
	rv := e.getStage().ResourceVersion
	if err := e.reconcileStage("payments-prod"); err != nil || e.getStage().ResourceVersion != rv {
		t.Fatalf("a tick with nothing new writes nothing: %v", err)
	}
	// A Stage spec edit replans the candidate that has not run yet, in place;
	// what already ran keeps the plan it had.
	s := e.getStage()
	s.Spec.Gates.Verify.Checks = healthChecks
	if err := e.c.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if got := e.getStage().Status; len(got.Shadow) != 2 || shadowOf(got, dB).IntentDigest == b.IntentDigest ||
		shadowOf(got, dB).IntentDigest != delivery.IntentDigest(e.st.shadowSpec(s, dA, dB), s.Spec) || shadowOf(got, dA).IntentDigest != a.IntentDigest {
		t.Fatalf("a spec edit replans the unobserved candidate only: %+v", got.Shadow)
	}
	e.now = e.now.Add(10 * time.Second)
	e.apply(dB)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	st = e.getStage().Status
	if st.ObservedRevision.Digest != dB || shadowOf(st, dB).ObservedAt != ts(e.now) || len(st.Shadow) != 2 || len(e.ops()) != 0 {
		t.Fatalf("GitOps applied B: %+v, %d ops", st, len(e.ops()))
	}
	if st.Pointer != "" || st.Locks[delivery.FacetPointer] != "" {
		t.Fatal("observe moves nothing")
	}
}

// TestShadowWouldHaveRefused: what GitOps applied that pacto publish never
// published is planned as a fully automatic Stage would: rejected.
func TestShadowWouldHaveRefused(t *testing.T) {
	e := observeEnv(t)
	e.setStage(func(st *delivery.StageStatus) { st.Published = []string{dA, dB} })
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	e.apply(dC) // GitOps applied what pacto publish never published
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	b := shadowOf(e.getStage().Status, dC)
	if b == nil || b.Predicted != delivery.PredictRejected || !strings.HasPrefix(b.Question, "NotPublished: ") || b.ObservedAt == "" {
		t.Fatalf("what GitOps applied that Pacto would have refused: %+v", b)
	}
}

// TestShadowVerify: once the verify window passed, the Stage's own checks run
// on what GitOps applied. All-Pass marks it verified, and offered only when
// published; Unknown writes nothing and is retried after the recheck
// interval; a confirmed Fail marks it failed.
func TestShadowVerify(t *testing.T) {
	e := observeEnv(t)
	e.st.Gates = NewGates(e.op.Cluster, map[string]delivery.Gate{"mira": e.health})
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.getStage().Status.Verified) != 0 {
		t.Fatalf("inside the window nothing verifies: %v", err)
	}
	s := e.getStage()
	s.Spec.Gates.Verify.Checks = healthChecks
	if err := e.c.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil { // the spec edit replans the candidate
		t.Fatal(err)
	}
	e.health.result = delivery.Unknown
	e.now = e.now.Add(2 * time.Minute)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	st := e.getStage().Status
	asked := len(e.health.inputs)
	if len(st.Verified) != 0 || len(st.FailedDigests) != 0 || asked == 0 || e.health.inputs[asked-1].Subjects[0] != dA || e.health.inputs[asked-1].OperationUID != "" {
		t.Fatalf("Unknown writes nothing: %+v %+v", st, e.health.inputs)
	}
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.health.inputs) != asked {
		t.Fatalf("an Unknown verify waits for the recheck interval: %v", err)
	}
	e.health.result = delivery.Pass
	e.now = e.now.Add(time.Hour)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	st = e.getStage().Status
	if !slices.Contains(st.Verified, dA) || !slices.Contains(st.Offered, dA) || shadowOf(st, dA).Verification != delivery.Verified {
		t.Fatalf("all-Pass verifies and offers a published digest: %+v", st)
	}

	// An unpublished digest verifies but is never offered.
	e.setStage(func(st *delivery.StageStatus) { st.Published, st.Verified, st.Offered = nil, nil, nil })
	e.now = e.now.Add(time.Hour)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if st = e.getStage().Status; !slices.Contains(st.Verified, dA) || len(st.Offered) != 0 {
		t.Fatalf("verified, not offered: %+v", st)
	}

	// A confirmed Fail marks what runs as failed, an incident for the proposal.
	e.setStage(func(st *delivery.StageStatus) { st.Verified = nil })
	e.health.result = delivery.Fail
	e.now = e.now.Add(time.Hour)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if st = e.getStage().Status; !slices.Contains(st.FailedDigests, dA) || len(st.Verified) != 0 || len(e.ops()) != 0 {
		t.Fatalf("a failed shadow verify rolls nothing back: %+v", st)
	}
}

// TestShadowEarnsAdvise: with proposeAfter verified versions GitOps applied
// and the applyRef labelled, Pacto proposes advise; another person confirms
// and the Stage adopts the observed digest.
func TestShadowEarnsAdvise(t *testing.T) {
	e := observeEnv(t)
	s := e.getStage()
	s.Spec.Autonomy.ProposeAfter = 1
	if err := e.c.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(2 * time.Minute)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if p := e.getStage().Status.Proposal; p.Rung != "" || len(p.Blockers) != 1 || p.Blockers[0].Kind != delivery.BlockerUnlabelledApplyRef {
		t.Fatalf("verified, still unlabelled: %+v", p)
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCI)
	_ = e.c.Get(context.Background(), keyOf("payments"), u)
	u.SetLabels(map[string]string{ApplyRefLabel: "true"})
	unstructured.RemoveNestedField(u.Object, "spec", "ref", "digest")
	if err := e.c.Update(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	st := e.getStage().Status
	if st.Proposal.Rung != delivery.RungAdvise || !slices.Equal(st.Proposal.Evidence, []string{dA}) {
		t.Fatalf("advise is proposed: %+v", st.Proposal)
	}
	e.create(bind(opObj("up", delivery.TypeAutonomy, "observe", "advise", "alice"), e.getStage()))
	o := e.drive("up")
	e.approve(o, "other", "bob", delivery.Approve)
	wantOutcome(t, e.drive("up"), delivery.Succeeded, "RungChanged")
	if st = e.getStage().Status; st.Rung != delivery.RungAdvise || st.Pointer != dA || st.Proposal != nil {
		t.Fatalf("raised: %+v", st)
	}
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if p := e.getStage().Status.Proposal; p == nil || p.Rung != "" || !strings.Contains(p.Blockers[0].Message, "0 of 1") {
		t.Fatalf("the count restarts on advise: %+v", p)
	}
}

// stepDownEnv ships B on a Stage at rung through a promote the promoter
// raised, with the health check answering result.
func stepDownEnv(t *testing.T, rung delivery.Rung, actor string, result delivery.Result) (*env, *dv1.Operation) {
	t.Helper()
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, actor))
	e.setStage(func(st *delivery.StageStatus) { st.Rung = rung })
	e.health.result = result
	return e, e.shipB()
}

// TestStepDownAfterAConfirmedFailure: an automatic promote that fails on a
// confirmed Fail lowers the Stage one rung through a promoter-raised
// autonomy operation that needs no approval and restarts the count.
func TestStepDownAfterAConfirmedFailure(t *testing.T) {
	e, o := stepDownEnv(t, delivery.RungAutonomous, promoterSA, delivery.Fail)
	wantOutcome(t, o, delivery.Failed, "VerifyFailed")
	sd := e.getOp("p1-stepdown")
	if sd.Spec.Type != delivery.TypeAutonomy || sd.Spec.Params != (delivery.Params{From: "autonomous", To: "auto-nonbreaking"}) ||
		sd.Spec.Actor.User != promoterSA || sd.Spec.Cause.Operation != "uid-p1" || sd.Spec.Cause.Verdict.Gate != "health" {
		t.Fatalf("step-down: %+v", sd.Spec)
	}
	if err := e.c.Get(context.Background(), keyOf("p1-rollback"), &dv1.Operation{}); err != nil {
		t.Fatalf("the rollback is raised beside it: %v", err)
	}
	// Raising it again is a no-op.
	if squat, err := raiseStepDown(context.Background(), e.c, e.op.Policy, o, e.getStage(), "again", sd.Spec.Cause.Verdict); err != nil || squat != "" {
		t.Fatal(squat, err)
	}
	wantOutcome(t, e.drive("p1-stepdown"), delivery.Succeeded, "RungChanged")
	st := e.getStage().Status
	if st.Rung != delivery.RungAutoNonBreaking || len(sd.Status.Approvals) != 0 {
		t.Fatalf("stepped down with no approval: %+v", st)
	}
	if c := st.Stream.Concluded[len(st.Stream.Concluded)-1]; c.Type != delivery.TypeAutonomy || c.Person {
		t.Fatalf("the step-down is the newest conclusion, raised by no person: %+v", c)
	}
	// The rollback of that same failure concludes after the step-down; it is
	// why the rung dropped, so the count restarts at zero with no incident.
	e.health.result = delivery.Pass
	e.drive("p1-rollback")
	e.apply(dA)
	e.evaluate(c1, "Compliant")
	rb := e.drive("p1-rollback")
	wantOutcome(t, rb, delivery.Succeeded, "Verified")
	st = e.getStage().Status
	if c := st.Stream.Concluded[len(st.Stream.Concluded)-1]; c.UID != string(rb.UID) || c.Cause != "uid-p1" {
		t.Fatalf("the rollback concludes after the step-down, citing p1: %+v %+v", c, rb.Status)
	}
	if p := delivery.ProposeRung(e.getStage().Spec, st, nil); slices.ContainsFunc(p.Blockers, func(b delivery.Blocker) bool { return b.Kind == delivery.BlockerIncident }) ||
		!strings.Contains(p.Blockers[0].Message, "0 of") {
		t.Fatalf("no incident blocks re-earning the rung: %+v", p)
	}
}

// TestStepDownRepeatedFailures: each confirmed failure of an automatic
// promote lowers one more rung, down to advise, where the promoter raises
// no promote and nothing steps down further.
func TestStepDownRepeatedFailures(t *testing.T) {
	e, o := stepDownEnv(t, delivery.RungAutoNonBreaking, promoterSA, delivery.Fail)
	wantOutcome(t, o, delivery.Failed, "VerifyFailed")
	wantOutcome(t, e.drive("p1-stepdown"), delivery.Succeeded, "RungChanged")
	if r := e.getStage().Status.Rung; r != delivery.RungAdvise {
		t.Fatalf("auto-nonbreaking steps down to advise: %s", r)
	}
	n := len(e.ops())
	if squat, err := raiseStepDown(context.Background(), e.c, e.op.Policy, o, e.getStage(), "again", &delivery.Verdict{Result: delivery.Fail}); err != nil || squat != "" || len(e.ops()) != n {
		t.Fatalf("nothing steps down below advise: %v %q %d", err, squat, len(e.ops()))
	}
}

// TestNoStepDown: Unknown never steps down.
func TestNoStepDown(t *testing.T) {
	e, _ := stepDownEnv(t, delivery.RungAutonomous, promoterSA, delivery.Unknown)
	e.now = e.now.Add(2 * time.Minute)
	o := e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld {
		t.Fatalf("Unknown holds: %+v", o.Status)
	}
	e.approve(o, "no", "bob", delivery.Reject)
	wantOutcome(t, e.drive("p1"), delivery.Failed, "RejectedByHuman")
	for _, x := range e.ops() {
		if x.Spec.Type == delivery.TypeAutonomy {
			t.Fatalf("an Unknown verify never steps down: %+v", x.Spec)
		}
	}
}

// TestStepDownAfterAnAgentPromote: on autonomous an agent's promote ships
// with no person, so its confirmed failure steps the Stage down like the
// promoter's would.
func TestStepDownAfterAnAgentPromote(t *testing.T) {
	e, o := stepDownEnv(t, delivery.RungAutonomous, bot, delivery.Fail)
	wantOutcome(t, o, delivery.Failed, "VerifyFailed")
	if o.Status.ActorClass != delivery.ClassAgent || len(o.Status.Approvals) != 0 {
		t.Fatalf("an agent's promote with no approval: %+v", o.Status)
	}
	if sd := e.getOp("p1-stepdown"); sd.Spec.Params != (delivery.Params{From: "autonomous", To: "auto-nonbreaking"}) {
		t.Fatalf("an agent's automatic promote failing steps down: %+v", sd.Spec)
	}
}

// TestStepDownAfterARecheckFailure: a version the promoter shipped that
// fails its re-check after release steps the Stage down too.
func TestStepDownAfterARecheckFailure(t *testing.T) {
	e, o := stepDownEnv(t, delivery.RungAutonomous, promoterSA, delivery.Pass)
	wantOutcome(t, o, delivery.Succeeded, "Verified")
	e.health.result = delivery.Fail
	e.now = e.now.Add(time.Hour)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	sd := e.getOp("p1-stepdown")
	if sd.Spec.Params.To != string(delivery.RungAutoNonBreaking) || !strings.Contains(sd.Spec.Reason, "RecheckFailed") {
		t.Fatalf("step-down after release: %+v", sd.Spec)
	}
}

// TestShadowEdges: errors are returned for a retry, a revision that is not a
// digest writes nothing, a frozen Stage's shadow says it would wait, and a
// verify about a digest GitOps moved off while the gates ran is dropped.
func TestShadowEdges(t *testing.T) {
	ctx := context.Background()
	e := observeEnv(t)
	e.fails["get/OCIRepository"] = true
	if err := e.st.shadow(ctx, e.getStage()); err == nil {
		t.Fatal("an applier read error is returned")
	}
	e.fails["get/OCIRepository"] = false
	up := e.getStage()
	up.Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "payments-test"}}
	if err := e.c.Update(ctx, up); err != nil {
		t.Fatal(err)
	}
	e.fails["get/Stage/payments-test"] = true
	if err := e.st.shadow(ctx, e.getStage()); err == nil {
		t.Fatal("a planning read error is returned")
	}
	e.fails["get/Stage/payments-test"] = false
	e.fails["status/Stage"] = true
	if err := e.st.shadow(ctx, e.getStage()); err == nil {
		t.Fatal("a status write error is returned")
	}
	e.fails["status/Stage"] = false

	busy := observeEnv(t)
	if err := busy.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	busy.apply(dB)
	busy.fails["status/Stage"] = true
	if err := busy.reconcileStage("payments-prod"); err == nil || busy.getStage().Status.ObservedRevision.Digest != dA {
		t.Fatalf("the reconcile returns the shadow's error: %v", err)
	}

	tag := observeEnv(t)
	tag.apply("v1.2.3") // a tag, not a digest
	if err := tag.st.shadow(ctx, tag.getStage()); err != nil || tag.getStage().Status.ObservedRevision != nil {
		t.Fatalf("a revision that is not a digest writes nothing: %v", err)
	}

	frozen := observeEnv(t)
	frozen.setStage(func(st *delivery.StageStatus) { st.Frozen, st.FrozenReason = true, "a person froze it" })
	if err := frozen.st.shadow(ctx, frozen.getStage()); err != nil {
		t.Fatal(err)
	}
	if a := shadowOf(frozen.getStage().Status, dA); a == nil || a.Predicted != delivery.PredictWaits || !strings.HasPrefix(a.Question, "StageFrozen: ") {
		t.Fatalf("a frozen Stage would wait: %+v", a)
	}

	moved := observeEnv(t)
	moved.st.Gates = NewGates(moved.op.Cluster, map[string]delivery.Gate{"mira": moved.health})
	if err := moved.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	s := moved.getStage()
	s.Spec.Gates.Verify.Checks = healthChecks
	if err := moved.c.Update(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := moved.reconcileStage("payments-prod"); err != nil { // the spec edit replans the candidate
		t.Fatal(err)
	}
	moved.now = moved.now.Add(2 * time.Minute)
	moved.health.during = func() {
		moved.setStage(func(st *delivery.StageStatus) {
			st.ObservedRevision = &delivery.ObservedRevision{Digest: dB, Since: "x"}
		})
	}
	if err := moved.st.shadow(ctx, moved.getStage()); err != nil || len(moved.getStage().Status.Verified) != 0 {
		t.Fatalf("a verify about a digest GitOps moved off is dropped: %v %+v", err, moved.getStage().Status)
	}
	moved.setStage(func(st *delivery.StageStatus) {
		st.ObservedRevision = &delivery.ObservedRevision{Digest: dA, Since: ts(moved.now.Add(-time.Hour))}
	})
	moved.now = moved.now.Add(time.Hour)
	moved.health.during = func() { moved.setFail("status/Stage", true) }
	if err := moved.st.shadow(ctx, moved.getStage()); err == nil {
		t.Fatal("a verify's status write error is returned")
	}
}

// TestProposeReadsTheNewestPlan: on auto-nonbreaking the proposal reads the
// graph from the plan of the operation that last moved the pointer.
func TestProposeReadsTheNewestPlan(t *testing.T) {
	e, o := stepDownEnv(t, delivery.RungAutoNonBreaking, bot, delivery.Pass)
	wantOutcome(t, o, delivery.Succeeded, "Verified")
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	p := e.getStage().Status.Proposal
	if p == nil || slices.ContainsFunc(p.Blockers, func(b delivery.Blocker) bool { return b.Kind == delivery.BlockerPartialGraph }) {
		t.Fatalf("the newest plan saw a complete graph: %+v", p)
	}
	e.setStage(func(st *delivery.StageStatus) { st.LastOp = "uid-gone" })
	if err := e.st.propose(context.Background(), e.getStage()); err != nil {
		t.Fatal(err)
	}
	if p = e.getStage().Status.Proposal; !slices.ContainsFunc(p.Blockers, func(b delivery.Blocker) bool { return b.Kind == delivery.BlockerPartialGraph }) {
		t.Fatalf("no plan to read the graph from: %+v", p)
	}
	e.fails["list/Operation"] = true
	if err := e.st.propose(context.Background(), e.getStage()); err == nil {
		t.Fatal("a list error is returned")
	}
	st := e.getStage()
	st.Status.LastCheck = &delivery.Recheck{Operation: st.Status.LastOp, At: ts(e.now)}
	if err := e.c.Status().Update(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err == nil {
		t.Fatal("the reconcile returns the proposal's error")
	}
}

// TestStepDownRaiseErrors: a step-down that cannot be raised is retried, and
// a name someone else took freezes the Stage.
func TestStepDownRaiseErrors(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, promoterSA))
	e.health.result = delivery.Fail
	e.drive("p1")
	e.apply(dB)
	e.evaluate(c2, "Compliant")
	e.fails["create/Operation"] = true
	var err error
	for range 5 {
		if err = e.reconcileOpErr("p1"); err != nil {
			break
		}
	}
	if err == nil || e.getOp("p1").Status.Phase == delivery.PhaseConcluded {
		t.Fatalf("a step-down create error is returned before the conclusion: %v", err)
	}

	r, o := stepDownEnv(t, delivery.RungAutonomous, promoterSA, delivery.Pass)
	wantOutcome(t, o, delivery.Succeeded, "Verified")
	r.health.result = delivery.Fail
	r.now = r.now.Add(time.Hour)
	r.fails["create/Operation"] = true
	if err := r.reconcileStage("payments-prod"); err == nil {
		t.Fatal("a step-down create error after release is returned")
	}
	r.fails["create/Operation"] = false
	r.create(bind(opObj("p1-stepdown", delivery.TypeAutonomy, "autonomous", "observe", bot), r.getStage()))
	wantOutcome(t, r.drive("p1-stepdown"), delivery.Rejected, "")
	if err := r.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if s := r.getStage().Status; !slices.Contains(s.RolledBack, "uid-p1") || !s.Frozen || !strings.Contains(s.FrozenReason, "p1-stepdown is taken") {
		t.Fatalf("a squatted step-down rolls back and freezes, naming the squatter: %+v", s)
	}
}
