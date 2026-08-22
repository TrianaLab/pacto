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
// are useful the moment the demo opens, with no server and no OCI registry.
// It is wasm-only (see the build tag): the host build never compiles it.

// demoRegistry is the synthetic registry the demo's revision refs live under. The
// refs are immutable digest refs so they satisfy the same identity rules the real
// product uses; the impact provider resolves them from memory, never over a
// network.
const demoRegistry = "oci://demo.pacto.local/"

// partnerRegistry and partnerDomain are the second domain's registry and logical
// scope. A domain comes from the SOURCE, never from the contract, which is what
// keeps two services that call themselves "platform-app-config" apart.
const (
	partnerRegistry = "oci://partners.demo.pacto.local/"
	partnerDomain   = "partners"
)

// demoNow is a fixed clock so the embedded snapshot (and its stale-target
// computation) is fully deterministic across builds.
func demoNow() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }

// buildDemoFleet builds a fleet query over every embedded revision plus the
// demo's operational targets, and returns a ref→bundle index so the impact
// provider can resolve the two revisions to compare from memory.
//
// It deliberately uses FOUR sources rather than one, because the source list, the
// per-target provenance and the completeness semantics are product surfaces in their
// own right and a single source leaves all three blank: the bundle registry and the
// cluster collector that observes it are not the same thing (section 0b, Data source
// vs Collector), telemetry comes from a third, and an environment nobody could reach
// is what makes the snapshot honestly partial.
func buildDemoFleet(src, partnersSrc *EmbedSource) (*fleet.FleetSnapshot, map[string]*contract.Bundle, error) {
	col := &fleet.Collection{}
	byRef := map[string]*contract.Bundle{}
	revisions := func(s *EmbedSource, registry string) []fleet.RawRevision {
		var out []fleet.RawRevision
		for _, name := range s.names {
			for ver, entry := range s.byName[name] {
				ref := registry + name + "@sha256:" + entry.hash
				out = append(out, fleet.RawRevision{
					Bundle:       entry.bundle,
					RequestedRef: registry + name + ":" + ver,
					ResolvedRef:  ref,
					Digest:       "sha256:" + entry.hash,
				})
				byRef[ref] = entry.bundle
			}
		}
		return out
	}
	col.Revisions = revisions(src, demoRegistry)

	// A second registry, in a second DOMAIN. It publishes its own platform-app-config
	// — the same NAME as the default domain's, and not the same service — plus the
	// settlement-service that reads it.
	//
	// This is the demo's adversarial identity case, and every product surface has to
	// survive it: settlement-service's configuration reference must resolve to the
	// PARTNER platform-app-config (a reference resolves within the referring revision's
	// own domain), its policy reference must stay honestly unresolved (the partner
	// domain publishes no http policy and the default domain's is not a candidate),
	// search must show two distinguishable platform-app-configs, each config's
	// "Referenced by" must list only its own domain's consumers, and each service's
	// documentation must be its own. Anything that identifies a service by bare name
	// reads the wrong contract here.
	//
	// The colliding name is deliberately one that carries NO observed traffic. An
	// observed edge names a bare service name, so a collision there resolves to
	// ObservedAmbiguous and the edge is dropped with a limitation rather than
	// misattributed — correct, but it would silently empty the demo's Observed layer.
	partnerCol := &fleet.Collection{Revisions: revisions(partnersSrc, partnerRegistry)}
	for i := range partnerCol.Revisions {
		partnerCol.Revisions[i].Domain = partnerDomain
	}
	partnerRegistrySource := fleet.NewMemorySource("partner-registry", "oci", partnerCol)

	// The cluster collector reports what is running; the registry above reports what
	// was declared. Keeping them apart is what lets a target say which source saw it.
	cluster := fleet.NewMemorySource("k8s-production", "k8s", &fleet.Collection{
		Targets: demoTargets(src),
	})
	// Telemetry is a second collector over the SAME estate: it corroborates the two
	// targets it can see (agreeing on every identity-bearing field, so they merge
	// instead of quarantining) and contributes the observed call edges. That is how a
	// target ends up with two sources, which is the only way the provenance block on
	// the target page shows anything worth reading.
	telemetry := fleet.NewMemorySource("otel-traces", "observation", &fleet.Collection{
		Targets:  demoCorroboratedTargets(),
		Observed: demoObservedEdges(),
	})
	// A registry mirror that answered, but not with everything it holds: partial is a
	// third source state, distinct from available and unavailable, and the source
	// health strip is meant to distinguish all three.
	mirror := fleet.NewMemorySource("registry-mirror", "oci", &fleet.Collection{
		State:       &fleet.SourceState{Status: fleet.SourcePartial},
		Limitations: []fleet.Limitation{{Code: fleet.LimitationSourcePartial, Source: "registry-mirror", Message: "tag listing truncated by the mirror"}},
	})
	unavailable := fleet.NewMemorySource("edge-cluster", "k8s", &fleet.Collection{
		State: &fleet.SourceState{
			Status: fleet.SourceUnavailable,
			Error:  &fleet.SourceError{Code: fleet.LimitationSourceUnavailable, Message: "edge cluster unreachable"},
		},
	})
	snap, err := fleet.Build(context.Background(),
		fleet.BuildOptions{Now: demoNow, FreshnessWindow: 24 * time.Hour},
		fleet.NewMemorySource("local", "local", col), partnerRegistrySource, cluster, telemetry, mirror, unavailable)
	if err != nil {
		return nil, nil, err
	}
	return snap, byRef, nil
}

