package readiness

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
)

var fixedNow = time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

func check(id string, weight int, expires string) contract.ReadinessCheck {
	return contract.ReadinessCheck{
		ID:       id,
		Type:     contract.EvidenceTypeURL,
		Evidence: "https://example.com/" + id,
		Weight:   weight,
		Expires:  expires,
	}
}

func TestEvaluate_NilReadiness(t *testing.T) {
	if got := Evaluate(nil, fixedNow); got != nil {
		t.Errorf("expected nil result for nil readiness, got %+v", got)
	}
}

func TestEvaluate_EmptyChecks(t *testing.T) {
	if got := Evaluate(&contract.Readiness{}, fixedNow); got != nil {
		t.Errorf("expected nil result for empty checks, got %+v", got)
	}
}

func TestEvaluate_AllCurrent(t *testing.T) {
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{
		check("dashboard", 60, "2026-12-31"),
		check("runbook", 40, "2026-09-30"),
	}}
	got := Evaluate(r, fixedNow)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.TotalWeight != 100 {
		t.Errorf("expected total weight 100, got %d", got.TotalWeight)
	}
	if got.CurrentWeight != 100 {
		t.Errorf("expected current weight 100, got %d", got.CurrentWeight)
	}
	if got.Score != 100 {
		t.Errorf("expected score 100, got %d", got.Score)
	}
	if got.CurrentCount != 2 || got.ExpiredCount != 0 || got.InvalidCount != 0 {
		t.Errorf("unexpected counts: current=%d expired=%d invalid=%d", got.CurrentCount, got.ExpiredCount, got.InvalidCount)
	}
	if got.Checks[0].Status != StatusCurrent {
		t.Errorf("expected first check Current, got %s", got.Checks[0].Status)
	}
	if got.Checks[0].DaysRemaining == nil {
		t.Error("expected DaysRemaining to be set for current check")
	}
}

func TestEvaluate_PartiallyExpired_ScoreExample(t *testing.T) {
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{
		check("dashboard", 40, "2026-12-31"),       // current
		check("runbook", 20, "2026-09-30"),         // current
		check("security-review", 20, "2026-01-15"), // expired
	}}
	got := Evaluate(r, fixedNow)
	if got.TotalWeight != 80 {
		t.Errorf("expected total weight 80, got %d", got.TotalWeight)
	}
	if got.CurrentWeight != 60 {
		t.Errorf("expected current weight 60, got %d", got.CurrentWeight)
	}
	if got.Score != 75 {
		t.Errorf("expected score 75, got %d", got.Score)
	}
	if got.ExpiredCount != 1 || got.CurrentCount != 2 {
		t.Errorf("unexpected counts: current=%d expired=%d", got.CurrentCount, got.ExpiredCount)
	}
}

func TestEvaluate_Expired_NoDaysRemaining(t *testing.T) {
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{check("old", 50, "2026-01-15")}}
	got := Evaluate(r, fixedNow)
	if got.Checks[0].Status != StatusExpired {
		t.Errorf("expected Expired, got %s", got.Checks[0].Status)
	}
	if got.Checks[0].DaysRemaining != nil {
		t.Error("expected nil DaysRemaining for expired check")
	}
	if got.Score != 0 {
		t.Errorf("expected score 0, got %d", got.Score)
	}
}

func TestEvaluate_InvalidDate(t *testing.T) {
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{
		check("good", 50, "2026-12-31"),
		check("bad", 50, "not-a-date"),
	}}
	got := Evaluate(r, fixedNow)
	if got.Checks[1].Status != StatusInvalid {
		t.Errorf("expected Invalid, got %s", got.Checks[1].Status)
	}
	if got.InvalidCount != 1 {
		t.Errorf("expected invalidCount 1, got %d", got.InvalidCount)
	}
	if got.TotalWeight != 100 {
		t.Errorf("expected total weight 100 (invalid still declared), got %d", got.TotalWeight)
	}
	if got.CurrentWeight != 50 {
		t.Errorf("expected current weight 50, got %d", got.CurrentWeight)
	}
	if got.Score != 50 {
		t.Errorf("expected score 50, got %d", got.Score)
	}
	if got.Checks[1].DaysRemaining != nil {
		t.Error("expected nil DaysRemaining for invalid check")
	}
}

func TestEvaluate_NonCanonicalDateInvalid(t *testing.T) {
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{check("bad", 50, "2026-1-1")}}
	got := Evaluate(r, fixedNow)
	if got.Checks[0].Status != StatusInvalid {
		t.Errorf("expected Invalid for non-canonical date, got %s", got.Checks[0].Status)
	}
}

