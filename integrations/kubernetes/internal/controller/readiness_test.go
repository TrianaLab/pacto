/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/api/v1alpha1"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

var readinessNow = time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

const (
	futureExpires = "2026-12-31"
	pastExpires   = "2026-01-15"
)

func pinReadinessClock(t *testing.T, at time.Time) {
	t.Helper()
	old := readinessClock
	readinessClock = func() time.Time { return at }
	t.Cleanup(func() { readinessClock = old })
}

// rdCheck builds a declared readiness claim with the given id, weight and status.
func rdCheck(id string, weight int, status string) contract.ReadinessClaim {
	return contract.ReadinessClaim{
		ID:       id,
		Type:     "url",
		Category: "documentation",
		Status:   status,
		Evidence: "https://example.com/" + id,
		Weight:   weight,
	}
}

// rdContractExpires builds a v1.2 contract whose readiness expires on the given
// date (assessment-level). With no claims, no readiness block is set.
func rdContractExpires(expires string, claims ...contract.ReadinessClaim) *contract.Contract {
	c := &contract.Contract{
		PactoVersion: "1.2",
		Service:      contract.Service{Name: "svc", Version: "1.0.0"},
	}
	if len(claims) > 0 {
		c.Readiness = &contract.Readiness{Expires: expires, Claims: claims}
	}
	return c
}

// rdContract builds a v1.2 contract with a far-future (current) assessment expiry.
func rdContract(claims ...contract.ReadinessClaim) *contract.Contract {
	return rdContractExpires(futureExpires, claims...)
}

func drainEvents(rec *record.FakeRecorder) []string {
	var out []string
	for {
		select {
		case e := <-rec.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

// ---------- readinessCondition ----------

func TestReadinessCondition(t *testing.T) {
	cases := []struct {
		name       string
		res        *readiness.Result
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{"passing", &readiness.Result{Passing: true, Score: 100, MinScore: 100, Checks: make([]readiness.CheckResult, 2)}, metav1.ConditionTrue, pactov1alpha1.ReasonReadinessSatisfied},
		{"below min score", &readiness.Result{Passing: false, Score: 60, MinScore: 100, DoneCount: 1, NotDoneCount: 1, Checks: make([]readiness.CheckResult, 2)}, metav1.ConditionFalse, pactov1alpha1.ReasonReadinessBelowMinScore},
		{"expired", &readiness.Result{Passing: false, Score: 0, MinScore: 100, Expired: true, Expires: pastExpires, Checks: make([]readiness.CheckResult, 2)}, metav1.ConditionFalse, pactov1alpha1.ReasonReadinessExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason, msg := readinessCondition(tc.res)
			if status != tc.wantStatus {
				t.Errorf("status: want %s got %s", tc.wantStatus, status)
			}
			if reason != tc.wantReason {
				t.Errorf("reason: want %s got %s", tc.wantReason, reason)
			}
			if msg == "" {
				t.Error("expected non-empty message")
			}
		})
	}
}

// ---------- buildReadinessStatus ----------

func TestBuildReadinessStatus(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.2",
		Service:      contract.Service{Name: "svc", Version: "1.0.0"},
		Readiness: &contract.Readiness{
			Expires: futureExpires,
			History: []contract.ReadinessRevision{
				{Date: "2026-06-01", Version: "1.0.0", Author: "alice", Description: "initial assessment"},
			},
			Claims: []contract.ReadinessClaim{
				rdCheck("dashboard", 60, contract.StatusDone),
				rdCheck("runbook", 20, contract.StatusPartial),
				rdCheck("dr-drill", 20, contract.StatusDeferred),
			},
		},
	}
	eval := readiness.Evaluate(c.Readiness, readinessNow)
	rs := buildReadinessStatus(eval, c.Readiness)

	// deferred is excluded from the total: total = 60 + 20 = 80;
	// earned = 60 + round(20*0.5)=10 = 70; score = round(70/80*100) = 88.
	if rs.Score != 88 || rs.TotalWeight != 80 || rs.EarnedWeight != 70 {
		t.Errorf("unexpected summary: score=%d total=%d earned=%d", rs.Score, rs.TotalWeight, rs.EarnedWeight)
	}
	if rs.MinScore != 100 || rs.Passing {
		t.Errorf("expected default minScore 100 and not passing, got minScore=%d passing=%v", rs.MinScore, rs.Passing)
	}
	if rs.Expired || rs.Expires != futureExpires || rs.DaysRemaining == nil {
		t.Errorf("expected current assessment with daysRemaining, got expired=%v expires=%s days=%v", rs.Expired, rs.Expires, rs.DaysRemaining)
	}
	if rs.DoneCount != 1 || rs.PartialCount != 1 || rs.NotDoneCount != 0 || rs.DeferredCount != 1 {
		t.Errorf("unexpected counts: done=%d partial=%d notDone=%d deferred=%d", rs.DoneCount, rs.PartialCount, rs.NotDoneCount, rs.DeferredCount)
	}
	if len(rs.Claims) != 3 {
		t.Fatalf("expected 3 claims, got %d", len(rs.Claims))
	}
	if rs.Claims[0].Status != contract.StatusDone || rs.Claims[0].Category != "documentation" || rs.Claims[0].EarnedWeight != 60 || rs.Claims[0].Excluded {
		t.Errorf("unexpected done claim: %+v", rs.Claims[0])
	}
	if rs.Claims[1].Status != contract.StatusPartial || rs.Claims[1].EarnedWeight != 10 {
		t.Errorf("unexpected partial claim: %+v", rs.Claims[1])
	}
	if rs.Claims[2].Status != contract.StatusDeferred || !rs.Claims[2].Excluded || rs.Claims[2].EarnedWeight != 0 {
		t.Errorf("expected deferred claim excluded with 0 earned, got %+v", rs.Claims[2])
	}
	if len(rs.Revisions) != 1 || rs.Revisions[0].Author != "alice" || rs.Revisions[0].Version != "1.0.0" {
		t.Errorf("unexpected revisions: %+v", rs.Revisions)
	}
}

func TestBuildReadinessStatus_Expired(t *testing.T) {
	c := rdContractExpires(pastExpires, rdCheck("dashboard", 100, contract.StatusDone))
	eval := readiness.Evaluate(c.Readiness, readinessNow)
	rs := buildReadinessStatus(eval, c.Readiness)

	// An expired assessment zeroes earned weight even though the check is done.
	if !rs.Expired || rs.Score != 0 || rs.EarnedWeight != 0 || rs.DaysRemaining != nil {
		t.Errorf("expected expired assessment with score 0, got %+v", rs)
	}
}

// ---------- reconcileReadiness ----------

func newReadinessReconciler() (*PactoReconciler, *record.FakeRecorder) {
	rec := record.NewFakeRecorder(20)
	return &PactoReconciler{Recorder: rec}, rec
}

func TestReconcileReadiness_NoReadiness(t *testing.T) {
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	r.reconcileReadiness(pacto, rdContract(), false)

	if pacto.Status.Readiness != nil {
		t.Errorf("expected no readiness status, got %+v", pacto.Status.Readiness)
	}
	if meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied) != nil {
		t.Error("expected no readiness condition")
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Errorf("expected no events, got %v", events)
	}
}

