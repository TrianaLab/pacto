package validation

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
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

// TestEvaluate_ExtensionCapability: an extension has no evaluator, so it is listed as unverified coverage
// rather than a required assertion nothing could satisfy, and a contract declaring one can reach Compliant.
func TestEvaluate_ExtensionCapability(t *testing.T) {
	c := contract.Contract{
		Service:      contract.Service{Name: "orders", Version: "1"},
		Capabilities: []contract.Capability{{Type: contract.CapabilityExtension, Ref: "example.com/custom"}},
	}
	fs, cov := Evaluate(c, es())
	if len(fs) != 0 {
		t.Fatalf("want no findings, got %v", fs)
	}
	if cov.Required != 0 || cov.Evaluated != 0 || !slices.Equal(cov.Unverified, []string{"capabilities[ref=example.com/custom]"}) {
		t.Errorf("extension coverage = %+v, want {0,0} with the ref unverified", cov)
	}
	if got := DeriveStatus(fs, cov); got != finding.StatusCompliant {
		t.Errorf("status = %s, want Compliant", got)
	}
}

func TestEvaluate_ExtensionIdentityByRef(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "orders", Version: "1"},
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityExtension, Ref: "acme.io/security-scan"},
			{Type: contract.CapabilityExtension, Ref: "acme.io/backup"},
		},
	}
	_, cov := Evaluate(c, es())
	want := []string{"capabilities[ref=acme.io/backup]", "capabilities[ref=acme.io/security-scan]"}
	if cov.Required != 0 || !slices.Equal(cov.Unverified, want) {
		t.Fatalf("two distinct extensions coverage = %+v, want each ref unverified, sorted", cov)
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

// The registry owns a finding's category, not the call site. Every code that
// reaches assert today happens to be RuntimeDrift, so a hardcoded literal
// there looks right until the first drift code that is not — at which point the
// finding carries a category the registry disagrees with. Driving assert
// with a non-RuntimeDrift code is how that stays caught.
func TestEvalAssertion_CategoryComesFromTheRegistry(t *testing.T) {
	set := es(evidence.NewCapabilityObserved(sr("capability", "health"), true, prov()))
	e := &evaluator{ev: set}

	e.assert(evidence.CapabilityObserved, "capability", "health", "capabilities[0]", true,
		func(evidence.Observation) (bool, finding.Code, string) {
			// Registry row: {Inconclusive, Unknown}, deliberately not RuntimeDrift.
			return true, finding.CodeEvidenceInsufficient, "contradicted"
		})

	findings := e.findings
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %v", findings)
	}
	if findings[0].Category != finding.CategoryInconclusive {
		t.Errorf("Category = %q, want the registry's %q", findings[0].Category, finding.CategoryInconclusive)
	}
}

func boolp(b bool) *bool { return &b }

func ifaceContract(typ, visibility string) contract.Contract {
	return contract.Contract{
		Service:    contract.Service{Name: "orders", Version: "1"},
		Interfaces: []contract.Interface{{Name: "api", Type: typ, Ref: "i", Visibility: visibility}},
	}
}

func ifaceObs(present bool, p evidence.Protocol, x evidence.Exposure) evidence.Observation {
	return evidence.NewInterfaceObservation(sr("interface", "api"),
		evidence.InterfaceObservation{Type: "openapi", Present: present, Protocol: p, Exposure: x}, prov())
}

// facetOutcome reduces one Evaluate run to what a facet table cares about: the codes emitted and whether
// the path was listed as unverified.
func facetOutcome(fs []finding.Finding, cov Coverage, path string) (codes []finding.Code, unverified bool) {
	for _, f := range fs {
		codes = append(codes, f.Code)
	}
	for _, p := range cov.Unverified {
		if p == path {
			unverified = true
		}
	}
	return codes, unverified
}

