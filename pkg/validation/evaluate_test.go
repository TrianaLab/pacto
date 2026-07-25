package validation

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/evidence"
	"github.com/trianalab/pacto/v2/pkg/finding"
)

func prov() evidence.Provenance {
	return evidence.Provenance{Collector: "test", DetectedAt: time.Unix(1, 0)}
}
func sr(kind, name string) evidence.SubjectRef { return evidence.SubjectRef{Kind: kind, Name: name} }

func es(obs ...evidence.Observation) evidence.EvidenceSet {
	return evidence.EvidenceSet{
		Subject:      sr("service", "ns/orders"),
		ContractRef:  "oci://x",
		Source:       "k8s",
		ObservedAt:   time.Unix(1, 0),
		Observations: obs,
	}
}

func unobs(t *testing.T, k evidence.ObservationKind, subjKind, name string, oc evidence.Outcome) evidence.Observation {
	t.Helper()
	o, err := evidence.NewUnobserved(k, sr(subjKind, name), oc, prov())
	if err != nil {
		t.Fatalf("NewUnobserved: %v", err)
	}
	return o
}

// fullContract declares one required assertion of every dimension. Required total = 6.
func fullContract() contract.Contract {
	return contract.Contract{
		Service:        contract.Service{Name: "orders", Version: "1.0.0"},
		Interfaces:     []contract.Interface{{Name: "public-api", Type: "openapi", Ref: "i.json"}},
		Capabilities:   []contract.Capability{{Type: contract.CapabilityHealth}},
		Dependencies:   []contract.Dependency{{Name: "payments", Ref: "oci://p", Required: true, Compatibility: "^1.0.0"}},
		Configurations: []contract.Configuration{{Name: "app", Schema: "s.json", Required: true}},
		Workload:       contract.WorkloadService,
		State:          &contract.State{Persistence: contract.Persistence{Durability: contract.DurabilityPersistent}},
	}
}

func satisfied() evidence.EvidenceSet {
	return es(
		evidence.NewInterfaceObserved(sr("interface", "public-api"), "openapi", true, prov()),
		evidence.NewCapabilityObserved(sr("capability", "health"), true, prov()),
		evidence.NewDependencyReachable(sr("dependency", "payments"), true, prov()),
		evidence.NewConfigurationPresent(sr("configuration", "app"), true, true, prov()),
		evidence.NewWorkloadObserved(sr("service", "orders"), "service", prov()),
		evidence.NewPersistenceObserved(sr("service", "orders"), true, prov()),
	)
}

func countSeverity(fs []finding.Finding, s finding.Severity) int {
	n := 0
	for _, f := range fs {
		if f.Severity == s {
			n++
		}
	}
	return n
}

func hasCode(fs []finding.Finding, code finding.Code) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestEvaluate_AllSatisfied(t *testing.T) {
	fs, cov := Evaluate(fullContract(), satisfied())
	if len(fs) != 0 {
		t.Fatalf("expected no findings, got %v", fs)
	}
	if cov.Required != 6 || cov.Evaluated != 6 {
		t.Fatalf("coverage = %+v, want {6,6}", cov)
	}
}

func TestEvaluate_ContradictionPerDimension(t *testing.T) {
	cases := []struct {
		name string
		obs  evidence.Observation
		code finding.Code
	}{
		{"interface", evidence.NewInterfaceObserved(sr("interface", "public-api"), "openapi", false, prov()), finding.CodeInterfaceAbsent},
		{"capability", evidence.NewCapabilityObserved(sr("capability", "health"), false, prov()), finding.CodeCapabilityAbsent},
		{"dependency", evidence.NewDependencyReachable(sr("dependency", "payments"), false, prov()), finding.CodeDependencyUnreachable},
		{"configuration-absent", evidence.NewConfigurationPresent(sr("configuration", "app"), false, false, prov()), finding.CodeConfigurationAbsent},
		{"configuration-mismatch", evidence.NewConfigurationPresent(sr("configuration", "app"), true, false, prov()), finding.CodeConfigurationMismatch},
		{"workload", evidence.NewWorkloadObserved(sr("service", "orders"), "job", prov()), finding.CodeWorkloadMismatch},
		{"persistence", evidence.NewPersistenceObserved(sr("service", "orders"), false, prov()), finding.CodePersistenceMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Start from satisfied, replace the one dimension's observation with the contradicting one.
			set := satisfied()
			for i := range set.Observations {
				if set.Observations[i].Kind == tc.obs.Kind && set.Observations[i].Subject == tc.obs.Subject {
					set.Observations[i] = tc.obs
				}
			}
			fs, cov := Evaluate(fullContract(), set)
			if !hasCode(fs, tc.code) {
				t.Fatalf("want code %s, got %v", tc.code, fs)
			}
			if countSeverity(fs, finding.SeverityError) != 1 {
				t.Errorf("want exactly 1 Error, got %d", countSeverity(fs, finding.SeverityError))
			}
			// Observed-and-contradicted still counts as evaluated: no coverage gap.
			if cov.Required != 6 || cov.Evaluated != 6 {
				t.Errorf("coverage = %+v, want {6,6}", cov)
			}
		})
	}
}

