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
		CodeCapabilityRefInvalid:         CategoryInvalidCapability,
		CodeDuplicateCapability:          CategoryDuplicateName,
		// Family 1 — confirmed violations (RuntimeDrift).
		CodeWorkloadMismatch:      CategoryRuntimeDrift,
		CodePersistenceMismatch:   CategoryRuntimeDrift,
		CodeDependencyUnreachable: CategoryRuntimeDrift,
		CodeCapabilityAbsent:      CategoryRuntimeDrift,
		CodeInterfaceAbsent:       CategoryRuntimeDrift,
		CodeConfigurationAbsent:   CategoryRuntimeDrift,
		CodeConfigurationMismatch: CategoryRuntimeDrift,
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

func TestSeverityFor_ByFamily(t *testing.T) {
	overrides := expectedSeverities()
	// Family 1 (confirmed violations) now default to Error, not Warning.
	errorFamily1 := []Code{
		CodeWorkloadMismatch, CodePersistenceMismatch, CodeDependencyUnreachable,
		CodeCapabilityAbsent, CodeInterfaceAbsent, CodeConfigurationAbsent, CodeConfigurationMismatch,
	}
	for _, code := range errorFamily1 {
		if got := SeverityFor(code, true); got != SeverityError {
			t.Errorf("SeverityFor(%q, true) = %q, want error (family-1)", code, got)
		}
	}
	// Family 2 -> Unknown.
	for _, code := range []Code{
		CodeEvidenceMissing, CodeObservationUnsupported, CodeCollectionFailed,
		CodeEvidenceStale, CodeEvidenceInsufficient, CodeExtensionEvaluatorUnavailable,
	} {
		if got := SeverityFor(code, true); got != SeverityUnknown {
			t.Errorf("SeverityFor(%q, true) = %q, want unknown (family-2)", code, got)
		}
	}
	// Structural crossfield -> Error.
	for _, code := range []Code{CodeCapabilityInterfaceUnknown, CodeCapabilityPathInvalid} {
		if got := SeverityFor(code, true); got != SeverityError {
			t.Errorf("SeverityFor(%q, true) = %q, want error (structural)", code, got)
		}
	}
	// Spot-check overrides map self-consistency against the registry.
	for code, want := range overrides {
		if got := SeverityFor(code, true); got != want {
			t.Errorf("SeverityFor(%q, true) = %q, want %q", code, got, want)
		}
	}
}

// An optional assertion cannot make the contract non-compliant, so a confirmed
// violation of one is a warning — but only for the three codes that declare the
// axis. Anything else keeps its registry row whatever required says.
func TestSeverityFor_OptionalAssertion(t *testing.T) {
	for _, code := range []Code{CodeDependencyUnreachable, CodeConfigurationAbsent, CodeConfigurationMismatch} {
		if got := SeverityFor(code, false); got != SeverityWarning {
			t.Errorf("SeverityFor(%q, optional) = %q, want warning", code, got)
		}
	}
	// No axis declared: an error-severity code with no optional row stays an error.
	if got := SeverityFor(CodeWorkloadMismatch, false); got != SeverityError {
		t.Errorf("SeverityFor(WORKLOAD_MISMATCH, optional) = %q, want error", got)
	}
	if got := SeverityFor(CodeEvidenceMissing, false); got != SeverityUnknown {
		t.Errorf("SeverityFor(EVIDENCE_MISSING, optional) = %q, want unknown", got)
	}
}

// Forgetting the registry row for a new drift code must not buy it silence. An
// unregistered code is an error whatever required says, so the omission surfaces as
// NonCompliant instead of a warning nobody reads.
func TestSeverityFor_UnregisteredCodeIsAlwaysAnError(t *testing.T) {
	for _, required := range []bool{true, false} {
		if got := SeverityFor("NOT_A_REAL_CODE", required); got != SeverityError {
			t.Errorf("SeverityFor(unregistered, required=%v) = %q, want error", required, got)
		}
	}
}