func TestEvaluate_InterfaceTypeFacet(t *testing.T) {
	const path = "interfaces[name=api].type"
	cases := []struct {
		typ        string
		p          evidence.Protocol
		violation  bool
		unverified bool
	}{
		{contract.InterfaceTypeOpenAPI, evidence.ProtocolHTTP1, false, false},
		{contract.InterfaceTypeOpenAPI, evidence.ProtocolHTTP2, false, false},
		{contract.InterfaceTypeOpenAPI, evidence.ProtocolOther, true, false},
		{contract.InterfaceTypeOpenAPI, evidence.ProtocolTLS, false, true},
		{contract.InterfaceTypeOpenAPI, "", false, true},
		{contract.InterfaceTypeGRPC, evidence.ProtocolHTTP2, false, false},
		{contract.InterfaceTypeGRPC, evidence.ProtocolHTTP1, true, false},
		{contract.InterfaceTypeGRPC, evidence.ProtocolOther, true, false},
		{contract.InterfaceTypeGRPC, evidence.ProtocolTLS, false, true},
		{contract.InterfaceTypeAsyncAPI, evidence.ProtocolHTTP1, false, true},
		{contract.InterfaceTypeAsyncAPI, evidence.ProtocolOther, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.typ+"/"+string(tc.p), func(t *testing.T) {
			fs, cov := Evaluate(ifaceContract(tc.typ, contract.VisibilityInternal), es(ifaceObs(true, tc.p, evidence.ExposureInternal)))
			codes, unv := facetOutcome(fs, cov, path)
			if got := hasCode(fs, finding.CodeInterfaceProtocolMismatch); got != tc.violation {
				t.Errorf("violation = %v, want %v (codes %v)", got, tc.violation, codes)
			}
			if unv != tc.unverified {
				t.Errorf("unverified = %v, want %v (%v)", unv, tc.unverified, cov.Unverified)
			}
			if cov.Required != 1 || cov.Evaluated != 1 {
				t.Errorf("facets must not touch Required/Evaluated: %+v", cov)
			}
		})
	}
	fs, _ := Evaluate(ifaceContract(contract.InterfaceTypeGRPC, ""), es(ifaceObs(true, evidence.ProtocolHTTP1, "")))
	for _, f := range fs {
		if f.Code == finding.CodeInterfaceProtocolMismatch {
			if f.Severity != finding.SeverityError || f.Category != finding.CategoryRuntimeDrift || f.ContractPath != path {
				t.Errorf("protocol mismatch finding = %+v", f)
			}
		}
	}
}

// The exposure vocabulary is the contract's visibility vocabulary, so the core names no platform.
func TestExposureMirrorsVisibility(t *testing.T) {
	if string(evidence.ExposureInternal) != contract.VisibilityInternal {
		t.Fatalf("ExposureInternal = %q, want contract.VisibilityInternal", evidence.ExposureInternal)
	}
	fs, _ := Evaluate(ifaceContract(contract.InterfaceTypeOpenAPI, contract.VisibilityInternal), es(ifaceObs(true, evidence.ProtocolHTTP1, evidence.ExposureExternal)))
	if !hasCode(fs, finding.CodeInterfaceVisibilityMismatch) || strings.Contains(fs[0].Message, "cluster") {
		t.Fatalf("visibility finding names a platform: %v", fs)
	}
}

func TestEvaluate_InterfaceVisibilityFacet(t *testing.T) {
	const path = "interfaces[name=api].visibility"
	cases := []struct {
		name       string
		visibility string
		x          evidence.Exposure
		violation  bool
		unverified bool
	}{
		{"internal-internal", contract.VisibilityInternal, evidence.ExposureInternal, false, false},
		{"omitted-internal", "", evidence.ExposureInternal, false, false},
		{"internal-external", contract.VisibilityInternal, evidence.ExposureExternal, true, false},
		{"omitted-external", "", evidence.ExposureExternal, true, false},
		{"public-external", contract.VisibilityPublic, evidence.ExposureExternal, false, false},
		{"public-internal", contract.VisibilityPublic, evidence.ExposureInternal, false, true},
		{"internal-unknown", contract.VisibilityInternal, "", false, true},
		{"public-unknown", contract.VisibilityPublic, "", false, true},
		{"unknown-visibility", "bogus", evidence.ExposureExternal, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, cov := Evaluate(ifaceContract(contract.InterfaceTypeOpenAPI, tc.visibility), es(ifaceObs(true, evidence.ProtocolHTTP1, tc.x)))
			codes, unv := facetOutcome(fs, cov, path)
			if got := hasCode(fs, finding.CodeInterfaceVisibilityMismatch); got != tc.violation {
				t.Errorf("violation = %v, want %v (codes %v)", got, tc.violation, codes)
			}
			if unv != tc.unverified {
				t.Errorf("unverified = %v, want %v (%v)", unv, tc.unverified, cov.Unverified)
			}
		})
	}
}

