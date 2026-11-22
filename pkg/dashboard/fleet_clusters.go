package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// registerClusterOperations registers the member cluster list when this
// host reads join requests from a registry.
func (s *Server) registerClusterOperations(api huma.API) {
	if s.opts.ClusterJoins == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-fleet-clusters",
		Method:      http.MethodGet,
		Path:        "/api/fleet/clusters",
		Summary:     "List the member clusters of this hub",
		Description: "The clusters that publish to this hub's registry and where each stands: approved members, pending clusters " +
			"with why they are pending and removed members, as pacto cluster list and pacto_cluster_list answer. A signed-in reader " +
			"who may not approve clusterjoins sees the approved members only; the rest are counted in hidden. Approving is a " +
			"person's signed decision with pacto cluster approve.",
		Tags:   []string{"Fleet"},
		Errors: []int{http.StatusBadGateway},
	}, s.getClusters)
}

type clustersOutput struct {
	Body delivery.ClusterJoinList
}

func (s *Server) getClusters(ctx context.Context, _ *struct{}) (*clustersOutput, error) {
	l, err := s.opts.ClusterJoins(ctx)
	if err != nil {
		return nil, huma.Error502BadGateway("the member clusters could not be read from the registry: " + err.Error())
	}
	return &clustersOutput{Body: l}, nil
}
