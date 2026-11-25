package dashboard

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ServiceCreator plans and carries out creating a service from a declared
// golden path for the person behind a request: the host reviews them in the
// namespace they name, signs with their fresh login or saves a draft, and writes
// only into the created service's repository and the organization index. The
// route has already resolved the golden path in the viewer's restricted view
// and refused callers with no identity, so nothing the request names is
// pulled.
type ServiceCreator interface {
	// Plan is what creating the service from p would publish, or accepting
	// in.FromDraft.
	Plan(r *http.Request, p CreateServicePath, in CreateServiceInput) (*CreateServiceAnswer, error)
	// Accept signs the planned first version or saves it as a draft.
	Accept(r *http.Request, p CreateServicePath, in CreateServiceAcceptInput) (*CreateServiceAcceptAnswer, error)
	// CanWrite reports whether the host can write at all.
	CanWrite() bool
}

// CreateServicePath is the declared golden path a create runs from, as the
// viewer's restricted view resolves it, and that view: the consumable
// offerings are read from it alone.
type CreateServicePath struct {
	fleet.CreatablePath
	View *fleet.Query
}

// CreateServiceOffering is the values a create sets on one offering.
type CreateServiceOffering struct {
	Offering     string                    `json:"offering" minLength:"1" maxLength:"63" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" doc:"The offering, as the consumable offerings list names it"`
	Values       map[string]any            `json:"values,omitempty" doc:"The values set once for every environment, by field"`
	Environments map[string]map[string]any `json:"environments,omitempty" doc:"The values set per environment the golden path declares, by environment then field; only tunable fields"`
}

// CreateServiceInput asks for a create plan. It names the golden path by
// its service key, never by a reference: the server resolves the pinned
// reference from the caller's view.
type CreateServiceInput struct {
	Path       string                  `json:"path" minLength:"1" maxLength:"512" doc:"The service key of the declared golden path to create from"`
	Name       string                  `json:"name" minLength:"1" maxLength:"63" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" doc:"The new service's name"`
	Owner      string                  `json:"owner,omitempty" maxLength:"253" pattern:"^[a-zA-Z0-9._/-]+$" doc:"The team that owns the new service; you must belong to it or be a default approver"`
	Namespace  string                  `json:"namespace" minLength:"1" maxLength:"63" pattern:"^[a-z0-9]([a-z0-9-]*[a-z0-9])?$" doc:"The namespace you are reviewed in: create services.pacto.trianalab.io there, named by the service"`
	Cluster    string                  `json:"cluster,omitempty" maxLength:"253" doc:"The cluster the review runs in; empty for the only one"`
	Repository string                  `json:"repository,omitempty" maxLength:"512" doc:"Where the first version goes: under the dashboard's registry and ending in the service name; defaults to <registry>/<name>"`
	Revision   string                  `json:"revision,omitempty" maxLength:"128" doc:"The publish revision; defaults to pacto-create:<you>:<time>"`
	Offerings  []CreateServiceOffering `json:"offerings,omitempty" maxItems:"16" doc:"The values set on each offering the service consumes; an offering the golden path does not request is added"`
	FromDraft  *DraftRef               `json:"fromDraft,omitempty" doc:"Plan accepting this created draft instead: its inputs are read from it and checked again against your view"`
	// IntentDigest names the plan a signing login was started for.
	IntentDigest string `json:"intentDigest,omitempty" maxLength:"128" doc:"The intent a signing login was started for"`
}

// CreateServiceAcceptInput carries out a create plan the person reviewed:
// the same inputs, its intent digest and the mode.
type CreateServiceAcceptInput struct {
	CreateServiceInput
	Mode string `json:"mode" enum:"sign,draft" doc:"sign: publish 0.1.0 signed by your fresh login; draft: save a draft-<n> a person signs later"`
}

// CreateServiceRefusal is one refused input of a create, at its field, with
// the closest valid value and the pacto init and pacto_create_service fixes.
type CreateServiceRefusal struct {
	Code    string `json:"code" enum:"OfferingNotConsumable,OfferingUnknown,ParameterNotDeclared,ParameterOutOfRange,ParameterSensitive,ClassNotOffered,TierNotTunable,EnvironmentNotDeclared"`
	Message string `json:"message"`
	Field   string `json:"field" doc:"configurations[<name>].values.<key>, environments[<env>].<configuration>.<key>, environments[<env>] or offerings[<offering>]"`
	Closest any    `json:"closest,omitempty" doc:"The closest valid value, when there is one"`
	Allowed []any  `json:"allowed,omitempty" doc:"The values an enum or class allows"`
	CLI     string `json:"cli,omitempty" doc:"The pacto init command that sets the fix"`
	MCP     string `json:"mcp,omitempty" doc:"The pacto_create_service argument fragment that sets the fix"`
}

