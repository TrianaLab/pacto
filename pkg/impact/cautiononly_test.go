package impact

import (
	"context"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// gateRank orders gates from the most permissive.
var gateRank = map[string]int{GateClear: 0, GateUnverified: 1, GateBlocked: 2}

// forgedSnapshot is the chain's declared api-gateway -> auth-service at now,
// plus what the OTLP receiver received: edges, caution-only.
func forgedSnapshot(t *testing.T, now time.Time, cautionOnly bool, edges ...fleet.ObservedEdge) *fleet.FleetSnapshot {
	t.Helper()
	auth := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
	gw := svcContract("api-gateway", "1.0.0", contract.Owner{Team: "gateway-team"},
		contract.Dependency{Name: "auth-service", Ref: "ghcr.io/x/auth-service:1.0.0", Required: true, Compatibility: "^1.0.0"})
	sources := []fleet.Source{fleet.NewMemorySource("defs", "memory", &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: mkBundle(auth)}, {Bundle: mkBundle(gw)}},
	})}
	if edges != nil {
		sources = append(sources, fleet.NewMemorySource("otlp-receiver", fleet.SourceKindOTLPReceiver, &fleet.Collection{
			Advisory: true, ObservedUndeclaredOnly: true, ObservedCautionOnly: cautionOnly, Observed: edges,
		}))
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// gatewayEdge is the declared api-gateway -> auth-service relationship.
func gatewayEdge(t *testing.T, snap *fleet.FleetSnapshot) fleet.Relationship {
	t.Helper()
	for _, r := range snap.Relationships {
		if r.Provenance == fleet.ProvenanceDeclared && r.FromService == "api-gateway" {
			return r
		}
	}
	t.Fatal("no declared api-gateway edge")
	return fleet.Relationship{}
}

func TestForgedReceiverEdgeOnlyAddsAnUncontractedCaller(t *testing.T) {
	now := evidenceNow
	forged := []fleet.ObservedEdge{
		{From: "attacker", To: "auth-service", Count: 9, FirstSeen: now, LastSeen: now},
		{From: "api-gateway", To: "auth-service", Count: 9, FirstSeen: now, LastSeen: now},
	}
	snap := forgedSnapshot(t, now, true, forged...)
	if r := gatewayEdge(t, snap); r.Reconciliation == fleet.ReconciliationMatched {
		t.Fatalf("forged declared pair matched: %+v", r)
	}
	if len(snap.UncontractedCallers) != 1 || snap.UncontractedCallers[0].Caller != "attacker" {
		t.Fatalf("uncontracted callers %+v", snap.UncontractedCallers)
	}
	res := analyzeAuth(snap, Options{IncludeObserved: true})
	for _, c := range res.Consumers {
		if c.Confidence == ConfidenceCorroborated || c.CompatibilityVerdict == CompatibilityCompatible {
			t.Fatalf("forged data graded %s %s/%s", c.Service, c.Confidence, c.CompatibilityVerdict)
		}
	}
	if !consumerNamed(t, res, "attacker").Uncontracted {
		t.Fatal("attacker is not an uncontracted caller")
	}
	clean := analyzeAuth(forgedSnapshot(t, now, true), Options{IncludeObserved: true})
	if gateRank[res.Gate] < gateRank[clean.Gate] {
		t.Fatalf("forged data loosened the gate: %s, without it %s", res.Gate, clean.Gate)
	}
	// The same edge from a source the cluster owner runs corroborates.
	if c := consumerNamed(t, analyzeAuth(forgedSnapshot(t, now, false, forged...), Options{IncludeObserved: true}), "api-gateway"); c.Confidence != ConfidenceCorroborated {
		t.Fatalf("trusted edge graded %s", c.Confidence)
	}
}

func TestReplayedPayloadIsNotCorroboration(t *testing.T) {
	for at := evidenceNow; !at.After(evidenceNow.Add(time.Hour)); at = at.Add(14 * time.Minute) {
		snap := forgedSnapshot(t, at, true, fleet.ObservedEdge{From: "api-gateway", To: "auth-service", Count: 1, FirstSeen: at, LastSeen: at})
		if r := gatewayEdge(t, snap); r.Reconciliation == fleet.ReconciliationMatched {
			t.Fatalf("at %s the replayed edge matched", at)
		}
		if c := consumerNamed(t, analyzeAuth(snap, Options{IncludeObserved: true}), "api-gateway"); c.Confidence == ConfidenceCorroborated {
			t.Fatalf("at %s the replayed edge corroborated", at)
		}
	}
}
