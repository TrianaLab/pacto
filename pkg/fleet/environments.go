package fleet

import (
	"fmt"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// Environment check statuses (see [EnvironmentResult.Status]).
const (
	EnvironmentPass       = "pass"
	EnvironmentFail       = "fail"
	EnvironmentUnverified = "unverified"
	EnvironmentSkip       = "skip"
)

// notReadReason is why nothing was checked for a revision whose contract
// document could not be read at Build.
const notReadReason = "the contract document was not read, so nothing was checked"

// ValidationStage is one stage of the validation run Build performed on a
// revision: structural, cross-field, policy or environments. Reason says why a
// skipped stage did not run, or what it could not verify.
type ValidationStage struct {
	Name   string `json:"name"`
	Status string `json:"status" enum:"pass,fail,skip"`
	Reason string `json:"reason,omitempty"`
}

// LocatedFinding is a validation finding about one environment, with the line
// and column it points at in the environment's values file when known.
type LocatedFinding struct {
	Code     finding.Code     `json:"code"`
	Severity finding.Severity `json:"severity"`
	Path     string           `json:"path"`
	Message  string           `json:"message"`
	File     string           `json:"file,omitempty"`
	Line     int              `json:"line,omitempty"`
	Column   int              `json:"column,omitempty"`
}

// LocatedFindingsPreview is a bounded preview of one environment's findings.
type LocatedFindingsPreview struct {
	Total     int              `json:"total"`
	Count     int              `json:"count"`
	Truncated bool             `json:"truncated"`
	Items     []LocatedFinding `json:"items"`
}

// EnvironmentResult is one declared environment (pactoVersion 2.3), the values
// file it names and what checking those values found. Status is pass when
// every value set was checked and none failed, fail on any error, unverified
// when a value set could not be checked (a missing observation, never a pass)
// and skip when the check did not run, with Reason saying why.
type EnvironmentResult struct {
	Name     string                 `json:"name"`
	File     string                 `json:"file"`
	Status   string                 `json:"status" enum:"pass,fail,unverified,skip"`
	Reason   string                 `json:"reason,omitempty"`
	Findings LocatedFindingsPreview `json:"findings"`
}

// EnvironmentsPreview is a bounded preview of a revision's environments.
type EnvironmentsPreview struct {
	Total     int                 `json:"total"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated"`
	Items     []EnvironmentResult `json:"items"`
}

// stageSummaries drops each stage's findings: they are already the revision's
// Validation, and the summary only says which stage ran and how it ended.
func stageSummaries(stages []validation.Stage) []ValidationStage {
	out := make([]ValidationStage, len(stages))
	for i, st := range stages {
		out[i] = ValidationStage{Name: st.Name, Status: st.Status, Reason: st.Reason}
	}
	return out
}

// environmentResults reads each declared environment's verdict back out of the
// validation stages: every finding whose path is under environments[i], from
// any stage, belongs to environment i. The environments stage did not run when
// it is skipped with nothing reported; a run that could not verify a value set
// is skipped too, but it always carries the ENVIRONMENT_VALUES_UNVERIFIED
// warning that says so. A nil stages means the document was never validated.
func environmentResults(envs []contract.Environment, stages []validation.Stage) []EnvironmentResult {
	if len(envs) == 0 {
		return nil
	}
	ran, reason := false, notReadReason
	if n := len(stages); n > 0 {
		last := stages[n-1]
		ran = last.Status != validation.StageSkip || len(last.Warnings) > 0
		reason = last.Reason
	}
	out := make([]EnvironmentResult, len(envs))
	for i, env := range envs {
		prefix := fmt.Sprintf("environments[%d]", i)
		var located []LocatedFinding
		failed, unverified := false, false
		for _, st := range stages {
			for _, e := range st.Errors {
				if underPath(e.Path, prefix) {
					failed = true
					located = append(located, LocatedFinding{Code: finding.Code(e.Code), Severity: finding.SeverityFor(finding.Code(e.Code), true),
						Path: e.Path, Message: e.Message, File: e.File, Line: e.Line, Column: e.Column})
				}
			}
			for _, w := range st.Warnings {
				if underPath(w.Path, prefix) {
					unverified = unverified || w.Code == string(finding.CodeEnvironmentValuesUnverified)
					located = append(located, LocatedFinding{Code: finding.Code(w.Code), Severity: finding.SeverityFor(finding.Code(w.Code), true),
						Path: w.Path, Message: w.Message})
				}
			}
		}
		r := EnvironmentResult{Name: env.Name, File: env.File, Status: EnvironmentPass}
		switch {
		case failed:
			r.Status = EnvironmentFail
		case !ran:
			r.Status, r.Reason = EnvironmentSkip, reason
		case unverified:
			r.Status = EnvironmentUnverified
		}
		items, total, trunc := boundSlice(located, MaxDetailPreview)
		r.Findings = LocatedFindingsPreview{Total: total, Count: len(items), Truncated: trunc, Items: items}
		out[i] = r
	}
	return out
}

// underPath reports whether path is prefix itself or a field or element under it.
func underPath(path, prefix string) bool {
	rest, ok := strings.CutPrefix(path, prefix)
	return ok && (rest == "" || rest[0] == '.' || rest[0] == '[')
}

// environmentsPreview bounds a revision's environments, copying each one's
// findings so a caller cannot reach the snapshot through them.
func environmentsPreview(rs []EnvironmentResult) EnvironmentsPreview {
	items, total, trunc := boundSlice(rs, MaxDetailPreview)
	for i := range items {
		items[i].Findings.Items = append([]LocatedFinding{}, items[i].Findings.Items...)
	}
	return EnvironmentsPreview{Total: total, Count: len(items), Truncated: trunc, Items: items}
}