// demoImpactProvider returns the impact provider the demo wires into the
// dashboard. It analyzes against the SAME published snapshot the Operational
// Graph serves (so snapshotIds match, section 2.2), resolving the two revisions from the
// in-memory ref index. Observed edges are folded into that snapshot by
// buildDemoFleet, so include-observed is a real capability driven by the SAME
// pipeline the Operational Graph uses (surfacing a shadow consumer), not a placebo
// and not a second, divergent source of observed data.
func demoImpactProvider(snap *fleet.FleetSnapshot, byRef map[string]*contract.Bundle) func(context.Context, string, string, bool) (*impact.Result, error) {
	return func(ctx context.Context, oldRef, newRef string, includeObserved bool) (*impact.Result, error) {
		oldB, newB := byRef[oldRef], byRef[newRef]
		if oldB == nil || newB == nil {
			return nil, fmt.Errorf("unknown revision reference (old=%q new=%q)", oldRef, newRef)
		}
		return impact.Analyze(ctx, oldB.Contract, newB.Contract, oldB.FS, newB.FS, snap, impact.Options{
			IncludeObserved: includeObserved,
		}), nil
	}
}

// demoTargets are deterministic operational targets covering the range of states
// the dashboard renders: three compliance verdicts (Compliant, NonCompliant and
// Unknown), exact and inferred revision matches alongside targets with nothing to
// match against, fresh and stale evidence, a service running in more than one scope,
// and the runtime detail a target page is FOR (labels and observed runtime values).
// A target page with no labels and no observed runtime is a page of four counters,
// which is what the demo used to show.
//
// src is threaded in so one target can pin the real content digest of an embedded
// revision. Nothing else in the demo can produce an EXACT revision match: an exact
// link means the collector reported the same immutable digest the registry holds,
// and inventing a digest that resolves to nothing would be the opposite of exact.
func demoTargets(src *EmbedSource) []fleet.RawTarget {
	fresh := demoNow().Add(-1 * time.Hour)
	older := demoNow().Add(-6 * time.Hour)
	stale := demoNow().Add(-72 * time.Hour) // older than the 24h freshness window
	cov := func(e, r int) *fleet.Coverage { return &fleet.Coverage{Evaluated: e, Required: r} }
	at := func(t time.Time) *time.Time { return &t }
	prod := func(extra map[string]string) map[string]string {
		l := map[string]string{"env": "production", "region": "eu-west-1", "managed-by": "platform"}
		for k, v := range extra {
			l[k] = v
		}
		return l
	}
	return []fleet.RawTarget{
		// EXACT, twice over and for two different reasons: the collector reported the
		// content digest of the embedded payments-service 2.1.0 revision (so the runs
		// edge is certain rather than deduced from a mutable tag) AND it resolved a
		// canonical digest reference (so the content is retrievable). Every other
		// target below separates the two -- a tag ref is a confident-enough match with
		// mutable content, and no ref at all is a match with nothing to fetch.
		{Scope: "prod", Kind: "Deployment", Name: "payments-service", Service: "payments-service",
			Digest:      demoDigest(src, "payments-service", "2.1.0"),
			ResolvedRef: demoRegistry + "payments-service@" + demoDigest(src, "payments-service", "2.1.0"),
			Compliance:  fleet.StatusCompliant,
			Coverage:    cov(5, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "payments-service", "tier": "critical"}),
			ObservedRuntime: map[string]any{"replicas": 6, "image": demoRegistry + "payments-service@" + demoDigest(src, "payments-service", "2.1.0"), "cpuRequest": "500m", "strategy": "RollingUpdate"}},
		// The SAME service in a second scope, matched only by a version tag: INFERRED.
		// One service, two targets, two certainties -- which is why the product keeps
		// Service and Operational Target apart in the first place.
		{Scope: "staging", Kind: "Deployment", Name: "payments-service", Service: "payments-service",
			ResolvedRef: demoRegistry + "payments-service:2.1.0", Compliance: fleet.StatusCompliant,
			Coverage: cov(5, 5), EvidenceAt: at(older), ReconciledAt: at(older),
			Labels:          map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "payments-service"},
			ObservedRuntime: map[string]any{"replicas": 1, "image": demoRegistry + "payments-service:2.1.0"}},
		{Scope: "prod", Kind: "Deployment", Name: "orders-service", Service: "orders-service",
			ResolvedRef: demoRegistry + "orders-service:1.2.0", Compliance: fleet.StatusNonCompliant,
			Coverage: cov(4, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "orders-service", "tier": "critical"}),
			ObservedRuntime: map[string]any{"replicas": 4, "image": demoRegistry + "orders-service:1.2.0"},
			// Three severities on one target: the severity distribution is a real
			// distribution, and the findings table has something to sort.
			Findings: []finding.Finding{
				{Code: "CONTRACT_VIOLATION", Severity: finding.SeverityError, Category: "RuntimeDrift", Subject: finding.SubjectRef{Kind: "interface", Name: "orders-http"}, Message: "response schema drifted from the declared contract"},
				{Code: "UNDECLARED_DEPENDENCY", Severity: finding.SeverityWarning, Category: "Relationship", Subject: finding.SubjectRef{Kind: "dependency", Name: "payments-service"}, Message: "observed calling payments-service more often than the declared budget"},
				{Code: "COVERAGE_INCOMPLETE", Severity: finding.SeverityInfo, Category: "Evidence", Message: "1 of 5 required checks could not be evaluated from the available evidence"},
			}},
		{Scope: "prod", Kind: "Deployment", Name: "auth-service", Service: "auth-service", Compliance: fleet.StatusUnknown,
			Coverage: cov(1, 5), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "auth-service"}),
			ObservedRuntime: map[string]any{"replicas": 3},
			Findings: []finding.Finding{
				{Code: "EVIDENCE_INSUFFICIENT", Severity: finding.SeverityWarning, Category: "Evidence", Message: "the collector could not read the running interface, so compliance is unknown rather than clean"},
			}},
		{Scope: "staging", Kind: "Deployment", Name: "fraud-service", Service: "fraud-service", Compliance: fleet.StatusCompliant,
			Coverage: cov(5, 5), EvidenceAt: at(stale), ReconciledAt: at(stale),
			Labels: map[string]string{"env": "staging", "region": "eu-west-1", "app.kubernetes.io/name": "fraud-service"}},
		{Scope: "prod", Kind: "Deployment", Name: "frontend", Service: "frontend", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "frontend:1.0.0", Coverage: cov(3, 3), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "frontend"}),
			ObservedRuntime: map[string]any{"replicas": 2, "ingress": "https://shop.example.com"}},
		{Scope: "prod", Kind: "Deployment", Name: "api-gateway", Service: "api-gateway", Compliance: fleet.StatusCompliant,
			ResolvedRef: demoRegistry + "api-gateway:1.2.0", Coverage: cov(4, 4), EvidenceAt: at(fresh), ReconciledAt: at(fresh),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "api-gateway", "tier": "edge"}),
			ObservedRuntime: map[string]any{"replicas": 3, "ingress": "https://api.example.com"}},
		// Never observed: the cluster inventory knows this workload exists, but no
		// evaluation evidence was ever collected for it. That is a third state, not a
		// stale one -- absence of evidence is not evidence of absence, and the freshness
		// bar is only a partition once "never looked" has its own bucket.
		{Scope: "prod", Kind: "Deployment", Name: "notification-worker", Service: "notification-worker",
			Compliance: fleet.StatusUnknown,
			Labels:     prod(map[string]string{"app.kubernetes.io/name": "notification-worker"})},
		{Scope: "prod", Kind: "CronJob", Name: "audit-log", Service: "audit-log", Compliance: fleet.StatusCompliant,
			Coverage: cov(2, 2), EvidenceAt: at(older), ReconciledAt: at(older),
			Labels:          prod(map[string]string{"app.kubernetes.io/name": "audit-log"}),
			ObservedRuntime: map[string]any{"schedule": "*/15 * * * *", "lastSuccessful": older.Format(time.RFC3339)}},
	}
}

