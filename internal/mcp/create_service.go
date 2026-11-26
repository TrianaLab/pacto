package mcp

import (
	"context"
	"encoding/json"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// createServiceSentence names pacto_create_service in the instructions of
// an HTTP server that registers it.
const createServiceSentence = "This server also offers pacto_create_service, a registry write tool that plans a new service from a declared golden path " +
	"(a server started with pacto mcp --fleet lists them with pacto_fleet_entities template=only) with the values you set on the offerings it consumes: infrastructure only through offerings, " +
	"every change planned and checked before it applies. dry_run defaults to true; with dry_run false and the plan's intent_digest it writes a draft " +
	"a person reviews and signs in the dashboard, and it never publishes a version."

// CreateServiceOffering is the values pacto_create_service sets on one
// offering: once for every environment, and per environment the golden
// path declares.
type CreateServiceOffering struct {
	Offering     string                    `json:"offering"`
	Values       map[string]any            `json:"values,omitempty"`
	Environments map[string]map[string]any `json:"environments,omitempty"`
}

// CreateServiceArgs are pacto_create_service's arguments. Path is the
// service key of a declared golden path in the caller's view, never a
// reference: the server resolves the pinned reference itself.
type CreateServiceArgs struct {
	Path         string                  `json:"path"`
	Name         string                  `json:"name"`
	Owner        string                  `json:"owner,omitempty"`
	Namespace    string                  `json:"namespace"`
	Repository   string                  `json:"repository,omitempty"`
	Revision     string                  `json:"revision,omitempty"`
	Offerings    []CreateServiceOffering `json:"offerings,omitempty"`
	OnBehalfOf   string                  `json:"on_behalf_of,omitempty"`
	DryRun       *bool                   `json:"dry_run,omitempty"`
	IntentDigest string                  `json:"intent_digest,omitempty"`
}

// CreateServicer plans and writes created services for
// pacto_create_service, as Declarer does for pacto_declare. Plan returns the
// plan as JSON (intentDigest is its digest; a refused plan's refusals member
// holds every refused input at its field), Execute the run as JSON
// (executed, tag, published, draft, signed, acceptURL). A refusal is the
// *Refused, never an error.
type CreateServicer interface {
	Plan(ctx context.Context, a CreateServiceArgs) (json.RawMessage, *Refused, error)
	Execute(ctx context.Context, a CreateServiceArgs) (json.RawMessage, *Refused, error)
}

// createdRun is the part of a CreateServicer's JSON pacto_create_service
// reads.
type createdRun struct {
	IntentDigest string          `json:"intentDigest"`
	Refusals     json.RawMessage `json:"refusals"`
	Executed     bool            `json:"executed"`
	Tag          string          `json:"tag"`
	Published    string          `json:"published"`
	Draft        bool            `json:"draft"`
	Signed       bool            `json:"signed"`
	AcceptURL    string          `json:"acceptURL"`
}

// registerCreateService adds pacto_create_service, a registry write tool
// under the agent write contract: an HTTP server registers it only when it
// has a registry.
func registerCreateService(server *mcpsdk.Server, c CreateServicer) {
	addTool(server, &mcpsdk.Tool{
		Name:        "pacto_create_service",
		Annotations: writes(false, false, true),
		Description: "Plan a new service from a declared golden path: pacto.yaml at 0.1.0, contract only, nothing delivered. " +
			"Golden paths come from pacto_fleet_entities template=only (pass a row's key as path); offerings and their parameters come from " +
			"pacto_fleet_entities kind=offering consumable=true. Every value is checked against the offering's closed parameter space and tiers. " +
			"dry_run defaults to true and returns the plan with intent_digest and create.revision. With dry_run false, the same arguments, that intent_digest " +
			"and create.revision sent back as revision it writes a write-once draft-<n> " +
			"on behalf of the caller this server reviewed; acceptCLI is the dashboard page where a person reviews and signs it. It never publishes a version. " +
			"A refusal carries a stable code; a refused value names its field, the closest valid value and the pacto init and pacto_create_service fixes in refusals.",
		InputSchema: inputSchema(map[string]property{
			"path":       {Type: "string", Description: "The service key of a declared golden path in your view (a pacto_fleet_entities template=only row's key); never a reference"},
			"name":       {Type: "string", Description: "The new service's name, lower case", Pattern: serviceNamePattern},
			"owner":      {Type: "string", Description: "The team that owns the new service; the person who signs must belong to it or be a default approver", Pattern: ownerPattern},
			"namespace":  {Type: "string", Description: "The namespace you are reviewed in: create services.pacto.trianalab.io there, named by the service", Pattern: serviceNamePattern},
			"repository": {Type: "string", Description: "Where the first version goes: under the server's registry and ending in the service name (default <registry>/<name>)"},
			"revision":   {Type: "string", Description: "The publish revision (default pacto-create:<you>:<time>, which the plan answers as create.revision); a write sends the plan's back"},
			"offerings": {Type: "array", Items: "object", Description: "The values set on each offering the service consumes, as [{offering, values: {field: value}, " +
				"environments: {env: {field: value}}}]; an offering the golden path does not request is added; per-environment values only on tunable fields"},
			"on_behalf_of":  {Type: "string", Description: "Over HTTP the caller this server reviewed; naming anyone else is refused"},
			"dry_run":       {Type: "boolean", Description: "Plan only (default true)"},
			"intent_digest": {Type: "string", Description: "With dry_run false: the intent_digest of the plan you reviewed, sent with its create.revision as revision; a different plan is refused with PlanStale"},
		}, []string{"path", "name", "namespace"}),
		OutputSchema: writeResultSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a CreateServiceArgs) (*mcpsdk.CallToolResult, any, error) {
		return createService(ctx, c, a)
	})
}

// createService plans, or with dry_run false checks the intent digest
// against a fresh plan and writes the draft.
func createService(ctx context.Context, c CreateServicer, a CreateServiceArgs) (*mcpsdk.CallToolResult, any, error) {
	raw, refused, err := c.Plan(ctx, a)
	if err != nil {
		return errorResult(err), nil, nil
	}
	var plan createdRun
	_ = json.Unmarshal(raw, &plan) // ponytail: the CreateServicer marshals the plan it returns
	out := WriteResult{IntentDigest: plan.IntentDigest, Refusals: plan.Refusals}
	if len(raw) > 0 {
		out.Plan = raw
	}
	if refused == nil && !dryRun(a.DryRun) {
		refused = RequireIntent(plan.IntentDigest, a.IntentDigest)
	}
	if refused != nil || dryRun(a.DryRun) {
		return writeAnswer(out, refused, "")
	}
	if raw, refused, err = c.Execute(ctx, a); err != nil {
		return errorResult(err), nil, nil
	}
	var run createdRun
	_ = json.Unmarshal(raw, &run)
	out.Executed, out.AcceptCLI = run.Executed, run.AcceptURL
	if run.Tag != "" {
		out.Draft = &DraftRef{Tag: run.Tag, Published: run.Published, Signed: run.Signed}
	}
	if refused != nil {
		out.Refusals = run.Refusals
	}
	return writeAnswer(out, refused, "")
}
