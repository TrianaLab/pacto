package validation

import "github.com/trianalab/pacto/v3/pkg/finding"

// Findings projects the layered ValidationResult into typed, severity-tagged
// findings. It is additive: ValidationResult is unchanged and remains the
// engine's internal accumulator.
func (r ValidationResult) Findings() []finding.Finding {
	out := make([]finding.Finding, 0, len(r.Errors)+len(r.Warnings))
	for _, e := range r.Errors {
		out = append(out, toFinding(e.Path, e.Code, e.Message))
	}
	for _, w := range r.Warnings {
		out = append(out, toFinding(w.Path, w.Code, w.Message))
	}
	return out
}

// toFinding stamps both severity and category from the finding registry rather
// than from which slice the entry sat in, so the registry is the single owner of a
// code's severity and the published reference cannot drift from what is emitted.
// Structural checks have no required-ness axis, hence required=true; an
// unregistered code is an error, which is what an entry the accumulator carries
// but nobody declared should be.
func toFinding(path, code, msg string) finding.Finding {
	c := finding.Code(code)
	return finding.Finding{
		Code:         c,
		Severity:     finding.SeverityFor(c, true),
		Category:     finding.CategoryOf(c),
		ContractPath: path,
		Message:      msg,
	}
}
