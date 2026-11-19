package mcp

import (
	"context"
	"encoding/json"
	"errors"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// declareSentence and remoteDeclareSentence name pacto_declare in the
// instructions of a server that registers it.
const (
	declareSentence = "Use pacto_declare to plan declaring a running workload as a contract-only version (dry_run defaults to true and returns the plan " +
		"with its intent_digest); with dry_run false and that intent_digest it writes a draft a person accepts, and it never publishes a declared version."
	remoteDeclareSentence = "This server also offers pacto_declare, a registry write tool that plans declaring a running workload with your own cluster " +
		"credentials; it writes nothing here, and its refusal names the pacto declare command a person runs."
)

// errOnBehalfOfRequired refuses a draft that names nobody it is written for.
var errOnBehalfOfRequired = errors.New("on_behalf_of is required to write a draft: name the person you act for " +
	"(asserted, never verified, and never a service account)")

// registerDeclare adds pacto_declare, the first tool of the agent write
// contract. Every MCP caller is an agent, so a write is always a draft;
// remote says the server answers over HTTP, where it only plans.
func registerDeclare(server *mcpsdk.Server, d Declarer, remote bool) {
	dir := "A folder pacto init --cluster wrote, declared instead of a new draft (stdio only)"
	if remote {
		dir = "Refused over HTTP: the folder would be this server's"
	}
	addTool(server, &mcpsdk.Tool{
		Name:        "pacto_declare",
		Annotations: writes(false, false, true),
		Description: "Plan declaring a running workload as a contract-only version. dry_run defaults to true and returns the plan with intent_digest. " +
			"With dry_run false and that intent_digest it writes a write-once draft-<n> for a person to accept with pacto declare <service> --from-draft <tag>; " +
			"it never publishes a declared version. A refusal carries a stable code and the pacto declare command a person runs.",
		InputSchema: inputSchema(map[string]property{
			"service":       {Type: "string", Description: "The workload: name, namespace/name, namespace/Kind/name or k8s:namespace/Kind/name"},
			"namespace":     {Type: "string", Description: "The namespace of the workload (default: every namespace the name is found in)"},
			"dir":           {Type: "string", Description: dir},
			"registry":      {Type: "string", Description: "The registry the contract is published to, as host/path"},
			"accept":        {Type: "array", Items: "string", Description: "Dependencies whose proposed range the draft requests as declared"},
			"on_behalf_of":  {Type: "string", Description: "Required with dry_run false: the person you act for (asserted, never verified)"},
			"dry_run":       {Type: "boolean", Description: "Plan only (default true)"},
			"intent_digest": {Type: "string", Description: "With dry_run false: the intent_digest of the plan you reviewed; a different plan is refused with PlanStale"},
			"max_items":     {Type: "integer", Description: "The most items read per kind of object (default 10000)"},
			"call_window":   {Type: "string", Description: "How far back a call counts as a dependency, as a Go duration (default 24h)"},
		}, []string{"service"}),
		OutputSchema: writeResultSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a DeclareArgs) (*mcpsdk.CallToolResult, any, error) {
		return declare(ctx, d, a)
	})
}

// declare plans, or with dry_run false checks the intent digest against a
// fresh plan and writes the draft.
func declare(ctx context.Context, d Declarer, a DeclareArgs) (*mcpsdk.CallToolResult, any, error) {
	if !dryRun(a.DryRun) && a.OnBehalfOf == "" {
		return errorResult(errOnBehalfOfRequired), nil, nil
	}
	raw, refused, err := d.Plan(ctx, a)
	if err != nil {
		return errorResult(err), nil, nil
	}
	var plan declaredRun
	_ = json.Unmarshal(raw, &plan) // ponytail: the Declarer marshals the plan it returns
	out := WriteResult{IntentDigest: plan.IntentDigest, AcceptCLI: plan.AcceptCLI}
	if len(raw) > 0 {
		out.Plan = raw
	}
	if refused == nil && !dryRun(a.DryRun) {
		refused = requireIntent(plan.IntentDigest, a.IntentDigest)
	}
	if refused != nil || dryRun(a.DryRun) {
		return writeAnswer(out, refused, a)
	}
	if raw, refused, err = d.Execute(ctx, a); err != nil {
		return errorResult(err), nil, nil
	}
	var run declaredRun
	_ = json.Unmarshal(raw, &run)
	if len(run.Plan) > 0 {
		out.Plan = run.Plan
	}
	var p declaredRun
	_ = json.Unmarshal(run.Plan, &p)
	out.Executed, out.AcceptCLI = run.Executed, p.AcceptCLI
	if run.Draft {
		out.Draft = &DraftRef{Tag: run.Tag, Published: run.Published, Signed: run.Signed}
	}
	return writeAnswer(out, refused, a)
}

// writeAnswer is out as the tool's result: a refusal is an error result
// that still carries the plan, its code and the command a person runs (the
// plan command for a refusal that names none).
func writeAnswer(out WriteResult, refused *Refused, a DeclareArgs) (*mcpsdk.CallToolResult, any, error) {
	if refused == nil {
		return nil, out, nil
	}
	if refused.CLI == "" {
		refused.CLI = a.Command("")
	}
	out.Refusal = refused
	return &mcpsdk.CallToolResult{IsError: true}, out, nil
}
