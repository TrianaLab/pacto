package validation

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/finding"
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
