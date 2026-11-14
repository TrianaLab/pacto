package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// DeliveryReads runs one dashboard delivery read route (caller, inbox, ...)
// with query q as the caller and returns its body verbatim, or the route's
// refusal as the error. The command layer runs the dashboard's own route code
// in process, so an agent reads exactly what a person reads.
type DeliveryReads func(ctx context.Context, route string, q url.Values) (json.RawMessage, error)

// deliveryReadsInstructions joins deliveryInstructions when the read tools
// are served.
const deliveryReadsInstructions = "pacto_delivery_caller, pacto_delivery_inbox, pacto_delivery_environments, " +
	"pacto_delivery_autonomy, pacto_delivery_published and pacto_delivery_refusals answer what the dashboard's " +
	"delivery views show, with your own identity: who you are to delivery, what waits on you, every Stage, the path " +
	"to autonomy, the versions the Stages know and the publishes Pacto refused. A list answer carries asOf and " +
	"completeness. It covers the namespaces you may list Stages in and leaves out, without naming them, those outside your " +
	"permissions; unreadable names a namespace that refused the list, and owners when no fleet tells whose Stages are yours."

// deliveryRead is one read tool: the route it runs and the route's query
// parameters, with the route's own names and descriptions.
type deliveryRead struct {
	route       string
	description string
	params      map[string]property
}

// Parameter descriptions several routes share, as the routes declare them.
const (
	readDomainDoc = "With service: the service's registry domain, empty for the default. A Stage that names another domain is left out; one that names none matches any"
	readOffsetDoc = "Where the page starts. Past the end answers the last page"
	readLimitDoc  = "How many per page; 0 means the list's default"
	readQDoc      = "Keep only what contains this text, ignoring case"
)

// page is the paging every list route but the inbox takes.
func page(more map[string]property) map[string]property {
	more["offset"] = property{Type: "integer", Description: readOffsetDoc}
	more["limit"] = property{Type: "integer", Description: readLimitDoc}
	more["q"] = property{Type: "string", Description: readQDoc}
	return more
}

// deliveryReadTools mirror the dashboard's delivery read routes one to one
// (TestDeliveryReadToolsMirrorRouteParams holds them to the OpenAPI document).
var deliveryReadTools = []deliveryRead{
	{route: "caller", description: "The caller as the API server names them, their actor class (human, agent, break-glass or controller), " +
		"when they last logged in and whether a question that names no team has nobody who may answer it."},
	{route: "inbox", description: "Held operations whose question names one of the caller's teams, with the decision asked and the affected services; " +
		"frozen Stages of the caller's services and the Errored operations on them.",
		params: map[string]property{
			"held":        {Type: "integer", Description: "Where the page of held changes starts"},
			"frozen":      {Type: "integer", Description: "Where the page of frozen Stages starts"},
			"errored":     {Type: "integer", Description: "Where the page of Errored changes starts"},
			"limit":       {Type: "integer", Description: "How many of each section per page; 0 means 25"},
			"q":           {Type: "string", Description: "Keep only what names this text (namespace, Stage, service or change), ignoring case"},
			"environment": {Type: "string", Description: "Keep only what waits to enter this environment: items on Stages whose spec.environment is this"},
			"except": {Type: "array", Items: "string", Description: "Leave out what waits to enter these environments (repeat the parameter); an empty value names held changes on " +
				"Stages the caller cannot read. Like environment, it never narrows byEnvironment"},
			"service": {Type: "string", Description: "Keep only the Stages of the service with exactly this name and the changes on them; it narrows byEnvironment too, " +
				"and a change on a Stage the caller cannot read is left out"},
			"domain": {Type: "string", Description: readDomainDoc},
		}},
	{route: "environments", description: "Service by environment: the digest and version each Stage runs and whether it is verified, failed, frozen, held or on auto-hold. " +
		"With service it also says, per Stage, which actions the caller may take and why, including freeze and undo.",
		params: page(map[string]property{
			"environment": {Type: "string", Description: "Keep only the services with a Stage in this environment, and only those Stages"},
			"tier": {Type: "string", Enum: []string{"dev", "test", "prod"}, Description: "Keep only the services with a Stage in an environment whose name says this tier " +
				"(inferred from the name, never an order), and only those Stages"},
			"state": {Type: "string", Enum: []string{"failed", "frozen", "autohold", "waiting", "unverified", "empty", "verified"},
				Description: "Keep only the services with a Stage in this state (the one counts.byEnvironment counts it in), and only those Stages"},
			"service": {Type: "string", Description: "Keep only the Stages of the service with exactly this name; with service, each row also offers freeze and undo"},
			"domain":  {Type: "string", Description: readDomainDoc},
			"except": {Type: "array", Items: "string", Description: "Leave out the Stages in these environments (repeat the parameter), and the services left with none: " +
				"the figure's folded station lists every environment but the ones it draws"},
		})},
	{route: "autonomy", description: "How many of the services Pacto has seen have a contract, how many promotions in each Stage's window no person asked for " +
		"or approved, each Stage's level with the next level Pacto proposes and its newest would-have decision, and the blockers with a fix " +
		"to copy. It only reads: a level moves through a proposed and approved operation.",
		params: page(map[string]property{
			"blockers": {Type: "integer", Description: "Where the page of blockers starts; limit and q apply to it too"},
			"level": {Type: "string", Enum: []string{"observe", "advise", "auto-nonbreaking", "autonomous", "none"},
				Description: "Keep only the Stages at this level; none keeps those that report no level the ladder knows. It narrows the Stages, never byLevel or the blockers"},
			"service": {Type: "string", Description: "Keep only the Stages of the service with exactly this name; it narrows byLevel too, never the blockers or promotionsWithoutPerson"},
			"domain":  {Type: "string", Description: readDomainDoc},
		})},
	{route: "published", description: "Per service the caller can see Stages of: the versions its Stages know, whether Flux verified their signature and which Stages run or verified each.",
		params: page(map[string]property{
			"service": {Type: "string", Description: "Keep only the versions of the service with exactly this name; it narrows counts too"},
			"domain":  {Type: "string", Description: readDomainDoc},
			"signed": {Type: "string", Enum: []string{"yes", "no", "unknown"}, Description: "yes: keep only the services whose every version is signed; " +
				"no: keep only the services with an unsigned version, and only those versions; unknown: keep only the services with no unsigned version " +
				"and a version whose signature Pacto cannot tell, and only those versions"},
		})},
	{route: "refusals", description: "Recent publishes Pacto refused, newest first, with the check that refused each and the version bump the change needs. " +
		"recorded false means this cluster keeps no record of refused publishes, so an empty list proves nothing."},
}

