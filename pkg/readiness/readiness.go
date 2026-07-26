// Package readiness derives operational readiness state from a contract's
// declared readiness section. It is a pure, provider-neutral library shared by
// the Pacto CLI and the Pacto operator: given the declared checks (each carrying
// its own completion status) and a point in time it computes per-check earned
// weight, weight totals, and an overall score. It does not verify that the
// referenced evidence actually exists.
package readiness

import (
	"math"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

const (
	// DefaultMinScore is the gate threshold used when a contract declares
	// readiness but omits minScore: every weighted check must be done.
	DefaultMinScore = 100
	// DefaultPartialCredit is the fraction of weight a "partial" check earns
	// when the contract omits partialCredit.
	DefaultPartialCredit = 0.5
	// dateLayout is the strict YYYY-MM-DD layout used for the assessment expiry.
	dateLayout = "2006-01-02"
)

// Result is the derived readiness assessment.
type Result struct {
	Score         int
	TotalWeight   int
	EarnedWeight  int
	MinScore      int
	PartialCredit float64
	Expires       string
	Expired       bool
	DaysRemaining *int
	DoneCount     int
	PartialCount  int
	NotDoneCount  int
	DeferredCount int
	Passing       bool
	Checks        []CheckResult
}

// CheckResult is the derived state of one declared check.
type CheckResult struct {
	ID           string
	Type         string
	Category     string
	Status       string
	Evidence     string
	Description  string
	Weight       int
	EarnedWeight int
	Excluded     bool
}

// Evaluate derives the readiness Result at time now. Returns nil when no
// readiness (or no claims) is declared. Deferred claims are excluded from both
// the numerator and denominator. Done claims earn their full weight; partial
// claims earn round(weight*partialCredit); not-done (and any unknown status)
// earn nothing. When the assessment is expired every in-scope claim earns zero.
func Evaluate(r *contract.Readiness, now time.Time) *Result {
	if r == nil || len(r.Claims) == 0 {
		return nil
	}
	minScore := DefaultMinScore
	if r.MinScore != nil {
		minScore = *r.MinScore
	}
	credit := DefaultPartialCredit
	if r.PartialCredit != nil {
		credit = *r.PartialCredit
	}

	res := &Result{MinScore: minScore, PartialCredit: credit, Expires: r.Expires}
	res.Expired, res.DaysRemaining = expiryState(r.Expires, now)

	for _, c := range r.Claims {
		cr := CheckResult{
			ID: c.ID, Type: c.Type, Category: c.Category, Status: c.Status,
			Evidence: c.Evidence, Description: c.Description, Weight: c.Weight,
		}
		switch c.Status {
		case contract.StatusDeferred:
			cr.Excluded = true
			res.DeferredCount++
			res.Checks = append(res.Checks, cr)
			continue
		case contract.StatusDone:
			res.DoneCount++
		case contract.StatusPartial:
			res.PartialCount++
		default: // not-done (and any unknown, treated as no credit)
			res.NotDoneCount++
		}
		res.TotalWeight += c.Weight
		if !res.Expired {
			switch c.Status {
			case contract.StatusDone:
				cr.EarnedWeight = c.Weight
			case contract.StatusPartial:
				cr.EarnedWeight = int(math.Round(float64(c.Weight) * credit))
			}
		}
		res.EarnedWeight += cr.EarnedWeight
		res.Checks = append(res.Checks, cr)
	}

	if res.TotalWeight > 0 {
		res.Score = int(math.Round(float64(res.EarnedWeight) / float64(res.TotalWeight) * 100))
	}
	res.Passing = !res.Expired && res.Score >= minScore
	return res
}

// expiryState reports whether the assessment is expired and the whole days
// remaining (nil when expired or unparseable). Expiry is inclusive through the
// end of the expires day in UTC. An unparseable date fails closed (expired).
func expiryState(expires string, now time.Time) (bool, *int) {
	parsed, err := time.ParseInLocation(dateLayout, expires, time.UTC)
	if err != nil || parsed.Format(dateLayout) != expires {
		return true, nil
	}
	endOfDay := parsed.Add(24*time.Hour - time.Nanosecond)
	if now.UTC().After(endOfDay) {
		return true, nil
	}
	// The assessment is not expired, so now is at or before the end of the
	// expires day; therefore today (the date part of now in UTC) is never after
	// the parsed day and the whole-days difference is always >= 0.
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	days := int(parsed.Sub(today).Hours() / 24)
	return false, &days
}
