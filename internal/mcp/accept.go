package mcp

import (
	"context"
	"encoding/json"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// acceptSentence and remoteAcceptSentence name pacto_accept in the
// instructions of a server that registers it.
const (
	acceptSentence = "Use pacto_accept to plan accepting the dependency ranges pacto ci proposed for a published service, or a break a provider announces " +
		"for it (dry_run defaults to true and returns the plan with its intent_digest and who may accept); with dry_run false and that intent_digest " +
		"it writes an acceptance draft a person accepts with pacto accept --draft, and it never signs an acceptance."
	remoteAcceptSentence = "This server also offers pacto_accept, which plans an acceptance; it writes nothing here, and its refusal names " +
		"the pacto accept command a person runs."
)

// AcceptArgs are pacto_accept's arguments.
type AcceptArgs struct {
	Service      string   `json:"service"`
	Proposed     bool     `json:"proposed"`
	Fields       []string `json:"fields"`
	Break        string   `json:"break"`
	From         string   `json:"from"`
	OnBehalfOf   string   `json:"on_behalf_of"`
	DryRun       *bool    `json:"dry_run"`
	IntentDigest string   `json:"intent_digest"`
}

// Command is the pacto accept a person runs for a, with the intent digest
// when intent is set.
func (a AcceptArgs) Command(intent string) string {
	parts := []string{"pacto accept", a.Service}
	if a.Proposed {
		parts = append(parts, "--proposed")
	}
	for _, f := range a.Fields {
		parts = append(parts, "--field '"+f+"'")
	}
	if a.Break != "" {
		parts = append(parts, "--break", a.Break)
	}
	if a.From != "" {
		parts = append(parts, "--from", a.From)
	}
	if intent != "" {
		parts = append(parts, "--intent", intent)
	}
	return strings.Join(parts, " ")
}

// Acceptor plans and drafts acceptances for pacto_accept. The command layer
// supplies it: Plan returns the plan as JSON (its intentDigest member is the
// plan's digest), Execute the run as JSON (plan, executed, signer, draft,
// acceptCli). A refusal is the *Refused, never an error.
type Acceptor interface {
	Plan(ctx context.Context, a AcceptArgs) (json.RawMessage, *Refused, error)
	Execute(ctx context.Context, a AcceptArgs) (json.RawMessage, *Refused, error)
}

// acceptedRun is the part of an Acceptor's JSON pacto_accept reads.
type acceptedRun struct {
	IntentDigest string          `json:"intentDigest"`
	Plan         json.RawMessage `json:"plan"`
	Executed     bool            `json:"executed"`
	Draft        string          `json:"draft"`
	AcceptCLI    string          `json:"acceptCli"`
	Signer       struct {
		Source string `json:"source"`
	} `json:"signer"`
}

// registerAccept adds pacto_accept under the agent write contract. Every
// MCP caller is an agent, so a write is always an acceptance draft; remote
// says the server answers over HTTP, where it only plans.
func registerAccept(server *mcpsdk.Server, acc Acceptor, remote bool) {
	addTool(server, &mcpsdk.Tool{
		Name:        "pacto_accept",
		Annotations: writes(false, false, true),
		Description: "Plan accepting, for the newest published version of a service, the dependency ranges pacto ci proposed in its inferred record " +
			"(proposed, or fields by key) or a break a provider announces for it (break). dry_run defaults to true and returns the plan with intent_digest " +
			"and who may accept. With dry_run false and that intent_digest it writes a write-once pacto-accept-draft-<n> for a person to accept with " +
			"pacto accept <service> --draft <ref>; it never signs an acceptance. A refusal carries a stable code and the pacto accept command a person runs.",
		InputSchema: inputSchema(map[string]property{
			"service":       {Type: "string", Description: "The accepting service: its bundle repository (oci://host/path) or its checkout folder (stdio only)"},
			"proposed":      {Type: "boolean", Description: "Accept every dependency range the newest published version proposes"},
			"fields":        {Type: "array", Items: "string", Description: "Proposed fields to accept, as dependencies[<name>].compatibility (optionally =<range>) or dependencies[<name>].ref"},
			"break":         {Type: "string", Description: "Accept the break this provider version announces, as <provider>@<version>"},
			"from":          {Type: "string", Description: "The provider's checkout or oci:// ref to read an unpublished break from"},
			"on_behalf_of":  {Type: "string", Description: "Required with dry_run false: the person you act for (asserted, never verified)"},
			"dry_run":       {Type: "boolean", Description: "Plan only (default true)"},
			"intent_digest": {Type: "string", Description: "With dry_run false: the intent_digest of the plan you reviewed; a different plan is refused with PlanStale"},
		}, []string{"service"}),
		OutputSchema: writeResultSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a AcceptArgs) (*mcpsdk.CallToolResult, any, error) {
		return accept(ctx, acc, a, remote)
	})
}

// accept plans, or with dry_run false checks the intent digest against a
// fresh plan and writes the acceptance draft. Over HTTP a write is refused
// with the command a person runs.
func accept(ctx context.Context, acc Acceptor, a AcceptArgs, remote bool) (*mcpsdk.CallToolResult, any, error) {
	if !dryRun(a.DryRun) && a.OnBehalfOf == "" {
		return errorResult(errOnBehalfOfRequired), nil, nil
	}
	raw, refused, err := acc.Plan(ctx, a)
	if err != nil {
		return errorResult(err), nil, nil
	}
	var plan acceptedRun
	_ = json.Unmarshal(raw, &plan) // ponytail: the Acceptor marshals the plan it returns
	out := WriteResult{IntentDigest: plan.IntentDigest}
	if len(raw) > 0 {
		out.Plan = raw
	}
	if refused == nil && !dryRun(a.DryRun) {
		refused = requireIntent(plan.IntentDigest, a.IntentDigest)
	}
	if refused == nil && !dryRun(a.DryRun) && remote {
		refused = &Refused{Code: RefusalExecuteNeedsStdio, Message: "this HTTP server writes no acceptance: run the command on your machine; " +
			"it prints the same plan and signs it as you", CLI: a.Command(plan.IntentDigest)}
	}
	if refused != nil || dryRun(a.DryRun) {
		return writeAnswer(out, refused, a.Command(""))
	}
	if raw, refused, err = acc.Execute(ctx, a); err != nil {
		return errorResult(err), nil, nil
	}
	var run acceptedRun
	_ = json.Unmarshal(raw, &run)
	if len(run.Plan) > 0 {
		out.Plan = run.Plan
	}
	out.Executed, out.AcceptCLI = run.Executed, run.AcceptCLI
	if run.Draft != "" {
		out.Draft = &DraftRef{Tag: run.Draft[strings.LastIndex(run.Draft, ":")+1:], Signed: run.Signer.Source != "none"}
	}
	return writeAnswer(out, refused, a.Command(""))
}