// Facets are only judged on an interface that is Observed and Present: an absent interface reports
// INTERFACE_ABSENT and nothing about a protocol nobody could have seen.
func TestEvaluate_InterfaceFacetsNeedPresentObservation(t *testing.T) {
	c := ifaceContract(contract.InterfaceTypeGRPC, contract.VisibilityInternal)
	fs, cov := Evaluate(c, es(ifaceObs(false, evidence.ProtocolHTTP1, evidence.ExposureExternal)))
	if len(fs) != 1 || fs[0].Code != finding.CodeInterfaceAbsent || len(cov.Unverified) != 0 {
		t.Fatalf("absent interface: fs=%v cov=%+v", fs, cov)
	}
	if want := `interface "api" is not being served`; fs[0].Message != want {
		t.Fatalf("absent message = %q, want the platform-neutral %q", fs[0].Message, want)
	}
	fs, cov = Evaluate(c, es(unobs(t, evidence.InterfaceObserved, "interface", "api", evidence.Stale)))
	if len(fs) != 1 || fs[0].Code != finding.CodeEvidenceStale || len(cov.Unverified) != 0 {
		t.Fatalf("stale interface: fs=%v cov=%+v", fs, cov)
	}
}

func depContract(required bool, compatibility string) contract.Contract {
	return contract.Contract{
		Service:      contract.Service{Name: "orders", Version: "1"},
		Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://p", Required: required, Compatibility: compatibility}},
	}
}

func depObs(reachable bool, version string, undeclared bool, name string) evidence.Observation {
	return evidence.NewDependencyObservation(sr("dependency", name),
		evidence.DependencyObservation{Reachable: reachable, Version: version, Undeclared: undeclared}, prov())
}

func TestEvaluate_DependencyCompatibilityFacet(t *testing.T) {
	const path = "dependencies[name=payments].compatibility"
	cases := []struct {
		name          string
		required      bool
		compatibility string
		version       string
		severity      finding.Severity // "" = no violation
		unverified    bool
	}{
		{"in-range", true, "^1.0.0", "1.4.2", "", false},
		{"out-of-range-required", true, "^1.0.0", "2.0.0", finding.SeverityError, false},
		{"out-of-range-optional", false, "^1.0.0", "2.0.0", finding.SeverityWarning, false},
		{"unknown-version", true, "^1.0.0", "", "", true},
		{"non-semver-version", true, "^1.0.0", "latest", "", true},
		{"unparseable-range", true, "not a range", "1.0.0", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, cov := Evaluate(depContract(tc.required, tc.compatibility), es(depObs(true, tc.version, false, "payments")))
			var got finding.Severity
			for _, f := range fs {
				if f.Code == finding.CodeDependencyVersionMismatch {
					got = f.Severity
					if f.ContractPath != path {
						t.Errorf("ContractPath = %q", f.ContractPath)
					}
				}
			}
			if got != tc.severity {
				t.Errorf("severity = %q, want %q (%v)", got, tc.severity, fs)
			}
			if _, unv := facetOutcome(fs, cov, path); unv != tc.unverified {
				t.Errorf("unverified = %v, want %v", unv, tc.unverified)
			}
		})
	}
	// An unreachable dependency is still Observed, so its version is still judged.
	fs, _ := Evaluate(depContract(true, "^1.0.0"), es(depObs(false, "2.0.0", false, "payments")))
	if !hasCode(fs, finding.CodeDependencyUnreachable) || !hasCode(fs, finding.CodeDependencyVersionMismatch) {
		t.Errorf("unreachable + out of range: %v", fs)
	}
	// No Observed observation: no facet verdict at all.
	fs, cov := Evaluate(depContract(false, "^1.0.0"), es(unobs(t, evidence.DependencyReachable, "dependency", "payments", evidence.Failed)))
	if len(fs) != 0 || len(cov.Unverified) != 0 {
		t.Errorf("optional failed dependency: fs=%v cov=%+v", fs, cov)
	}
}

