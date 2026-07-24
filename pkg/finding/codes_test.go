package finding

import "testing"

func TestCategoryOf(t *testing.T) {
	cases := map[Code]Category{
		CodeStatelessPersistent:          CategoryStateMismatch,
		CodePolicyViolation:              CategoryPolicyViolation,
		CodeDuplicateInterfaceName:       CategoryDuplicateName,
		CodeInvalidSemver:                CategoryInvalidVersion,
		CodeTagNotDigest:                 CategoryInvalidReference,
		CodeEmptyCompatibility:           CategoryInvalidDependency,
		CodeInvalidConfigRef:             CategoryInvalidReference,
		CodeYamlParseError:               CategorySchemaViolation,
		CodePolicyRefUnresolved:          CategoryUnresolvedReference,
		CodePolicyRefCycle:               CategoryReferenceCycle,
		CodeInvalidReadinessExpires:      CategoryInvalidReadiness,
		CodeEmptyReadinessEvidence:       CategoryMissingEvidence,
		CodeValuesWithoutSchema:          CategoryMissingConfiguration,
		CodeConfigValuesValidationFailed: CategoryConfigurationViolation,
		CodeInvalidInterfaceSpec:         CategoryInvalidFile,
		CodeFileNotFound:                 CategoryInvalidFile,
		CodeInvalidInterfaceType:         CategoryInterfaceMismatch,
		CodeInterfaceRefRequired:         CategoryInterfaceMismatch,
		CodeInvalidCapabilityType:        CategoryInvalidCapability,
		CodeCapabilityRefRequired:        CategoryInvalidCapability,
		CodeCapabilityRefInvalid:         CategoryInvalidCapability,
		CodeDuplicateCapability:          CategoryDuplicateName,
		CodeUnsupportedPolicyTarget:      CategoryPolicyViolation,
		CodePortNotObserved:              CategoryRuntimeDrift,
		CodeConfigNotObserved:            CategoryRuntimeDrift,
		CodeCapabilityNotObserved:        CategoryRuntimeDrift,
		CodeInterfaceNotObserved:         CategoryRuntimeDrift,
		CodeDependencyUnreachable:        CategoryRuntimeDrift,
		CodeWorkloadMismatch:             CategoryRuntimeDrift,
		CodePersistenceMismatch:          CategoryRuntimeDrift,
	}
	for code, want := range cases {
		if got := CategoryOf(code); got != want {
			t.Errorf("CategoryOf(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestDefaultSeverity_WarningCodes(t *testing.T) {
	warningCodes := []Code{
		CodeTagNotDigest,
		CodePolicyRefNotEnforced,
		CodeConfigNotObserved,
		CodeCapabilityNotObserved,
		CodeInterfaceNotObserved,
		CodeDependencyUnreachable,
		CodeWorkloadMismatch,
		CodePersistenceMismatch,
	}
	for _, code := range warningCodes {
		if got := DefaultSeverity(code); got != SeverityWarning {
			t.Errorf("DefaultSeverity(%q) = %q, want %q", code, got, SeverityWarning)
		}
	}
}

func TestDefaultSeverity_ErrorCodes(t *testing.T) {
	errorCodes := []Code{
		CodePortNotObserved,
		CodePolicyViolation,
		CodeInvalidSemver,
		CodeStatelessPersistent,
		CodeInvalidInterfaceType,
		CodeInterfaceRefRequired,
		CodeInvalidCapabilityType,
		CodeCapabilityRefRequired,
		CodeCapabilityRefInvalid,
		CodeDuplicateCapability,
		CodeUnsupportedPolicyTarget,
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
		CodeTagNotDigest:          true,
		CodePolicyRefNotEnforced:  true,
		CodeConfigNotObserved:     true,
		CodeCapabilityNotObserved: true,
		CodeInterfaceNotObserved:  true,
		CodeDependencyUnreachable: true,
		CodeWorkloadMismatch:      true,
		CodePersistenceMismatch:   true,
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
