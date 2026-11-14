package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// textOf runs print on a fresh command and returns what it wrote.
func textOf(t *testing.T, print func(*cobra.Command) error) string {
	t.Helper()
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&bytes.Buffer{})
	if err := print(cmd); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func wantLines(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("text lacks %q:\n%s", w, got)
		}
	}
}

func TestFleetEntitiesOfferingColumns(t *testing.T) {
	res := &fleet.EntityList{Count: 1, Total: 1, Entities: []fleet.EntityRef{{
		Kind: fleet.KindOffering, Key: "postgresql", Label: "postgresql", Owner: "2 owners claim it", Lifecycle: "deprecated",
		Availability: fleet.AvailabilityUnavailable, AvailabilityProvenance: "inferred", Consumers: 3, HiddenConsumers: 2, FailedRequests: 1, Targets: 4,
	}}}
	got := textOf(t, func(cmd *cobra.Command) error { return printFleetEntities(cmd, res, "text") })
	wantLines(t, got, "owners 2 owners claim it  lifecycle deprecated  availability unavailable (inferred)  consumers 3 (+2 you may not see)  failed 1  observed targets 4\n")
}

func TestFleetEntitiesOfferingTargetsUnknownText(t *testing.T) {
	res := &fleet.EntityList{Count: 1, Total: 1, Entities: []fleet.EntityRef{{Kind: fleet.KindOffering, Key: "pg", Label: "pg", TargetsUnknown: true}}}
	got := textOf(t, func(cmd *cobra.Command) error { return printFleetEntities(cmd, res, "text") })
	wantLines(t, got, "owners -  lifecycle -  availability - (-)  consumers 0  failed 0  observed targets unknown\n")
	if strings.Contains(got, "observed targets 0") {
		t.Errorf("a withheld target count reads 0:\n%s", got)
	}
}

func TestFleetEntitiesPhaseLine(t *testing.T) {
	res := &fleet.EntityList{Aggregate: fleet.EntityAggregate{
		OfferingPhases:   fleet.PhaseTally{Requested: 1, Provisioning: 2, Ready: 3, Failed: 4, Deleting: 5, Unknown: 6},
		FailedByOffering: []fleet.TypeCount{{Type: "a", Services: 3}, {Type: "b", Services: 1}}, FailedOfferingsBeyond: 2,
	}}
	got := textOf(t, func(cmd *cobra.Command) error { return printFleetEntities(cmd, res, "text") })
	wantLines(t, got, "Requests by phase: 1 requested, 2 provisioning, 3 ready, 4 failed, 5 deleting, 6 not reported\n",
		"Most failed: a 3, b 1, and 2 more offerings\n")
	res.Aggregate.FailedByOffering, res.Aggregate.FailedOfferingsBeyond = nil, 0
	if got := textOf(t, func(cmd *cobra.Command) error { return printFleetEntities(cmd, res, "text") }); strings.Contains(got, "Most failed") {
		t.Errorf("no failed offering still ranks:\n%s", got)
	}
	res.Aggregate.OfferingPhases = fleet.PhaseTally{}
	if got := textOf(t, func(cmd *cobra.Command) error { return printFleetEntities(cmd, res, "text") }); strings.Contains(got, "Requests by phase") {
		t.Errorf("no request still prints the phase line:\n%s", got)
	}
}

func TestFleetEntityOfferingAvailabilityText(t *testing.T) {
	prod := fleet.EnvironmentAvailability{Environment: "prod", State: fleet.AvailabilityAvailable, Provenance: fleet.ProvenanceObserved, Reason: "2 of 2 targets reporting", Partial: true}
	o := &fleet.OfferingDetailData{
		Availability:    fleet.Availability{State: fleet.AvailabilityAvailable, ByEnvironment: []fleet.EnvironmentAvailability{prod}, Total: 1},
		HiddenProviders: 2,
		Providers: fleet.OfferingProvidersPreview{Items: []fleet.OfferingProvider{{Service: fleet.EntityRef{Label: "pg"}, OfferingProvenance: "declared",
			Availability: fleet.Availability{State: fleet.AvailabilityAvailable, ByEnvironment: []fleet.EnvironmentAvailability{prod}, Total: 1}}}},
	}
	var buf bytes.Buffer
	printOfferingText(&buf, o)
	wantLines(t, buf.String(),
		"Availability: available\n  prod: available (observed, 2 of 2 targets reporting) [partial]\nProviders you may not see: 2\n",
		"  pg (declared)\n    availability: available\n      prod: available (observed, 2 of 2 targets reporting) [partial]\n")
}

