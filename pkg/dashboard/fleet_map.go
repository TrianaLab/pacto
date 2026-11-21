package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/dashboard/productview"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// registerMapOperations registers the fleet map: the whole fleet as groups,
// one group's nodes page by page and one service's focus.
func (s *Server) registerMapOperations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "fleet-map", Method: http.MethodGet, Path: "/api/fleet/map",
		Summary: "Fleet map", Tags: []string{"Fleet"},
		Description: "The whole fleet as groups laid out once, by business domain, owner team or environment, with the ribbons between groups, each group's declared and observed match, contract completeness and attention, and the fleet's match and completeness. Environments follow the Stages the caller may read, else their names, and the answer says so.",
	}, s.fleetMap)
	huma.Register(api, huma.Operation{
		OperationID: "fleet-map-nodes", Method: http.MethodGet, Path: "/api/fleet/map/nodes",
		Summary: "Fleet map group nodes", Tags: []string{"Fleet"},
		Description: "One page of one map group's nodes, with coordinates relative to the group's centre, and the edges between them and the nodes of earlier pages. With an environment outside the environment grouping, each node says whether it runs there and with which version; no node moves.",
	}, s.fleetMapNodes)
	huma.Register(api, huma.Operation{
		OperationID: "fleet-map-focus", Method: http.MethodGet, Path: "/api/fleet/map/focus",
		Summary: "Fleet map focus", Tags: []string{"Fleet"},
		Description: "One service's callers and callees in columns, one or two hops out, worst first, at most 9 a column, with each edge's interface, range, running version, provenance, evidence and problem.",
	}, s.fleetMapFocus)
}

type fleetMapInput struct {
	GroupBy string `query:"groupBy" doc:"Grouping: domain (default), team or environment"`
}

type fleetMapOutput struct{ Body *fleet.FleetMap }

func (s *Server) fleetMap(ctx context.Context, in *fleetMapInput) (*fleetMapOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	mq := fleet.MapQuery{GroupBy: in.GroupBy}
	if in.GroupBy == fleet.MapGroupByEnvironment {
		mq.EnvironmentOrder, mq.OrderWhy = s.environmentRank(ctx)
	}
	m, err := q.Map(mq)
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetMapOutput{Body: m}, nil
}

// environmentRank ranks the environments by the promotion order of the
// Stages the caller can read (SPEC 21), or says why there is none.
func (s *Server) environmentRank(ctx context.Context) (map[string]int, string) {
	g := s.stageOrder(ctx)
	rank := map[string]int{}
	for i, env := range g.order {
		rank[env] = i
	}
	return rank, g.why
}

type fleetMapNodesInput struct {
	GroupBy     string `query:"groupBy" doc:"Grouping the group belongs to: domain (default), team or environment"`
	Group       string `query:"group" required:"true" doc:"Group key, as the map answer names it"`
	Environment string `query:"environment" doc:"Outside the environment grouping, marks the nodes that run in this environment and the version they run"`
	Offset      int    `query:"offset" minimum:"0"`
	Limit       int    `query:"limit" minimum:"0" doc:"Nodes a page (0 = 1000, at most 5000)"`
}

type fleetMapNodesOutput struct{ Body *fleet.MapNodes }

func (s *Server) fleetMapNodes(ctx context.Context, in *fleetMapNodesInput) (*fleetMapNodesOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	res, err := q.MapNodes(fleet.MapNodesQuery{GroupBy: in.GroupBy, Group: in.Group, Environment: in.Environment, Offset: in.Offset, Limit: in.Limit})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetMapNodesOutput{Body: res}, nil
}

type fleetMapFocusInput struct {
	Service string `query:"service" required:"true" doc:"Service name, or its key when the name is used in more than one domain"`
	Hops    int    `query:"hops" minimum:"0" maximum:"2" doc:"Hops out on each side (0 = 1, at most 2)"`
}

type fleetMapFocusOutput struct{ Body *ProductMapFocus }

func (s *Server) fleetMapFocus(ctx context.Context, in *fleetMapFocusInput) (*fleetMapFocusOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	res, err := q.MapFocus(fleet.MapFocusQuery{Service: in.Service, Hops: in.Hops})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetMapFocusOutput{Body: productview.ToMapFocus(res)}, nil
}