// registerDeliveryReads adds one read-only tool per delivery read route.
func registerDeliveryReads(server *mcpsdk.Server, reads DeliveryReads) {
	for _, r := range deliveryReadTools {
		addTool(server, &mcpsdk.Tool{
			Name:        "pacto_delivery_" + r.route,
			Annotations: readOnly(true),
			Description: r.description + " Read-only; runs with your own identity." + noApprove,
			InputSchema: inputSchema(r.params, nil),
		}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a map[string]any) (*mcpsdk.CallToolResult, any, error) {
			body, err := reads(ctx, r.route, queryOf(a))
			if err != nil {
				return errorResult(err), nil, nil
			}
			return textResult(string(body)), nil, nil
		})
	}
}

// queryOf is the route query the validated arguments a stand for: a number
// as the integer it is, a list as a repeated parameter, null as absent.
func queryOf(a map[string]any) url.Values {
	q := url.Values{}
	for k, v := range a {
		switch v := v.(type) {
		case string:
			q.Set(k, v)
		case float64:
			q.Set(k, strconv.FormatFloat(v, 'f', -1, 64))
		case []any:
			for _, e := range v {
				q.Add(k, fmt.Sprint(e))
			}
		}
	}
	return q
}

// DeliveryReadParam is one query parameter of a delivery read route: its
// name, JSON type (string, integer or array of strings), allowed values and
// description, as the route declares them.
type DeliveryReadParam struct {
	Name, Type, Description string
	Enum                    []string
}

// DeliveryReadParams are the query parameters of a delivery read route,
// sorted by name, so `pacto delivery <route>` takes the same ones the tool
// and the dashboard do. ok is false for a route that is not a read tool.
func DeliveryReadParams(route string) (params []DeliveryReadParam, description string, ok bool) {
	for _, r := range deliveryReadTools {
		if r.route != route {
			continue
		}
		for name, p := range r.params {
			params = append(params, DeliveryReadParam{Name: name, Type: p.Type, Description: p.Description, Enum: p.Enum})
		}
		slices.SortFunc(params, func(a, b DeliveryReadParam) int { return strings.Compare(a.Name, b.Name) })
		return params, r.description, true
	}
	return nil, "", false
}
