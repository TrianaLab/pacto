package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// This file declares the contract-view half of the API: the single-service
// endpoints the OFFLINE static export answers.
//
// `pacto doc --format html` ships a self-contained bundle with no server in it.
// The exported page carries the responses inline as window.__PACTO_STATIC__ and
// the frontend's transport reads that table instead of issuing a request, so the
// document below is the contract those responses have to satisfy — the generated
// SDK the frontend calls is built from it.
//
// A live host does NOT serve these. It has a whole fleet rather than one bundle,
// so it answers /api/fleet/* instead, and [Server.RegisterOperations] leaves these
// out rather than mounting routes that would have to fail. That is why they are
// registered from [ExportOpenAPI] only.

// offline is the handler for an operation no live host mounts. It exists because
// huma.Register derives the request and response schemas from a handler's
// signature; the body is never reached through the API, since the only caller of
// registerOfflineOperations builds the document and discards the router.
func offline[I, O any](context.Context, *I) (*O, error) {
	return nil, huma.Error501NotImplemented("this operation is answered only by the offline static export")
}

const offlineDoc = " Answered by the offline static export (`pacto doc --format html`); a live dashboard serves /api/fleet/* instead."

// registerOfflineOperations adds the contract-view operations to the API
// DOCUMENT. Called only from [ExportOpenAPI].
func registerOfflineOperations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-services",
		Method:      http.MethodGet,
		Path:        "/api/services",
		Summary:     "List services",
		Description: "Returns an enriched list of all services." + offlineDoc,
		Tags:        []string{"Services"},
	}, offline[struct{}, listServicesOutput])

	huma.Register(api, huma.Operation{
		OperationID: "get-service",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}",
		Summary:     "Get service details",
		Description: "Returns full details for a single service by name." + offlineDoc,
		Tags:        []string{"Services"},
	}, offline[ServiceNameInput, getServiceOutput])

	huma.Register(api, huma.Operation{
		OperationID: "get-service-versions",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/versions",
		Summary:     "Get service versions",
		Description: "Returns the version history for a service." + offlineDoc,
		Tags:        []string{"Services"},
	}, offline[ServiceNameInput, getVersionsOutput])

	huma.Register(api, huma.Operation{
		OperationID: "get-service-version",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/versions/{version}",
		Summary:     "Get service details at a version",
		Description: "Returns full details for a specific version of a service." + offlineDoc,
		Tags:        []string{"Services"},
	}, offline[serviceVersionInput, getServiceVersionOutput])

	huma.Register(api, huma.Operation{
		OperationID: "get-service-sources",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/sources",
		Summary:     "Get service sources",
		Description: "Returns per-source breakdown and merged view for a service." + offlineDoc,
		Tags:        []string{"Services"},
	}, offline[ServiceNameInput, getServiceSourcesOutput])

	huma.Register(api, huma.Operation{
		OperationID: "get-global-graph",
		Method:      http.MethodGet,
		Path:        "/api/graph",
		Summary:     "Get global dependency graph",
		Description: "Returns the full dependency graph across all services." + offlineDoc,
		Tags:        []string{"Graph"},
	}, offline[struct{}, getGlobalGraphOutput])

	huma.Register(api, huma.Operation{
		OperationID: "get-service-dependents",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/dependents",
		Summary:     "Get service dependents",
		Description: "Returns services that depend on the given service." + offlineDoc,
		Tags:        []string{"Services"},
	}, offline[ServiceNameInput, getDependentsOutput])

	huma.Register(api, huma.Operation{
		OperationID: "get-service-refs",
		Method:      http.MethodGet,
		Path:        "/api/services/{name}/refs",
		Summary:     "Get service cross-references",
		Description: "Returns config/policy cross-references for a service." + offlineDoc,
		Tags:        []string{"Services"},
	}, offline[ServiceNameInput, getCrossRefsOutput])

	huma.Register(api, huma.Operation{
		OperationID: "get-diff",
		Method:      http.MethodGet,
		Path:        "/api/diff",
		Summary:     "Diff two service versions",
		Description: "Compares two service versions and returns classified changes." + offlineDoc,
		Tags:        []string{"Diff"},
	}, offline[diffInput, getDiffOutput])
}

// ── Contract-view request/response types ─────────────────────────────

// ServiceNameInput is the path parameter for service-scoped endpoints.
type ServiceNameInput struct {
	Name string `path:"name" maxLength:"255" example:"order-service" doc:"Service name"`
}

type listServicesOutput struct {
	Body []ServiceListEntry `doc:"List of enriched services"`
}

type getServiceOutput struct {
	Body *ServiceDetails `doc:"Service details"`
}

type getVersionsOutput struct {
	Body []Version `doc:"Version history"`
}

type serviceVersionInput struct {
	Name    string `path:"name" maxLength:"255" example:"order-service" doc:"Service name"`
	Version string `path:"version" maxLength:"255" example:"1.2.0" doc:"Service version tag"`
}

type getServiceVersionOutput struct {
	Body *ServiceDetails `doc:"Service details at a specific version"`
}

type getServiceSourcesOutput struct {
	Body *AggregatedService `doc:"Per-source breakdown and merged view"`
}

type getGlobalGraphOutput struct {
	Body *GlobalGraph `doc:"Global dependency graph"`
}

type getDependentsOutput struct {
	Body []DependentInfo `doc:"Services that depend on this service"`
}

type getCrossRefsOutput struct {
	Body *CrossReferences `doc:"Config/policy cross-references"`
}

type diffInput struct {
	FromName    string `query:"from_name" required:"true" example:"order-service" doc:"Source service name"`
	FromVersion string `query:"from_version" example:"1.0.0" doc:"Source version"`
	ToName      string `query:"to_name" required:"true" example:"order-service" doc:"Target service name"`
	ToVersion   string `query:"to_version" example:"2.0.0" doc:"Target version"`
}

type getDiffOutput struct {
	Body *DiffResult `doc:"Classified diff between two versions"`
}
