package validation

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// validateByName validates a contract at version whose only dependency is the
// YAML mapping dep (indented as one list item).
func validateByName(t *testing.T, version, dep string) ValidationResult {
	t.Helper()
	raw := "pactoVersion: \"" + version + "\"\nservice:\n  name: reports\n  version: 1.0.0\ndependencies:\n  - " + dep + "\n"
	c, err := contract.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return Validate(c, []byte(raw), nil)
}

func errorCodes(r ValidationResult) []string {
	var out []string
	for _, e := range r.Errors {
		out = append(out, e.Path+" "+e.Code)
	}
	return out
}

// TestRefLessDependencyValidIn25: a 2.5 dependency may name its provider with
// no ref, with or without a range.
func TestRefLessDependencyValidIn25(t *testing.T) {
	for _, dep := range []string{"name: ledger", "{name: ledger, compatibility: ^2.0.0}", "{name: ledger, compatibility: unknown}"} {
		if r := validateByName(t, "2.5", dep); !r.IsValid() {
			t.Errorf("%s: %v", dep, errorCodes(r))
		}
	}
}

// TestRefLessDependencyWithVersionRefused: a version on a dependency by name
// is refused, since Pacto reads the version from the fleet.
func TestRefLessDependencyWithVersionRefused(t *testing.T) {
	got := errorCodes(validateByName(t, "2.5", "{name: ledger, version: \"1\"}"))
	if len(got) != 1 || got[0] != "dependencies[0].version VERSION_WITHOUT_REF" {
		t.Fatalf("errors = %v", got)
	}
}

// TestRefLessDependencyNeeds25: 2.1 to 2.4 say a dependency by name needs
// 2.5; 2.0 keeps the schema's required-property error and runs no cross-field
// stage.
func TestRefLessDependencyNeeds25(t *testing.T) {
	for _, v := range []string{"2.4", "2.1"} {
		got := errorCodes(validateByName(t, v, "{name: ledger, required: true}"))
		if len(got) != 1 || got[0] != "dependencies[0].ref NEEDS_PACTO_2_5" {
			t.Errorf("%s: errors = %v", v, got)
		}
	}
	r := validateByName(t, "2.0", "{name: ledger, required: true}")
	if len(r.Errors) == 0 || r.Errors[0].Code != "SCHEMA_VIOLATION" || !strings.Contains(r.Errors[0].Message, "ref") {
		t.Fatalf("2.0 errors = %+v", r.Errors)
	}
	for _, e := range r.Errors {
		if e.Code == "NEEDS_PACTO_2_5" || e.Code == "DEPENDENCY_REF_REQUIRED" {
			t.Fatalf("2.0 ran the cross-field stage: %+v", r.Errors)
		}
	}
	// With a version the older rule stands.
	if got := errorCodes(validateByName(t, "2.4", "{name: ledger, required: true, version: \"1\"}")); len(got) != 1 || got[0] != "dependencies[0].ref DEPENDENCY_REF_REQUIRED" {
		t.Fatalf("2.4 with version: %v", got)
	}
}

// TestRefLessExternalStillNeedsVersion: an external dependency keeps its rule.
func TestRefLessExternalStillNeedsVersion(t *testing.T) {
	got := errorCodes(validateByName(t, "2.5", "{name: model, external: true}"))
	if len(got) != 1 || got[0] != "dependencies[0].version EXTERNAL_VERSION_REQUIRED" {
		t.Fatalf("errors = %v", got)
	}
}

// TestRefLessDependencyInvalidRange: a range that does not parse is an error.
func TestRefLessDependencyInvalidRange(t *testing.T) {
	got := errorCodes(validateByName(t, "2.5", "{name: ledger, compatibility: not a range}"))
	if len(got) != 1 || got[0] != "dependencies[0].compatibility INVALID_COMPATIBILITY" {
		t.Fatalf("errors = %v", got)
	}
}
