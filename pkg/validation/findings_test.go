package validation

import (
	"testing"

	"github.com/trianalab/pacto/v4/pkg/finding"
)

func TestValidationResult_Findings(t *testing.T) {
	var r ValidationResult
	r.AddError("service.version", "INVALID_SEMVER", "\"1.x\" is not valid semver")
	r.AddWarning("dependencies[0].ref", "TAG_NOT_DIGEST", "use a digest")

	fs := r.Findings()
	if len(fs) != 2 {
		t.Fatalf("got %d findings, want 2", len(fs))
	}
	if fs[0].Severity != finding.SeverityError || fs[0].Category != finding.CategoryInvalidVersion {
		t.Errorf("error mapping wrong: %+v", fs[0])
	}
	if fs[1].Severity != finding.SeverityWarning || fs[1].Code != finding.CodeTagNotDigest {
		t.Errorf("warning mapping wrong: %+v", fs[1])
	}
	if fs[0].ContractPath != "service.version" {
		t.Errorf("path not carried: %q", fs[0].ContractPath)
	}
}

// TestEnvironmentFindings: an environment the stage could not verify stays a
// warning in Findings, and every code the stage reports is registered.
func TestEnvironmentFindings(t *testing.T) {
	st := EnvironmentStage(envContract("staging.yaml"), nil)
	fs := ValidationResult{Errors: st.Errors, Warnings: st.Warnings}.Findings()
	if len(fs) != 1 || fs[0].Severity != finding.SeverityWarning || fs[0].Category != finding.CategoryUnresolvedReference {
		t.Fatalf("got %+v", fs)
	}
	for _, code := range []string{"DUPLICATE_ENVIRONMENT_NAME", "ENVIRONMENT_UNKNOWN_CONFIGURATION", "ENVIRONMENT_VALUES_INVALID", "ENVIRONMENT_VALUES_MISSING"} {
		if f := toFinding("", code, ""); f.Category == "" || f.Severity != finding.SeverityError {
			t.Errorf("%s: %+v", code, f)
		}
	}
}
