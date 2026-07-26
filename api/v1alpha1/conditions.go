/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

const (
	// LabelPactoName is the label key used on PactoRevision resources to link them to their parent Pacto.
	LabelPactoName = "pacto.trianalab.io/pacto"

	// LabelRevisionVersion is the label key used on PactoRevision resources to store the contract version.
	LabelRevisionVersion = "pacto.trianalab.io/version"
)

// Condition types (lean finding-derived set).
const (
	// ConditionContractValid indicates whether the contract was successfully parsed and validated.
	ConditionContractValid = "ContractValid"

	// ConditionRuntimeObserved indicates whether the operator successfully collected runtime evidence.
	// False with reason ObservationFailed means the cluster query errored.
	ConditionRuntimeObserved = "RuntimeObserved"

	// ConditionReadinessSatisfied indicates whether the derived readiness gate is met
	// (score >= minScore). Absent when the contract declares no readiness.
	ConditionReadinessSatisfied = "ReadinessSatisfied"
)

// Condition reasons.
const (
	ReasonContractParsed  = "Parsed"
	ReasonContractInvalid = "Invalid"
	// ReasonContractUnavailable marks a transient inability to OBTAIN the contract (registry/auth/not-found),
	// distinct from a malformed one: validity is undetermined, not False.
	ReasonContractUnavailable = "Unavailable"

	ReasonFound    = "Found"
	ReasonNotFound = "NotFound"

	ReasonPortsMatch   = "AllPortsMatch"
	ReasonMissingPorts = "MissingPorts"

	ReasonReferenceOnly = "ReferenceOnly"

	// Endpoint probe reasons.
	ReasonEndpointOK               = "OK"
	ReasonEndpointConnectionError  = "ConnectionFailed"
	ReasonEndpointInvalidStatus    = "InvalidStatusCode"
	ReasonEndpointEmptyResponse    = "EmptyResponse"
	ReasonEndpointNotDeclared      = "NotDeclared"
	ReasonEndpointNoPort           = "InterfaceHasNoPort"
	ReasonEndpointInterfaceMissing = "InterfaceNotFound"

	// Runtime reconciliation reasons.
	ReasonMatch    = "Match"
	ReasonMismatch = "Mismatch"
	ReasonMissing  = "Missing"
	ReasonSkipped  = "Skipped"

	// ReasonObservationFailed indicates the operator could not query runtime state.
	ReasonObservationFailed = "ObservationFailed"

	// Readiness condition reasons (for ConditionReadinessSatisfied).
	ReasonReadinessSatisfied     = "Satisfied"
	ReasonReadinessBelowMinScore = "BelowMinScore"
	ReasonReadinessExpired       = "Expired"
)

// Event reasons (free-form, emitted via the recorder). Readiness events are
// emitted on gate transitions only to avoid per-reconcile spam.
const (
	EventReadinessGateUnmet = "ReadinessGateUnmet"
	EventReadinessRecovered = "ReadinessRecovered"
)

// Severity levels for runtime reconciliation checks.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// ContractStatus values represent contract compliance state (not runtime health).
const (
	ContractStatusCompliant    = "Compliant"
	ContractStatusWarning      = "Warning"
	ContractStatusNonCompliant = "NonCompliant"
	ContractStatusReference    = "Reference"
	ContractStatusUnknown      = "Unknown"
	// ContractStatusInvalid means structural validation failed OR a malformed artifact could not be parsed.
	ContractStatusInvalid = "Invalid"
	// ContractStatusNotEvaluated is a reserved enum value that the operator does not currently emit:
	// no reconciler path assigns it. A valid, targeted contract with no runtime evidence yields
	// SeverityUnknown findings and resolves to ContractStatusUnknown (see summarizeFindings), not
	// NotEvaluated. The value exists for CRD and metrics parity with the engine dashboard, which uses
	// it for offline OCI or local sources that were never runtime-evaluated.
	ContractStatusNotEvaluated = "NotEvaluated"
)

// ResolutionPolicy values describe how the OCI reference is resolved.
const (
	// ResolutionPolicyLatest means the operator resolves the highest semver tag
	// from the registry on every reconciliation (unversioned OCI ref).
	ResolutionPolicyLatest = "Latest"
	// ResolutionPolicyPinnedTag means the OCI ref includes an explicit tag
	// and the operator uses it as-is without re-resolving.
	ResolutionPolicyPinnedTag = "PinnedTag"
	// ResolutionPolicyPinnedDigest means the OCI ref includes a digest
	// and the operator uses it as-is (immutable).
	ResolutionPolicyPinnedDigest = "PinnedDigest"
)
