// Package finding defines the typed result of Pacto engine reasoning. It is a
// pure data package: zero external dependencies, no knowledge of collectors,
// reporters, k8s, OCI, or persistence. Reporters at the edge project Finding
// into external shapes (SARIF, PolicyReport); this package never imports them.
package finding

// Severity ranks a finding.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Code is a stable, specific finding identifier (e.g. STATELESS_PERSISTENT_CONFLICT).
type Code string

// Category groups related codes into a coarse kind for consumers.
type Category string

const (
	CategoryInterfaceMismatch      Category = "InterfaceMismatch"
	CategoryStateMismatch          Category = "StateMismatch"
	CategoryPolicyViolation        Category = "PolicyViolation"
	CategoryConfigurationViolation Category = "ConfigurationViolation"
	CategoryMissingConfiguration   Category = "MissingConfiguration"
	CategoryInvalidReference       Category = "InvalidReference"
	CategoryUnresolvedReference    Category = "UnresolvedReference"
	CategoryReferenceCycle         Category = "ReferenceCycle"
	CategoryDuplicateName          Category = "DuplicateName"
	CategoryInvalidVersion         Category = "InvalidVersion"
	CategoryInvalidReadiness       Category = "InvalidReadiness"
	CategoryMissingEvidence        Category = "MissingEvidence"
	CategoryInvalidFile            Category = "InvalidFile"
	CategorySchemaViolation        Category = "SchemaViolation"
	CategoryRuntimeDrift           Category = "RuntimeDrift"
	CategoryInvalidDependency      Category = "InvalidDependency"
)

// One representative durable v2 code; the full set (including evidence-based and
// legacy-v1 codes) is registered in Task 2.2.
const (
	CodeStatelessPersistent Code = "STATELESS_PERSISTENT_CONFLICT"
)

// SubjectRef identifies the thing a finding is about.
type SubjectRef struct {
	Kind string // e.g. service, interface, dependency, configuration, policy
	Name string
}

// Finding is a typed conclusion produced by the engine. Phase 2 covers
// contract-internal findings only; evidence linkage (EvidenceRefs) is added in
// Phase 5 when Evaluate produces evidence-based findings. It is deliberately
// omitted here so the Finding type does not prematurely encode Phase 5 shape.
type Finding struct {
	Code         Code
	Severity     Severity
	Category     Category
	Subject      SubjectRef
	ContractPath string
	Message      string
}
