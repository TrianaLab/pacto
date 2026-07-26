package readiness

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

var refNow = time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

func ptrInt(i int) *int       { return &i }
func ptrF(f float64) *float64 { return &f }

func mk(expires string, ms *int, pc *float64, claims ...contract.ReadinessClaim) *contract.Readiness {
	return &contract.Readiness{MinScore: ms, Expires: expires, PartialCredit: pc, Claims: claims}
}

func TestEvaluate_NilAndEmpty(t *testing.T) {
	if Evaluate(nil, refNow) != nil {
		t.Fatal("nil readiness should evaluate to nil")
	}
	if Evaluate(&contract.Readiness{Expires: "2026-12-31"}, refNow) != nil {
		t.Fatal("no checks should evaluate to nil")
	}
}

func TestEvaluate_ScoringWithStatuses(t *testing.T) {
	r := mk("2026-12-31", ptrInt(80), nil,
		contract.ReadinessClaim{ID: "a", Type: "url", Status: contract.StatusDone, Evidence: "e", Weight: 30},
		contract.ReadinessClaim{ID: "b", Type: "url", Status: contract.StatusPartial, Evidence: "e", Weight: 20},
		contract.ReadinessClaim{ID: "c", Type: "url", Status: contract.StatusNotDone, Evidence: "e", Weight: 10},
		contract.ReadinessClaim{ID: "d", Type: "url", Status: contract.StatusDeferred, Evidence: "e", Weight: 40},
	)
	got := Evaluate(r, refNow)
	if got.TotalWeight != 60 {
		t.Fatalf("TotalWeight: want 60 got %d", got.TotalWeight)
	}
	if got.EarnedWeight != 40 { // 30 + round(20*0.5)=10 + 0
		t.Fatalf("EarnedWeight: want 40 got %d", got.EarnedWeight)
	}
	if got.Score != 67 { // round(40/60*100)
		t.Fatalf("Score: want 67 got %d", got.Score)
	}
	if got.DoneCount != 1 || got.PartialCount != 1 || got.NotDoneCount != 1 || got.DeferredCount != 1 {
		t.Fatalf("counts: %+v", got)
	}
	if got.Passing {
		t.Fatal("67 < 80 must not pass")
	}
}

func TestEvaluate_PassingGate(t *testing.T) {
	r := mk("2026-12-31", ptrInt(80), nil,
		contract.ReadinessClaim{ID: "a", Type: "url", Status: contract.StatusDone, Evidence: "e", Weight: 30},
		contract.ReadinessClaim{ID: "b", Type: "url", Status: contract.StatusPartial, Evidence: "e", Weight: 20},
		contract.ReadinessClaim{ID: "d", Type: "url", Status: contract.StatusDeferred, Evidence: "e", Weight: 40},
	)
	got := Evaluate(r, refNow) // total 50, earned 40 -> 80
	if got.Score != 80 || !got.Passing {
		t.Fatalf("want score 80 passing, got %d passing=%v", got.Score, got.Passing)
	}
}

func TestEvaluate_CustomPartialCredit(t *testing.T) {
	r := mk("2026-12-31", ptrInt(0), ptrF(0.25),
		contract.ReadinessClaim{ID: "b", Type: "url", Status: contract.StatusPartial, Evidence: "e", Weight: 40},
	)
	got := Evaluate(r, refNow)
	if got.EarnedWeight != 10 || got.Score != 25 { // round(40*0.25)=10, 10/40
		t.Fatalf("custom credit: earned=%d score=%d", got.EarnedWeight, got.Score)
	}
}

func TestEvaluate_Expired(t *testing.T) {
	r := mk("2026-01-01", ptrInt(50), nil,
		contract.ReadinessClaim{ID: "a", Type: "url", Status: contract.StatusDone, Evidence: "e", Weight: 100},
	)
	got := Evaluate(r, refNow)
	if !got.Expired || got.Score != 0 || got.Passing {
		t.Fatalf("expired assessment must zero score and fail: %+v", got)
	}
}

func TestEvaluate_InclusiveExpiryAndDays(t *testing.T) {
	r := mk("2026-06-08", ptrInt(0), nil,
		contract.ReadinessClaim{ID: "a", Type: "url", Status: contract.StatusDone, Evidence: "e", Weight: 10},
	)
	got := Evaluate(r, refNow) // expires today -> still current
	if got.Expired {
		t.Fatal("expiry should be inclusive through end of day")
	}
	if got.DaysRemaining == nil || *got.DaysRemaining != 0 {
		t.Fatalf("days remaining today: %v", got.DaysRemaining)
	}
}

func TestEvaluate_InvalidExpiresFailsClosed(t *testing.T) {
	r := mk("2026-13-40", ptrInt(0), nil,
		contract.ReadinessClaim{ID: "a", Type: "url", Status: contract.StatusDone, Evidence: "e", Weight: 10},
	)
	got := Evaluate(r, refNow)
	if !got.Expired || got.Score != 0 {
		t.Fatalf("invalid expires must fail closed: %+v", got)
	}
}

func TestEvaluate_AllDeferred(t *testing.T) {
	r := mk("2026-12-31", ptrInt(100), nil,
		contract.ReadinessClaim{ID: "a", Type: "url", Status: contract.StatusDeferred, Evidence: "e", Weight: 50},
	)
	got := Evaluate(r, refNow)
	if got.TotalWeight != 0 || got.Score != 0 || got.Passing {
		t.Fatalf("all-deferred: %+v", got)
	}
}

func TestEvaluate_Defaults(t *testing.T) {
	// No minScore, no partialCredit: defaults are 100 and 0.5.
	r := &contract.Readiness{Expires: "2026-12-31", Claims: []contract.ReadinessClaim{
		{ID: "a", Type: "url", Status: contract.StatusPartial, Evidence: "e", Weight: 40},
	}}
	got := Evaluate(r, refNow)
	if got.MinScore != DefaultMinScore {
		t.Errorf("expected default minScore %d, got %d", DefaultMinScore, got.MinScore)
	}
	if got.PartialCredit != DefaultPartialCredit {
		t.Errorf("expected default partialCredit %v, got %v", DefaultPartialCredit, got.PartialCredit)
	}
	if got.EarnedWeight != 20 { // round(40*0.5)
		t.Errorf("expected earned 20, got %d", got.EarnedWeight)
	}
	if got.Passing {
		t.Error("score 50 < default minScore 100 must not pass")
	}
}

func TestEvaluate_CheckResultFields(t *testing.T) {
	r := mk("2026-12-31", ptrInt(0), nil,
		contract.ReadinessClaim{ID: "a", Type: "url", Category: contract.CategorySecurity, Status: contract.StatusDone, Evidence: "e", Description: "d", Weight: 30},
		contract.ReadinessClaim{ID: "b", Type: "url", Status: contract.StatusDeferred, Evidence: "e2", Weight: 40},
	)
	got := Evaluate(r, refNow)
	if len(got.Checks) != 2 {
		t.Fatalf("expected 2 check results, got %d", len(got.Checks))
	}
	a := got.Checks[0]
	if a.ID != "a" || a.Category != contract.CategorySecurity || a.Status != contract.StatusDone || a.Description != "d" {
		t.Errorf("unexpected check a: %+v", a)
	}
	if a.EarnedWeight != 30 || a.Excluded {
		t.Errorf("done check should earn full weight, not excluded: %+v", a)
	}
	b := got.Checks[1]
	if !b.Excluded || b.EarnedWeight != 0 {
		t.Errorf("deferred check should be excluded with zero earned: %+v", b)
	}
}
