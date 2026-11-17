package main

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// This file holds the demo's trace edges and inferred external uses. It has no
// build tag, so TestDemoObservedOnlyExternal builds a snapshot from them on the
// host; buildDemoFleet (wasm only) feeds them to the browser demo.

// demoObservedEdges are embedded observed caller→provider edges Build folds into
// the snapshot as domain-qualified observed relationships. audit-log is observed
// calling payments-service WITHOUT declaring it — an observed-only (shadow)
// dependency; orders-service→payments-service corroborates a declared dependency;
// reporting-api reads postgresql directly without declaring any use of the
// offering, so the postgresql page lists it as an undeclared caller.
// notification-worker calls api.twilio.com, which no contract declares: the
// catalog classifies the callee as the twilio external, known from traces alone.
// They surface in the Operational Graph's Observed layer and, when include-observed
// is enabled, in impact.
func demoObservedEdges(now time.Time) []fleet.ObservedEdge {
	return []fleet.ObservedEdge{
		{From: "audit-log", To: "payments-service", Count: 12},
		{From: "orders-service", To: "payments-service", Count: 340},
		// Seen 20 minutes before the demo loaded, so the page reads it as current.
		{From: "reporting-api", To: "postgresql", Count: 57, FirstSeen: now.Add(-6 * time.Hour), LastSeen: now.Add(-20 * time.Minute)},
		{From: "notification-worker", To: "api.twilio.com", Count: 88, FirstSeen: now.Add(-3 * time.Hour), LastSeen: now.Add(-10 * time.Minute)},
	}
}

// demoExternalUses is what the cluster collector inferred from
// notification-worker's pod spec: an env var names the Stripe API host. The
// edge is inferred, so it shows apart from declared and observed ones and
// never clears a gate.
func demoExternalUses(now time.Time) []fleet.RawExternalUse {
	at := now.Add(-30 * time.Minute)
	return []fleet.RawExternalUse{{
		Service: "notification-worker", Namespace: "prod", Identity: "stripe", Class: externalcatalog.ClassVendorAPI,
		Confidence: externalcatalog.ConfidenceLikely, Kind: "env", Where: "deployment/notification-worker env STRIPE_API_BASE",
		Host: "api.stripe.com", Entry: "stripe", Catalog: externalcatalog.DefaultDigest(), ObservedAt: &at,
	}}
}