// CreateServiceFile is one file the first version publishes.
type CreateServiceFile struct {
	Path string `json:"path"`
	DeclareText
}

// MaxCreateServiceFiles bounds the files a create answer carries.
const MaxCreateServiceFiles = 64

// CreateServiceOfferingView is one offering a create may set values on:
// its classes, fields and guarantees as the consumable read gives them.
type CreateServiceOfferingView struct {
	Offering   string                      `json:"offering"`
	Version    string                      `json:"version"`
	Requested  bool                        `json:"requested" doc:"The golden path requests it; any other is added when a value is set on it"`
	Classes    []string                    `json:"classes"`
	Parameters []fleet.OfferingParameter   `json:"parameters"`
	Guarantees []fleet.ConsumableGuarantee `json:"guarantees,omitempty" doc:"What each interface of the offering's provider promises; empty when it declares none"`
	// The totals behind the bounded classes and parameters.
	ClassesTotal        int  `json:"classesTotal"`
	ClassesTruncated    bool `json:"classesTruncated,omitempty"`
	ParametersTotal     int  `json:"parametersTotal"`
	ParametersTruncated bool `json:"parametersTruncated,omitempty" doc:"More fields than are listed: set the rest with pacto init --values or pacto_create_service"`
}

// CreateServicePlan is what a create would publish, signed by whom.
type CreateServicePlan struct {
	Service      string              `json:"service"`
	Version      string              `json:"version"`
	BundleRepo   string              `json:"bundleRepo"`
	State        string              `json:"state" doc:"declared for a person's login, else draft"`
	Owner        string              `json:"owner,omitempty"`
	MayAccept    []string            `json:"mayAccept,omitempty" doc:"Who may create the service with this owner"`
	Signer       DeclareSigner       `json:"signer"`
	PolicyNote   string              `json:"policyNote,omitempty"`
	IntentDigest string              `json:"intentDigest"`
	Notes        []string            `json:"notes,omitempty"`
	Path         string              `json:"path" doc:"The golden path's digest-pinned reference"`
	PathName     string              `json:"pathName"`
	PathVersion  string              `json:"pathVersion"`
	Signature    fleet.CreationStep  `json:"signature" doc:"The golden path's signature step, checked against the installation's template keys"`
	Namespace    string              `json:"namespace"`
	Revision     string              `json:"revision"`
	InitCLI      string              `json:"initCli" doc:"The pacto init that recreates the folder on your machine"`
	ValuesFile   *CreateServiceFile  `json:"valuesFile,omitempty" doc:"The values file InitCLI reads"`
	Files        []CreateServiceFile `json:"files,omitempty" doc:"Every other file it would publish, the environment values files included"`
	FilesTotal   int                 `json:"filesTotal,omitempty"`
	// ConsumableAsOf and ConsumableCompleteness are the consumable read's.
	ConsumableAsOf         string      `json:"consumableAsOf,omitempty"`
	ConsumableCompleteness string      `json:"consumableCompleteness"`
	Contract               DeclareText `json:"contract" doc:"pacto.yaml as it would be published"`
	Record                 DeclareText `json:"record" doc:"pacto.inferred.yaml as it would be published"`
}

// CreateServiceAnswer is a create plan, the offerings it may set values on
// and what this person can do with it here.
type CreateServiceAnswer struct {
	Plan      *CreateServicePlan          `json:"plan,omitempty" doc:"Absent when the plan was refused"`
	Offerings []CreateServiceOfferingView `json:"offerings" doc:"The offerings you may consume, the requested ones first"`
	// ConsumableCompleteness and ConsumableAsOf are the offerings read's.
	ConsumableCompleteness string                 `json:"consumableCompleteness"`
	ConsumableAsOf         string                 `json:"consumableAsOf,omitempty"`
	Drafts                 []DeclareDraft         `json:"drafts,omitempty" doc:"Your own drafts of this service, newest first"`
	DraftsTotal            int                    `json:"draftsTotal,omitempty"`
	Signing                DeclareSigning         `json:"signing"`
	Write                  DeclareWrite           `json:"write"`
	SignTicket             string                 `json:"signTicket,omitempty" doc:"Starts the signing login once: /auth/sign?ticket=<signTicket>&return=<page>"`
	SignedIn               bool                   `json:"signedIn,omitempty"`
	Refusal                *DeclareRefusal        `json:"refusal,omitempty"`
	Refusals               []CreateServiceRefusal `json:"refusals,omitempty" doc:"Every refused input, each at its field; a refused plan has no intent digest"`
}

