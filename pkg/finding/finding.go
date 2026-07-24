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
	SeverityUnknown Severity = "unknown" // required assertion could not be evaluated
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
	CategoryInvalidCapability      Category = "InvalidCapability"
	CategoryInconclusive           Category = "Inconclusive" // family 2; distinct from CategoryMissingEvidence
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

// EvidenceRef links a finding to the evidence that supports it.
type EvidenceRef struct {
	Source     string
	ObservedAt string
}

// Finding is a typed conclusion produced by the engine. Contract-only findings
// leave EvidenceRefs empty; evidence-based findings (Phase 5, Evaluate) populate it.
type Finding struct {
	Code         Code
	Severity     Severity
	Category     Category
	Subject      SubjectRef
	ContractPath string
	Message      string
	EvidenceRefs []EvidenceRef
}
