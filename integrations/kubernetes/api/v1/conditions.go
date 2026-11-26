/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1

import "github.com/trianalab/pacto/v4/pkg/finding"

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

	// ConditionOwnerVerified says whether the signer of a running bundle that
	// changes the service owner may set it: True allowed, False refused,
	// Unknown when the signer or the owner policy could not be verified. On
	// anything but True the status leaves the owner out. Absent for a bundle
	// that changes no owner, an unsigned one without declaration keys and an
	// inline contract.
	ConditionOwnerVerified = "OwnerVerified"
)

// Condition reasons.
const (
	ReasonContractParsed  = "Parsed"
	ReasonContractInvalid = "Invalid"
	// ReasonContractUndeclared marks a contract that does not declare its pactoVersion, service.name
	// or service.version: a published or inline contract must declare all three.
	ReasonContractUndeclared = "ContractUndeclared"
	// ReasonContractUnavailable marks a transient inability to OBTAIN the contract (registry/auth/not-found),
	// distinct from a malformed one: validity is undetermined, not False.
	ReasonContractUnavailable = "Unavailable"
	// ReasonPullSecretNotGranted marks a contract the operator could not obtain because it has no grant
	// to read the pull Secret: validity is undetermined, so ContractValid is Unknown, never False. The
	// message names the grant that would let it read the Secret.
	ReasonPullSecretNotGranted = "PullSecretNotGranted"

	ReasonFound = "Found"

	ReasonReferenceOnly = "ReferenceOnly"

	// ReasonObservationFailed indicates the operator could not query runtime state.
	ReasonObservationFailed = "ObservationFailed"

	// Readiness condition reasons (for ConditionReadinessSatisfied).
	ReasonReadinessSatisfied     = "Satisfied"
	ReasonReadinessBelowMinScore = "BelowMinScore"
	ReasonReadinessExpired       = "Expired"

	// OwnerVerified reasons. ReasonOwnerSignerAllowed: a member of the
	// current owner or a default approver signed the owner change.
	ReasonOwnerSignerAllowed = "OwnerSignerAllowed"
	// ReasonOwnerSignerNotPerson: an agent, CI or workload identity signed it;
	// only a person sets an owner, whoever it acts for.
	ReasonOwnerSignerNotPerson = "OwnerSignerNotPerson"
	// ReasonOwnerSignerNotAllowed: a person outside the owner's members and
	// the default approvers signed it.
	ReasonOwnerSignerNotAllowed = "OwnerSignerNotAllowed"
	// ReasonOwnerSignerUnverified: no signature verified, the owner policy or
	// the previous owner could not be read.
	ReasonOwnerSignerUnverified = "OwnerSignerUnverified"
)

// Event reasons (free-form, emitted via the recorder). Readiness and contract-status
// events are emitted on transitions only to avoid per-reconcile spam: a Pacto is
// re-reconciled on every write to the workloads it watches, so an unguarded event
// fires once per step of a rolling update.
const (
	EventReadinessGateUnmet = "ReadinessGateUnmet"
	EventReadinessRecovered = "ReadinessRecovered"

	// EventValidationFailed is the warning reason for a degraded contract status
	// reached through a completed evaluation (Warning, NonCompliant, Unknown).
	EventValidationFailed = "ValidationFailed"
	// EventContractInvalid is the warning reason when the contract itself is Invalid.
	EventContractInvalid = "ContractInvalid"
	// EventContractUnavailable is the warning reason when the contract could not be
	// obtained (registry/auth/not-found), so validity is undetermined.
	EventContractUnavailable = "ContractUnavailable"
	// EventContractRecovered is the normal reason emitted when the contract status
	// returns to Compliant or Reference from any degraded value.
	EventContractRecovered = "ContractRecovered"
)

// ContractStatus values represent contract compliance state (not runtime health).
// The four a finding set ranks onto are the engine's own (pkg/finding), so the
// operator and the dashboard can never spell one differently.
const (
	ContractStatusCompliant    = finding.StatusCompliant
	ContractStatusWarning      = finding.StatusWarning
	ContractStatusNonCompliant = finding.StatusNonCompliant
	ContractStatusReference    = "Reference"
	ContractStatusUnknown      = finding.StatusUnknown
	// ContractStatusInvalid means structural validation failed OR a malformed artifact could not be parsed.
	ContractStatusInvalid = "Invalid"
	// ContractStatusNotEvaluated is a reserved enum value that the operator does not currently emit:
	// no reconciler path assigns it. A valid, targeted contract with no runtime evidence yields
	// SeverityUnknown findings and resolves to ContractStatusUnknown (see validation.DeriveStatus), not
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
