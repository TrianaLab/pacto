package validation

import "github.com/trianalab/pacto/v3/pkg/finding"

// DeriveStatus ranks a finding set and its coverage onto the compliance ladder.
// It is the single producer of that ranking: the operator writes it to
// status.contract.status and evidence ingestion writes it to a fleet target's
// compliance, and the two are ranked against each other, so deriving it twice made
// one contract read Warning in kubectl and Compliant in the dashboard.
//
// The ladder, worst first:
//
//   - a confirmed violation (error) is NonCompliant;
//   - uncertainty is Unknown — an unknown-severity finding OR coverage that did not
//     evaluate every required assertion. Those two are equivalent today because the
//     engine emits the finding in the same branch that charges coverage, but that is
//     an accident of one function and a caller may supply either alone;
//   - a warning is Warning;
//   - otherwise Compliant.
//
// Uncertainty ranks BELOW a confirmed violation and ABOVE a warning: not being able
// to observe something is not a contradiction, but it is not evidence of compliance
// either.
//
// Callers pass every finding in scope, STRUCTURAL FINDINGS INCLUDED. A layer-2
// warning (a policy ref nobody enforces, a mutable tag where a digest was wanted) is
// a real defect in the contract being asserted, and dropping it would report
// Compliant for a contract `pacto validate` warns about. Structural ERRORS never
// reach here: they are the Invalid rung, and a caller that has them short-circuits
// to its own Invalid before evaluating anything — which is why Invalid is not
// derived and not spelled here. Evidence ingestion passes runtime findings only
// because it accepts an already validated record and has no structural findings to
// give; that is a difference in what the two callers hold, not in the rule.
func DeriveStatus(findings []finding.Finding, cov Coverage) string {
	var unknown, warning bool
	for _, f := range findings {
		switch f.Severity {
		case finding.SeverityError:
			return finding.StatusNonCompliant
		case finding.SeverityUnknown:
			unknown = true
		case finding.SeverityWarning:
			warning = true
		}
	}
	switch {
	case unknown || cov.Evaluated < cov.Required:
		return finding.StatusUnknown
	case warning:
		return finding.StatusWarning
	default:
		return finding.StatusCompliant
	}
}
