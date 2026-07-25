package dashboard

import (
	"fmt"
	"math"
	"strings"
)

// ValidationCatalogEntry enriches a condition type with category, label, and default severity.
type ValidationCatalogEntry struct {
	Category string
	Label    string
	Severity string // "error" or "warning"
}

// validationCatalog maps condition types to their enrichment metadata.
var validationCatalog = map[string]ValidationCatalogEntry{
	"ContractValid":         {Category: "contract", Label: "Contract Structure", Severity: "error"},
	"ServiceExists":         {Category: "infrastructure", Label: "Service Exists", Severity: "error"},
	"WorkloadExists":        {Category: "infrastructure", Label: "Workload Exists", Severity: "error"},
	"PortsValid":            {Category: "networking", Label: "Port Alignment", Severity: "error"},
	"HealthEndpointValid":   {Category: "networking", Label: "Health Endpoint", Severity: "error"},
	"MetricsEndpointValid":  {Category: "networking", Label: "Metrics Endpoint", Severity: "error"},
	"WorkloadTypeMatch":     {Category: "workload", Label: "Workload Type", Severity: "error"},
	"StateModelMatch":       {Category: "state", Label: "State Model", Severity: "error"},
	"UpgradeStrategyMatch":  {Category: "lifecycle", Label: "Upgrade Strategy", Severity: "warning"},
	"GracefulShutdownMatch": {Category: "lifecycle", Label: "Graceful Shutdown", Severity: "warning"},
	"ImageMatch":            {Category: "image", Label: "Container Image", Severity: "error"},
	"HealthTimingMatch":     {Category: "health", Label: "Health Probe Timing", Severity: "warning"},
}

// LookupValidation returns the catalog entry for a condition type.
// Unknown types get category "other", the type name as label, and "error" severity.
func LookupValidation(conditionType string) ValidationCatalogEntry {
	if entry, ok := validationCatalog[conditionType]; ok {
		return entry
	}
	return ValidationCatalogEntry{
		Category: "other",
		Label:    conditionType,
		Severity: "error",
	}
}

// ComputeCompliance computes the compliance status and score from contract status and conditions.
func ComputeCompliance(cs ContractStatus, conditions []Condition) *ComplianceInfo {
	if info := shortCircuitCompliance(cs); info != nil {
		return info
	}

	counts := countConditions(conditions)
	info := &ComplianceInfo{Summary: &counts}

	if len(conditions) > 0 {
		score := int(math.Round(float64(counts.Passed) / float64(counts.Total) * 100))
		info.Score = &score
	}

	info.Status = determineComplianceStatus(counts.Errors, counts.Unknown, counts.Warnings, cs)
	return info
}

// shortCircuitCompliance returns early results for non-runtime-evaluated states.
func shortCircuitCompliance(cs ContractStatus) *ComplianceInfo {
	switch cs {
	case StatusReference, StatusNotEvaluated:
		return &ComplianceInfo{Status: ComplianceReference}
	case StatusInvalid:
		return &ComplianceInfo{Status: ComplianceError}
	default:
		return nil
	}
}

// countConditions tallies condition outcomes by severity.
func countConditions(conditions []Condition) ComplianceCounts {
	total := len(conditions)
	passed, errors, warnings, unknown := 0, 0, 0, 0

	for _, c := range conditions {
		severity := c.Severity
		if severity == "" {
			severity = LookupValidation(c.Type).Severity
		}
		switch c.Status {
		case "True":
			passed++
		case "False":
			if severity == "warning" {
				warnings++
			} else {
				errors++
			}
		case "Unknown":
			unknown++
		}
	}

	return ComplianceCounts{
		Total:            total,
		Passed:           passed,
		Failed:           total - passed,
		Errors:           errors,
		Warnings:         warnings,
		Unknown:          unknown,
		RuntimeEvaluated: passed + warnings + errors + unknown,
		Conclusive:       passed + warnings + errors,
	}
}

// determineComplianceStatus derives the final status from counts and contract status.
func determineComplianceStatus(errors, unknown, warnings int, cs ContractStatus) ComplianceStatus {
	switch {
	case errors > 0 || cs == StatusNonCompliant:
		return ComplianceError
	case unknown > 0 || cs == StatusUnknown:
		return ComplianceUnknown
	case warnings > 0 || cs == StatusWarning:
		return ComplianceWarning
	default:
		return ComplianceOK
	}
}

// ComputeRuntimeDiff builds the semantic contract-vs-runtime comparison rows.
func ComputeRuntimeDiff(workload string, state *StateInfo, observed *ObservedRuntime) []RuntimeDiffRow {
	if state == nil && observed == nil {
		return nil
	}

	var rows []RuntimeDiffRow
	obs := observed
	if obs == nil {
		obs = &ObservedRuntime{}
	}

	// Workload Type
	rows = append(rows, diffRow(
		"Workload Type",
		"workload",
		mapWorkloadToDeclared(workload),
		obs.WorkloadKind,
	))

	// State / Storage
	declaredState := ""
	if state != nil {
		declaredState = state.Type
	}
	observedState := storageState(obs)
	rows = append(rows, diffRow(
		"State / Storage",
		"state.type",
		declaredState,
		observedState,
	))

	return rows
}

func diffRow(field, path, declared, observed string) RuntimeDiffRow {
	var status string
	switch {
	case declared == "" || observed == "":
		status = "skipped"
	case strings.EqualFold(declared, observed):
		status = "match"
	default:
		status = "mismatch"
	}
	return RuntimeDiffRow{
		Field:         field,
		ContractPath:  path,
		DeclaredValue: declared,
		ObservedValue: observed,
		Status:        status,
	}
}

// mapWorkloadToDeclared converts contract workload type (service, job, scheduled) to
// the Kubernetes kind that would be expected.
func mapWorkloadToDeclared(workload string) string {
	switch strings.ToLower(workload) {
	case "service":
		return "Deployment"
	case "job":
		return "Job"
	case "scheduled":
		return "CronJob"
	default:
		return workload
	}
}

func storageState(obs *ObservedRuntime) string {
	if obs == nil {
		return ""
	}
	parts := []string{}
	if obs.HasPVC != nil && *obs.HasPVC {
		parts = append(parts, "PVC")
	}
	if obs.HasEmptyDir != nil && *obs.HasEmptyDir {
		parts = append(parts, "emptyDir")
	}
	if len(parts) == 0 {
		return "stateless"
	}
	return strings.Join(parts, ", ")
}

func intPtrToString(p *int) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%d", *p)
}