func TestReconcileReadiness_Passing_NoEvent(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	r.reconcileReadiness(pacto, rdContract(rdCheck("dashboard", 100, contract.StatusDone)), false)

	if pacto.Status.Readiness == nil || pacto.Status.Readiness.Score != 100 {
		t.Fatalf("expected readiness score 100, got %+v", pacto.Status.Readiness)
	}
	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != pactov1alpha1.ReasonReadinessSatisfied {
		t.Fatalf("expected True/Satisfied, got %+v", cond)
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Errorf("expected no events when passing, got %v", events)
	}
}

func TestReconcileReadiness_BelowMin_EmitsWarningOnTransition(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	// A not-done check scores 0, below the default minScore 100; wasUnmet=false → warning.
	r.reconcileReadiness(pacto, rdContract(rdCheck("security-review", 50, contract.StatusNotDone)), false)

	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != pactov1alpha1.ReasonReadinessBelowMinScore {
		t.Fatalf("expected False/BelowMinScore, got %+v", cond)
	}
	events := drainEvents(rec)
	if len(events) != 1 || !strings.Contains(events[0], pactov1alpha1.EventReadinessGateUnmet) || !strings.Contains(events[0], "Warning") {
		t.Errorf("expected one Warning ReadinessGateUnmet event, got %v", events)
	}
}

func TestReconcileReadiness_BelowMin_NoEventWhenAlreadyUnmet(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	// wasUnmet=true → steady below-min → no event (avoid spam).
	r.reconcileReadiness(pacto, rdContract(rdCheck("security-review", 50, contract.StatusNotDone)), true)

	if events := drainEvents(rec); len(events) != 0 {
		t.Errorf("expected no event for steady-unmet, got %v", events)
	}
}

func TestReconcileReadiness_Recovered_EmitsNormalEvent(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	// wasUnmet=true and now passing → recovery event.
	r.reconcileReadiness(pacto, rdContract(rdCheck("dashboard", 100, contract.StatusDone)), true)

	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected True condition, got %+v", cond)
	}
	events := drainEvents(rec)
	if len(events) != 1 || !strings.Contains(events[0], pactov1alpha1.EventReadinessRecovered) || !strings.Contains(events[0], "Normal") {
		t.Errorf("expected one Normal ReadinessRecovered event, got %v", events)
	}
}

func TestReconcileReadiness_Expired(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, rec := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	// A past assessment expiry → expired; the done check still earns 0.
	r.reconcileReadiness(pacto, rdContractExpires(pastExpires, rdCheck("dashboard", 100, contract.StatusDone)), false)

	if pacto.Status.Readiness == nil || !pacto.Status.Readiness.Expired || pacto.Status.Readiness.Score != 0 {
		t.Fatalf("expected expired readiness with score 0, got %+v", pacto.Status.Readiness)
	}
	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != pactov1alpha1.ReasonReadinessExpired {
		t.Fatalf("expected False/Expired, got %+v", cond)
	}
	if events := drainEvents(rec); len(events) != 1 {
		t.Errorf("expected one event for expired transition, got %v", events)
	}
}

func TestReconcileReadiness_InvalidExpiresFailsClosed(t *testing.T) {
	pinReadinessClock(t, readinessNow)
	r, _ := newReadinessReconciler()
	pacto := &pactov1alpha1.Pacto{}
	// An unparseable assessment expiry is treated as expired (fail-closed).
	r.reconcileReadiness(pacto, rdContractExpires("not-a-date", rdCheck("dashboard", 100, contract.StatusDone)), false)

	if pacto.Status.Readiness == nil || !pacto.Status.Readiness.Expired {
		t.Fatalf("expected expired (fail-closed) readiness, got %+v", pacto.Status.Readiness)
	}
	cond := meta.FindStatusCondition(pacto.Status.Conditions, pactov1alpha1.ConditionReadinessSatisfied)
	if cond == nil || cond.Reason != pactov1alpha1.ReasonReadinessExpired {
		t.Fatalf("expected Expired reason, got %+v", cond)
	}
}
