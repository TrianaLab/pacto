package fleet

import (
	"strings"
	"testing"
)

// registryListingSnap is a cluster with services in the namespaces payments
// and team-a, and a partial registry listing of the prefix reg.example/payments.
func registryListingSnap(t *testing.T) *FleetSnapshot {
	t.Helper()
	return buildClusterSnap(t,
		NewMemorySource("k8s", SourceKindKubernetes, &Collection{Targets: []RawTarget{workload("payments", "ledger"), workload("team-a", "orders")}}),
		NewMemorySource("registry", SourceKindRegistryListing, &Collection{
			State: &SourceState{Status: SourcePartial},
			Limitations: []Limitation{
				{Code: LimitationRegistryListingIndexOnly, Source: "registry", Message: "reg.example serves no _catalog; listed from reg.example/payments/pacto-index"},
				{Code: LimitationRegistryListingEntryUnreadable, Source: "registry", Message: "3 bundles listed under reg.example/payments could not be read or do not match their repository; they are left out"},
			},
		}),
	)
}

// TestRegistryListingNeverDegradesConsumers: a listing that reads partial
// (a registry with no _catalog) leaves the snapshot partial but never an
// impact or gate answer, since templates and offerings are never consumers.
func TestRegistryListingNeverDegradesConsumers(t *testing.T) {
	snap := registryListingSnap(t)
	if snap.Completeness != CompletenessPartial || snap.ConsumerCompleteness() != CompletenessComplete {
		t.Fatalf("completeness %s / consumer %s", snap.Completeness, snap.ConsumerCompleteness())
	}
}

// TestRestrictKeepsRegistryListingGapsCountFree: a hidden namespace named
// like the prefix's org never drops the listing gap, and a restricted view
// never counts the unreadable bundles, which can be hidden services.
func TestRestrictKeepsRegistryListingGapsCountFree(t *testing.T) {
	s := NewQuery(registryListingSnap(t)).Restrict(anyCluster(func(ns string) bool { return ns == "team-a" }), anyClusterKind(nil)).SnapshotForSerialization()
	if l := limitationsWith(s, LimitationRegistryListingIndexOnly); len(l) != 1 {
		t.Errorf("INDEX_ONLY dropped by the hidden namespace scrub: %+v", s.Limitations)
	}
	l := limitationsWith(s, LimitationRegistryListingEntryUnreadable)
	if len(l) != 1 || strings.ContainsAny(l[0].Message, "0123456789") {
		t.Errorf("ENTRY_UNREADABLE must stay, with no count: %+v", l)
	}
}
