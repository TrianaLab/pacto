package finding

import (
	"slices"
	"strings"
)

const (
	// Durable v2 codes (may occur for v2 Operational Contracts).
	CodeUnsupportedPactoVersion      Code = "UNSUPPORTED_PACTO_VERSION"
	CodeNeedsPacto21                 Code = "NEEDS_PACTO_2_1" // a 2.0 contract uses publish, agent or an mcp interface
	CodeSchemaError                  Code = "SCHEMA_ERROR"
	CodeSchemaViolation              Code = "SCHEMA_VIOLATION"
	CodeYamlParseError               Code = "YAML_PARSE_ERROR"
	CodeInvalidSemver                Code = "INVALID_SEMVER"
	CodeInvalidConfigRef             Code = "INVALID_CONFIG_REF"
	CodeInvalidPolicyRef             Code = "INVALID_POLICY_REF"
	CodeInvalidOciRef                Code = "INVALID_OCI_REF"
	CodeTagNotDigest                 Code = "TAG_NOT_DIGEST"
	CodeDuplicateInterfaceName       Code = "DUPLICATE_INTERFACE_NAME"
	CodeDuplicateConfigurationName   Code = "DUPLICATE_CONFIGURATION_NAME"
	CodeDuplicatePolicyName          Code = "DUPLICATE_POLICY_NAME"
	CodeDuplicateDependencyName      Code = "DUPLICATE_DEPENDENCY_NAME"
	CodeEmptyCompatibility           Code = "EMPTY_COMPATIBILITY"
	CodeInvalidCompatibility         Code = "INVALID_COMPATIBILITY"
	CodeValuesWithoutSchema          Code = "VALUES_WITHOUT_SCHEMA"
	CodeConfigValuesValidationFailed Code = "CONFIG_VALUES_VALIDATION_FAILED"
	CodeInvalidInterfaceSpec         Code = "INVALID_INTERFACE_SPEC"
	CodeInvalidConfigJson            Code = "INVALID_CONFIG_JSON"
	CodeInvalidConfigSchema          Code = "INVALID_CONFIG_SCHEMA"
	CodeInvalidPolicyJson            Code = "INVALID_POLICY_JSON"
	CodeInvalidPolicySchema          Code = "INVALID_POLICY_SCHEMA"
	CodeFileNotFound                 Code = "FILE_NOT_FOUND"
	CodeInvalidReadinessExpires      Code = "INVALID_READINESS_EXPIRES"
	CodeInvalidReadinessRevision     Code = "INVALID_READINESS_REVISION"
	CodeDuplicateReadinessId         Code = "DUPLICATE_READINESS_ID"
	CodeEmptyReadinessEvidence       Code = "EMPTY_READINESS_EVIDENCE"
	CodeEmptyReadinessDescription    Code = "EMPTY_READINESS_DESCRIPTION"
	CodePolicyEnforcementError       Code = "POLICY_ENFORCEMENT_ERROR"
	CodePolicyViolation              Code = "POLICY_VIOLATION"
	CodePolicyRefNotEnforced         Code = "POLICY_REF_NOT_ENFORCED"
	CodePolicyRefUnresolved          Code = "POLICY_REF_UNRESOLVED"
	CodePolicyRefCycle               Code = "POLICY_REF_CYCLE"
	CodeCapabilityRefInvalid         Code = "CAPABILITY_REF_INVALID"
	CodeDuplicateCapability          Code = "DUPLICATE_CAPABILITY"

	// Family 1 — confirmed violations (emitted only by Evaluate). Registry: {RuntimeDrift, Error}.
	CodeWorkloadMismatch      Code = "WORKLOAD_MISMATCH"
	CodePersistenceMismatch   Code = "PERSISTENCE_MISMATCH"
	CodeDependencyUnreachable Code = "DEPENDENCY_UNREACHABLE"
	CodeCapabilityAbsent      Code = "CAPABILITY_ABSENT"
	CodeInterfaceAbsent       Code = "INTERFACE_ABSENT"
	CodeConfigurationAbsent   Code = "CONFIGURATION_ABSENT"
	CodeConfigurationMismatch Code = "CONFIGURATION_MISMATCH"
	// Facet violations: a declared facet of an observed assertion contradicts the observed fact.
	CodeInterfaceProtocolMismatch   Code = "INTERFACE_PROTOCOL_MISMATCH"   // interfaces[].type vs the probed protocol
	CodeInterfaceVisibilityMismatch Code = "INTERFACE_VISIBILITY_MISMATCH" // internal interface exposed outside the cluster
	CodeDependencyVersionMismatch   Code = "DEPENDENCY_VERSION_MISMATCH"   // provider version outside dependencies[].compatibility

	// Runtime drift that is not a contract violation. Registry: {RuntimeDrift, Warning}. The contract does not
	// claim its dependency list is exhaustive, so an undeclared peer can never make it non-compliant.
	CodeDependencyUndeclared Code = "DEPENDENCY_UNDECLARED"

	// Family 2 — insufficient/unreliable evidence (emitted by Evaluate). Registry: {Inconclusive, Unknown}.
	CodeEvidenceMissing        Code = "EVIDENCE_MISSING"
	CodeObservationUnsupported Code = "OBSERVATION_UNSUPPORTED"
	CodeCollectionFailed       Code = "COLLECTION_FAILED"
	CodeEvidenceStale          Code = "EVIDENCE_STALE"
	CodeEvidenceInsufficient   Code = "EVIDENCE_INSUFFICIENT"

	// Structural (crossfield) — capability binding. Registry: {InvalidCapability, Error}.
	CodeCapabilityInterfaceUnknown Code = "CAPABILITY_INTERFACE_UNKNOWN" // binding.interface references no declared interface
	CodeCapabilityPathInvalid      Code = "CAPABILITY_PATH_INVALID"      // binding.path fails the net/url SSRF check

)

