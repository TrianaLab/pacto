package finding

import "testing"

func TestCategoryOf(t *testing.T) {
	cases := map[Code]Category{
		CodePortRequired:                 CategoryInterfaceMismatch,
		CodeStatelessPersistent:          CategoryStateMismatch,
		CodePolicyViolation:              CategoryPolicyViolation,
		CodeDuplicateInterfaceName:       CategoryDuplicateName,
		CodePortNotObserved:              CategoryRuntimeDrift,
		CodeInvalidSemver:                CategoryInvalidVersion,
		CodeTagNotDigest:                 CategoryInvalidReference,
		CodeEmptyCompatibility:           CategoryInvalidDependency,
		CodeInvalidConfigRef:             CategoryInvalidReference,
		CodeYamlParseError:               CategorySchemaViolation,
		CodeHealthInterfaceNotFound:      CategoryInterfaceMismatch,
		CodePolicyRefUnresolved:          CategoryUnresolvedReference,
		CodePolicyRefCycle:               CategoryReferenceCycle,
		CodeInvalidReadinessExpires:      CategoryInvalidReadiness,
		CodeEmptyReadinessEvidence:       CategoryMissingEvidence,
		CodeValuesWithoutSchema:          CategoryMissingConfiguration,
		CodeConfigValuesValidationFailed: CategoryConfigurationViolation,
		CodeInvalidContractFile:          CategoryInvalidFile,
	}
	for code, want := range cases {
		if got := CategoryOf(code); got != want {
			t.Errorf("CategoryOf(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestDefaultSeverity_WarningCodes(t *testing.T) {
	warningCodes := []Code{
		CodePortIgnored,
		CodeHealthPathIgnored,
		CodeMetricsPathIgnored,
		CodeTagNotDigest,
		CodeUpgradeStrategyStateMismatch,
		CodePolicyRefNotEnforced,
		CodeConfigNotObserved,
	}
	for _, code := range warningCodes {
		if got := DefaultSeverity(code); got != SeverityWarning {
			t.Errorf("DefaultSeverity(%q) = %q, want %q", code, got, SeverityWarning)
		}
	}
}

func TestDefaultSeverity_ErrorCodes(t *testing.T) {
	errorCodes := []Code{
		CodePortRequired,
		CodePortNotObserved,
		CodePolicyViolation,
		CodeInvalidSemver,
		CodeStatelessPersistent,
	}
	for _, code := range errorCodes {
		if got := DefaultSeverity(code); got != SeverityError {
			t.Errorf("DefaultSeverity(%q) = %q, want %q", code, got, SeverityError)
		}
	}
}

func TestUnknownCodeCategory(t *testing.T) {
	if CategoryOf("NOT_A_REAL_CODE") != "" {
		t.Fatalf("unknown code must map to empty category, not a guess")
	}
}

func TestUnknownCodeSeverity(t *testing.T) {
	if DefaultSeverity("NOT_A_REAL_CODE") != SeverityError {
		t.Fatalf("unknown code must default to error severity")
	}
}

func TestRegistryContract(t *testing.T) {
	warnCodes := map[Code]bool{
		CodePortIgnored:                  true,
		CodeHealthPathIgnored:            true,
		CodeMetricsPathIgnored:           true,
		CodeTagNotDigest:                 true,
		CodeUpgradeStrategyStateMismatch: true,
		CodePolicyRefNotEnforced:         true,
		CodeConfigNotObserved:            true,
	}
	for code := range registry {
		if cat := CategoryOf(code); cat == "" {
			t.Errorf("CategoryOf(%q) = empty, want non-empty", code)
		}
		wantSev := SeverityError
		if warnCodes[code] {
			wantSev = SeverityWarning
		}
		if got := DefaultSeverity(code); got != wantSev {
			t.Errorf("DefaultSeverity(%q) = %q, want %q", code, got, wantSev)
		}
	}
}
