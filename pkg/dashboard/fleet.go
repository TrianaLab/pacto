package dashboard

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
	"github.com/trianalab/pacto/v3/pkg/oci"
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

// impactProviderFunc resolves the old and new contract revisions, builds a fleet
// snapshot and analyzes the change's blast radius over it. The dashboard consumes
// it so this package depends only on the pure impact result, never on the app
// service, OCI client or Kubernetes.
type impactProviderFunc func(ctx context.Context, old, new string, includeObserved bool) (*impact.Result, error)

// SetImpactProvider enables the read-only /api/fleet/impact endpoint. When unset,
// the endpoint is not registered, so existing deployments are unaffected.
func (s *Server) SetImpactProvider(fn impactProviderFunc) { s.impactProvider = fn }

// capabilitiesOutput reports which optional capabilities the running host has
// registered, so the frontend can gate its navigation and never expose a
// capability the host does not serve (a fleet-less host must not show a dead
// Operational Graph tab).
type capabilitiesOutput struct {
	Body struct {
		Fleet  bool `json:"fleet" doc:"The operational-graph (fleet) endpoints are served"`
		Impact bool `json:"impact" doc:"The impact endpoint is served"`
		// Observed reports whether an observation source (e.g. OTel traces) backs
		// the impact provider, so a consumer can honestly enable an include-observed
		// control instead of shipping a placebo.
		Observed bool `json:"observed" doc:"An observation source backs impact analysis"`
	}
}

// registerCapabilitiesOperation registers the always-on capabilities endpoint. It
// reports state at request time, so a provider set after registration is
// reflected.
func (s *Server) registerCapabilitiesOperation(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "capabilities",
		Method:      http.MethodGet,
		Path:        "/api/capabilities",
		Summary:     "Report enabled capabilities",
		Description: "Reports which optional capabilities (fleet, impact) the running host serves.",
		Tags:        []string{"Meta"},
	}, s.capabilities)
}

func (s *Server) capabilities(_ context.Context, _ *struct{}) (*capabilitiesOutput, error) {
	out := &capabilitiesOutput{}
	out.Body.Fleet = s.fleetQuery != nil
	out.Body.Impact = s.impactProvider != nil
	out.Body.Observed = s.observedAvailable
	return out, nil
}

// SetObservedAvailable declares that the impact provider is backed by an
// observation source (e.g. embedded OTel traces), so the frontend may enable its
// include-observed control. Off by default — a host without observed data must
// never advertise it (no placebo).
func (s *Server) SetObservedAvailable(v bool) { s.observedAvailable = v }

// registerFleetOperations registers the fleet endpoints when a provider is set.
func (s *Server) registerFleetOperations(api huma.API) {
	s.registerImpactOperation(api)
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
		OperationID: "fleet-service-detail",
		Method:      http.MethodGet,
		Path:        "/api/fleet/service",
		Summary:     "Operational-graph service detail",
		Description: "Returns one logical service's revisions, targets, declared dependencies and " +
			"dependents. Keyed by the domain-qualified ServiceKey (query param) for bounded lazy detail " +
			"loading, so the whole snapshot need not be shipped to open a service.",
		Tags: []string{"Fleet"},
	}, s.fleetServiceDetail)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-target-detail",
		Method:      http.MethodGet,
		Path:        "/api/fleet/target",
		Summary:     "Operational-graph target detail",
		Description: "Returns one operational target and its exact linked contract revision. Keyed by " +
			"the TargetKey (query param) for bounded lazy detail loading.",
		Tags: []string{"Fleet"},
	}, s.fleetTargetDetail)

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
	Scope  string `query:"scope"`
	Status string `query:"status"`
	Source string `query:"source"`
	Limit  int    `query:"limit"`
	Offset int    `query:"offset"`
}

type fleetSearchOutput struct {
	Body *fleet.SearchResult
}

func (s *Server) fleetSearch(ctx context.Context, in *fleetSearchInput) (*fleetSearchOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Search(fleet.SearchFilter{
		Text: in.Text, Owner: in.Owner, Scope: in.Scope, Status: in.Status,
		Source: in.Source, Limit: in.Limit, Offset: in.Offset,
	})
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("invalid fleet search filter", err)
	}
	return &fleetSearchOutput{Body: res}, nil
}

type fleetServiceDetailInput struct {
	Key string `query:"key" required:"true" doc:"Domain-qualified ServiceKey (or a unique service name)"`
}

type fleetServiceDetailOutput struct {
	Body *fleet.ServiceView
}