func TestEvaluate_UndeclaredDependency(t *testing.T) {
	c := depContract(true, "^1.0.0")
	set := es(
		depObs(true, "1.0.0", false, "payments"),
		depObs(true, "", true, "ledger"),        // reported
		depObs(false, "", true, "audit"),        // not reachable: no claim
		depObs(true, "", false, "unmarked"),     // producer did not mark it undeclared
		depObs(true, "1.0.0", true, "payments"), // names a declared dependency: already judged
		unobs(t, evidence.DependencyReachable, "dependency", "ghost", evidence.Insufficient),
	)
	fs, cov := Evaluate(c, set)
	var got []finding.Finding
	for _, f := range fs {
		if f.Code == finding.CodeDependencyUndeclared {
			got = append(got, f)
		}
	}
	if len(got) != 1 || got[0].Subject.Name != "ledger" || got[0].Severity != finding.SeverityWarning ||
		got[0].Category != finding.CategoryRuntimeDrift || got[0].ContractPath != "dependencies" {
		t.Fatalf("undeclared findings = %+v", got)
	}
	if cov.Required != 1 || cov.Evaluated != 1 {
		t.Errorf("undeclared peers must not touch coverage: %+v", cov)
	}
	if DeriveStatus(fs, cov) == finding.StatusNonCompliant {
		t.Errorf("an undeclared peer alone must never make the contract non-compliant")
	}
}

func stateContract(scope, durability string) contract.Contract {
	return contract.Contract{
		Service: contract.Service{Name: "orders", Version: "1"},
		State:   &contract.State{Type: contract.StateStateful, Persistence: contract.Persistence{Scope: scope, Durability: durability}},
	}
}

func persObs(durable bool, shared *bool) evidence.Observation {
	return evidence.NewPersistenceObservation(sr("service", "orders"),
		evidence.PersistenceObservation{Durable: durable, Shared: shared}, prov())
}

func TestEvaluate_PersistenceScopeFacet(t *testing.T) {
	const path = "state.persistence.scope"
	cases := []struct {
		name       string
		scope      string
		shared     *bool
		violation  bool
		unverified bool
	}{
		{"local-not-shared", contract.ScopeLocal, boolp(false), false, false},
		{"local-shared", contract.ScopeLocal, boolp(true), true, false},
		{"shared-shared", contract.ScopeShared, boolp(true), false, false},
		{"shared-not-shared", contract.ScopeShared, boolp(false), false, true},
		{"local-unknown", contract.ScopeLocal, nil, false, true},
		{"bogus-scope", "bogus", boolp(true), false, true},
	}
	for _, durability := range []string{contract.DurabilityPersistent, contract.DurabilityEphemeral} {
		for _, tc := range cases {
			t.Run(durability+"/"+tc.name, func(t *testing.T) {
				fs, cov := Evaluate(stateContract(tc.scope, durability), es(persObs(true, tc.shared)))
				var scopeViolation bool
				for _, f := range fs {
					if f.Code == finding.CodePersistenceMismatch && f.ContractPath == path {
						scopeViolation = true
					}
				}
				if scopeViolation != tc.violation {
					t.Errorf("violation = %v, want %v (%v)", scopeViolation, tc.violation, fs)
				}
				if _, unv := facetOutcome(fs, cov, path); unv != tc.unverified {
					t.Errorf("unverified = %v, want %v", unv, tc.unverified)
				}
				wantReq := 0
				if durability == contract.DurabilityPersistent {
					wantReq = 1
				}
				if cov.Required != wantReq || cov.Evaluated != wantReq {
					t.Errorf("coverage = %+v, want %d/%d", cov, wantReq, wantReq)
				}
			})
		}
	}
}

