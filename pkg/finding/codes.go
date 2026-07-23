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
	CodeContractRequired             Code = "CONTRACT_REQUIRED"
	CodeEmptyCompatibility           Code = "EMPTY_COMPATIBILITY"
	CodeInvalidCompatibility         Code = "INVALID_COMPATIBILITY"
	CodeValuesWithoutSchema          Code = "VALUES_WITHOUT_SCHEMA"
	CodeConfigValuesValidationFailed Code = "CONFIG_VALUES_VALIDATION_FAILED"
	CodeInvalidContractFile          Code = "INVALID_CONTRACT_FILE"
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

	// Evidence-based codes (emitted only by Evaluate, Phase 5).
	CodePortNotObserved   Code = "PORT_NOT_OBSERVED"
	CodeConfigNotObserved Code = "CONFIG_NOT_OBSERVED"

	// Legacy v1 codes: still emitted when validating v1 bundles, but NOT part of
	// the v2 domain (their fields are removed in v2). Grouped and documented here
	// so they are not mistaken for canonical v2 findings. No runtime taxonomy flag
	// (Scope/Legacy) is added until a consumer needs one — docs-only distinction.
	CodePortRequired                 Code = "PORT_REQUIRED"
	CodePortIgnored                  Code = "PORT_IGNORED"
	CodeHealthInterfaceNotFound      Code = "HEALTH_INTERFACE_NOT_FOUND"
	CodeHealthInterfaceInvalid       Code = "HEALTH_INTERFACE_INVALID"
	CodeHealthPathRequired           Code = "HEALTH_PATH_REQUIRED"
	CodeHealthPathIgnored            Code = "HEALTH_PATH_IGNORED"
	CodeMetricsInterfaceNotFound     Code = "METRICS_INTERFACE_NOT_FOUND"
	CodeMetricsInterfaceInvalid      Code = "METRICS_INTERFACE_INVALID"
	CodeMetricsPathRequired          Code = "METRICS_PATH_REQUIRED"
	CodeMetricsPathIgnored           Code = "METRICS_PATH_IGNORED"
	CodeInvalidImageRef              Code = "INVALID_IMAGE_REF"
	CodeInvalidChartRef              Code = "INVALID_CHART_REF"
	CodeInvalidChartVersion          Code = "INVALID_CHART_VERSION"
	CodeScalingMinExceedsMax         Code = "SCALING_MIN_EXCEEDS_MAX"
	CodeJobScalingNotAllowed         Code = "JOB_SCALING_NOT_ALLOWED"
	CodeUpgradeStrategyStateMismatch Code = "UPGRADE_STRATEGY_STATE_MISMATCH"
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
	CodeContractRequired:             {CategoryInterfaceMismatch, SeverityError},
	CodeEmptyCompatibility:           {CategoryInvalidDependency, SeverityError},
	CodeInvalidCompatibility:         {CategoryInvalidDependency, SeverityError},
	CodeValuesWithoutSchema:          {CategoryMissingConfiguration, SeverityError},
	CodeConfigValuesValidationFailed: {CategoryConfigurationViolation, SeverityError},
	CodeInvalidContractFile:          {CategoryInvalidFile, SeverityError},
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

	// Evidence-based
	CodePortNotObserved:   {CategoryRuntimeDrift, SeverityError},
	CodeConfigNotObserved: {CategoryRuntimeDrift, SeverityWarning},

	// Legacy v1
	CodePortRequired:                 {CategoryInterfaceMismatch, SeverityError},
	CodePortIgnored:                  {CategoryInterfaceMismatch, SeverityWarning},
	CodeHealthInterfaceNotFound:      {CategoryInterfaceMismatch, SeverityError},
	CodeHealthInterfaceInvalid:       {CategoryInterfaceMismatch, SeverityError},
	CodeHealthPathRequired:           {CategoryInterfaceMismatch, SeverityError},
	CodeHealthPathIgnored:            {CategoryInterfaceMismatch, SeverityWarning},
	CodeMetricsInterfaceNotFound:     {CategoryInterfaceMismatch, SeverityError},
	CodeMetricsInterfaceInvalid:      {CategoryInterfaceMismatch, SeverityError},
	CodeMetricsPathRequired:          {CategoryInterfaceMismatch, SeverityError},
	CodeMetricsPathIgnored:           {CategoryInterfaceMismatch, SeverityWarning},
	CodeInvalidImageRef:              {CategoryInvalidReference, SeverityError},
	CodeInvalidChartRef:              {CategoryInvalidReference, SeverityError},
	CodeInvalidChartVersion:          {CategoryInvalidVersion, SeverityError},
	CodeScalingMinExceedsMax:         {CategoryConfigurationViolation, SeverityError},
	CodeJobScalingNotAllowed:         {CategoryConfigurationViolation, SeverityError},
	CodeUpgradeStrategyStateMismatch: {CategoryStateMismatch, SeverityWarning},
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