func TestEvaluate_UnknownCodesAndMessages(t *testing.T) {
	// Drive the interface (required) through every non-Observed outcome + the missing case.
	base := func(ifaceObs *evidence.Observation) evidence.EvidenceSet {
		obs := []evidence.Observation{
			evidence.NewCapabilityObserved(sr("capability", "health"), true, prov()),
			evidence.NewDependencyReachable(sr("dependency", "payments"), true, prov()),
			evidence.NewConfigurationPresent(sr("configuration", "app"), true, true, prov()),
			evidence.NewWorkloadObserved(sr("service", "orders"), "service", prov()),
			evidence.NewPersistenceObserved(sr("service", "orders"), true, prov()),
		}
		if ifaceObs != nil {
			obs = append(obs, *ifaceObs)
		}
		return es(obs...)
	}
	uns := func(oc evidence.Outcome) *evidence.Observation {
		o := unobs(t, evidence.InterfaceObserved, "interface", "public-api", oc)
		return &o
	}
	cases := []struct {
		name  string
		iface *evidence.Observation
		code  finding.Code
	}{
		{"missing", nil, finding.CodeEvidenceMissing},
		{"unsupported", uns(evidence.Unsupported), finding.CodeObservationUnsupported},
		{"failed", uns(evidence.Failed), finding.CodeCollectionFailed},
		{"stale", uns(evidence.Stale), finding.CodeEvidenceStale},
		{"insufficient", uns(evidence.Insufficient), finding.CodeEvidenceInsufficient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, cov := Evaluate(fullContract(), base(tc.iface))
			if !hasCode(fs, tc.code) {
				t.Fatalf("want %s, got %v", tc.code, fs)
			}
			for _, f := range fs {
				if f.Code == tc.code {
					if f.Severity != finding.SeverityUnknown || f.Category != finding.CategoryInconclusive {
						t.Errorf("%s: want Unknown/Inconclusive, got %s/%s", tc.code, f.Severity, f.Category)
					}
					if f.Message == "" {
						t.Errorf("%s: empty message", tc.code)
					}
				}
			}
			// interface Required but not Evaluated -> gap of 1.
			if cov.Required != 6 || cov.Evaluated != 5 {
				t.Errorf("coverage = %+v, want {5,6}", cov)
			}
		})
	}
}

func TestEvaluate_OptionalDependency(t *testing.T) {
	c := contract.Contract{
		Service:      contract.Service{Name: "orders", Version: "1"},
		Dependencies: []contract.Dependency{{Name: "cache", Ref: "oci://c", Required: false, Compatibility: "^1"}},
	}
	// Missing optional -> no finding, no coverage.
	fs, cov := Evaluate(c, es())
	if len(fs) != 0 || cov.Required != 0 || cov.Evaluated != 0 {
		t.Fatalf("optional missing: fs=%v cov=%+v", fs, cov)
	}
	// Observed + satisfied -> no finding, no coverage.
	fs, cov = Evaluate(c, es(evidence.NewDependencyReachable(sr("dependency", "cache"), true, prov())))
	if len(fs) != 0 || cov.Required != 0 {
		t.Fatalf("optional satisfied: fs=%v cov=%+v", fs, cov)
	}
	// Observed + contradicted -> Warning (not Error), still no coverage.
	fs, cov = Evaluate(c, es(evidence.NewDependencyReachable(sr("dependency", "cache"), false, prov())))
	if countSeverity(fs, finding.SeverityWarning) != 1 || countSeverity(fs, finding.SeverityError) != 0 {
		t.Fatalf("optional contradicted: want 1 Warning 0 Error, got %v", fs)
	}
	if cov.Required != 0 {
		t.Errorf("optional must not touch coverage: %+v", cov)
	}
}

func TestEvaluate_ExtensionCapability(t *testing.T) {
	c := contract.Contract{
		Service:      contract.Service{Name: "orders", Version: "1"},
		Capabilities: []contract.Capability{{Type: contract.CapabilityExtension, Ref: "example.com/custom"}},
	}
	fs, cov := Evaluate(c, es())
	if !hasCode(fs, finding.CodeExtensionEvaluatorUnavailable) {
		t.Fatalf("want EXTENSION_EVALUATOR_UNAVAILABLE, got %v", fs)
	}
	if cov.Required != 1 || cov.Evaluated != 0 {
		t.Errorf("extension coverage = %+v, want {0,1}", cov)
	}
	if hasCode(fs, finding.CodeEvidenceMissing) {
		t.Error("extension must NOT route through EVIDENCE_MISSING")
	}
}