// CreateServiceAcceptAnswer is what accepting a create plan wrote, the
// person's own handover, or why nothing was written.
type CreateServiceAcceptAnswer struct {
	Published     string                 `json:"published,omitempty" doc:"The bundle written, as <repository>@sha256:<hex>"`
	Tag           string                 `json:"tag,omitempty"`
	Draft         bool                   `json:"draft,omitempty"`
	Signed        bool                   `json:"signed,omitempty"`
	AcceptURL     string                 `json:"acceptUrl,omitempty" doc:"The page a person reviews and signs the draft at"`
	InitCLI       string                 `json:"initCli,omitempty" doc:"The pacto init that recreates the folder on your machine"`
	ValuesFile    *CreateServiceFile     `json:"valuesFile,omitempty" doc:"The values file InitCLI reads, beside it"`
	GitCLI        string                 `json:"gitCli,omitempty" doc:"Makes the repository with your own Git token; Pacto holds none"`
	PullCLI       string                 `json:"pullCli,omitempty" doc:"Pulls the published bundle"`
	Files         []CreateServiceFile    `json:"files,omitempty" doc:"The published files, at most 64 of at most 64 KiB each"`
	FilesTotal    int                    `json:"filesTotal,omitempty"`
	Index         string                 `json:"index,omitempty" doc:"Where the organization index entry went, or why it was not written: the publish stands either way"`
	AppearsWithin string                 `json:"appearsWithin,omitempty" doc:"How soon the fleet shows the service declared at 0.1.0; empty when its index entry was not written"`
	Refusal       *DeclareRefusal        `json:"refusal,omitempty"`
	Refusals      []CreateServiceRefusal `json:"refusals,omitempty"`
	Plan          *CreateServiceAnswer   `json:"plan,omitempty" doc:"The fresh plan of a PlanStale refusal"`
}

type createServicePlanInput struct{ Body CreateServiceInput }
type createServicePlanOutput struct{ Body CreateServiceAnswer }
type createServiceAcceptInput struct{ Body CreateServiceAcceptInput }
type createServiceAcceptOutput struct{ Body CreateServiceAcceptAnswer }

// registerCreateServiceOperations registers the plan route with a creator,
// and the accept route only when it can also write. GET of the plan path
// stays the fleet search.
func (s *Server) registerCreateServiceOperations(api huma.API) {
	c := s.opts.CreateService
	if c == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-create-service",
		Method:      http.MethodPost,
		Path:        "/api/fleet/services",
		Summary:     "Plan creating a service from a golden path",
		Description: "Plans the first version of a service created from a declared golden path in your view: pacto.yaml at 0.1.0, " +
			"the offering values checked against each offering's closed schema, the repository, the revision and who signs. " +
			"It writes nothing. It needs create services.pacto.trianalab.io in the namespace, named by the service.",
		Tags: []string{"Fleet"},
	}, func(ctx context.Context, in *createServicePlanInput) (*createServicePlanOutput, error) {
		p, err := s.creatablePath(ctx, in.Body.Path)
		if err != nil {
			return nil, err
		}
		ans, err := c.Plan(requestFrom(ctx), p, in.Body)
		if err != nil {
			return nil, err
		}
		return &createServicePlanOutput{Body: *ans}, nil
	})
	if !c.CanWrite() {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-create-service-accept",
		Method:      http.MethodPost,
		Path:        "/api/fleet/services/accept",
		Summary:     "Sign a created service or save it as a draft",
		Description: "Carries out a create plan the person reviewed, by its intent digest: publishes 0.1.0 signed by the person's " +
			"fresh login and writes its organization index entry, saves a draft-<n> a person signs later, or accepts a draft. " +
			"Pacto holds no Git token: the answer hands over the files and the commands.",
		Tags: []string{"Fleet"},
	}, func(ctx context.Context, in *createServiceAcceptInput) (*createServiceAcceptOutput, error) {
		p, err := s.creatablePath(ctx, in.Body.Path)
		if err != nil {
			return nil, err
		}
		ans, err := c.Accept(requestFrom(ctx), p, in.Body)
		if err != nil {
			return nil, err
		}
		return &createServiceAcceptOutput{Body: *ans}, nil
	})
}

// creatablePath is the declared golden path key names in the viewer's
// restricted view, after the caller's identity is checked: a hidden path
// answers as a missing one.
func (s *Server) creatablePath(ctx context.Context, key string) (CreateServicePath, error) {
	if err := s.declareIdentity(ctx); err != nil {
		return CreateServicePath{}, err
	}
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return CreateServicePath{}, err
	}
	p, why := q.CreatablePath(fleet.ServiceKey(key))
	if why == fleet.UnconfirmedCreatablePath {
		return CreateServicePath{}, Refuse(http.StatusNotFound, "GoldenPathUnknown", why)
	}
	if why != "" {
		return CreateServicePath{}, Refuse(http.StatusNotFound, "GoldenPathNotDeclared", why)
	}
	return CreateServicePath{CreatablePath: p, View: q}, nil
}
