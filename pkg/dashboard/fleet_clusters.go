package dashboard

import (
	"context"
	"errors"
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
			"who may not approve clusterjoins sees the approved members only; the rest are counted in hidden, and the candidate clusters " +
			"the hub's GitOps and cluster managers know in hiddenCandidates. A candidate is matched to a member only by a UID it states. Approving is a " +
			"person's signed decision with pacto cluster approve.",
		Tags:   []string{"Fleet"},
		Errors: []int{http.StatusBadGateway},
	}, s.getClusters)
	if s.opts.ClusterInstall == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-fleet-clusters-install",
		Method:      http.MethodGet,
		Path:        "/api/fleet/clusters/install",
		Summary:     "Download the install of the Pacto chart on every member",
		Description: "The Argo CD ApplicationSet (from=argocd) or Flux HelmReleases (from=flux) that install the Pacto chart on every " +
			"cluster the engine reaches, as pacto cluster install prints them. The values are only registry: set hub.url with " +
			"pacto cluster install --hub-url. Pacto writes nothing to Git and applies nothing; deleting the files never uninstalls " +
			"Pacto from a member. Only for a reader who may approve clusterjoins.",
		Tags:   []string{"Fleet"},
		Errors: []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusBadGateway},
		Responses: map[string]*huma.Response{"200": {Description: "The install as YAML, an attachment",
			Content: map[string]*huma.MediaType{"application/yaml": {Schema: &huma.Schema{Type: huma.TypeString}}}}},
	}, s.getClusterInstall)
}

type clusterInstallInput struct {
	From string `query:"from" required:"true" enum:"argocd,flux" doc:"the engine that applies the install"`
}

type clusterInstallOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

// getClusterInstall answers the install for in.From to a reader the
// member list lets approve clusterjoins.
func (s *Server) getClusterInstall(ctx context.Context, in *clusterInstallInput) (*clusterInstallOutput, error) {
	l, err := s.opts.ClusterJoins(ctx)
	switch {
	case err != nil:
		return nil, huma.Error502BadGateway("the member clusters could not be read from the registry: " + err.Error())
	case !l.CanApprove:
		return nil, huma.Error403Forbidden("the member install is for those who may approve clusterjoins")
	}
	b, err := s.opts.ClusterInstall(ctx, in.From)
	switch {
	case errors.Is(err, delivery.ErrNoChartVersion):
		return nil, huma.Error422UnprocessableEntity("the hub's operator advertises no chart version: run pacto cluster install --from " + in.From + " --chart-version <version>")
	case err != nil:
		return nil, huma.Error422UnprocessableEntity("the member install could not be generated: " + err.Error())
	}
	return &clusterInstallOutput{ContentType: "application/yaml",
		ContentDisposition: `attachment; filename="pacto-members-` + in.From + `.yaml"`, Body: b}, nil
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
