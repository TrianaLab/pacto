package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// writePacto writes pacto.yaml into a new directory and returns it.
func writePacto(t *testing.T, pacto string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(pacto), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// assertPacto fails unless dir's pacto.yaml is still want.
func assertPacto(t *testing.T, dir, want string) {
	t.Helper()
	if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != want {
		t.Errorf("pacto.yaml changed:\n%s", raw)
	}
}

func TestEditRaisesThroughUpgrade(t *testing.T) {
	dir := writePacto(t, orders23)
	result, err := Edit(context.Background(), nil, EditOptions{}, EditInput{Path: dir, Domain: strPtr("payments")})
	if err != nil {
		t.Fatal(err)
	}
	if want := (contract.Upgraded{From: "2.3", To: "2.4"}).Change(); len(result.Changes) == 0 || result.Changes[0] != want {
		t.Errorf("changes %q, want first %q", result.Changes, want)
	}
	if c := readContract(t, dir); c.PactoVersion != "2.4" {
		t.Errorf("pactoVersion %q", c.PactoVersion)
	}
}

// ordersRefDefaults is a 2.3 contract whose required ref configuration db gets
// no values in prod. From 2.4 its defaults must pass the provider's schema,
// which requires size: raising it changes what it means.
const ordersRefDefaults = "pactoVersion: \"2.3\"\nservice: {name: orders, version: 1.0.0}\n" +
	"configurations:\n  - {name: db, ref: ../pg, required: true}\n" +
	"environments:\n  - {name: prod, file: environments/prod.yaml}\n"

func TestEditKeptRaiseFailsWithFinding(t *testing.T) {
	for name, tc := range map[string]struct {
		validate Validator
		code     string
	}{
		"resolving validator": {realValidator, "ENVIRONMENT_VALUES_MISSING"},
		"no validator":        {nil, "CONFIG_REF_UNRESOLVED"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := refRoot(t, ordersRefDefaults, map[string]string{"environments/prod.yaml": "{}\n"})
			_, err := Edit(context.Background(), tc.validate, EditOptions{}, EditInput{Path: dir, Lifecycle: &LifecycleInput{Deprecated: "2026-01-01"}})
			if err == nil || !strings.Contains(err.Error(), `raising pactoVersion to "2.4" for this edit would add `+tc.code) {
				t.Fatalf("err %v", err)
			}
			assertPacto(t, dir, ordersRefDefaults)
		})
	}
}

func TestEditUnverifiedRaiseFails(t *testing.T) {
	for name, tc := range map[string]struct{ pacto, code string }{
		"fails cross-field": {orders23 + "dependencies:\n" +
			"  - {name: a, ref: oci://r/a:1.0.0, compatibility: ^1.0.0, required: true}\n" +
			"  - {name: a, ref: oci://r/b:1.0.0, compatibility: ^1.0.0, required: true}\n", "DUPLICATE_DEPENDENCY_NAME"},
		"does not parse": {"pactoVersion: \"2.3\"\nservice: [orders]\n", "PARSE_ERROR"},
	} {
		t.Run(name, func(t *testing.T) {
			pacto := tc.pacto
			dir := writePacto(t, pacto)
			_, err := Edit(context.Background(), nil, EditOptions{}, EditInput{Path: dir, Domain: strPtr("payments")})
			// The error names what already fails, since an MCP client gets no report.
			if err == nil || !strings.Contains(err.Error(), validation.ErrUnverified.Error()) ||
				!strings.Contains(err.Error(), "(first: "+tc.code) || !strings.Contains(err.Error(), "the contract as it is cannot be raised") {
				t.Fatalf("err %v", err)
			}
			assertPacto(t, dir, pacto)
		})
	}
}

func TestEditAtOrPastMinDoesNotRaise(t *testing.T) {
	pacto := strings.Replace(orders23, `"2.3"`, `"`+contract.Current()+`"`, 1)
	dir := writePacto(t, pacto)
	result, err := Edit(context.Background(), nil, EditOptions{}, EditInput{Path: dir, Domain: strPtr("payments")})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`set domain to "payments"`}; !slices.Equal(result.Changes, want) {
		t.Errorf("changes %q, want %q", result.Changes, want)
	}
	if c := readContract(t, dir); c.PactoVersion != contract.Current() {
		t.Errorf("pactoVersion %q", c.PactoVersion)
	}
}

