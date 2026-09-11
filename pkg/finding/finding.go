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

// The compliance ladder's rungs, worst first. Untyped so every producer can use
// them against its own string-kind status field: pkg/fleet's Status* and the
// operator's ContractStatus* are the same strings and are ranked against each
// other, so a rename on one side silently sorts wrong on the other.
//
// pkg/finding is where they live because it is the one package all three producers
// may import — the engine's import allowlist (pkg/validation/boundary_test.go)
// admits contract, evidence, finding and graph, so pkg/validation cannot reach
// pkg/fleet, and neither may reach the operator.
//
// Only the rungs a finding set can be ranked onto are declared here. Invalid,
// Reference and NotEvaluated are decided before any finding exists and belong to
// whichever producer decides them, until one moves here.
const (
	StatusNonCompliant = "NonCompliant"
	StatusUnknown      = "Unknown"
	StatusWarning      = "Warning"
	StatusCompliant    = "Compliant"
)

// Code is a stable, specific finding identifier (e.g. STATELESS_PERSISTENT_CONFLICT).
type Code string

// Category groups related codes into a coarse kind for consumers.
type Category string

const (
	// Deprecated: no code maps to this category. The interface shape rules that
	// used it (INVALID_INTERFACE_TYPE, INTERFACE_REF_REQUIRED) moved into the
	// structural JSON Schema, which reports SCHEMA_VIOLATION; a missing or
	// unparseable spec file is CategoryInvalidFile. Nothing can produce it, so do
	// not write a consumer case for it. Removed at v4.
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