// codeMeta is the registry row for a code: the category and severity a producer
// must stamp on a finding carrying it. Required-ness is the second axis and has
// its own rows in optionalSeverity, because the published operator reference is
// generated from these rows and a column no producer honours is how the reference
// drifted from what is actually emitted.
type codeMeta struct {
	category Category
	severity Severity
}

var registry = map[Code]codeMeta{
	// Durable v2
	CodeUnsupportedPactoVersion:      {CategoryInvalidVersion, SeverityError},
	CodeNeedsPacto21:                 {CategoryInvalidVersion, SeverityError},
	CodeSchemaError:                  {CategorySchemaViolation, SeverityError},
	CodeSchemaViolation:              {CategorySchemaViolation, SeverityError},
	CodeYamlParseError:               {CategorySchemaViolation, SeverityError},
	CodeInvalidSemver:                {CategoryInvalidVersion, SeverityError},
	CodeInvalidConfigRef:             {CategoryInvalidReference, SeverityError},
	CodeInvalidPolicyRef:             {CategoryInvalidReference, SeverityError},
	CodeInvalidOciRef:                {CategoryInvalidReference, SeverityError},
	CodeTagNotDigest:                 {CategoryInvalidReference, SeverityWarning},
	CodeDuplicateInterfaceName:       {CategoryDuplicateName, SeverityError},
	CodeDuplicateConfigurationName:   {CategoryDuplicateName, SeverityError},
	CodeDuplicatePolicyName:          {CategoryDuplicateName, SeverityError},
	CodeDuplicateDependencyName:      {CategoryDuplicateName, SeverityError},
	CodeEmptyCompatibility:           {CategoryInvalidDependency, SeverityError},
	CodeInvalidCompatibility:         {CategoryInvalidDependency, SeverityError},
	CodeValuesWithoutSchema:          {CategoryMissingConfiguration, SeverityError},
	CodeConfigValuesValidationFailed: {CategoryConfigurationViolation, SeverityError},
	CodeInvalidInterfaceSpec:         {CategoryInvalidFile, SeverityError},
	CodeInvalidConfigJson:            {CategoryInvalidFile, SeverityError},
	CodeInvalidConfigSchema:          {CategorySchemaViolation, SeverityError},
	CodeInvalidPolicyJson:            {CategoryInvalidFile, SeverityError},
	CodeInvalidPolicySchema:          {CategorySchemaViolation, SeverityError},
	CodeFileNotFound:                 {CategoryInvalidFile, SeverityError},
	CodeStatelessPersistent:          {CategoryStateMismatch, SeverityError},
	CodeInvalidReadinessExpires:      {CategoryInvalidReadiness, SeverityError},
	CodeInvalidReadinessRevision:     {CategoryInvalidReadiness, SeverityError},
	CodeDuplicateReadinessId:         {CategoryDuplicateName, SeverityError},
	CodeEmptyReadinessEvidence:       {CategoryMissingEvidence, SeverityError},
	CodeEmptyReadinessDescription:    {CategoryMissingEvidence, SeverityError},
	CodePolicyEnforcementError:       {CategoryPolicyViolation, SeverityError},
	CodePolicyViolation:              {CategoryPolicyViolation, SeverityError},
	CodePolicyRefNotEnforced:         {CategoryUnresolvedReference, SeverityWarning},
	CodePolicyRefUnresolved:          {CategoryUnresolvedReference, SeverityError},
	CodePolicyRefCycle:               {CategoryReferenceCycle, SeverityError},
	CodeCapabilityRefInvalid:         {CategoryInvalidCapability, SeverityError},
	CodeDuplicateCapability:          {CategoryDuplicateName, SeverityError},

	// Family 1 — confirmed violations
	CodeWorkloadMismatch:            {CategoryRuntimeDrift, SeverityError},
	CodePersistenceMismatch:         {CategoryRuntimeDrift, SeverityError},
	CodeDependencyUnreachable:       {CategoryRuntimeDrift, SeverityError},
	CodeCapabilityAbsent:            {CategoryRuntimeDrift, SeverityError},
	CodeInterfaceAbsent:             {CategoryRuntimeDrift, SeverityError},
	CodeConfigurationAbsent:         {CategoryRuntimeDrift, SeverityError},
	CodeConfigurationMismatch:       {CategoryRuntimeDrift, SeverityError},
	CodeInterfaceProtocolMismatch:   {CategoryRuntimeDrift, SeverityError},
	CodeInterfaceVisibilityMismatch: {CategoryRuntimeDrift, SeverityError},
	CodeDependencyVersionMismatch:   {CategoryRuntimeDrift, SeverityError},

	// Runtime drift, never a violation
	CodeDependencyUndeclared: {CategoryRuntimeDrift, SeverityWarning},

	// Family 2 — uncertainty
	CodeEvidenceMissing:        {CategoryInconclusive, SeverityUnknown},
	CodeObservationUnsupported: {CategoryInconclusive, SeverityUnknown},
	CodeCollectionFailed:       {CategoryInconclusive, SeverityUnknown},
	CodeEvidenceStale:          {CategoryInconclusive, SeverityUnknown},
	CodeEvidenceInsufficient:   {CategoryInconclusive, SeverityUnknown},

	// Structural (crossfield) — capability binding
	CodeCapabilityInterfaceUnknown: {CategoryInvalidCapability, SeverityError},
	CodeCapabilityPathInvalid:      {CategoryInvalidCapability, SeverityError},
}

