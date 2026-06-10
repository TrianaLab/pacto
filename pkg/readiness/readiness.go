// Package readiness derives operational readiness state from a contract's
// declared readiness section. It is a pure, provider-neutral library shared by
// the Pacto CLI and the Pacto operator: given the declared checks and a point in
// time it computes per-check status, weight totals, and an overall score. It does
// not verify that the referenced evidence actually exists.
package readiness

import (
	"math"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
)

// dateLayout is the strict YYYY-MM-DD layout used for readiness expiry dates.
const dateLayout = "2006-01-02"

// Status is the derived state of a single readiness check.
type Status string

const (
	// StatusCurrent means the evidence has not yet expired.
	StatusCurrent Status = "Current"
	// StatusExpired means the expires date has passed.
	StatusExpired Status = "Expired"
	// StatusInvalid means the expires date could not be parsed.
	StatusInvalid Status = "Invalid"
)

// CheckResult is the derived state of one declared readiness check. It echoes the
// declared fields and adds the computed Status and, for current checks, the number
// of whole days remaining until the evidence expires.
type CheckResult struct {
	ID            string
	Type          string
	Evidence      string
	Weight        int
	Expires       string
	Description   string
	Status        Status
	DaysRemaining *int
}

// Result is the derived readiness assessment of a contract. Score is the
// percentage of declared weight that is currently satisfied (0 when no weight is
// declared, avoiding division by zero).
type Result struct {
	Score         int
	TotalWeight   int
	CurrentWeight int
	CurrentCount  int
	ExpiredCount  int
	InvalidCount  int
	// MinScore is the effective gate threshold (the declared readiness.minScore,
	// or DefaultMinScore when omitted).
	MinScore int
	// Passing reports whether the gate is met (Score >= MinScore).
	Passing bool
	Checks  []CheckResult
}

// DefaultMinScore is the gate threshold used when a contract declares readiness
// but omits minScore: every weighted check must be current.
const DefaultMinScore = 100

// Evaluate derives readiness state from the declared readiness section as of now.
// It returns nil when no readiness is declared (a nil section or no checks), so
// callers can treat "no readiness" as an absent result. Evidence is considered
// current through the end of its expires date; the day boundary is computed in
// UTC so the result is independent of the caller's timezone.
func Evaluate(r *contract.Readiness, now time.Time) *Result {
	if r == nil || len(r.Checks) == 0 {
		return nil
	}

	nowUTC := now.UTC()
	today := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)

	res := &Result{Checks: make([]CheckResult, 0, len(r.Checks))}
	for _, check := range r.Checks {
		cr := CheckResult{
			ID:          check.ID,
			Type:        check.Type,
			Evidence:    check.Evidence,
			Weight:      check.Weight,
			Expires:     check.Expires,
			Description: check.Description,
		}
		res.TotalWeight += check.Weight

		exp, err := time.Parse(dateLayout, check.Expires)
		switch {
		case err != nil || exp.Format(dateLayout) != check.Expires:
			cr.Status = StatusInvalid
			res.InvalidCount++
		case !today.After(exp):
			cr.Status = StatusCurrent
			res.CurrentCount++
			res.CurrentWeight += check.Weight
			days := int(exp.Sub(today).Hours()) / 24
			cr.DaysRemaining = &days
		default:
			cr.Status = StatusExpired
			res.ExpiredCount++
		}

		res.Checks = append(res.Checks, cr)
	}

	if res.TotalWeight > 0 {
		res.Score = int(math.Round(float64(res.CurrentWeight) / float64(res.TotalWeight) * 100))
	}

	res.MinScore = DefaultMinScore
	if r.MinScore != nil {
		res.MinScore = *r.MinScore
	}
	res.Passing = res.Score >= res.MinScore

	return res
}
