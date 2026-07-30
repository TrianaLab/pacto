//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
)

// This file builds the in-memory operational graph (fleet) and impact provider
// the WASM demo wires into the dashboard, entirely from the embedded bundles plus
// deterministic operational targets — so the Operational Graph and Impact pages
// are useful the moment the demo opens, with no server and no OCI registry. It
// carries no build tag so the host build compiles and TESTS it (source_embed_test).

// demoRegistry is the synthetic registry the demo's revision refs live under. The
// refs are immutable digest refs so they satisfy the same identity rules the real
// product uses; the impact provider resolves them from memory, never over a
// network.
const demoRegistry = "oci://demo.pacto.local/"

// demoNow is a fixed clock so the embedded snapshot (and its stale-target
// computation) is fully deterministic across builds.
func demoNow() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }

// buildDemoFleet builds a fleet query over every embedded revision plus the
// demo's operational targets, and returns a ref→bundle index so the impact
// provider can resolve the two revisions to compare from memory. A second,
// deliberately-unavailable source models a degraded environment so the
// completeness/partial semantics are exercised in the demo.
func buildDemoFleet(src *EmbedSource) (*fleet.FleetSnapshot, map[string]*contract.Bundle, error) {
	col := &fleet.Collection{}
	byRef := map[string]*contract.Bundle{}
	for _, name := range src.names {
		for ver, entry := range src.byName[name] {
			ref := demoRegistry + name + "@sha256:" + entry.hash
			col.Revisions = append(col.Revisions, fleet.RawRevision{
				Bundle:       entry.bundle,
				RequestedRef: demoRegistry + name + ":" + ver,
				ResolvedRef:  ref,
				Digest:       "sha256:" + entry.hash,
			})
			byRef[ref] = entry.bundle
		}
	}
	col.Targets = demoTargets()

	partial := fleet.NewMemorySource("edge-cluster", "k8s", &fleet.Collection{
		State: &fleet.SourceState{
			Status: fleet.SourceUnavailable,
			Error:  &fleet.SourceError{Code: fleet.LimitationSourceUnavailable, Message: "edge cluster unreachable"},
		},
	})
	snap, err := fleet.Build(context.Background(),
		fleet.BuildOptions{Now: demoNow, FreshnessWindow: 24 * time.Hour},
		fleet.NewMemorySource("local", "local", col), partial)
	if err != nil {
		return nil, nil, err
	}
	return snap, byRef, nil
}

// demoImpactProvider returns the impact provider the demo wires into the
// dashboard. It analyzes against the SAME published snapshot the Operational
// Graph serves (so snapshotIds match, §2.2), resolving the two revisions from the
// in-memory ref index, and carries embedded observed edges so include-observed is
// a real capability in the demo (surfacing a shadow consumer), not a placebo.
func demoImpactProvider(snap *fleet.FleetSnapshot, byRef map[string]*contract.Bundle) func(context.Context, string, string, bool) (*impact.Result, error) {
	return func(ctx context.Context, oldRef, newRef string, includeObserved bool) (*impact.Result, error) {
		oldB, newB := byRef[oldRef], byRef[newRef]
		if oldB == nil || newB == nil {
			return nil, fmt.Errorf("unknown revision reference (old=%q new=%q)", oldRef, newRef)
		}
		return impact.Analyze(ctx, oldB.Contract, newB.Contract, oldB.FS, newB.FS, snap, impact.Options{
			IncludeObserved: includeObserved,
			ObservedEdges:   demoObservedEdges(),
		}), nil
	}
}

// demoTargets are deterministic operational targets covering the full range of
// states the dashboard renders: compliant, non-compliant (with a finding),
// unknown (incomplete coverage), and a stale target (evidence older than the
// freshness window), across more than one scope.
func demoTargets() []fleet.RawTarget {
	fresh := demoNow().Add(-1 * time.Hour)
	stale := demoNow().Add(-72 * time.Hour) // older than the 24h freshness window
	cov := func(e, r int) *fleet.Coverage { return &fleet.Coverage{Evaluated: e, Required: r} }
	at := func(t time.Time) *time.Time { return &t }
	return []fleet.RawTarget{
		{Scope: "prod", Kind: "k8s", Name: "payments-service", Service: "payments-service", Compliance: fleet.StatusCompliant, Coverage: cov(5, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh)},
		{Scope: "prod", Kind: "k8s", Name: "orders-service", Service: "orders-service", Compliance: fleet.StatusNonCompliant, Coverage: cov(4, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Findings: []finding.Finding{{Code: "CONTRACT_VIOLATION", Severity: finding.SeverityError, Message: "response schema drifted from the declared contract"}}},
		{Scope: "prod", Kind: "k8s", Name: "auth-service", Service: "auth-service", Compliance: fleet.StatusUnknown, Coverage: cov(1, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh)},
		{Scope: "staging", Kind: "k8s", Name: "fraud-service", Service: "fraud-service", Compliance: fleet.StatusCompliant, Coverage: cov(5, 5), EvidenceAt: at(stale), ReconciledAt: at(stale)},
		{Scope: "prod", Kind: "k8s", Name: "frontend", Service: "frontend", Compliance: fleet.StatusCompliant, Coverage: cov(3, 3), EvidenceAt: at(fresh), ReconciledAt: at(fresh)},
		{Scope: "prod", Kind: "k8s", Name: "api-gateway", Service: "api-gateway", Compliance: fleet.StatusCompliant, Coverage: cov(4, 4), EvidenceAt: at(fresh), ReconciledAt: at(fresh)},
		{Scope: "prod", Kind: "k8s", Name: "audit-log", Service: "audit-log", Compliance: fleet.StatusCompliant, Coverage: cov(2, 2), EvidenceAt: at(fresh), ReconciledAt: at(fresh)},
	}
}

// demoObservedEdges are embedded observed caller→provider edges. audit-log is
// observed calling payments-service WITHOUT declaring it — an observed-only
// (shadow) dependency; orders-service→payments-service corroborates a declared
// dependency. They only affect impact when include-observed is enabled.
func demoObservedEdges() []impact.ObservedEdge {
	return []impact.ObservedEdge{
		{Consumer: "audit-log", Provider: "payments-service"},
		{Consumer: "orders-service", Provider: "payments-service"},
	}
}
