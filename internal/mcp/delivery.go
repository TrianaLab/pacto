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
	Blame(ctx context.Context, namespace, stage, digest string) (*delivery.Blame, error)
	Undo(ctx context.Context, namespace, name, stage string) (*delivery.Undo, error)
	VerifyRecords(ctx context.Context, namespace, stage string) (*delivery.StreamVerification, error)
	// HistoryAll and VerifyAll walk every Stage in namespace, or in every
	// namespace the caller can list for "".
	HistoryAll(ctx context.Context, namespace string) (*delivery.Histories, error)
	VerifyAll(ctx context.Context, namespace string) (*delivery.StreamVerifications, error)
}

// noApprove is in every delivery tool description, so an agent never goes
// looking for a way around it.
const noApprove = " There is no approve tool: when a person must decide, the plan names who, and they approve themselves."

const deliveryInstructions = "Pacto delivery decides which version each environment (a Stage) runs. Use " +
	"pacto_delivery_operation_types to learn what you may propose (with a type, what it checks), " +
	"pacto_delivery_operation_plan to see what the promoter will do (verdicts, whether a person is needed, " +
	"the one question they are asked), then propose with the plan's intent digest and follow it with " +
	"pacto_delivery_get. pacto_delivery_explain shows every verdict with the citation behind it and how to run it " +
	"again, and pacto_delivery_history what happened on a Stage, read from its signed records when the recorder runs. " +
	"pacto_delivery_blame says who put a version in place, pacto_delivery_undo_plan returns the plan that would undo an operation " +
	"(it proposes nothing) and pacto_delivery_record_verify checks every record's signature and the chain. Every history answer " +
	"carries asOf and completeness: when it is partial, a change missing from it may still have happened. A plan's precedent " +
	"says what the records attached to the version say happened to it on other Stages. Unknown is never Pass: an operation Pacto cannot " +
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
		Name: "pacto_delivery_operation_types",
		Description: "List the operation types you may propose, with their facet and who may raise them, or with type set " +
			"describe that one: what it checks, when a person is needed, how it is undone and its digest. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{"type": {Type: "string", Description: "Type id to describe, e.g. delivery.pacto.trianalab.io/promote; empty lists them all"}}, nil),
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Type string `json:"type"`
	}) (*mcpsdk.CallToolResult, any, error) {
		if a.Type == "" {
			r, err := jsonResult(delivery.Types())
			return r, nil, err
		}
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
			"check, the exact query with absolute time bounds) and how to run that citation again, plus the Stage as it is now, " +
			"its last re-check and what must move first when the plan found one. Read-only." + noApprove,
		InputSchema: nameSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a nameArgs) (*mcpsdk.CallToolResult, any, error) {
		e, err := api.Explain(ctx, a.Namespace, a.Name)
		return resultOrError(e, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_history",
		Description: "List what happened on a Stage. With the recorder installed it is read from the Stage's signed records in the " +
			"registry (source records), plus the operations not recorded yet; otherwise from the operations still in the cluster " +
			"(source status, always partial). The answer says asOf, completeness, problems (missing seqs, forks, hidden changes) " +
			"and what was unreadable: when completeness is partial, a change missing from it may still have happened. Leave stage " +
			"empty to walk every Stage in the namespace (or, with no namespace, every Stage you can list) for a fleet audit; that " +
			"answer is complete only when every Stage was read and every history is complete. Read-only." + noApprove,
		InputSchema: auditSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a stageArgs) (*mcpsdk.CallToolResult, any, error) {
		if a.Stage == "" {
			return resultOrError(api.HistoryAll(ctx, a.Namespace))
		}
		h, err := api.History(ctx, a.Namespace, a.Stage)
		return resultOrError(h, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_blame",
		Description: "Say who put a version in place on a Stage (by default the one it runs): the newest change that moved the " +
			"Stage to it, who asked, on whose behalf (asserted), why, who approved and what every gate said, from the records. " +
			"No change with completeness partial means unknown, never nobody. Read-only." + noApprove,
		InputSchema: stageSchema("digest"),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a stageArgs) (*mcpsdk.CallToolResult, any, error) {
		b, err := api.Blame(ctx, a.Namespace, a.Stage, a.Digest)
		return resultOrError(b, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_undo_plan",
		Description: "Plan how to undo an operation and propose nothing: a rollback bound to it when it is the change the Stage " +
			"runs now, a promote back to the version it replaced when something changed since (every gate runs), or nothing " +
			"when it moved nothing. To act, review the plan and propose it yourself with its intent digest. Read-only." + noApprove,
		InputSchema: inputSchema(map[string]property{
			"namespace": {Type: "string", Description: "Namespace of the operation"},
			"name":      {Type: "string", Description: "Operation name"},
			"stage":     {Type: "string", Description: "Its Stage, needed only when the Operation was deleted and must be found in the records"},
		}, []string{"namespace", "name"}),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Stage     string `json:"stage"`
	}) (*mcpsdk.CallToolResult, any, error) {
		u, err := api.Undo(ctx, a.Namespace, a.Name, a.Stage)
		return resultOrError(u, err)
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "pacto_delivery_record_verify",
		Description: "Verify every signed record of a Stage's stream against the trust config this server was started with and " +
			"check the chain from seq 1 to the head the Stage names: ok, the records, problems and what did not verify, plus the " +
			"eval runners' own signatures on the verdicts each record keeps (who ran each exam, checked against the runner keys of the trust config), and the " +
			"cosign command that checks one record without Pacto. Leave stage empty to verify every Stage's stream in the " +
			"namespace (or, with no namespace, every Stage you can list); a Stage with no records does not verify. Read-only." + noApprove,
		InputSchema: auditSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a stageArgs) (*mcpsdk.CallToolResult, any, error) {
		if a.Stage == "" {
			return resultOrError(api.VerifyAll(ctx, a.Namespace))
		}
		v, err := api.VerifyRecords(ctx, a.Namespace, a.Stage)
		return resultOrError(v, err)
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
		"type":         {Type: "string", Description: "Operation type (default delivery.pacto.trianalab.io/promote)"},
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

type stageArgs struct {
	Namespace string `json:"namespace"`
	Stage     string `json:"stage"`
	Digest    string `json:"digest"`
}

// stageSchema is a Stage input, with digest for blame.
func stageSchema(extra ...string) map[string]any {
	props := map[string]property{
		"namespace": {Type: "string", Description: "Namespace of the Stage"},
		"stage":     {Type: "string", Description: "Stage name"},
	}
	if len(extra) > 0 {
		props["digest"] = property{Type: "string", Description: "The version (sha256:...) to blame; defaults to the one the Stage runs"}
	}
	return inputSchema(props, []string{"namespace", "stage"})
}

// auditSchema is one Stage, or with stage empty every Stage in the
// namespace, or with both empty every Stage the caller can list.
func auditSchema() map[string]any {
	return inputSchema(map[string]property{
		"namespace": {Type: "string", Description: "Namespace of the Stage; empty with no stage walks every namespace you can list"},
		"stage":     {Type: "string", Description: "Stage name; empty walks every Stage"},
	}, nil)
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