// optionalSeverity holds the registry's required-ness axis: the row a code carries
// when the assertion it reports on was declared OPTIONAL. An optional assertion
// cannot make the contract non-compliant, so a confirmed violation of one is a
// warning. Only the assertions that carry `required` in a contract can reach
// these codes with required=false; every other code has no such axis and keeps its
// registry row whatever the caller passes.
//
// These are registry rows in the same shape for a reason: the published operator
// reference is generated from them, so listing DEPENDENCY_UNREACHABLE here is what
// stops the reference from claiming it is only ever an error.
var optionalSeverity = map[Code]codeMeta{
	CodeDependencyUnreachable:     {CategoryRuntimeDrift, SeverityWarning},
	CodeConfigurationAbsent:       {CategoryRuntimeDrift, SeverityWarning},
	CodeConfigurationMismatch:     {CategoryRuntimeDrift, SeverityWarning},
	CodeDependencyVersionMismatch: {CategoryRuntimeDrift, SeverityWarning},
}

// metaFor resolves the registry row for a code on both axes: the required-ness row
// when the assertion was declared optional, the base row otherwise. It is the one
// lookup, so category and severity can never be read off different rows.
func metaFor(c Code, required bool) (codeMeta, bool) {
	return metaIn(optionalSeverity, c, required)
}

// metaIn is metaFor reading the required-ness rows from optional.
func metaIn(optional map[Code]codeMeta, c Code, required bool) (codeMeta, bool) {
	if !required {
		if m, ok := optional[c]; ok {
			return m, true
		}
	}
	m, ok := registry[c]
	return m, ok
}

// CategoryOf returns the coarse category for a code, or "" if unknown. It is
// CategoryFor for producers with no required-ness axis to report.
func CategoryOf(c Code) Category {
	return CategoryFor(c, true)
}

// CategoryFor returns the category a producer must stamp on a finding for this
// code, given whether the assertion it reports on was declared required. An
// unregistered code has no category: "" rather than a guess, because a consumer
// switching on a fabricated category is worse than one handling the empty case.
func CategoryFor(c Code, required bool) Category {
	m, _ := metaFor(c, required)
	return m.category
}

// SeverityFor returns the severity a producer must stamp on a finding for this
// code, given whether the assertion it reports on was declared required. An
// unregistered code is an error whatever required says: a code nobody declared
// must not be able to downgrade itself into silence, so forgetting a registry row
// for a new drift code fails loudly instead of emitting a warning nobody reads.
// Structural codes have no required-ness axis and pass required=true.
func SeverityFor(c Code, required bool) Severity {
	if m, ok := metaFor(c, required); ok {
		return m.severity
	}
	return SeverityError
}

// CodeInfo is one registry row: the category and severity a finding carrying
// Code is stamped with. Optional marks the row used when the assertion was
// declared optional (see SeverityFor); a code without that axis has no such row.
type CodeInfo struct {
	Code     Code     `json:"code"`
	Category Category `json:"category"`
	Severity Severity `json:"severity"`
	Optional bool     `json:"optional,omitempty"`
}

// Registry returns every registry row, sorted by code with the required row
// first. It is a fresh copy: the registry itself cannot be changed through it.
func Registry() []CodeInfo {
	out := make([]CodeInfo, 0, len(registry)+len(optionalSeverity))
	for c, m := range registry {
		out = append(out, CodeInfo{Code: c, Category: m.category, Severity: m.severity})
	}
	for c, m := range optionalSeverity {
		out = append(out, CodeInfo{Code: c, Category: m.category, Severity: m.severity, Optional: true})
	}
	// Stable, so the required row (appended first) stays ahead of the optional one.
	slices.SortStableFunc(out, func(a, b CodeInfo) int { return strings.Compare(string(a.Code), string(b.Code)) })
	return out
}
