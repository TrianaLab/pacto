package validation

import (
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/finding"
)

// The ladder, driven by the producer whose output it ranks. Hand-built severities
// would pin the switch and nothing else; going through Evaluate pins the pairing of
// severity and coverage that the operator and evidence ingestion actually hand over.
func TestDeriveStatus_FromEvaluate(t *testing.T) {
	optionalDep := contract.Contract{
		Service:      contract.Service{Name: "orders", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://p", Compatibility: "^1.0.0"}},
	}
	unreachable := es(evidence.NewDependencyReachable(sr("dependency", "payments"), false, prov()))

	cases := []struct {
		name string
		c    contract.Contract
		ev   evidence.EvidenceSet
		want string
	}{
		{"every required assertion satisfied", fullContract(), satisfied(), finding.StatusCompliant},
		{"contradicted required assertion", fullContract(), unreachableDependency(), finding.StatusNonCompliant},
		{"required assertion never observed", fullContract(), es(), finding.StatusUnknown},
		// The required-ness downgrade, end to end: an optional dependency cannot make
		// the contract non-compliant, so a confirmed violation of one lands on Warning.
		{"contradicted optional assertion", optionalDep, unreachable, finding.StatusWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveStatus(Evaluate(tc.c, tc.ev)); got != tc.want {
				t.Errorf("DeriveStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

// unreachableDependency satisfies everything fullContract declares except the
// required dependency, so exactly one error-severity finding is produced.
func unreachableDependency() evidence.EvidenceSet {
	ev := satisfied()
	for i, o := range ev.Observations {
		if o.Kind == evidence.DependencyReachable {
			ev.Observations[i] = evidence.NewDependencyReachable(sr("dependency", "payments"), false, prov())
		}
	}
	return ev
}

// Uncertainty outranks a warning and a confirmed violation outranks both, on finding
// sets one Evaluate call really produces.
func TestDeriveStatus_Precedence(t *testing.T) {
	c := contract.Contract{
		Service:      contract.Service{Name: "orders", Version: "1.0.0"},
		Interfaces:   []contract.Interface{{Name: "public-api", Type: "openapi", Ref: "i.json"}},
		Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://p", Compatibility: "^1.0.0"}},
	}
	// Optional dependency contradicted (warning) + required interface unobserved (unknown).
	ev := es(evidence.NewDependencyReachable(sr("dependency", "payments"), false, prov()))
	if got := DeriveStatus(Evaluate(c, ev)); got != finding.StatusUnknown {
		t.Errorf("warning + unknown = %q, want %q", got, finding.StatusUnknown)
	}
	// Add a contradicted REQUIRED assertion on top: the confirmed violation wins.
	c.Configurations = []contract.Configuration{{Name: "app", Schema: "s.json", Required: true}}
	ev.Observations = append(ev.Observations,
		evidence.NewConfigurationPresent(sr("configuration", "app"), false, false, prov()))
	if got := DeriveStatus(Evaluate(c, ev)); got != finding.StatusNonCompliant {
		t.Errorf("warning + unknown + error = %q, want %q", got, finding.StatusNonCompliant)
	}
}

// A structural warning must move the status, or the operator reports Compliant for a
// contract `pacto validate` warns about. ValidationResult.Findings() is what the
// operator folds in, so derive from it rather than from a hand-built severity.
func TestDeriveStatus_FoldsStructuralFindings(t *testing.T) {
	var vr ValidationResult
	vr.AddWarning("policies[0].ref", "POLICY_REF_NOT_ENFORCED", "not enforced by local validation")
	if got := DeriveStatus(vr.Findings(), Coverage{Evaluated: 2, Required: 2}); got != finding.StatusWarning {
		t.Errorf("DeriveStatus with a structural warning = %q, want %q", got, finding.StatusWarning)
	}
	vr.AddError("service.version", "INVALID_SEMVER", `"1.x" is not valid semver`)
	if got := DeriveStatus(vr.Findings(), Coverage{Evaluated: 2, Required: 2}); got != finding.StatusNonCompliant {
		t.Errorf("DeriveStatus with a structural error = %q, want %q", got, finding.StatusNonCompliant)
	}
}

// Coverage that did not evaluate every required assertion is uncertainty on its own.
// Evaluate never hands over that shape — it emits an unknown finding in the same
// branch that charges coverage — but a caller assembling a record from two sources
// can, and the ladder must not read it as Compliant.
func TestDeriveStatus_UnpairedCoverageGapIsUnknown(t *testing.T) {
	if got := DeriveStatus(nil, Coverage{Evaluated: 1, Required: 3}); got != finding.StatusUnknown {
		t.Errorf("DeriveStatus with an unpaired coverage gap = %q, want %q", got, finding.StatusUnknown)
	}
	if got := DeriveStatus(nil, Coverage{Evaluated: 3, Required: 3}); got != finding.StatusCompliant {
		t.Errorf("DeriveStatus with full coverage and no findings = %q, want %q", got, finding.StatusCompliant)
	}
}

// Info is explanatory, not a defect: it must not move the status off Compliant.
func TestDeriveStatus_InfoIsNotAWarning(t *testing.T) {
	fs := []finding.Finding{{Code: finding.CodeTagNotDigest, Severity: finding.SeverityInfo}}
	if got := DeriveStatus(fs, Coverage{}); got != finding.StatusCompliant {
		t.Errorf("DeriveStatus with an info finding = %q, want %q", got, finding.StatusCompliant)
	}
}
