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

	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/client-go/tools/record"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// drain returns the Events a fake recorder holds.
func drain(r *record.FakeRecorder) []string {
	var out []string
	for {
		select {
		case e := <-r.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func hasEvent(events []string, prefix string) bool {
	for _, e := range events {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// TestPromoterIsObservable: a concluded operation is counted with its
// verdicts and emits an Event; a re-check that comes back Unknown sets
// Watched=False and the watched gauge to 0 and says so once; a freeze and a
// raised rollback emit Events; a deleted Stage drops its series.
func TestPromoterIsObservable(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
	opEvents, stEvents := record.NewFakeRecorder(100), record.NewFakeRecorder(100)
	e.op.Events, e.st.Events = opEvents, stEvents
	shipped := Operations.WithLabelValues(delivery.TypePromote, string(delivery.Succeeded), string(delivery.Verified), string(delivery.ClassAgent))
	before, healthPass := promtest.ToFloat64(shipped), promtest.ToFloat64(Verdicts.WithLabelValues("health", "verify", string(delivery.Pass)))
	wantOutcome(t, e.shipB(), delivery.Succeeded, "Verified")
	if promtest.ToFloat64(shipped) != before+1 || promtest.ToFloat64(Verdicts.WithLabelValues("health", "verify", string(delivery.Pass))) <= healthPass {
		t.Fatal("the conclusion and its verdicts are not counted")
	}
	if ev := drain(opEvents); !hasEvent(ev, "Normal Succeeded Verified") {
		t.Fatalf("operation events: %v", ev)
	}

	watchedGauge := func() float64 { return promtest.ToFloat64(StageWatched.WithLabelValues(ns, "payments-prod")) }
	e.health.result = delivery.Unknown
	for range 2 { // said once, not on every Unknown
		e.now = e.now.Add(10 * time.Minute)
		if err := e.reconcileStage("payments-prod"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	w := conditionOf(e.getStage().Status.Conditions, ConditionWatched)
	if w == nil || w.Status != statusFalse || w.Reason != "RecheckUnknown" || !strings.Contains(w.Message, "health: error ratio Unknown") || watchedGauge() != 0 {
		t.Fatalf("an Unknown re-check: %+v, gauge %v", w, watchedGauge())
	}
	if ev := drain(stEvents); len(ev) != 1 || !strings.HasPrefix(ev[0], "Warning RecheckUnknown nothing watches the running version") {
		t.Fatalf("stage events: %v", ev)
	}
	e.health.result = delivery.Pass
	e.now = e.now.Add(10 * time.Minute)
	for range 2 {
		if err := e.reconcileStage("payments-prod"); err != nil {
			t.Fatal(err)
		}
	}
	if w := conditionOf(e.getStage().Status.Conditions, ConditionWatched); w.Status != "True" || w.Reason != "Rechecked" || watchedGauge() != 1 {
		t.Fatalf("a passing re-check: %+v, gauge %v", w, watchedGauge())
	}

	e.health.result = delivery.Fail
	e.now = e.now.Add(10 * time.Minute)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if w := conditionOf(e.getStage().Status.Conditions, ConditionWatched); w.Reason != "RecheckFailed" {
		t.Fatalf("a failed re-check: %+v", w)
	}
	if ev := drain(stEvents); !hasEvent(ev, "Warning RollbackRaised p1 RecheckFailed: health") {
		t.Fatalf("rollback event: %v", ev)
	}

	e.setStage(func(s *delivery.StageStatus) { s.Frozen, s.FrozenReason = true, "a person froze it" })
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if ev := drain(stEvents); !hasEvent(ev, "Warning Frozen a person froze it") || promtest.ToFloat64(StageFrozen.WithLabelValues(ns, "payments-prod")) != 1 {
		t.Fatalf("freeze event %v", ev)
	}

	series := promtest.CollectAndCount(StageFrozen)
	if err := e.c.Delete(context.Background(), e.getStage()); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil || promtest.CollectAndCount(StageFrozen) != series-1 {
		t.Fatalf("a deleted Stage keeps its series: %v", err)
	}
	(&StageReconciler{}).observe(stageObj(dA)) // no memory, no recorder: metrics only
}

// TestHeldAndRolledBackEmitEvents: an operation waiting on a person and a
// promote that failed verify say so in Events.
func TestHeldAndRolledBackEmitEvents(t *testing.T) {
	e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
	rec := record.NewFakeRecorder(100)
	e.op.Events = rec
	e.health.result = delivery.Unknown
	e.shipB()
	e.now = e.now.Add(2 * time.Minute)
	e.drive("p1")
	if ev := drain(rec); !hasEvent(ev, "Warning Held VerifyUnknown: The verify gates could not confirm") {
		t.Fatalf("held: %v", ev)
	}
	e = baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.op.Events = rec
	e.drive("p1")
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	e.drive("p1")
	if ev := drain(rec); !hasEvent(ev, "Warning RollbackRaised p1 failed (VerifyFailed") || !hasEvent(ev, "Warning Failed VerifyFailed") {
		t.Fatalf("failed: %v", ev)
	}
}

func conditionOf(cs []delivery.Condition, typ string) *delivery.Condition {
	for i := range cs {
		if cs[i].Type == typ {
			return &cs[i]
		}
	}
	return nil
}
