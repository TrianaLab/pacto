package backstage

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// A --namespace scope whose namespaces were all read is no outage, and
// neither is a limitation that loses no record; an unreadable namespace, any
// other degraded source or an empty fleet is. A stale source keeps its records.
func TestServable(t *testing.T) {
	svc := map[fleet.ServiceKey]*fleet.ServiceRecord{"pay": {Key: "pay"}}
	k8s := func(st fleet.SourceStatus) fleet.SourceState {
		return fleet.SourceState{ID: "k8s", Kind: fleet.SourceKindKubernetes, Status: st}
	}
	scope := fleet.NamespaceScopeLimitation("k8s", []string{"payments"}, 0)
	derived := fleet.Limitation{Code: fleet.LimitationSourcePartial, Source: "k8s"}
	scopedWith := func(extra ...fleet.Limitation) fleet.FleetSnapshot {
		return fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial)},
			Limitations: append([]fleet.Limitation{scope, derived}, extra...),
		}
	}
	for _, tc := range []struct {
		name   string
		snap   fleet.FleetSnapshot
		reason string // "" when servable
	}{
		{"complete", fleet.FleetSnapshot{Completeness: fleet.CompletenessComplete}, ""},
		{"scoped, every namespace read", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial), {ID: "oci", Status: fleet.SourceAvailable}},
			Limitations: []fleet.Limitation{scope, derived},
		}, ""},
		{"scoped, an unresolved observed callee", scopedWith(
			fleet.Limitation{Code: fleet.LimitationObservedIdentityUnresolved, Source: "k8s"}), ""},
		{"scoped, an ambiguous revision link and a merge conflict", scopedWith(
			fleet.Limitation{Code: fleet.LimitationRevisionAmbiguous, Source: "k8s"},
			fleet.Limitation{Code: fleet.LimitationTargetFieldConflict, Source: "k8s"}), ""},
		{"stale", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{{ID: "old", Status: fleet.SourceStale}},
			Limitations: []fleet.Limitation{{Code: fleet.LimitationSourceStale, Source: "old"}},
		}, ""},
		{"scoped, a namespace unreadable", scopedWith(fleet.NamespaceUnreadableLimitation("k8s", "ledger", "forbidden")), "source k8s is partial"},
		{"scoped, a record invalid", scopedWith(fleet.Limitation{Code: fleet.LimitationSourceRecordInvalid, Source: "k8s"}), "source k8s is partial"},
		{"scoped, a code it does not know", scopedWith(fleet.Limitation{Code: "SOMETHING_NEW", Source: "k8s"}), "source k8s is partial"},
		{"scoped, another source unavailable", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial), {ID: "oci", Status: fleet.SourceUnavailable}},
			Limitations: []fleet.Limitation{scope, derived},
		}, "source oci is unavailable"},
		{"scoped, empty", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial,
			Sources:      []fleet.SourceState{k8s(fleet.SourcePartial)},
			Limitations:  []fleet.Limitation{scope, derived},
		}, "the fleet is empty"},
		{"every source available, empty", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessEmpty,
			Sources:      []fleet.SourceState{{ID: "oci", Status: fleet.SourceAvailable}},
		}, "the fleet is empty"},
		{"partial without scope", fleet.FleetSnapshot{
			Completeness: fleet.CompletenessPartial, Services: svc,
			Sources:     []fleet.SourceState{k8s(fleet.SourcePartial)},
			Limitations: []fleet.Limitation{derived},
		}, "source k8s is partial"},
		{"scoped, external catalog unavailable", scopedWith(
			fleet.Limitation{Code: fleet.LimitationExternalCatalogUnavailable, Source: "external-catalog"}), "source external-catalog is unavailable"},
	} {
		reason, ok := Servable(&tc.snap)
		if ok != (tc.reason == "") || !strings.Contains(reason, tc.reason) {
			t.Errorf("%s: Servable = %q %v, want %q", tc.name, reason, ok, tc.reason)
		}
	}
}
