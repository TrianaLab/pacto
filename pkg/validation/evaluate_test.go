package validation

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/evidence"
	"github.com/trianalab/pacto/v2/pkg/finding"
)

func TestEvaluate_NoEvidenceNoFindings(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "payments", Version: "1.0.0"},
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityHealth},
		},
		Interfaces: []contract.Interface{
			{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"},
		},
		Dependencies: []contract.Dependency{
			{Name: "database", Ref: "oci://reg/db:v1", Required: true, Compatibility: "^1.0.0"},
		},
		Configurations: []contract.Configuration{
			{Name: "app", Schema: "configuration/schema.json"},
		},
		Workload: contract.WorkloadService,
		State: &contract.State{
			Type: contract.StateStateful,
			Persistence: contract.Persistence{
				Scope:      contract.ScopeShared,
				Durability: contract.DurabilityPersistent,
			},
		},
	}

	ev := evidence.EvidenceSet{
		Subject:      evidence.SubjectRef{Kind: "service", Name: "payments"},
		ContractRef:  "payments:1.0.0",
		Source:       "test",
		ObservedAt:   time.Now(),
		Observations: []evidence.Observation{},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 0 {
		t.Errorf("expected no findings when no observations exist, got %d", len(findings))
	}
}

func TestEvaluate_CapabilityNotObserved(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "payments", Version: "1.0.0"},
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityHealth},
		},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "payments"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "payments:1.0.0",
		Source:      "kubernetes",
		ObservedAt:  time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		Observations: []evidence.Observation{
			evidence.NewCapabilityObserved(subj, contract.CapabilityHealth, false, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Code != finding.CodeCapabilityNotObserved {
		t.Errorf("code = %q, want %q", f.Code, finding.CodeCapabilityNotObserved)
	}
	if f.Subject.Kind != "capability" || f.Subject.Name != contract.CapabilityHealth {
		t.Errorf("subject = %+v, want {capability, health}", f.Subject)
	}
	if len(f.EvidenceRefs) != 1 {
		t.Fatalf("expected 1 evidence ref, got %d", len(f.EvidenceRefs))
	}
	if f.EvidenceRefs[0].Source != "kubernetes" {
		t.Errorf("evidence source = %q, want kubernetes", f.EvidenceRefs[0].Source)
	}
	if f.EvidenceRefs[0].ObservedAt != "2026-07-24T12:00:00Z" {
		t.Errorf("evidence observedAt = %q", f.EvidenceRefs[0].ObservedAt)
	}
}

func TestEvaluate_InterfaceNotObserved(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "api-gateway", Version: "2.0.0"},
		Interfaces: []contract.Interface{
			{Name: "rest", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.json"},
		},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "api-gateway"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "api-gateway:2.0.0",
		Source:      "docker",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewInterfaceObserved(subj, "rest", contract.InterfaceTypeOpenAPI, false, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Code != finding.CodeInterfaceNotObserved {
		t.Errorf("code = %q, want %q", f.Code, finding.CodeInterfaceNotObserved)
	}
	if f.Subject.Name != "rest" {
		t.Errorf("subject name = %q, want rest", f.Subject.Name)
	}
}

func TestEvaluate_DependencyUnreachable(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "worker", Version: "1.5.0"},
		Dependencies: []contract.Dependency{
			{Name: "queue", Ref: "oci://reg/queue:v2", Required: true, Compatibility: "^2.0.0"},
		},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "worker"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "worker:1.5.0",
		Source:      "kubernetes",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewDependencyReachable(subj, "queue", false, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Code != finding.CodeDependencyUnreachable {
		t.Errorf("code = %q, want %q", f.Code, finding.CodeDependencyUnreachable)
	}
	if f.Subject.Name != "queue" {
		t.Errorf("subject name = %q, want queue", f.Subject.Name)
	}
}

func TestEvaluate_ConfigNotObserved(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "frontend", Version: "3.0.0"},
		Configurations: []contract.Configuration{
			{Name: "app", Schema: "configuration/schema.json"},
		},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "frontend"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "frontend:3.0.0",
		Source:      "ci",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewConfigurationPresent(subj, "app", false, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Code != finding.CodeConfigNotObserved {
		t.Errorf("code = %q, want %q", f.Code, finding.CodeConfigNotObserved)
	}
	if f.Subject.Name != "app" {
		t.Errorf("subject name = %q, want app", f.Subject.Name)
	}
}

func TestEvaluate_WorkloadMismatch(t *testing.T) {
	c := contract.Contract{
		Service:  contract.Service{Name: "batch-processor", Version: "1.0.0"},
		Workload: contract.WorkloadJob,
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "batch-processor"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "batch-processor:1.0.0",
		Source:      "kubernetes",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewWorkloadObserved(subj, contract.WorkloadService, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Code != finding.CodeWorkloadMismatch {
		t.Errorf("code = %q, want %q", f.Code, finding.CodeWorkloadMismatch)
	}
}

func TestEvaluate_PersistenceMismatch(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "storage-service", Version: "2.0.0"},
		State: &contract.State{
			Type: contract.StateStateful,
			Persistence: contract.Persistence{
				Scope:      contract.ScopeShared,
				Durability: contract.DurabilityPersistent,
			},
		},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "storage-service"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "storage-service:2.0.0",
		Source:      "kubernetes",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewPersistenceObserved(subj, false, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Code != finding.CodePersistenceMismatch {
		t.Errorf("code = %q, want %q", f.Code, finding.CodePersistenceMismatch)
	}
}

func TestEvaluate_MultipleFindings(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "multi-service", Version: "1.0.0"},
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityHealth},
			{Type: contract.CapabilityMetrics},
		},
		Dependencies: []contract.Dependency{
			{Name: "db", Ref: "oci://reg/db:v1", Required: true, Compatibility: "^1.0.0"},
		},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "multi-service"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "multi-service:1.0.0",
		Source:      "test",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewCapabilityObserved(subj, contract.CapabilityHealth, false, prov),
			evidence.NewCapabilityObserved(subj, contract.CapabilityMetrics, false, prov),
			evidence.NewDependencyReachable(subj, "db", false, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(findings))
	}

	codes := make(map[finding.Code]bool)
	for _, f := range findings {
		codes[f.Code] = true
	}
	if !codes[finding.CodeCapabilityNotObserved] {
		t.Errorf("missing capability finding")
	}
	if !codes[finding.CodeDependencyUnreachable] {
		t.Errorf("missing dependency finding")
	}
}