func TestEvaluate_ZeroTotalWeight(t *testing.T) {
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{check("z", 0, "2026-12-31")}}
	got := Evaluate(r, fixedNow)
	if got.Score != 0 {
		t.Errorf("expected score 0 when total weight is 0, got %d", got.Score)
	}
	if got.Checks[0].Status != StatusCurrent {
		t.Errorf("expected Current, got %s", got.Checks[0].Status)
	}
}

func minScore(n int) *int { return &n }

func TestEvaluate_GateDefaultMinScore100_AllCurrentPasses(t *testing.T) {
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{
		check("dashboard", 60, "2026-12-31"),
		check("runbook", 40, "2026-09-30"),
	}}
	got := Evaluate(r, fixedNow)
	if got.MinScore != 100 {
		t.Errorf("expected default minScore 100, got %d", got.MinScore)
	}
	if !got.Passing {
		t.Error("expected passing when all current and minScore defaults to 100")
	}
}

func TestEvaluate_GateDefaultFailsWhenStale(t *testing.T) {
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{
		check("dashboard", 60, "2026-12-31"),       // current
		check("security-review", 40, "2026-01-15"), // expired
	}}
	got := Evaluate(r, fixedNow)
	if got.Score != 60 {
		t.Fatalf("expected score 60, got %d", got.Score)
	}
	if got.MinScore != 100 || got.Passing {
		t.Errorf("expected default minScore 100 and not passing, got minScore=%d passing=%v", got.MinScore, got.Passing)
	}
}

func TestEvaluate_GatePassesAtDeclaredMinScore(t *testing.T) {
	checks := []contract.ReadinessCheck{
		check("dashboard", 60, "2026-12-31"),       // current
		check("security-review", 40, "2026-01-15"), // expired -> score 60
	}
	if got := Evaluate(&contract.Readiness{MinScore: minScore(60), Checks: checks}, fixedNow); !got.Passing || got.MinScore != 60 {
		t.Errorf("expected passing at minScore 60, got passing=%v minScore=%d", got.Passing, got.MinScore)
	}
	if got := Evaluate(&contract.Readiness{MinScore: minScore(70), Checks: checks}, fixedNow); got.Passing {
		t.Error("expected not passing at minScore 70 (score 60)")
	}
}

func TestEvaluate_GateZeroMinScoreAlwaysPasses(t *testing.T) {
	r := &contract.Readiness{MinScore: minScore(0), Checks: []contract.ReadinessCheck{
		check("old", 50, "2026-01-15"), // expired -> score 0
	}}
	got := Evaluate(r, fixedNow)
	if got.Score != 0 {
		t.Fatalf("expected score 0, got %d", got.Score)
	}
	if !got.Passing || got.MinScore != 0 {
		t.Errorf("expected passing with minScore 0, got passing=%v minScore=%d", got.Passing, got.MinScore)
	}
}

func TestEvaluate_GateZeroTotalWeightDefaultFails(t *testing.T) {
	// total weight 0 -> score 0; default minScore 100 -> not passing.
	got := Evaluate(&contract.Readiness{Checks: []contract.ReadinessCheck{check("z", 0, "2026-12-31")}}, fixedNow)
	if got.Passing {
		t.Error("expected not passing: score 0 < default minScore 100")
	}
}

func TestEvaluate_ExpiresTodayInclusive(t *testing.T) {
	// Late in the day, expiring today: still current through end of day.
	now := time.Date(2026, 6, 8, 23, 59, 59, 0, time.UTC)
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{check("today", 30, "2026-06-08")}}
	got := Evaluate(r, now)
	if got.Checks[0].Status != StatusCurrent {
		t.Errorf("expected Current on expiry date, got %s", got.Checks[0].Status)
	}
	if got.Checks[0].DaysRemaining == nil || *got.Checks[0].DaysRemaining != 0 {
		t.Errorf("expected DaysRemaining 0 on expiry date, got %v", got.Checks[0].DaysRemaining)
	}
}

func TestEvaluate_LocalTimeNormalizedToUTC(t *testing.T) {
	// A non-UTC "now" must be normalized; the day boundary is computed in UTC.
	loc := time.FixedZone("UTC-5", -5*3600)
	now := time.Date(2026, 6, 8, 10, 0, 0, 0, loc) // 15:00 UTC
	r := &contract.Readiness{Checks: []contract.ReadinessCheck{check("d", 10, "2026-06-09")}}
	got := Evaluate(r, now)
	if got.Checks[0].Status != StatusCurrent {
		t.Errorf("expected Current, got %s", got.Checks[0].Status)
	}
	if got.Checks[0].DaysRemaining == nil || *got.Checks[0].DaysRemaining != 1 {
		t.Errorf("expected DaysRemaining 1, got %v", got.Checks[0].DaysRemaining)
	}
}
