package fleetsrc

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/otelobserver"
)

// Service-graph read. Tempo's metrics-generator and the Collector's
// servicegraph connector both write traces_service_graph_request_total; the
// virtual "user" client of root spans is no service.
const (
	serviceGraphName      = "Service graph in Prometheus"
	maxServiceGraphSeries = 5000
	serviceGraphQuery     = `topk(5000, sum by (client, server) (increase(traces_service_graph_request_total{client!="user",server!="user"}[15m])))`
	serviceGraphNoProm    = "no reachable Prometheus holds the service-graph metrics"
	serviceGraphNoSeries  = "Prometheus holds no traces_service_graph_request_total series: " +
		"enable Tempo's service-graphs processor or the Collector's servicegraph connector"
)

// ServiceGraphSource is an advisory [fleet.Source] over the service graph a
// detected Tempo or Collector writes to the Prometheus the detection names
// on its entry. It reads through the mesh source's PromQL reader, puts raw
// service names into the collection and lets [fleet.Build] resolve them.
// Like [JaegerSource] it never fails and its silence never counts against a
// declared call.
type ServiceGraphSource struct {
	id    string
	res   detect.Result
	entry detect.Entry
	row   string
	live  *LiveEdges
	now   func() time.Time
	prom  *MeshSource
}

// NewServiceGraphSource reads the service graph for entry e of res (the
// tempo or otel-collector entry) from the Prometheus at e's Endpoint and
// records a read with complete coverage in live. A tempo entry keeps its row
// id; a Collector entry, left to the OTLP receiver, reports row
// "service-graph".
func NewServiceGraphSource(id string, res detect.Result, e detect.Entry, live *LiveEdges, now func() time.Time) *ServiceGraphSource {
	prom := NewMeshSource(id, nil, nil, nil)
	// ponytail: the dashboard runs in the cluster and the Endpoint already
	// passed detect.CheckEndpoint, so it is the base URL as it is.
	prom.inCluster, prom.now = true, now
	prom.baseURL = func(string, string, int32) string { return e.Endpoint }
	// Anyone who can create a labelled Service can answer here, so a
	// redirect is an answer, never a hop out of the checked Endpoint.
	prom.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	row := ""
	if e.Kind != "tempo" {
		row = "service-graph"
	}
	return &ServiceGraphSource{id: id, res: res, entry: e, row: row, live: live, now: now, prom: prom}
}

// ID implements [fleet.Source].
func (s *ServiceGraphSource) ID() string { return s.id }

// Kind is [fleet.SourceKindTraces].
func (s *ServiceGraphSource) Kind() string { return fleet.SourceKindTraces }

// Collect runs the bounded query and reports the edges with the source's
// one row.
func (s *ServiceGraphSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	at := s.now()
	live := detect.LiveState{ReadAt: at, Row: s.row, Name: serviceGraphName}
	var edges []fleet.ObservedEdge
	if s.entry.Endpoint == "" {
		live.Status, live.Reason = fleet.SourcePartial, serviceGraphNoProm
	} else if series, err := s.prom.promQuery(ctx, svcInfo{}, serviceGraphQuery); err != nil {
		live.Status, live.Reason = fleet.SourceUnavailable, promErrorText(err)
	} else {
		var reasons []string
		edges, reasons = serviceGraphEdges(series, at)
		live.Status, live.Edges = fleet.SourceAvailable, len(edges)
		if len(reasons) > 0 {
			live.Status, live.Reason = fleet.SourcePartial, strings.Join(reasons, "; ")
		}
	}
	return edgeCollection(s.res, s.entry, s.id, live, s.live, serviceGraphName, edges), nil
}

// serviceGraphEdges folds service-graph series into one edge per pair of
// names, busiest first, each seen in the window ending at at, with the
// reasons the read is incomplete (counts only, never names). A series with
// no calls or the virtual "user" end is skipped; an end
// [otelobserver.AttributableName] refuses is dropped and counted.
func serviceGraphEdges(series []promSeries, at time.Time) ([]fleet.ObservedEdge, []string) {
	var reasons []string
	switch {
	case len(series) == 0:
		reasons = append(reasons, serviceGraphNoSeries)
	case len(series) >= maxServiceGraphSeries:
		reasons = append(reasons, fmt.Sprintf("the read stopped at the %d busiest call pairs", maxServiceGraphSeries))
	}
	var calls []meshCall
	unattributed := 0
	for _, p := range series {
		v, client, server := p.value(), p.Metric["client"], p.Metric["server"]
		switch {
		case v <= 0 || client == "user" || server == "user":
		case !otelobserver.AttributableName(client) || !otelobserver.AttributableName(server):
			unattributed++
		default:
			calls = append(calls, meshCall{from: client, to: server, count: v})
		}
	}
	if unattributed > 0 {
		reasons = append(reasons, fmt.Sprintf("%d call pairs are not attributable to a named service", unattributed))
	}
	edges, _ := foldCalls(calls, at)
	for i := range edges {
		edges[i].FirstSeen = at.Add(-traceWindow)
	}
	return edges, reasons
}
