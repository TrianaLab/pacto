package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/trianalab/pacto/v2/pkg/evidence"
)

func TestStaticCollector(t *testing.T) {
	subject := evidence.SubjectRef{Kind: "Service", Name: "test-service"}
	prov := evidence.Provenance{Collector: "static", DetectedAt: time.Now()}

	obs := []evidence.Observation{
		evidence.NewCapabilityObserved(evidence.SubjectRef{Kind: "capability", Name: "health"}, true, prov),
		evidence.NewWorkloadObserved(evidence.SubjectRef{Kind: "service", Name: "test-service"}, "service", prov),
	}

	collector := NewStaticCollector("test-contract@1.0.0", "static", obs)

	ctx := context.Background()
	result, err := collector.Collect(ctx, subject)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if result.Subject != subject {
		t.Errorf("Subject = %v, want %v", result.Subject, subject)
	}
	if result.ContractRef != "test-contract@1.0.0" {
		t.Errorf("ContractRef = %s, want test-contract@1.0.0", result.ContractRef)
	}
	if result.Source != "static" {
		t.Errorf("Source = %s, want static", result.Source)
	}
	if result.ObservedAt.IsZero() {
		t.Error("ObservedAt is zero")
	}
	if len(result.Observations) != 2 {
		t.Fatalf("len(Observations) = %d, want 2", len(result.Observations))
	}
}

func TestStaticCollector_ValidateEvidenceSet(t *testing.T) {
	subject := evidence.SubjectRef{Kind: "Service", Name: "test-service"}
	prov := evidence.Provenance{Collector: "static", DetectedAt: time.Now()}

	obs := []evidence.Observation{
		evidence.NewCapabilityObserved(evidence.SubjectRef{Kind: "capability", Name: "health"}, true, prov),
		evidence.NewWorkloadObserved(evidence.SubjectRef{Kind: "service", Name: "test-service"}, "service", prov),
		evidence.NewInterfaceObserved(evidence.SubjectRef{Kind: "interface", Name: "public-api"}, "openapi", true, prov),
		evidence.NewDependencyReachable(evidence.SubjectRef{Kind: "dependency", Name: "db"}, true, prov),
		evidence.NewConfigurationPresent(evidence.SubjectRef{Kind: "configuration", Name: "port"}, true, prov),
		evidence.NewPersistenceObserved(evidence.SubjectRef{Kind: "service", Name: "test-service"}, true, prov),
	}

	collector := NewStaticCollector("test-contract@1.0.0", "static", obs)

	ctx := context.Background()
	result, err := collector.Collect(ctx, subject)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	errs := evidence.ValidateEvidenceSet(result)
	if len(errs) > 0 {
		t.Errorf("ValidateEvidenceSet returned errors: %v", errs)
	}
}

func TestStaticCollector_JSONRoundTrip(t *testing.T) {
	subject := evidence.SubjectRef{Kind: "Service", Name: "test-service"}
	prov := evidence.Provenance{Collector: "static", DetectedAt: time.Now().Truncate(time.Second)}

	obs := []evidence.Observation{
		evidence.NewCapabilityObserved(evidence.SubjectRef{Kind: "capability", Name: "health"}, true, prov),
		evidence.NewWorkloadObserved(evidence.SubjectRef{Kind: "service", Name: "test-service"}, "service", prov),
	}

	collector := NewStaticCollector("test-contract@1.0.0", "static", obs)

	ctx := context.Background()
	original, err := collector.Collect(ctx, subject)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	// Reset ObservedAt to a fixed time for comparison
	fixedTime := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	original.ObservedAt = fixedTime

	// Marshal to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal from JSON
	var roundtrip evidence.EvidenceSet
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Validate round-tripped set
	errs := evidence.ValidateEvidenceSet(roundtrip)
	if len(errs) > 0 {
		t.Errorf("ValidateEvidenceSet after round-trip returned errors: %v", errs)
	}

	// Check key fields
	if roundtrip.Subject != original.Subject {
		t.Errorf("Subject mismatch after round-trip")
	}
	if roundtrip.ContractRef != original.ContractRef {
		t.Errorf("ContractRef mismatch after round-trip")
	}
	if len(roundtrip.Observations) != len(original.Observations) {
		t.Errorf("Observation count mismatch after round-trip")
	}
}
