package delivery

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// egressPacto is payments' Pacto at status format 4 with the egress and
// coverage the operator wrote.
func egressPacto(cov *EgressCoverageView, egress ...EgressView) PactoView {
	p := externalsPacto(complete())
	p.Status.StatusFormat = StatusFormatEgress
	p.Status.ObservedEgress = egress
	p.Status.ObservedEgressCoverage = cov
	return p
}

func egressCov(state, reason string, nodes, withAgent int32) *EgressCoverageView {
	return &EgressCoverageView{CallerCoverageView: CallerCoverageView{State: state, Reason: reason, RefreshSeconds: 300}, Nodes: nodes, NodesWithAgent: withAgent}
}

var stripeEgress = EgressView{Identity: "stripe", Class: "vendor-api", Host: "api.stripe.com", Port: 443, Source: "ebpf"}

func TestObservedEdgesIncludeEgress(t *testing.T) {
	legacyHost := EgressView{Identity: "host/corp-legacy.com", Class: "legacy", Host: "erp.corp-legacy.com", Port: 8443, Source: "ebpf"}
	got := ObservedEdges(egressPacto(egressCov("complete", "", 2, 2), stripeEgress, legacyHost))
	want := []fleet.ObservedEdge{{From: "payments", To: "api.stripe.com", Count: 1}, {From: "payments", To: "erp.corp-legacy.com", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges = %+v", got)
	}
}

// TestEgressStatusFormat3Unchanged: a status an operator before format 4
// wrote yields exactly the edges, uses and limitations it did before.
func TestEgressStatusFormat3Unchanged(t *testing.T) {
	reconciled := now
	p := externalsPacto(complete(), stripeEnv)
	p.Status.ObservedCallers = []ObservedCallerView{legacy}
	if got := ObservedEdges(p); len(got) != 1 || got[0].To != "payments" {
		t.Fatalf("edges = %+v", got)
	}
	if len(ExternalUses(p, &reconciled)) != 1 {
		t.Fatal("uses changed")
	}
	if lims := RuntimeLimitations(p, &reconciled, now); lims != nil {
		t.Fatalf("limitations = %+v", lims)
	}
	// A coverage on an older format is not read either.
	p.Status.ObservedEgressCoverage = egressCov("partial", "dns-unavailable", 2, 1)
	if EgressLimitations(p, &reconciled, now) != nil {
		t.Fatal("a format-3 status claims an egress coverage")
	}
	if _, ok := EgressCoverage(p, &reconciled, now); ok {
		t.Fatal("a format-3 status claims an egress coverage")
	}
}

func TestEgressLimitationsPartialAndStale(t *testing.T) {
	reconciled := now
	lims := EgressLimitations(egressPacto(egressCov("partial", "dns-unavailable", 2, 2)), &reconciled, now)
	unreported(t, lims, "partial, dns-unavailable", "cannot read DNS answers")
	if !strings.HasPrefix(lims[0].Message, "the external calls payments makes cannot be ruled out: ") {
		t.Errorf("message = %q", lims[0].Message)
	}
	old := now.Add(-11 * time.Minute)
	unreported(t, EgressLimitations(egressPacto(egressCov("complete", "", 2, 2)), &old, now), "unavailable, stale-status", "has not updated")
	unreported(t, EgressLimitations(egressPacto(egressCov("unavailable", "no-agent-data", 2, 0)), &reconciled, now), "unavailable, no-agent-data", "no node agent")
	// RuntimeLimitations carries it beside the caller and external ones.
	if got := RuntimeLimitations(egressPacto(egressCov("partial", "unnamed-public", 2, 2)), &reconciled, now); len(got) != 1 || !strings.Contains(got[0].Message, "unnamed-public") {
		t.Fatalf("runtime limitations = %+v", got)
	}
}

func TestEgressNilCoverageNoLimitation(t *testing.T) {
	reconciled := now
	if EgressLimitations(egressPacto(nil, stripeEgress), &reconciled, now) != nil {
		t.Fatal("no coverage is no claim")
	}
	if EgressLimitations(egressPacto(egressCov("complete", "", 2, 2), stripeEgress), &reconciled, now) != nil {
		t.Fatal("a fresh complete coverage adds no limitation")
	}
}

func TestCoverageReasonsCoverEgressReasons(t *testing.T) {
	for _, slug := range []string{"no-agent-data", "agent-data-unreadable", "pods-unreadable", "pactos-unreadable", "services-unreadable",
		"node-without-agent", "stale-node", "degraded-node", "collection-failed", "shared-netns", "no-running-pods",
		"dns-unavailable", "ambiguous-peers", "unnamed-public", "unnamed-private-peers", "egress-host-unclassified", "truncated",
		"gateway-log-format", "gateway-access-log-off", "gateway-log-unreadable", "gateway-log-truncated", "not-evaluated", "cilium-unreadable", "fqdn-pattern-unbounded", "cilium-selector-unevaluated"} {
		if coverageReasons[slug] == "" {
			t.Errorf("no sentence for %s", slug)
		}
	}
}

func TestEgressCoverage(t *testing.T) {
	reconciled := now
	st, ok := EgressCoverage(egressPacto(egressCov("partial", "node-without-agent", 4, 3)), &reconciled, now)
	if !ok || st.State != "partial" || st.Reason != "node-without-agent" || st.Nodes != 4 || st.NodesWithAgent != 3 || !strings.Contains(st.Why, "has no node agent") {
		t.Fatalf("state = %+v, %v", st, ok)
	}
	if st, _ := EgressCoverage(egressPacto(egressCov("complete", "", 4, 4)), nil, now); st.State != "unavailable" || st.Reason != "stale-status" {
		t.Fatalf("stale = %+v", st)
	}
	if _, ok := EgressCoverage(egressPacto(nil), &reconciled, now); ok {
		t.Fatal("no coverage is no claim")
	}
}

// TestSnapshotEgressPartialNoProviderRow: partial egress turns the external
// answer of the delivery snapshot partial, and the gate path carries no
// provider row (the kernel-egress row is built by the surfaces).
func TestSnapshotEgressPartialNoProviderRow(t *testing.T) {
	snap, err := Snapshot(context.Background(), []PactoView{egressPacto(egressCov("partial", "unnamed-public", 4, 3), stripeEgress)}, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Providers) != 0 {
		t.Fatalf("providers = %+v", snap.Providers)
	}
	v, err := fleet.NewQuery(snap).GetExternal("stripe")
	if err != nil || v.Meta.Completeness != fleet.CompletenessPartial {
		t.Fatalf("external = %+v, %v", v, err)
	}
}
