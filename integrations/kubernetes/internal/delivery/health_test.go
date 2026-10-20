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

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// healthChecks is a Stage that verifies with a health provider beside the
// Pacto contract check.
var healthChecks = []delivery.GateCheck{{Name: "health", Provider: "mira", Params: map[string]string{"maxErrorRatio": "0.05"}}}

// healthEnv is baseEnv with the Stage verifying through checks, and op (built
// with opObj) bound to that Stage's policy.
func healthEnv(t *testing.T, checks []delivery.GateCheck, op *dv1Op) *env {
	t.Helper()
	e := baseEnv(t, nil)
	s := e.getStage()
	s.Spec.Gates.Verify.Checks = checks
	if err := e.c.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if op != nil {
		e.create(bind(op, s))
	}
	return e
}

func bind(op *dv1Op, s *dv1.Stage) *dv1Op {
	op.Spec.IntentDigest = delivery.IntentDigest(op.Spec, s.Spec)
	return op
}

// shipB drives p1 (A -> B) until it verifies: Flux applies B and the operator
// evaluates B's contract as Compliant.
func (e *env) shipB() *dv1Op {
	e.t.Helper()
	e.drive("p1")
	e.apply(dB)
	e.evaluate(c2, "Compliant")
	return e.drive("p1")
}

func verdictOf(vs []delivery.Verdict, gate string) delivery.Verdict {
	for _, v := range vs {
		if v.Gate == gate {
			return v
		}
	}
	return delivery.Verdict{}
}

// TestHealthGateVerifies: the health provider is asked about exactly the
// applied version over the time since Flux applied it, and its Pass beside the
// contract check verifies the version.
func TestHealthGateVerifies(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
	o := e.shipB()
	wantOutcome(t, o, delivery.Succeeded, "Verified")
	in := e.health.inputs[len(e.health.inputs)-1]
	if in.OperationUID != "uid-p1" || in.Subjects[0] != dB || in.Service.Name != "payments" || in.Stage.Name != "payments-prod" ||
		!in.Window.From.Equal(e.now) || !in.Window.To.Equal(e.now) {
		t.Fatalf("gate input: %+v", in)
	}
	h := verdictOf(o.Status.Verdicts, "health")
	if h.Result != delivery.Pass || h.Citation["provider"] != "mira" || h.Citation["params"] != `{"maxErrorRatio":"0.05"}` ||
		!strings.Contains(h.Citation["input"], dB) || verdictOf(o.Status.Verdicts, delivery.GateContract).Result != delivery.Pass {
		t.Fatalf("verdicts: %+v", o.Status.Verdicts)
	}
}

// TestHealthFailRollsBackWithTheQuery: a health Fail fails the version, and
// the one bound rollback cites the failing verdict, query included.
func TestHealthFailRollsBackWithTheQuery(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.health.result = delivery.Fail
	o := e.shipB()
	wantOutcome(t, o, delivery.Failed, "VerifyFailed")
	if !strings.HasPrefix(o.Status.Message, "health: error ratio Fail") {
		t.Fatalf("message: %s", o.Status.Message)
	}
	rb := e.getOp("p1-rollback")
	if c := rb.Spec.Cause; c == nil || c.Operation != "uid-p1" || c.Verdict == nil || c.Verdict.Gate != "health" ||
		c.Verdict.Citation["query"] != `{"signal":"traces"}` {
		t.Fatalf("rollback cause: %+v", rb.Spec.Cause)
	}
}

// TestHealthUnknownHolds: too little telemetry is Unknown; it never verifies
// and never rolls back, and at the window's end a person is asked.
func TestHealthUnknownHolds(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.health.result = delivery.Unknown
	o := e.shipB()
	if o.Status.Phase != delivery.PhaseVerifying || o.Status.Message != "health: error ratio Unknown" {
		t.Fatalf("want Verifying on Unknown health: %s %q", o.Status.Phase, o.Status.Message)
	}
	e.now = e.now.Add(2 * time.Minute)
	o = e.drive("p1")
	if o.Status.Phase != delivery.PhaseHeld || o.Status.Reason != "VerifyUnknown" || !strings.Contains(o.Status.Question, "health: error ratio Unknown") {
		t.Fatalf("want Held after the window: %+v", o.Status)
	}
	for _, x := range e.ops() {
		if x.Spec.Type == delivery.TypeRollback {
			t.Fatal("Unknown must not roll back")
		}
	}
}