// For ephemeral state Durable is never asserted and a missing observation is not a gap.
func TestEvaluate_EphemeralStateNeverAssertsDurability(t *testing.T) {
	c := stateContract(contract.ScopeLocal, contract.DurabilityEphemeral)
	fs, cov := Evaluate(c, es(persObs(false, boolp(false))))
	if len(fs) != 0 || cov.Required != 0 {
		t.Fatalf("ephemeral + not durable: fs=%v cov=%+v", fs, cov)
	}
	fs, cov = Evaluate(c, es())
	if len(fs) != 0 || cov.Required != 0 || len(cov.Unverified) != 0 {
		t.Fatalf("ephemeral + no observation: fs=%v cov=%+v", fs, cov)
	}
	fs, cov = Evaluate(c, es(unobs(t, evidence.PersistenceObserved, "service", "orders", evidence.Unsupported)))
	if len(fs) != 0 || cov.Required != 0 || len(cov.Unverified) != 0 {
		t.Fatalf("ephemeral + unsupported: fs=%v cov=%+v", fs, cov)
	}
	// Persistent durability contradicted and scope contradicted: two findings, one per path.
	fs, _ = Evaluate(stateContract(contract.ScopeLocal, contract.DurabilityPersistent), es(persObs(false, boolp(true))))
	if len(fs) != 2 {
		t.Fatalf("durability + scope contradiction: %v", fs)
	}
}

// Every finding derived from an observation carries that observation's provenance; a finding with no
// matched observation keeps the set-level ref only.
func TestEvaluate_PerObservationProvenance(t *testing.T) {
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	p := evidence.Provenance{Collector: "k8s-observer+ebpf", DetectedAt: at}
	iface := evidence.NewInterfaceObservation(sr("interface", "public-api"),
		evidence.InterfaceObservation{Type: "openapi", Present: true, Protocol: evidence.ProtocolOther}, p)
	stale, _ := evidence.NewUnobserved(evidence.CapabilityObserved, sr("capability", "health"), evidence.Stale,
		evidence.Provenance{Collector: "k8s-observer"})
	fs, _ := Evaluate(fullContract(), es(iface, stale))

	refs := map[finding.Code]finding.EvidenceRef{}
	for _, f := range fs {
		refs[f.Code] = f.EvidenceRefs[0]
	}
	if r := refs[finding.CodeInterfaceProtocolMismatch]; r.Collector != "k8s-observer+ebpf" || r.DetectedAt != "2026-10-17T12:00:00Z" || r.Source != "k8s" {
		t.Errorf("protocol mismatch ref = %+v", r)
	}
	if r := refs[finding.CodeEvidenceStale]; r.Collector != "k8s-observer" || r.DetectedAt != "" {
		t.Errorf("stale ref (zero DetectedAt) = %+v", r)
	}
	if r := refs[finding.CodeEvidenceMissing]; r.Collector != "" || r.DetectedAt != "" || r.Source != "k8s" {
		t.Errorf("missing ref = %+v", r)
	}
}

func TestEvaluate_UnverifiedSortedUnique(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "orders", Version: "1"},
		Interfaces: []contract.Interface{
			{Name: "b", Type: "openapi", Ref: "i"},
			{Name: "a", Type: "openapi", Ref: "i"},
		},
	}
	set := es(
		evidence.NewInterfaceObserved(sr("interface", "b"), "openapi", true, prov()),
		evidence.NewInterfaceObserved(sr("interface", "a"), "openapi", true, prov()),
	)
	_, cov := Evaluate(c, set)
	want := []string{"interfaces[name=a].type", "interfaces[name=a].visibility", "interfaces[name=b].type", "interfaces[name=b].visibility"}
	if strings.Join(cov.Unverified, ",") != strings.Join(want, ",") {
		t.Errorf("Unverified = %v, want %v", cov.Unverified, want)
	}
	// Nothing unverified: the key is off the wire.
	_, cov = Evaluate(contract.Contract{Service: c.Service}, es())
	if data, _ := json.Marshal(cov); string(data) != `{"evaluated":0,"required":0}` {
		t.Errorf("empty coverage JSON = %s", data)
	}
}
