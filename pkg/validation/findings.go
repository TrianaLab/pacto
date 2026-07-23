package validation

import "github.com/trianalab/pacto/v2/pkg/finding"

// Findings projects the layered ValidationResult into typed, severity-tagged
// findings. It is additive: ValidationResult is unchanged and remains the
// engine's internal accumulator. Errors become SeverityError, warnings
// SeverityWarning; category is looked up from the finding registry.
func (r ValidationResult) Findings() []finding.Finding {
	out := make([]finding.Finding, 0, len(r.Errors)+len(r.Warnings))
	for _, e := range r.Errors {
		out = append(out, toFinding(e.Path, e.Code, e.Message, finding.SeverityError))
	}
	for _, w := range r.Warnings {
		out = append(out, toFinding(w.Path, w.Code, w.Message, finding.SeverityWarning))
	}
	return out
}

func toFinding(path, code, msg string, sev finding.Severity) finding.Finding {
	c := finding.Code(code)
	return finding.Finding{
		Code:         c,
		Severity:     sev,
		Category:     finding.CategoryOf(c),
		ContractPath: path,
		Message:      msg,
	}
}