func TestEditMinVersionTable(t *testing.T) {
	s := strPtr("x")
	for _, tc := range []struct {
		name  string
		input EditInput
		want  string
	}{
		{"nothing", EditInput{Name: s}, ""},
		{"undeclared range", EditInput{AddDependencies: []DependencyInput{{Name: "a", Ref: "oci://r/a"}}}, "2.5"},
		{"explicit unknown", EditInput{AddDependencies: []DependencyInput{{Name: "a", Ref: "oci://r/a", Compatibility: contract.CompatibilityUnknown}}}, "2.5"},
		{"applier", EditInput{Applier: s}, "2.5"},
		{"applier clear", EditInput{Applier: strPtr("")}, ""},
		{"domain", EditInput{Domain: s}, "2.4"},
		{"offering", EditInput{Offering: s}, "2.4"},
		{"summary", EditInput{Summary: s}, "2.4"},
		{"lifecycle", EditInput{Lifecycle: &LifecycleInput{Deprecated: "2026-01-01"}}, "2.4"},
		{"lifecycle clear", EditInput{Lifecycle: &LifecycleInput{Clear: true}}, ""},
		{"template", EditInput{Template: &TemplateInput{}}, "2.4"},
		{"template clear", EditInput{Template: &TemplateInput{Clear: true}}, ""},
		{"ref selector", EditInput{AddConfigurationRefs: []ConfigurationRefInput{{Ref: "../pg", Configuration: "provisioning"}}}, "2.4"},
		{"ref values", EditInput{AddConfigurationRefs: []ConfigurationRefInput{{Ref: "../pg", Values: map[string]any{"size": "small"}}}}, "2.4"},
		{"ref only", EditInput{AddConfigurationRefs: []ConfigurationRefInput{{Ref: "../pg"}}}, ""},
		{"environment lifetime", EditInput{AddEnvironments: []EnvironmentInput{{Name: "pr", Lifetime: "temporary"}}}, "2.4"},
		{"environment expires", EditInput{AddEnvironments: []EnvironmentInput{{Name: "pr", Expires: "2026-11-20"}}}, "2.4"},
		{"environment owner", EditInput{AddEnvironments: []EnvironmentInput{{Name: "pr", Owner: &contract.Owner{Team: "perf"}}}}, "2.4"},
		{"environment", EditInput{AddEnvironments: []EnvironmentInput{{Name: "prod"}}}, "2.3"},
		{"external dependency", EditInput{AddDependencies: []DependencyInput{{Name: "model", External: true, Version: "v1"}}}, "2.1"},
		{"ranged dependency", EditInput{AddDependencies: []DependencyInput{{Name: "a", Ref: "oci://r/a", Compatibility: "^1.0.0"}}}, ""},
	} {
		if got := editMinVersion(tc.input); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCreateMinVersion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input CreateInput
		want  string
	}{
		{"plain", CreateInput{Name: "a"}, "2.0"},
		{"external", CreateInput{Dependencies: []DependencyInput{{Name: "m", External: true, Version: "v1"}}}, "2.1"},
		{"domain", CreateInput{Domain: "payments"}, "2.4"},
		{"undeclared range", CreateInput{Domain: "payments", Dependencies: []DependencyInput{{Name: "a", Ref: "oci://r/a"}}}, "2.5"},
	} {
		if got := createMinVersion(tc.input); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCheckSuggestsBehind(t *testing.T) {
	const msg = `pactoVersion "2.3" is behind`
	for name, errs := range map[string][]contract.ValidationError{
		"valid":   nil,
		"invalid": {{Code: "E", Message: "broken"}},
	} {
		t.Run(name, func(t *testing.T) {
			validate := func(context.Context, string) (validation.Report, error) {
				return validation.Report{ValidationResult: validation.ValidationResult{Errors: errs, Warnings: []contract.ValidationWarning{
					{Path: "pactoVersion", Code: string(finding.CodeContractBehind), Message: msg},
				}}}, nil
			}
			res, err := Check(context.Background(), validate, writePacto(t, orders23), nil)
			if err != nil {
				t.Fatal(err)
			}
			var got []Suggestion
			for _, s := range res.Suggestions {
				if s.Message == msg {
					got = append(got, s)
				}
			}
			if len(got) != 1 || got[0].ToolCall != nil {
				t.Errorf("suggestions %+v", res.Suggestions)
			}
		})
	}
}

// TestEditStagelessValidatorIsOneStage: a Validator that reports no stages is
// judged by its result, so its error still keeps the version.
func TestEditStagelessValidatorIsOneStage(t *testing.T) {
	failing := func(context.Context, string) (validation.Report, error) {
		return validation.Report{ValidationResult: validation.ValidationResult{Errors: []contract.ValidationError{{Code: "E"}}}}, nil
	}
	dir := refRoot(t, ordersRefDefaults, map[string]string{"environments/prod.yaml": "{}\n"})
	_, err := Edit(context.Background(), failing, EditOptions{}, EditInput{Path: dir, Domain: strPtr("payments")})
	if err == nil || !strings.Contains(err.Error(), validation.ErrUnverified.Error()) {
		t.Fatalf("err %v", err)
	}
	assertPacto(t, dir, ordersRefDefaults)
}

// TestEditStagingFailsWithoutRaise: an edit that raises nothing still fails
// when its own resolution cannot stage the bundle.
func TestEditStagingFailsWithoutRaise(t *testing.T) {
	dir := refRoot(t, orders24, nil)
	o := osFileOps()
	o.mkdirTemp = func(string, string) (string, error) { return "", errors.New("read-only") }
	input := EditInput{Path: dir, AddConfigurationRefs: []ConfigurationRefInput{{Name: "cache", Ref: "../pg", Values: map[string]any{"size": "small"}}}}
	if _, err := o.edit(context.Background(), realValidator, EditOptions{}, input); err == nil || !strings.Contains(err.Error(), "failed to stage the edited bundle") || strings.Contains(err.Error(), "raising") {
		t.Fatalf("err %v", err)
	}
	assertPacto(t, dir, orders24)
}
