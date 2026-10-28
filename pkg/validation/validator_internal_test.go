package validation

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestValidateStructuralRaw_UnquotedTimestampFailsPattern is the fail-open guard:
// an unquoted timestamp in a pattern-constrained field must still be rejected.
// Truncating it to a bare date would make it match ^[a-z0-9-]+$ and turn a
// rejected contract into an accepted one.
func TestValidateStructuralRaw_UnquotedTimestampFailsPattern(t *testing.T) {
	raw := []byte(`pactoVersion: "2.0"
service:
  name: 2024-01-15T00:00:00Z
  version: "1.0.0"
workload: service
`)
	result := ValidateStructuralRaw(raw)
	if result.IsValid() {
		t.Fatal("expected service.name to be rejected, got a valid result")
	}
	found := false
	for _, e := range result.Errors {
		if e.Code == "SCHEMA_VIOLATION" && e.Path == "service.name" && strings.Contains(e.Message, "2024-01-15T00:00:00Z") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a SCHEMA_VIOLATION on service.name naming the literal scalar, got %+v", result.Errors)
	}
}

func TestValidate_InvalidYAMLBytes(t *testing.T) {
	c := &contract.Contract{} // dummy contract, won't be used
	result := Validate(c, []byte(`{{{invalid yaml`), nil)
	if result.IsValid() {
		t.Error("expected error for invalid YAML bytes")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
	if result.Errors[0].Code != "YAML_PARSE_ERROR" {
		t.Errorf("expected YAML_PARSE_ERROR, got %s", result.Errors[0].Code)
	}
}

func TestValidateStructural_InvalidData(t *testing.T) {
	data := map[string]any{
		"pactoVersion": "2.0",
	}
	result := ValidateStructural(data)
	if result.IsValid() {
		t.Error("expected structural validation to fail for incomplete data")
	}
}