func TestEvaluate_ExtensionIdentityByRef(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "orders", Version: "1"},
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityExtension, Ref: "acme.io/backup"},
			{Type: contract.CapabilityExtension, Ref: "acme.io/security-scan"},
		},
	}
	fs, cov := Evaluate(c, es())
	if cov.Required != 2 || cov.Evaluated != 0 {
		t.Fatalf("two distinct extensions coverage = %+v, want {0,2}", cov)
	}
	subjects := map[string]bool{}
	for _, f := range fs {
		if f.Code == finding.CodeExtensionEvaluatorUnavailable {
			subjects[f.Subject.Name] = true
		}
	}
	if !subjects["acme.io/backup"] || !subjects["acme.io/security-scan"] {
		t.Errorf("extension findings must be keyed by ref, got %v", subjects)
	}
}

func TestEvaluate_ConformanceOptIn(t *testing.T) {
	c := contract.Contract{
		Service:      contract.Service{Name: "orders", Version: "1"},
		Interfaces:   []contract.Interface{{Name: "public-api", Type: "openapi", Ref: "i.json"}},
		Verification: &contract.Verification{Conformance: []string{"public-api"}},
	}
	// Interface available + conformance requested with no evaluator.
	fs, cov := Evaluate(c, es(evidence.NewInterfaceObserved(sr("interface", "public-api"), "openapi", true, prov())))
	if !hasCode(fs, finding.CodeExtensionEvaluatorUnavailable) {
		t.Fatalf("want conformance Unknown, got %v", fs)
	}
	// interface availability evaluated (1/1) + conformance Required++ only -> {1,2}.
	if cov.Required != 2 || cov.Evaluated != 1 {
		t.Errorf("conformance coverage = %+v, want {1,2}", cov)
	}
}

func TestEvaluate_CoverageBiconditional(t *testing.T) {
	// no Unknown -> Evaluated == Required
	fs, cov := Evaluate(fullContract(), satisfied())
	if (cov.Evaluated == cov.Required) != (countSeverity(fs, finding.SeverityUnknown) == 0) {
		t.Errorf("biconditional violated (satisfied): cov=%+v unknown=%d", cov, countSeverity(fs, finding.SeverityUnknown))
	}
	// one Unknown -> Evaluated < Required
	set := satisfied()
	set.Observations = set.Observations[1:] // drop the interface observation
	fs, cov = Evaluate(fullContract(), set)
	if (cov.Evaluated < cov.Required) != (countSeverity(fs, finding.SeverityUnknown) >= 1) {
		t.Errorf("biconditional violated (gap): cov=%+v unknown=%d", cov, countSeverity(fs, finding.SeverityUnknown))
	}
}

func TestEvaluate_FabricationGuard(t *testing.T) {
	// A Failed interface observation must map to Unknown (COLLECTION_FAILED), never a violation.
	set := satisfied()
	set.Observations[0] = unobs(t, evidence.InterfaceObserved, "interface", "public-api", evidence.Failed)
	fs, _ := Evaluate(fullContract(), set)
	if hasCode(fs, finding.CodeInterfaceAbsent) {
		t.Fatal("Failed observation must NOT produce a confirmed violation")
	}
	if !hasCode(fs, finding.CodeCollectionFailed) {
		t.Fatalf("Failed observation must map to COLLECTION_FAILED, got %v", fs)
	}
}

func TestEvaluate_ServiceScopedMatchByServiceName(t *testing.T) {
	c := contract.Contract{Service: contract.Service{Name: "orders", Version: "1"}, Workload: contract.WorkloadService}
	// Correct: subject name = c.Service.Name -> found + evaluated.
	fs, cov := Evaluate(c, es(evidence.NewWorkloadObserved(sr("service", "orders"), "service", prov())))
	if len(fs) != 0 || cov.Evaluated != 1 {
		t.Fatalf("service-scoped match: fs=%v cov=%+v", fs, cov)
	}
	// Wrong: subject name = k8s target (ns/orders) -> NOT found -> EVIDENCE_MISSING.
	fs, cov = Evaluate(c, es(evidence.NewWorkloadObserved(sr("service", "ns/orders"), "service", prov())))
	if !hasCode(fs, finding.CodeEvidenceMissing) || cov.Evaluated != 0 {
		t.Fatalf("k8s-target subject must NOT match c.Service.Name: fs=%v cov=%+v", fs, cov)
	}
}