// TestRuntimeClaimGate: a version the node agent saw calling a service its
// contract never declares fails the claim gate, citing what was seen, though
// Pacto's own contract status for it is only a Warning.
func TestRuntimeClaimGate(t *testing.T) {
	e := healthEnv(t, []delivery.GateCheck{{Name: "claims", Provider: delivery.GateRuntime}}, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.drive("p1")
	e.apply(dB)
	p := pactoObj("payments", "payments", c2, "Warning", 2)
	p.Object["status"].(map[string]any)["evaluatedRevision"].(map[string]any)["kernelCoverage"] = "Complete"
	p.Object["status"].(map[string]any)["findings"] = []any{map[string]any{"code": "DEPENDENCY_UNDECLARED", "severity": "warning",
		"subject": "dependency/fraud", "message": "m", "evidenceRefs": []any{map[string]any{"collector": "ebpf", "detectedAt": "2026-10-20T10:00:00Z"}}}}
	_ = e.c.Delete(context.Background(), p)
	e.create(p)
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Failed, "VerifyFailed")
	v := verdictOf(o.Status.Verdicts, "claims")
	if v.Result != delivery.Fail || !strings.Contains(v.Citation["payments.findings"], "DEPENDENCY_UNDECLARED dependency/fraud (ebpf") ||
		verdictOf(o.Status.Verdicts, delivery.GateContract).Result != delivery.Pass {
		t.Fatalf("claim verdict: %+v", o.Status.Verdicts)
	}
}

// TestRecheckAfterRelease: checks keep running once a version shipped. A
// confirmed Fail marks it failed and raises exactly one rollback; Unknown and a
// check inside the interval do nothing.
func TestRecheckAfterRelease(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
	wantOutcome(t, e.shipB(), delivery.Succeeded, "Verified")
	shippedAt := e.now

	e.now = e.now.Add(10 * time.Minute)
	e.health.result = delivery.Unknown
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	s := e.getStage()
	if lc := s.Status.LastCheck; lc == nil || lc.Operation != "uid-p1" || lc.Digest != dB || verdictOf(lc.Verdicts, "health").Result != delivery.Unknown {
		t.Fatalf("an Unknown re-check is recorded: %+v", s.Status.LastCheck)
	}
	if len(e.ops()) != 1 || len(s.Status.FailedDigests) != 0 {
		t.Fatal("an Unknown re-check must not roll back")
	}
	// The re-check judges the last window, never more than the verify window.
	if in := e.health.inputs[len(e.health.inputs)-1]; !in.Window.From.Equal(e.now.Add(-time.Minute)) || !in.Window.From.After(shippedAt) {
		t.Fatalf("re-check window: %+v", in.Window)
	}

	e.health.result = delivery.Fail
	asked := len(e.health.inputs)
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.health.inputs) != asked {
		t.Fatalf("a re-check inside the interval must not run: %v", err)
	}
	e.now = e.now.Add(time.Minute)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	s = e.getStage()
	if s.Status.FailedDigests[0] != dB || len(s.Status.Verified) != 1 || s.Status.AutoRollbacks["uid-p1"] != 1 || s.Status.Frozen {
		t.Fatalf("stage after a failed re-check: %+v", s.Status)
	}
	rb := e.getOp("p1-rollback")
	if rb.Spec.Params.From != dB || rb.Spec.Params.To != dA || rb.Spec.Cause.Verdict.Gate != "health" || !strings.Contains(rb.Spec.Reason, "RecheckFailed") {
		t.Fatalf("rollback: %+v", rb.Spec)
	}
	// While the rollback is in flight, nothing re-checks or raises another.
	e.now = e.now.Add(time.Hour)
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 2 {
		t.Fatalf("one rollback per cause: %v %d", err, len(e.ops()))
	}
	e.health.result = delivery.Pass
	e.drive("p1-rollback")
	e.apply(dA)
	e.evaluate(c1, "Compliant")
	wantOutcome(t, e.drive("p1-rollback"), delivery.Succeeded, "Verified")
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 2 || e.getStage().Status.LastCheck.Operation != "uid-p1-rollback" {
		t.Fatalf("after the rollback the version it restored is re-checked: %v %+v", err, e.getStage().Status.LastCheck)
	}
}

// TestRecheckFreezesWhenItCannotRollBack: a cause that already had its
// rollback, or a first version with nothing before it, freezes instead.
func TestRecheckFreezesWhenItCannotRollBack(t *testing.T) {
	for name, mutate := range map[string]func(*delivery.StageStatus){
		"second fail for the cause": func(s *delivery.StageStatus) { s.AutoRollbacks = map[string]int{"uid-p1": 1} },
		"rollback target failed":    func(s *delivery.StageStatus) { s.FailedDigests = []string{dA} },
	} {
		t.Run(name, func(t *testing.T) {
			e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
			e.shipB()
			e.setStage(mutate)
			e.health.result = delivery.Fail
			if err := e.reconcileStage("payments-prod"); err != nil {
				t.Fatal(err)
			}
			s := e.getStage()
			if !s.Status.Frozen || !strings.Contains(s.Status.FrozenReason, "cannot be rolled back automatically") {
				t.Fatalf("want frozen: %+v", s.Status)
			}
			for _, o := range e.ops() {
				if o.Spec.Type == delivery.TypeRollback {
					t.Fatal("no rollback when it cannot be bound")
				}
			}
		})
	}
}