// Every optional row must name a code the registry already knows, or the axis
// silently applies to nothing.
func TestOptionalSeverityRowsAreRegistered(t *testing.T) {
	for code, meta := range optionalSeverity {
		reg, ok := registry[code]
		if !ok {
			t.Errorf("optionalSeverity has a row for unregistered code %q", code)
			continue
		}
		if meta.category != reg.category {
			t.Errorf("optionalSeverity[%q].category = %q, want the registry's %q", code, meta.category, reg.category)
		}
		if meta.severity == reg.severity {
			t.Errorf("optionalSeverity[%q] repeats the registry severity %q, so the row is dead weight", code, meta.severity)
		}
	}
}

func TestUnknownCodeCategory(t *testing.T) {
	if CategoryOf("NOT_A_REAL_CODE") != "" {
		t.Fatalf("unknown code must map to empty category, not a guess")
	}
	for _, required := range []bool{true, false} {
		if got := CategoryFor("NOT_A_REAL_CODE", required); got != "" {
			t.Errorf("CategoryFor(unregistered, required=%v) = %q, want empty", required, got)
		}
	}
}

// The category must be read off the SAME row the severity came from, or the two
// drift apart the first time a required-ness row needs its own category. The real
// table repeats the registry's category by design (TestOptionalSeverityRowsAreRegistered
// enforces that), so a row that deliberately differs is the only way to observe
// which row CategoryFor read.
func TestCategoryFor_ReadsTheRequiredNessRow(t *testing.T) {
	orig := optionalSeverity
	t.Cleanup(func() { optionalSeverity = orig })
	optionalSeverity = map[Code]codeMeta{
		CodeConfigurationAbsent: {CategoryInconclusive, SeverityWarning},
	}

	if got := CategoryFor(CodeConfigurationAbsent, false); got != CategoryInconclusive {
		t.Errorf("CategoryFor(optional) = %q, want the optional row's %q", got, CategoryInconclusive)
	}
	if got := CategoryFor(CodeConfigurationAbsent, true); got != CategoryRuntimeDrift {
		t.Errorf("CategoryFor(required) = %q, want the registry row's %q", got, CategoryRuntimeDrift)
	}
	// No optional row: the base row answers whatever required says.
	if got := CategoryFor(CodeWorkloadMismatch, false); got != CategoryRuntimeDrift {
		t.Errorf("CategoryFor(WORKLOAD_MISMATCH, optional) = %q, want RuntimeDrift", got)
	}
}

// Deprecated shims kept for the released v3 major. DefaultSeverity must answer
// exactly what it answered in v3.2.9 — SeverityFor with required=true — and the
// retired codes must keep their published strings.
func TestDeprecatedShims(t *testing.T) {
	for code := range registry {
		if got, want := DefaultSeverity(code), SeverityFor(code, true); got != want {
			t.Errorf("DefaultSeverity(%q) = %q, want SeverityFor(%q, true) = %q", code, got, code, want)
		}
	}
	if got := DefaultSeverity("NOT_A_REAL_CODE"); got != SeverityError {
		t.Errorf("DefaultSeverity(unregistered) = %q, want error", got)
	}
	// Retired codes: strings frozen, and deliberately unregistered so the generated
	// operator reference stops listing codes nothing can emit.
	retired := map[Code]string{
		CodeInvalidInterfaceType:    "INVALID_INTERFACE_TYPE",
		CodeInterfaceRefRequired:    "INTERFACE_REF_REQUIRED",
		CodeInvalidCapabilityType:   "INVALID_CAPABILITY_TYPE",
		CodeCapabilityRefRequired:   "CAPABILITY_REF_REQUIRED",
		CodeUnsupportedPolicyTarget: "UNSUPPORTED_POLICY_TARGET",
	}
	for code, want := range retired {
		if string(code) != want {
			t.Errorf("retired code = %q, want the published %q", code, want)
		}
		if _, ok := registry[code]; ok {
			t.Errorf("retired code %q must stay out of the registry", code)
		}
		if got := DefaultSeverity(code); got != SeverityError {
			t.Errorf("DefaultSeverity(%q) = %q, want error (v3.2.9 behaviour)", code, got)
		}
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
		if got := SeverityFor(code, true); got != wantSev {
			t.Errorf("SeverityFor(%q, true) = %q, want %q", code, got, wantSev)
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