func (s *Server) fleetServiceDetail(ctx context.Context, in *fleetServiceDetailInput) (*fleetServiceDetailOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	view, err := q.GetService(in.Key)
	if err != nil {
		return nil, fleetLookupError(err)
	}
	return &fleetServiceDetailOutput{Body: view}, nil
}

type fleetTargetDetailInput struct {
	Key string `query:"key" required:"true" doc:"TargetKey (or a unique target name)"`
}

type fleetTargetDetailOutput struct {
	Body *fleet.TargetView
}

func (s *Server) fleetTargetDetail(ctx context.Context, in *fleetTargetDetailInput) (*fleetTargetDetailOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	view, err := q.GetTarget(in.Key)
	if err != nil {
		return nil, fleetLookupError(err)
	}
	return &fleetTargetDetailOutput{Body: view}, nil
}

// fleetLookupError maps a by-key lookup error to HTTP: absent → 404; otherwise
// (the only other error GetService/GetTarget return is AmbiguousError — a bare
// name matched several domains/targets) → 422, since the fix is to pass the
// fully-qualified key the error lists.
func fleetLookupError(err error) error {
	var nf *fleet.NotFoundError
	if errors.As(err, &nf) {
		return huma.Error404NotFound("not found", err)
	}
	return huma.Error422UnprocessableEntity("ambiguous or invalid key: qualify it", err)
}

type fleetGraphInput struct {
	Name       string `path:"name"`
	Revision   string `query:"revision"`
	Target     string `query:"target"`
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
	res, err := q.Graph(fleet.GraphQuery{
		Service: in.Name, Revision: fleet.RevisionKey(in.Revision), Target: in.Target,
		Direction: fleet.Direction(in.Direction), Transitive: in.Transitive, MaxDepth: in.MaxDepth,
	})
	if err != nil {
		var nf *fleet.NotFoundError
		if errors.As(err, &nf) {
			return nil, huma.Error404NotFound("not found", err)
		}
		return nil, huma.Error422UnprocessableEntity("invalid fleet graph query", err)
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

// registerImpactOperation registers the read-only impact endpoint when a provider
// is set. It is independent of the fleet query provider so an impact-only wiring
// is possible, though the dashboard command sets both from the same local root.
func (s *Server) registerImpactOperation(api huma.API) {
	if s.impactProvider == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-impact",
		Method:      http.MethodGet,
		Path:        "/api/fleet/impact",
		Summary:     "Analyze the blast radius of a change",
		Description: "Projects the semantic diff between an old and a new contract revision onto " +
			"the operational graph: classification, breaking changes, affected consumers (with " +
			"confidence and compatibility verdict), active targets and owners. Read-only.",
		Tags: []string{"Fleet"},
	}, s.fleetImpact)
}

type fleetImpactInput struct {
	Old             string `query:"old" required:"true" example:"oci://ghcr.io/org/svc:1.0.0" doc:"Old contract revision (local dir or oci:// ref)"`
	New             string `query:"new" required:"true" example:"oci://ghcr.io/org/svc:2.0.0" doc:"New contract revision (local dir or oci:// ref)"`
	IncludeObserved bool   `query:"includeObserved" doc:"Let observed (runtime) relationships raise consumer confidence"`
}

type fleetImpactOutput struct {
	Body *impact.Result
}

func (s *Server) fleetImpact(ctx context.Context, in *fleetImpactInput) (*fleetImpactOutput, error) {
	res, err := s.impactProvider(ctx, in.Old, in.New, in.IncludeObserved)
	if err != nil {
		return nil, impactHTTPError(err)
	}
	return &fleetImpactOutput{Body: res}, nil
}

// impactHTTPError maps an impact provider error to an HTTP status: a bad/incompatible
// reference is the caller's fault (422), a missing artifact is 404, and everything
// else (registry auth/reachability, fleet snapshot build) is a transient upstream
// condition (503) rather than a bug in the request.
func impactHTTPError(err error) error {
	var invalidRef *oci.InvalidRefError
	var invalidBundle *oci.InvalidBundleError
	var noMatch *oci.NoMatchingVersionError
	var notFound *oci.ArtifactNotFoundError
	switch {
	case errors.As(err, &invalidRef), errors.As(err, &noMatch), errors.As(err, &invalidBundle):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.As(err, &notFound):
		return huma.Error404NotFound(err.Error())
	default:
		return huma.Error503ServiceUnavailable("impact analysis unavailable", err)
	}
}
