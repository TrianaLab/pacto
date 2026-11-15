package fleet

import (
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidence"
)

func TestRequestedText(t *testing.T) {
	r := RequestedConfiguration{Provenance: ProvenanceDeclared, Configuration: "db", Revision: &EntityRef{ParentService: "postgresql", Version: "16.1.0", Label: "postgresql 16.1.0"},
		Values: RuntimePreview{Items: []RuntimeFact{{Key: "size", Value: "medium"}}}}
	if got := r.Text(); got != "requested (declared): size=medium, provider postgresql@16.1.0, configuration db" {
		t.Errorf("versioned = %q", got)
	}
	r = RequestedConfiguration{Provenance: ProvenanceDeclared, Revision: &EntityRef{Label: "postgresql sha256:ab"}}
	if got := r.Text(); got != "requested (declared): none, provider postgresql sha256:ab" {
		t.Errorf("unversioned = %q", got)
	}
}

func TestRequestedTextNamesTheDeclaringRevision(t *testing.T) {
	r := RequestedConfiguration{Provenance: ProvenanceDeclared, Entry: "db", ConsumerRevision: &EntityRef{ParentService: "orders", Version: "1.4.0"}}
	if got := r.Text(); got != "requested (declared): none, declared by orders@1.4.0" {
		t.Errorf("text = %q", got)
	}
}

// Two entries in one environment print apart, and a report with no figures
// still says who reported it, when and about which revision.
func TestConsumptionTextNamesEntryAndProvenance(t *testing.T) {
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	rev := &EntityRef{ParentService: "orders", Version: "1.3.0"}
	signed := ResourceConsumption{Environment: "production", Entry: "database", State: consumptionReported, Producer: "adapter", Verified: true, AsOf: &at, Revision: rev,
		Figures: &UsageFigures{Metrics: []evidence.UsageMetric{{Name: "storage-used", Value: 31, Unit: "Gi"}}}}
	if got := signed.Text(); got != "usage production database: storage-used 31 Gi  adapter  signed  asOf 2026-11-01T10:00:00Z  about orders@1.3.0" {
		t.Errorf("signed = %q", got)
	}
	failed := ResourceConsumption{Environment: "production", Entry: "replica", State: consumptionUnknown, Reason: "the provider reported failed", Producer: "adapter", Verified: true, AsOf: &at}
	if got := failed.Text(); got != "usage production replica: unknown: the provider reported failed (adapter, signed, asOf 2026-11-01T10:00:00Z)" {
		t.Errorf("failed = %q", got)
	}
}
