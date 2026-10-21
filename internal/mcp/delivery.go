package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// DeliveryAPI is what the delivery tools call. The command layer adapts
// app.Delivery to it, so this package never reaches a cluster or a registry.
type DeliveryAPI interface {
	Plan(ctx context.Context, req delivery.Request) (delivery.Plan, error)
	Propose(ctx context.Context, req delivery.Request) (*delivery.OperationObject, delivery.Plan, error)
	Get(ctx context.Context, namespace, name string) (*delivery.OperationObject, error)
	Cancel(ctx context.Context, namespace, name string) error
	Explain(ctx context.Context, namespace, name string) (*delivery.Explanation, error)
	History(ctx context.Context, namespace, stage string) (*delivery.History, error)
}

// noApprove is in every delivery tool description, so an agent never goes
// looking for a way around it.
const noApprove = " There is no approve tool: when a person must decide, the plan names who, and they approve themselves."

const deliveryInstructions = "Pacto delivery decides which version each environment (a Stage) runs. Use " +
	"pacto_delivery_operation_types and pacto_delivery_operation_describe to learn what you may propose, " +
	"pacto_delivery_operation_plan to see what the promoter will do (verdicts, whether a person is needed, " +
	"the one question they are asked), then propose with the plan's intent digest and follow it with " +
	"pacto_delivery_get. pacto_delivery_explain shows every verdict with the citation behind it and how to run it " +
	"again, and pacto_delivery_history what happened on a Stage. Unknown is never Pass: an operation Pacto cannot " +
	"judge waits for a person. A verdict you cite as a cause is run again by the promoter and refused if it does not hold. " +
	"When a consumer running in the environment does not accept the new version, the plan's order.moveFirst names that " +
	"consumer, its Stage there and, as candidate, a version of it its upstream Stage verified that accepts both versions: " +
	"plan and propose that promote on the consumer's Stage first, then plan this one again. Pacto never moves the other " +
	"service for you, and order.unknown says what it could not see." + noApprove

// NewDeliveryServer builds a server with the authoring tools and the delivery
// tools. writes registers propose and cancel; the command layer passes true
// only when the caller is not a human identity, so a person's credentials in an
// agent's hands propose nothing through this server.
func NewDeliveryServer(version string, api DeliveryAPI, writes bool, validate Validator) *mcpsdk.Server {
	server := newServer(version, baseInstructions+"\n\n"+deliveryInstructions, validate)
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "pacto_delivery_operation_types",
		Description: "List the operation types you may propose, with their facet and who may raise them. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{}, nil),
	}, func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, any, error) {
		r, err := jsonResult(delivery.Types())
		return r, nil, err
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "pacto_delivery_operation_describe",
		Description: "Describe one operation type: what it checks, when a person is needed and how it is undone. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{"type": {Type: "string", Description: "Type id, e.g. pacto.dev/promote"}}, []string{"type"}),
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Type string `json:"type"`
	}) (*mcpsdk.CallToolResult, any, error) {
		t, ok := delivery.TypeByID(a.Type)
		if !ok {
			return errorResult(fmt.Errorf("unknown operation type %q", a.Type)), nil, nil
		}
		r, err := jsonResult(struct {
			delivery.OperationType
			Digest string `json:"digest"`
		}{t, t.Digest()})
		return r, nil, err
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_operation_plan",
		Description: "Plan an operation without doing it: the intent digest, the running version, the predicted " +
			"verdicts of every gate, whether a person must decide, the one question they would be asked and, when a consumer " +
			"running there does not accept the new version, the order: which consumer must move first and the verified " +
			"version of it to promote. Read-only." + noApprove,
		InputSchema: requestSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a requestArgs) (*mcpsdk.CallToolResult, any, error) {
		req, err := a.request()
		if err != nil {
			return errorResult(err), nil, nil
		}
		p, err := api.Plan(ctx, req)
		return resultOrError(p, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "pacto_delivery_get",
		Description: "Read an operation: phase, outcome, verdicts and the question a person is asked while it is held. Read-only." + noApprove,
		InputSchema: nameSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a nameArgs) (*mcpsdk.CallToolResult, any, error) {
		op, err := api.Get(ctx, a.Namespace, a.Name)
		return resultOrError(op, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_explain",
		Description: "Explain an operation: what was asked, where it is, every gate verdict with its citation (for a health " +
			"check, the exact query with absolute time bounds) and how to run that citation again, plus the Stage as it is now " +
			"and its last re-check, and what must move first when the plan found one. Read-only." + noApprove,
		InputSchema: nameSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a nameArgs) (*mcpsdk.CallToolResult, any, error) {
		e, err := api.Explain(ctx, a.Namespace, a.Name)
		return resultOrError(e, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_history",
		Description: "List what happened on a Stage: every operation still in the cluster in stream order, then the ones in " +
			"flight. The answer says asOf and completeness: it is partial, because a deleted operation is gone until signed " +
			"records exist. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{
			"namespace": {Type: "string", Description: "Namespace of the Stage"},
			"stage":     {Type: "string", Description: "Stage name"},
		}, []string{"namespace", "stage"}),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Namespace string `json:"namespace"`
		Stage     string `json:"stage"`
	}) (*mcpsdk.CallToolResult, any, error) {
		h, err := api.History(ctx, a.Namespace, a.Stage)
		return resultOrError(h, err)
	})
	if writes {
		registerDeliveryWrites(server, api)
	}
	return server
}