func TestFleetEntityOfferingSuccessorText(t *testing.T) {
	rev := fleet.EntityRef{Label: "pg 17.0.0"}
	sup := fleet.EntityRef{Label: "pg 16.0.1"}
	o := &fleet.OfferingDetailData{
		Providers: fleet.OfferingProvidersPreview{Items: []fleet.OfferingProvider{
			{Service: fleet.EntityRef{Label: "pg"}, Successor: &fleet.SuccessorInfo{Ref: "oci://x/pg:17.0.0", Revision: &rev}, LatestSupported: &sup},
			{Service: fleet.EntityRef{Label: "ha"}, Successor: &fleet.SuccessorInfo{Ref: "oci://x/ha:2", Reason: "no revision matches it"}},
		}},
		Consumers: fleet.OfferingUsesPreview{Total: 1, Count: 1, Items: []fleet.OfferingUse{{Consumer: fleet.EntityRef{Label: "app"}, Provider: fleet.EntityRef{Label: "pg"},
			Offering: "pg", Via: "dependency", RangeStatus: fleet.RangeSatisfied, SuccessorRange: fleet.RangeUnsatisfied}}},
	}
	var buf bytes.Buffer
	printOfferingText(&buf, o)
	wantLines(t, buf.String(),
		"    successor: oci://x/pg:17.0.0 (pg 17.0.0)\n    latest supported: pg 16.0.1\n",
		"    successor: oci://x/ha:2 (unknown: no revision matches it)\n",
		"  app -> pg (pg) via dependency, range satisfied, successor range unsatisfied\n")
}

func TestFleetEntityObservedConsumersText(t *testing.T) {
	o := &fleet.OfferingDetailData{
		ObservedConsumers: fleet.ObservedConsumersPreview{Total: 3, Count: 2, Truncated: true, Items: []fleet.ObservedConsumer{
			{Consumer: &fleet.EntityRef{Label: "a"}, Stale: true}, {Consumer: &fleet.EntityRef{Label: "b"}},
		}},
		HiddenObservedConsumers: 4,
	}
	var buf bytes.Buffer
	printOfferingText(&buf, o)
	wantLines(t, buf.String(), "Also calling a provider (observed, no declaration): a (stale), b; 1 more\n", "Observed callers you may not see: 4\n")
}

func TestFleetVersionsSuccessorLine(t *testing.T) {
	rev := fleet.EntityRef{Label: "pg 17.0.0"}
	res := &fleet.OfferingVersionsResult{Providers: []fleet.OfferingProviderVersions{
		{Provider: fleet.EntityRef{Label: "pg"}, Versions: fleet.ProviderVersionsPreview{Range: "^16.0.0",
			Successor: &fleet.SuccessorInfo{Ref: "oci://x/pg:17.0.0", Revision: &rev}, SuccessorRange: fleet.RangeUnsatisfied}},
		{Provider: fleet.EntityRef{Label: "ha"}, Versions: fleet.ProviderVersionsPreview{Range: "^1",
			Successor: &fleet.SuccessorInfo{Ref: "oci://x/ha:2", Reason: "r"}, SuccessorRange: fleet.RangeUnknown}},
		{Provider: fleet.EntityRef{Label: "plain"}, Versions: fleet.ProviderVersionsPreview{Successor: &fleet.SuccessorInfo{Ref: "oci://x/p:2", Reason: "r"}}},
	}}
	got := textOf(t, func(cmd *cobra.Command) error { return printFleetVersions(cmd, res, "text") })
	wantLines(t, got,
		"  successor: oci://x/pg:17.0.0 (pg 17.0.0)\n  Successor pg 17.0.0: unsatisfied for ^16.0.0\n",
		"  Successor oci://x/ha:2: unknown for ^1\n",
		"plain\n  successor: oci://x/p:2 (unknown: r)\n")
	if strings.Count(got, "  Successor ") != 2 {
		t.Errorf("a provider with no range prints a successor range:\n%s", got)
	}
}

func TestImpactUnavailableOfferingText(t *testing.T) {
	asOf := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	res := &impact.Result{AsOf: asOf, Completeness: fleet.CompletenessComplete, Gate: impact.GateUnverified,
		Event: &impact.Event{Kind: impact.EventUnavailable, Offering: "postgresql"}, Providers: []string{"postgresql", "postgresql-ha"}}
	got := textOf(t, func(cmd *cobra.Command) error { return printImpactResult(cmd, res, "text") })
	wantLines(t, got, "Event: offering postgresql unavailable (providers postgresql, postgresql-ha)  asOf 2026-11-13T00:00:00Z  completeness complete\n")
	res.Providers = nil
	got = textOf(t, func(cmd *cobra.Command) error { return printImpactResult(cmd, res, "text") })
	wantLines(t, got, "Event: offering postgresql unavailable  asOf")
}