func TestEvaluate_MatchingEvidenceNoFindings(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "healthy-service", Version: "1.0.0"},
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityHealth},
		},
		Workload: contract.WorkloadService,
		State: &contract.State{
			Type: contract.StateStateful,
			Persistence: contract.Persistence{
				Scope:      contract.ScopeShared,
				Durability: contract.DurabilityPersistent,
			},
		},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "healthy-service"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "healthy-service:1.0.0",
		Source:      "test",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewCapabilityObserved(subj, contract.CapabilityHealth, true, prov),
			evidence.NewWorkloadObserved(subj, contract.WorkloadService, prov),
			evidence.NewPersistenceObserved(subj, true, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 0 {
		t.Errorf("expected no findings when evidence matches contract, got %d", len(findings))
	}
}

func TestEvaluate_EphemeralDurabilityNoFinding(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "cache", Version: "1.0.0"},
		State: &contract.State{
			Type: contract.StateStateless,
			Persistence: contract.Persistence{
				Scope:      contract.ScopeLocal,
				Durability: contract.DurabilityEphemeral,
			},
		},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "cache"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "cache:1.0.0",
		Source:      "test",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewPersistenceObserved(subj, false, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 0 {
		t.Errorf("expected no finding for ephemeral durability with non-durable storage, got %d", len(findings))
	}
}

func TestEvaluate_NoWorkloadInContractNoFinding(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "generic", Version: "1.0.0"},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "generic"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "generic:1.0.0",
		Source:      "test",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewWorkloadObserved(subj, contract.WorkloadService, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 0 {
		t.Errorf("expected no finding when contract has no workload declared, got %d", len(findings))
	}
}

func TestEvaluate_NoStateInContractNoFinding(t *testing.T) {
	c := contract.Contract{
		Service: contract.Service{Name: "stateless", Version: "1.0.0"},
	}

	prov := evidence.Provenance{Collector: "test", DetectedAt: time.Now()}
	subj := evidence.SubjectRef{Kind: "service", Name: "stateless"}

	ev := evidence.EvidenceSet{
		Subject:     subj,
		ContractRef: "stateless:1.0.0",
		Source:      "test",
		ObservedAt:  time.Now(),
		Observations: []evidence.Observation{
			evidence.NewPersistenceObserved(subj, false, prov),
		},
	}

	findings := Evaluate(c, ev)
	if len(findings) != 0 {
		t.Errorf("expected no finding when contract has no state declared, got %d", len(findings))
	}
}