// demoDigest returns the content digest of an embedded revision, so a target can
// claim an exact match against something that actually exists.
func demoDigest(src *EmbedSource, name, version string) string {
	if e := src.byName[name][version]; e != nil {
		return "sha256:" + e.hash
	}
	return ""
}

// demoCorroboratedTargets is the telemetry collector's view of two targets the
// cluster collector also reported. Every identity-bearing field either agrees or is
// left empty, so Build MERGES them and the targets carry two sources -- corroborated
// observation, not a conflict. (A disagreement here would quarantine the target,
// which is the correct behaviour and a state the demo deliberately does not enter:
// a fixture that ships a self-contradicting fleet teaches the wrong lesson.)
//
// Its observations are deliberately OLDER than the cluster collector's, because the
// fresher contribution owns the evaluation fields. Telemetry does not evaluate
// compliance -- it watches traffic -- so a fresher telemetry contribution would
// replace a real verdict with an empty one and the demo would show every target as
// unknown. Corroboration here means provenance, not a second opinion on compliance.
func demoCorroboratedTargets() []fleet.RawTarget {
	seen := demoNow().Add(-3 * time.Hour)
	at := func(t time.Time) *time.Time { return &t }
	return []fleet.RawTarget{
		{Scope: "prod", Kind: "Deployment", Name: "payments-service", Service: "payments-service", EvidenceAt: at(seen)},
		{Scope: "prod", Kind: "Deployment", Name: "orders-service", Service: "orders-service", EvidenceAt: at(seen)},
	}
}

// demoObservedEdges are embedded observed caller→provider edges Build folds into
// the snapshot as domain-qualified observed relationships. audit-log is observed
// calling payments-service WITHOUT declaring it — an observed-only (shadow)
// dependency; orders-service→payments-service corroborates a declared dependency.
// They surface in the Operational Graph's Observed layer and, when include-observed
// is enabled, in impact.
func demoObservedEdges() []fleet.ObservedEdge {
	return []fleet.ObservedEdge{
		{From: "audit-log", To: "payments-service", Count: 12},
		{From: "orders-service", To: "payments-service", Count: 340},
	}
}
