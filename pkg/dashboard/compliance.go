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
	info := &ComplianceInfo{}

	if cs == StatusReference {
		info.Status = ComplianceReference
		return info
	}

	if cs == StatusNonCompliant {
		info.Status = ComplianceError
	}

	total := len(conditions)
	passed := 0
	errors := 0
	warnings := 0

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
		}
	}

	failed := total - passed
	info.Summary = &ComplianceCounts{
		Total:    total,
		Passed:   passed,
		Failed:   failed,
		Errors:   errors,
		Warnings: warnings,
	}

	if total > 0 {
		score := int(math.Round(float64(passed) / float64(total) * 100))
		info.Score = &score
	}

	// Determine status from conditions if not already set by contract status.
	if info.Status == "" {
		switch {
		case errors > 0:
			info.Status = ComplianceError
		case warnings > 0:
			info.Status = ComplianceWarning
		default:
			info.Status = ComplianceOK
		}
	}

	return info
}

// ComputeRuntimeDiff builds the semantic contract-vs-runtime comparison rows.
func ComputeRuntimeDiff(runtime *RuntimeInfo, observed *ObservedRuntime) []RuntimeDiffRow {
	if runtime == nil && observed == nil {
		return nil
	}

	var rows []RuntimeDiffRow
	rt := runtime
	if rt == nil {
		rt = &RuntimeInfo{}
	}
	obs := observed
	if obs == nil {
		obs = &ObservedRuntime{}
	}

	// Workload Type
	rows = append(rows, diffRow(
		"Workload Type",
		"runtime.workload",
		mapWorkloadToDeclared(rt.Workload),
		obs.WorkloadKind,
	))

	// Upgrade Strategy
	rows = append(rows, diffRow(
		"Upgrade Strategy",
		"runtime.lifecycle.upgradeStrategy",
		rt.UpgradeStrategy,
		obs.DeploymentStrategy,
	))

	// Graceful Shutdown
	rows = append(rows, diffRow(
		"Graceful Shutdown",
		"runtime.lifecycle.gracefulShutdownSeconds",
		intPtrToString(rt.GracefulShutdownSeconds),
		intPtrToString(obs.TerminationGracePeriodSeconds),
	))

	// Container Image
	declaredImage := ""
	observedImages := strings.Join(obs.ContainerImages, ", ")
	rows = append(rows, diffRow(
		"Container Image",
		"service.image",
		declaredImage,
		observedImages,
	))

	// State / Storage
	declaredState := rt.StateType
	observedState := storageState(obs)
	rows = append(rows, diffRow(
		"State / Storage",
		"runtime.state.type",
		declaredState,
		observedState,
	))

	// Health Probe Delay
	rows = append(rows, diffRow(
		"Health Probe Delay",
		"runtime.health",
		"",
		intPtrToString(obs.HealthProbeInitialDelay),
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
