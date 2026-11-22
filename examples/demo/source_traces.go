package main

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// The demo cluster's live edge sources, as the dashboard builds them over
// the detected trace backends (internal/app's liveEdgeSources): the service
// graph takes Tempo, Jaeger takes its entry and the OTLP receiver takes the
// Collector. Datadog stays a detection row. Each row comes from
// detect.EdgeSourceRow, the dashboard's wording;
// TestDemoTraceBackendRowsMatchTheDashboard pins them against the real
// sources.
const (
	demoServiceGraphID = "detected-service-graph"
	demoJaegerID       = "detected-jaeger"
	demoReceiverID     = "otlp-receiver"
)

// demoTraceKinds are the detected kinds a live edge source takes.
var demoTraceKinds = []string{"tempo", "jaeger", "otel-collector"}

// demoServiceGraphName and the reasons below restate what the dashboard's
// sources say; the pinning test fails when they drift.
const (
	demoServiceGraphName = "Service graph in Prometheus"
	demoJaegerNoLinks    = "Jaeger returned no dependencies for the last 15 minutes; " +
		"Cassandra and Elasticsearch storage need the spark-dependencies job"
)

// demoTraceSources splits the trace backends out of res and returns the
// rest, for the detection source, and one advisory source per backend: the
// service graph read the demo's observed calls and won, so the OTLP receiver
// is off and caution-only, and Jaeger answered with no dependency.
func demoTraceSources(res detect.Result, now time.Time) (detect.Result, []fleet.Source) {
	rest, taken := detect.SplitEdgeKinds(res, demoTraceKinds)
	var out []fleet.Source
	for _, e := range taken {
		switch e.Kind {
		case "tempo":
			edges := demoObservedEdges(now)
			out = append(out, demoEdgeSource(demoServiceGraphID, res, e, detect.LiveState{
				Status: fleet.SourceAvailable, Edges: len(edges), Won: "the OTLP receiver is off", Name: demoServiceGraphName, ReadAt: now,
			}, edges, false))
		case "jaeger":
			out = append(out, demoEdgeSource(demoJaegerID, res, e, detect.LiveState{
				Status: fleet.SourcePartial, Reason: demoJaegerNoLinks, ReadAt: now,
			}, nil, false))
		case "otel-collector":
			out = append(out, demoEdgeSource(demoReceiverID, res, e, detect.LiveState{
				Status: fleet.SourcePartial, Reason: "off: " + demoServiceGraphName + " reports complete coverage", ReadAt: now,
			}, nil, true))
		}
	}
	return rest, out
}

// demoEdgeSource is the advisory, undeclared-only source id of entry e with
// the row live gives it and edges; a caution-only source never corroborates
// a declared call.
func demoEdgeSource(id string, res detect.Result, e detect.Entry, live detect.LiveState, edges []fleet.ObservedEdge, cautionOnly bool) fleet.Source {
	kind := fleet.SourceKindTraces
	if cautionOnly {
		kind = fleet.SourceKindOTLPReceiver
	}
	row := detect.EdgeSourceRow(res, e, id, live)
	return fleet.NewMemorySource(id, kind, &fleet.Collection{
		Advisory: true, Observed: edges, ObservedUndeclaredOnly: true, ObservedCautionOnly: cautionOnly,
		Providers: []fleet.Provider{row},
		State:     &fleet.SourceState{Status: row.Status, ObservedAt: &live.ReadAt},
	})
}
