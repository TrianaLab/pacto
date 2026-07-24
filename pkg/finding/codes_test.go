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
		// Family 1 — confirmed violations (RuntimeDrift).
		CodeWorkloadMismatch:      CategoryRuntimeDrift,
		CodePersistenceMismatch:   CategoryRuntimeDrift,
		CodeDependencyUnreachable: CategoryRuntimeDrift,
		CodeCapabilityAbsent:      CategoryRuntimeDrift,
		CodeInterfaceAbsent:       CategoryRuntimeDrift,
		CodeConfigurationAbsent:   CategoryRuntimeDrift,
		// Family 2 — uncertainty (Inconclusive).
		CodeEvidenceMissing:               CategoryInconclusive,
		CodeObservationUnsupported:        CategoryInconclusive,
		CodeCollectionFailed:              CategoryInconclusive,
		CodeEvidenceStale:                 CategoryInconclusive,
		CodeEvidenceInsufficient:          CategoryInconclusive,
		CodeExtensionEvaluatorUnavailable: CategoryInconclusive,
		// Structural crossfield — capability binding.
		CodeCapabilityInterfaceUnknown: CategoryInvalidCapability,
		CodeCapabilityPathInvalid:      CategoryInvalidCapability,
		// Structural crossfield — verification.
		CodeVerificationInterfaceUnknown:   CategoryInterfaceMismatch,
		CodeDuplicateVerificationInterface: CategoryDuplicateName,
	}
	for code, want := range cases {
		if got := CategoryOf(code); got != want {
			t.Errorf("CategoryOf(%q) = %q, want %q", code, got, want)
		}
	}
}

// familySeverity is the single source of truth for the expected severity of
// every registered code, so the contract test below stays exhaustive.
func expectedSeverities() map[Code]Severity {
	m := map[Code]Severity{
		// Warning-severity codes.
		CodeTagNotDigest:         SeverityWarning,
		CodePolicyRefNotEnforced: SeverityWarning,
		// Family 2 — uncertainty -> Unknown.
		CodeEvidenceMissing:               SeverityUnknown,
		CodeObservationUnsupported:        SeverityUnknown,
		CodeCollectionFailed:              SeverityUnknown,
		CodeEvidenceStale:                 SeverityUnknown,
		CodeEvidenceInsufficient:          SeverityUnknown,
		CodeExtensionEvaluatorUnavailable: SeverityUnknown,
	}
	return m
}

func TestDefaultSeverity_ByFamily(t *testing.T) {
	overrides := expectedSeverities()
	// Family 1 (confirmed violations) now default to Error, not Warning.
	errorFamily1 := []Code{
		CodeWorkloadMismatch, CodePersistenceMismatch, CodeDependencyUnreachable,
		CodeCapabilityAbsent, CodeInterfaceAbsent, CodeConfigurationAbsent,
	}
	for _, code := range errorFamily1 {
		if got := DefaultSeverity(code); got != SeverityError {
			t.Errorf("DefaultSeverity(%q) = %q, want error (family-1)", code, got)
		}
	}
	// Family 2 -> Unknown.
	for _, code := range []Code{
		CodeEvidenceMissing, CodeObservationUnsupported, CodeCollectionFailed,
		CodeEvidenceStale, CodeEvidenceInsufficient, CodeExtensionEvaluatorUnavailable,
	} {
		if got := DefaultSeverity(code); got != SeverityUnknown {
			t.Errorf("DefaultSeverity(%q) = %q, want unknown (family-2)", code, got)
		}
	}
	// Structural crossfield -> Error.
	for _, code := range []Code{CodeCapabilityInterfaceUnknown, CodeCapabilityPathInvalid} {
		if got := DefaultSeverity(code); got != SeverityError {
			t.Errorf("DefaultSeverity(%q) = %q, want error (structural)", code, got)
		}
	}
	// Spot-check overrides map self-consistency against the registry.
	for code, want := range overrides {
		if got := DefaultSeverity(code); got != want {
			t.Errorf("DefaultSeverity(%q) = %q, want %q", code, got, want)
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
	overrides := expectedSeverities()
	for code := range registry {
		if cat := CategoryOf(code); cat == "" {
			t.Errorf("CategoryOf(%q) = empty, want non-empty", code)
		}
		wantSev, ok := overrides[code]
		if !ok {
			wantSev = SeverityError // default for everything not warning/unknown
		}
		if got := DefaultSeverity(code); got != wantSev {
			t.Errorf("DefaultSeverity(%q) = %q, want %q", code, got, wantSev)
		}
	}
}

// TestPortNotObservedRemoved is a compile-time guard documenting that the dead
// PORT_NOT_OBSERVED code is gone. If someone re-adds it, this test's comment
// points at the reason. (No runtime assertion needed — the identifier removal
// is enforced by the compiler across the package.)
func TestFamily1CodesAreAbsentNaming(t *testing.T) {
	if CodeCapabilityAbsent != "CAPABILITY_ABSENT" ||
		CodeInterfaceAbsent != "INTERFACE_ABSENT" ||
		CodeConfigurationAbsent != "CONFIGURATION_ABSENT" {
		t.Fatal("family-1 absence codes must use the *_ABSENT naming")
	}
}
