package finding

const (
	// Durable v2 codes (may occur for v2 Operational Contracts).
	CodeUnsupportedPactoVersion      Code = "UNSUPPORTED_PACTO_VERSION"
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
	CodeInvalidInterfaceType         Code = "INVALID_INTERFACE_TYPE"
	CodeInterfaceRefRequired         Code = "INTERFACE_REF_REQUIRED"
	CodeInvalidCapabilityType        Code = "INVALID_CAPABILITY_TYPE"
	CodeCapabilityRefRequired        Code = "CAPABILITY_REF_REQUIRED"
	CodeCapabilityRefInvalid         Code = "CAPABILITY_REF_INVALID"
	CodeDuplicateCapability          Code = "DUPLICATE_CAPABILITY"
	CodeUnsupportedPolicyTarget      Code = "UNSUPPORTED_POLICY_TARGET"

	// Family 1 — confirmed violations (emitted only by Evaluate). Registry: {RuntimeDrift, Error}.
	CodeWorkloadMismatch      Code = "WORKLOAD_MISMATCH"
	CodePersistenceMismatch   Code = "PERSISTENCE_MISMATCH"
	CodeDependencyUnreachable Code = "DEPENDENCY_UNREACHABLE"
	CodeCapabilityAbsent      Code = "CAPABILITY_ABSENT"
	CodeInterfaceAbsent       Code = "INTERFACE_ABSENT"
	CodeConfigurationAbsent   Code = "CONFIGURATION_ABSENT"
	CodeConfigurationMismatch Code = "CONFIGURATION_MISMATCH"

	// Family 2 — insufficient/unreliable evidence (emitted by Evaluate). Registry: {Inconclusive, Unknown}.
	CodeEvidenceMissing               Code = "EVIDENCE_MISSING"
	CodeObservationUnsupported        Code = "OBSERVATION_UNSUPPORTED"
	CodeCollectionFailed              Code = "COLLECTION_FAILED"
	CodeEvidenceStale                 Code = "EVIDENCE_STALE"
	CodeEvidenceInsufficient          Code = "EVIDENCE_INSUFFICIENT"
	CodeExtensionEvaluatorUnavailable Code = "EXTENSION_EVALUATOR_UNAVAILABLE"

	// Structural (crossfield) — capability binding. Registry: {InvalidCapability, Error}.
	CodeCapabilityInterfaceUnknown Code = "CAPABILITY_INTERFACE_UNKNOWN" // binding.interface references no declared interface
	CodeCapabilityPathInvalid      Code = "CAPABILITY_PATH_INVALID"      // binding.path fails the net/url SSRF check

)

type codeMeta struct {
	category Category
	severity Severity
}

var registry = map[Code]codeMeta{
	// Durable v2
	CodeUnsupportedPactoVersion:      {CategoryInvalidVersion, SeverityError},
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
	CodeInvalidInterfaceType:         {CategoryInterfaceMismatch, SeverityError},
	CodeInterfaceRefRequired:         {CategoryInterfaceMismatch, SeverityError},
	CodeInvalidCapabilityType:        {CategoryInvalidCapability, SeverityError},
	CodeCapabilityRefRequired:        {CategoryInvalidCapability, SeverityError},
	CodeCapabilityRefInvalid:         {CategoryInvalidCapability, SeverityError},
	CodeDuplicateCapability:          {CategoryDuplicateName, SeverityError},
	CodeUnsupportedPolicyTarget:      {CategoryPolicyViolation, SeverityError},

	// Family 1 — confirmed violations
	CodeWorkloadMismatch:      {CategoryRuntimeDrift, SeverityError},
	CodePersistenceMismatch:   {CategoryRuntimeDrift, SeverityError},
	CodeDependencyUnreachable: {CategoryRuntimeDrift, SeverityError},
	CodeCapabilityAbsent:      {CategoryRuntimeDrift, SeverityError},
	CodeInterfaceAbsent:       {CategoryRuntimeDrift, SeverityError},
	CodeConfigurationAbsent:   {CategoryRuntimeDrift, SeverityError},
	CodeConfigurationMismatch: {CategoryRuntimeDrift, SeverityError},

	// Family 2 — uncertainty
	CodeEvidenceMissing:               {CategoryInconclusive, SeverityUnknown},
	CodeObservationUnsupported:        {CategoryInconclusive, SeverityUnknown},
	CodeCollectionFailed:              {CategoryInconclusive, SeverityUnknown},
	CodeEvidenceStale:                 {CategoryInconclusive, SeverityUnknown},
	CodeEvidenceInsufficient:          {CategoryInconclusive, SeverityUnknown},
	CodeExtensionEvaluatorUnavailable: {CategoryInconclusive, SeverityUnknown},

	// Structural (crossfield) — capability binding
	CodeCapabilityInterfaceUnknown: {CategoryInvalidCapability, SeverityError},
	CodeCapabilityPathInvalid:      {CategoryInvalidCapability, SeverityError},
}

// CategoryOf returns the coarse category for a code, or "" if unknown.
func CategoryOf(c Code) Category {
	return registry[c].category
}

// DefaultSeverity returns the code's default severity (error if unknown).
func DefaultSeverity(c Code) Severity {
	if m, ok := registry[c]; ok {
		return m.severity
	}
	return SeverityError
}
