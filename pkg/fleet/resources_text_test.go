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

// ResourceLines prints owner and availability (unknown when unset), a cut
// request list, each status and each consumption, as the CLI and TUI share it.
func TestResourceLines(t *testing.T) {
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	u := OfferingUse{
		RequestedTruncated: true,
		Requested:          []RequestedConfiguration{{Provenance: ProvenanceDeclared}},
		Provisioning: []ProvisioningStatus{
			{Environment: "production", Entry: "db", Phase: StatusUnknown, ReportedPhase: "Ready", Provider: "aws", Source: "s", AsOf: &at, Reason: "stale"},
			{Phase: "Ready", Source: "s", Verified: true},
		},
		Consumption: []ResourceConsumption{
			{State: consumptionNotReported},
			{Environment: "production", State: consumptionUnknown, Reason: "the usage report is not signed", Reported: &UsageFigures{Cost: &evidence.UsageCost{Amount: "1", Currency: "USD", Period: "P1M"}}},
			{State: consumptionUnknown, Reason: "disagree", Conflict: []string{"a", "b"}},
		},
	}
	want := []string{
		"owner unknown, availability unknown",
		"requested (declared): none",
		"requested values may be missing: the configurations list was cut",
		"production db  Unknown (reported Ready)  aws  unsigned  asOf 2026-11-01T10:00:00Z: stale",
		"-  Ready  signed",
		"usage: not reported by any provider; for live signals ask Mira (query_metric)",
		"usage production: unknown: the usage report is not signed (reported cost 1 USD/P1M, unsigned)",
		"usage: unknown: disagree (a, b)",
	}
	got := u.ResourceLines()
	if len(got) != len(want) {
		t.Fatalf("lines:\n%q\nwant\n%q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	u.ProviderOwner, u.ProviderAvailability = &OwnershipInfo{Owner: "infra"}, &Availability{State: "available"}
	if got := u.ResourceLines()[0]; got != "owner infra, availability available" {
		t.Errorf("owner line = %q", got)
	}
}
