package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// fleetProvider builds a pure fleet query on demand. The dashboard is a consumer
// of the reusable operational-graph layer: rather than re-inventing fleet
// aggregation, graph, freshness and completeness semantics, it delegates to a
// provider (wired by the dashboard command from the same sources it detected)
// and serves the resulting immutable snapshot through the API. Keeping this a
// provider closure means the dashboard package depends only on pkg/fleet, never
// on the concrete source implementations.
type fleetProvider func(ctx context.Context) (*fleet.Query, error)

// SetFleetProvider enables the read-only operational-graph endpoints. When unset,
// none of the /api/fleet/* endpoints are registered, so existing deployments are
// unaffected.
func (s *Server) SetFleetProvider(fn fleetProvider) { s.fleetQuery = fn }

// registerFleetOperations registers the fleet endpoints when a provider is set.
func (s *Server) registerFleetOperations(api huma.API) {
	if s.fleetQuery == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-snapshot",
		Method:      http.MethodGet,
		Path:        "/api/fleet/snapshot",
		Summary:     "Operational-graph snapshot",
		Description: "Returns the immutable fleet snapshot: logical services, contract revisions, " +
			"operational targets, relationships and source states, with an as-of time and completeness.",
		Tags: []string{"Fleet"},
	}, s.fleetSnapshot)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-search",
		Method:      http.MethodGet,
		Path:        "/api/fleet/services",
		Summary:     "Search the operational graph",
		Description: "Searches logical services with bounded, deterministically ordered results. " +
			"Every answer reports its as-of time and completeness.",
		Tags: []string{"Fleet"},
	}, s.fleetSearch)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-graph",
		Method:      http.MethodGet,
		Path:        "/api/fleet/services/{name}/graph",
		Summary:     "Traverse the operational graph",
		Description: "Traverses fleet dependencies or dependents from a service (cycle-safe).",
		Tags:        []string{"Fleet"},
	}, s.fleetGraph)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-status",
		Method:      http.MethodGet,
		Path:        "/api/fleet/status",
		Summary:     "Fleet attention report",
		Description: "Reports services and targets needing attention across every category.",
		Tags:        []string{"Fleet"},
	}, s.fleetStatus)
}

type fleetSnapshotOutput struct {
	Body *fleet.FleetSnapshot
}

func (s *Server) fleetSnapshot(ctx context.Context, _ *struct{}) (*fleetSnapshotOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	return &fleetSnapshotOutput{Body: q.Snapshot()}, nil
}

type fleetSearchInput struct {
	Text   string `query:"text"`
	Owner  string `query:"owner"`
	Status string `query:"status"`
	Source string `query:"source"`
	Limit  int    `query:"limit"`
}

type fleetSearchOutput struct {
	Body *fleet.SearchResult
}

func (s *Server) fleetSearch(ctx context.Context, in *fleetSearchInput) (*fleetSearchOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res := q.Search(fleet.SearchFilter{
		Text: in.Text, Owner: in.Owner, Status: in.Status, Source: in.Source, Limit: in.Limit,
	})
	return &fleetSearchOutput{Body: res}, nil
}

type fleetGraphInput struct {
	Name       string `path:"name"`
	Direction  string `query:"direction"`
	Transitive bool   `query:"transitive"`
	MaxDepth   int    `query:"maxDepth"`
}

type fleetGraphOutput struct {
	Body *fleet.GraphResult
}

func (s *Server) fleetGraph(ctx context.Context, in *fleetGraphInput) (*fleetGraphOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	dir := fleet.DirectionDependencies
	if in.Direction == "dependents" {
		dir = fleet.DirectionDependents
	}
	res, err := q.Graph(fleet.GraphQuery{Service: in.Name, Direction: dir, Transitive: in.Transitive, MaxDepth: in.MaxDepth})
	if err != nil {
		return nil, huma.Error404NotFound("service not found", err)
	}
	return &fleetGraphOutput{Body: res}, nil
}

type fleetStatusOutput struct {
	Body *fleet.StatusResult
}

func (s *Server) fleetStatus(ctx context.Context, _ *struct{}) (*fleetStatusOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	return &fleetStatusOutput{Body: q.Status(fleet.StatusQuery{NeedsAttention: true})}, nil
}