func registerDeliveryWrites(server *mcpsdk.Server, api DeliveryAPI) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_operation_propose",
		Description: "Propose an operation. Pass the intent_digest from your plan: if anything moved since, it fails " +
			"with PlanStale and returns the fresh plan instead of proposing something you did not review." + noApprove,
		InputSchema: requestSchema("intent_digest", "name"),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a requestArgs) (*mcpsdk.CallToolResult, any, error) {
		req, err := a.request()
		if err != nil {
			return errorResult(err), nil, nil
		}
		op, plan, err := api.Propose(ctx, req)
		if err != nil && plan.IntentDigest != "" {
			// PlanStale (or a refused create) after planning: hand back the fresh plan.
			r, jerr := jsonResult(struct {
				Error string        `json:"error"`
				Plan  delivery.Plan `json:"plan"`
			}{err.Error(), plan})
			r.IsError = true
			return r, nil, jerr
		}
		return resultOrError(struct {
			Operation *delivery.OperationObject `json:"operation"`
			Plan      delivery.Plan             `json:"plan"`
		}{op, plan}, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "pacto_delivery_cancel",
		Description: "Cancel an operation that has not moved the pointer yet. Once it has, cancel is refused: propose a rollback." + noApprove,
		InputSchema: nameSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a nameArgs) (*mcpsdk.CallToolResult, any, error) {
		err := api.Cancel(ctx, a.Namespace, a.Name)
		return resultOrError(map[string]string{"cancelRequested": a.Namespace + "/" + a.Name}, err)
	})
}

func resultOrError(v any, err error) (*mcpsdk.CallToolResult, any, error) {
	if err != nil {
		return errorResult(err), nil, nil
	}
	r, err := jsonResult(v)
	return r, nil, err
}

type requestArgs struct {
	Namespace    string `json:"namespace"`
	Stage        string `json:"stage"`
	Type         string `json:"type"`
	To           string `json:"to"`
	From         string `json:"from"`
	Undoes       string `json:"undoes"`
	Reason       string `json:"reason"`
	OnBehalfOf   string `json:"on_behalf_of"`
	IntentDigest string `json:"intent_digest"`
	Name         string `json:"name"`
	// CauseVerdict is a verdict the caller cites as its cause, as JSON.
	CauseVerdict string `json:"cause_verdict"`
}

func (a requestArgs) request() (delivery.Request, error) {
	req := delivery.Request{Namespace: a.Namespace, Stage: a.Stage, Type: a.Type, To: a.To, From: a.From, Undoes: a.Undoes,
		Reason: a.Reason, OnBehalfOf: a.OnBehalfOf, IntentDigest: a.IntentDigest, Name: a.Name}
	if a.CauseVerdict != "" {
		var v delivery.Verdict
		if err := json.Unmarshal([]byte(a.CauseVerdict), &v); err != nil {
			return req, fmt.Errorf("cause_verdict is not a verdict: %w", err)
		}
		req.Cause = &delivery.Cause{Verdict: &v}
	}
	return req, nil
}

// requestSchema is the plan input, plus the extra propose-only fields.
func requestSchema(extra ...string) map[string]any {
	props := map[string]property{
		"namespace":    {Type: "string", Description: "Namespace of the Stage"},
		"stage":        {Type: "string", Description: "Stage name"},
		"type":         {Type: "string", Description: "Operation type (default pacto.dev/promote)"},
		"to":           {Type: "string", Description: "Artifact digest to move to (sha256:...); a tag is refused"},
		"from":         {Type: "string", Description: "Digest the Stage runs now (defaults to its pointer)"},
		"undoes":       {Type: "string", Description: "For a rollback: the name of the operation it undoes"},
		"reason":       {Type: "string", Description: "Why, for the record"},
		"on_behalf_of": {Type: "string", Description: "The person you act for (asserted, not verified)"},
		"cause_verdict": {Type: "string", Description: "A verdict you cite as the cause, as JSON (a verdict from pacto_delivery_explain); " +
			"the promoter runs it again and refuses a cause that does not hold"},
	}
	for _, e := range extra {
		switch e {
		case "intent_digest":
			props[e] = property{Type: "string", Description: "The intent digest from your plan"}
		case "name":
			props[e] = property{Type: "string", Description: "Operation name (generated when empty)"}
		}
	}
	return inputSchema(props, []string{"namespace", "stage"})
}

type nameArgs struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func nameSchema() map[string]any {
	return inputSchema(map[string]property{
		"namespace": {Type: "string", Description: "Namespace of the operation"},
		"name":      {Type: "string", Description: "Operation name"},
	}, []string{"namespace", "name"})
}
