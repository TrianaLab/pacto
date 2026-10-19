package finding

import (
	"strings"
	"testing"
)

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
		// Facet violations and undeclared drift (RuntimeDrift).
		CodeInterfaceProtocolMismatch:   CategoryRuntimeDrift,
		CodeInterfaceVisibilityMismatch: CategoryRuntimeDrift,
		CodeDependencyVersionMismatch:   CategoryRuntimeDrift,
		CodeDependencyUndeclared:        CategoryRuntimeDrift,
		// Family 2 — uncertainty (Inconclusive).
		CodeEvidenceMissing:        CategoryInconclusive,
		CodeObservationUnsupported: CategoryInconclusive,
		CodeCollectionFailed:       CategoryInconclusive,
		CodeEvidenceStale:          CategoryInconclusive,
		CodeEvidenceInsufficient:   CategoryInconclusive,
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
		CodeDependencyUndeclared: SeverityWarning,
		// Family 2 — uncertainty -> Unknown.
		CodeEvidenceMissing:        SeverityUnknown,
		CodeObservationUnsupported: SeverityUnknown,
		CodeCollectionFailed:       SeverityUnknown,
		CodeEvidenceStale:          SeverityUnknown,
		CodeEvidenceInsufficient:   SeverityUnknown,
	}
	return m
}

func TestSeverityFor_ByFamily(t *testing.T) {
	overrides := expectedSeverities()
	// Family 1 (confirmed violations) now default to Error, not Warning.
	errorFamily1 := []Code{
		CodeWorkloadMismatch, CodePersistenceMismatch, CodeDependencyUnreachable,
		CodeCapabilityAbsent, CodeInterfaceAbsent, CodeConfigurationAbsent, CodeConfigurationMismatch,
		CodeInterfaceProtocolMismatch, CodeInterfaceVisibilityMismatch, CodeDependencyVersionMismatch,
	}
	for _, code := range errorFamily1 {
		if got := SeverityFor(code, true); got != SeverityError {
			t.Errorf("SeverityFor(%q, true) = %q, want error (family-1)", code, got)
		}
	}
	// An undeclared peer is drift the contract never ruled out: a warning even for a required caller.
	if got := SeverityFor(CodeDependencyUndeclared, true); got != SeverityWarning {
		t.Errorf("SeverityFor(DEPENDENCY_UNDECLARED, true) = %q, want warning", got)
	}
	// Family 2 -> Unknown.
	for _, code := range []Code{
		CodeEvidenceMissing, CodeObservationUnsupported, CodeCollectionFailed,
		CodeEvidenceStale, CodeEvidenceInsufficient,
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
// violation of one is a warning — but only for the codes that declare the axis.
// Anything else keeps its registry row whatever required says.
func TestSeverityFor_OptionalAssertion(t *testing.T) {
	for _, code := range []Code{CodeDependencyUnreachable, CodeConfigurationAbsent, CodeConfigurationMismatch, CodeDependencyVersionMismatch} {
		if got := SeverityFor(code, false); got != SeverityWarning {
			t.Errorf("SeverityFor(%q, optional) = %q, want warning", code, got)
		}
	}
	// No axis declared: an error-severity code with no optional row stays an error.
	for _, code := range []Code{CodeWorkloadMismatch, CodeInterfaceProtocolMismatch, CodeInterfaceVisibilityMismatch} {
		if got := SeverityFor(code, false); got != SeverityError {
			t.Errorf("SeverityFor(%q, optional) = %q, want error", code, got)
		}
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
	optional := map[Code]codeMeta{
		CodeConfigurationAbsent: {CategoryInconclusive, SeverityWarning},
	}
	categoryIn := func(c Code, required bool) Category { m, _ := metaIn(optional, c, required); return m.category }

	if got := categoryIn(CodeConfigurationAbsent, false); got != CategoryInconclusive {
		t.Errorf("CategoryFor(optional) = %q, want the optional row's %q", got, CategoryInconclusive)
	}
	if got := categoryIn(CodeConfigurationAbsent, true); got != CategoryRuntimeDrift {
		t.Errorf("CategoryFor(required) = %q, want the registry row's %q", got, CategoryRuntimeDrift)
	}
	// No optional row: the base row answers whatever required says.
	if got := categoryIn(CodeWorkloadMismatch, false); got != CategoryRuntimeDrift {
		t.Errorf("CategoryFor(WORKLOAD_MISMATCH, optional) = %q, want RuntimeDrift", got)
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

// A facet violation contradicts a declared value rather than reporting a missing
// one, so family 1 names it *_MISMATCH.
func TestFamily1FacetCodesAreMismatchNaming(t *testing.T) {
	for code, want := range map[Code]string{
		CodeInterfaceProtocolMismatch:   "INTERFACE_PROTOCOL_MISMATCH",
		CodeInterfaceVisibilityMismatch: "INTERFACE_VISIBILITY_MISMATCH",
		CodeDependencyVersionMismatch:   "DEPENDENCY_VERSION_MISMATCH",
	} {
		if string(code) != want || !strings.HasSuffix(want, "_MISMATCH") {
			t.Errorf("family-1 facet code %q must be the *_MISMATCH string %q", code, want)
		}
	}
	if CodeDependencyUndeclared != "DEPENDENCY_UNDECLARED" {
		t.Errorf("CodeDependencyUndeclared = %q", CodeDependencyUndeclared)
	}
}

func TestRegistry(t *testing.T) {
	rows := Registry()
	if len(rows) != len(registry)+len(optionalSeverity) {
		t.Fatalf("rows = %d, want %d", len(rows), len(registry)+len(optionalSeverity))
	}
	for i := 1; i < len(rows); i++ {
		a, b := rows[i-1], rows[i]
		if a.Code > b.Code || (a.Code == b.Code && a.Optional && !b.Optional) {
			t.Fatalf("not sorted at %d: %+v then %+v", i, a, b)
		}
	}
	for _, r := range rows {
		if r.Severity != SeverityFor(r.Code, !r.Optional) || r.Category != CategoryFor(r.Code, !r.Optional) {
			t.Errorf("row %+v disagrees with SeverityFor/CategoryFor", r)
		}
	}
	rows[0].Code = "MUTATED"
	if Registry()[0].Code == "MUTATED" {
		t.Error("Registry must return a copy")
	}
}