// TestRecheckSkips: nothing to re-check on a frozen Stage, before anything
// shipped, while a lock is held, on an Unverified version or when the rollback
// cannot be created.
func TestRecheckSkips(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
	if err := e.reconcileStage("payments-prod"); err != nil || e.getStage().Status.LastCheck != nil {
		t.Fatal("nothing shipped through an operation yet")
	}
	e.shipB()
	for _, mutate := range []func(*delivery.StageStatus){
		func(s *delivery.StageStatus) { s.Frozen = true },
		func(s *delivery.StageStatus) { s.Frozen, s.Locks = false, map[string]string{"pointer": "x"} },
		func(s *delivery.StageStatus) { s.Locks, s.LastOp = nil, "uid-gone" },
	} {
		e.setStage(mutate)
		if err := e.reconcileStage("payments-prod"); err != nil || e.getStage().Status.LastCheck != nil {
			t.Fatalf("re-checked: %v %+v", err, e.getStage().Status.LastCheck)
		}
	}
	e.setStage(func(s *delivery.StageStatus) { s.LastOp = "uid-p1" })
	pending := opObj("p2", delivery.TypePromote, dB, dC, bot)
	pending.Finalizers = nil
	e.create(pending)
	if err := e.reconcileStage("payments-prod"); err != nil || e.getStage().Status.LastCheck != nil {
		t.Fatal("an operation in flight owns the Stage")
	}
	if err := e.c.Delete(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	e.setStage(func(s *delivery.StageStatus) { s.LastCheck = nil })
	e.health.result = delivery.Fail
	e.fails["create/Operation"] = true
	if err := e.reconcileStage("payments-prod"); err == nil {
		t.Fatal("a rollback that cannot be created is an error, retried")
	}
}

// TestCitedCauseIsReRun: an agent rolling back a frozen Stage cites a Fail
// verdict as its cause. The promoter re-runs it against the Stage's own gate;
// a re-run that passes, or comes back Unknown, is refused as a cause.
func TestCitedCauseIsReRun(t *testing.T) {
	for _, c := range []struct {
		now  delivery.Result
		want delivery.Outcome
	}{{delivery.Pass, delivery.Rejected}, {delivery.Unknown, delivery.Rejected}, {delivery.Fail, ""}} {
		t.Run(string(c.now), func(t *testing.T) {
			e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
			e.shipB()
			applied := e.now
			e.now = e.now.Add(5 * time.Minute)
			e.setStage(func(s *delivery.StageStatus) { s.Frozen = true })
			cited := delivery.RunChecks(context.Background(), map[string]delivery.Gate{"mira": &fakeHealth{result: delivery.Fail}},
				healthChecks, gateInput(e.getOp("p1"), e.getStage(), applied, e.now))[0]
			rb := opObj("rb", delivery.TypeRollback, dB, dA, bot)
			rb.Spec.Params.Undoes = "uid-p1"
			rb.Spec.Cause = &delivery.Cause{Verdict: &cited}
			e.create(bind(rb, e.getStage()))
			e.health.result = c.now
			o := e.drive("rb")
			if c.want != "" {
				wantOutcome(t, o, c.want, "CauseUnconfirmed")
				if !strings.Contains(o.Status.Message, "re-running it now says "+string(c.now)) {
					t.Fatalf("message: %s", o.Status.Message)
				}
				return
			}
			if o.Status.Phase != delivery.PhaseRunning || verdictOf(o.Status.Verdicts, "health").Phase != "pre" {
				t.Fatalf("a confirmed cause admits the rollback: %+v", o.Status)
			}
			// The re-run judged the cited window over the version the Stage runs.
			if in := e.health.inputs[len(e.health.inputs)-1]; in.Subjects[0] != dB || !in.Window.From.Equal(applied) {
				t.Fatalf("re-run input: %+v", in)
			}
		})
	}
}

// TestCitedCauseMustBeAboutTheRunningVersion: a verdict from before the
// running version was applied says nothing about it and is refused.
func TestCitedCauseMustBeAboutTheRunningVersion(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.shipB()
	e.setStage(func(s *delivery.StageStatus) { s.Frozen = true })
	cited := delivery.RunChecks(context.Background(), map[string]delivery.Gate{"mira": &fakeHealth{result: delivery.Fail}},
		healthChecks, gateInput(e.getOp("p1"), e.getStage(), e.now.Add(-time.Hour), e.now))[0]
	rb := opObj("rb", delivery.TypeRollback, dB, dA, bot)
	rb.Spec.Params.Undoes = "uid-p1"
	rb.Spec.Cause = &delivery.Cause{Verdict: &cited}
	e.create(bind(rb, e.getStage()))
	e.health.result = delivery.Fail
	o := e.drive("rb")
	wantOutcome(t, o, delivery.Rejected, "CauseUnconfirmed")
	if !strings.Contains(o.Status.Message, "outside the time the version under check has run") {
		t.Fatalf("message: %s", o.Status.Message)
	}
}
