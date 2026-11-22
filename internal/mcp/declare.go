package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// declareSentence and remoteDeclareSentence name pacto_declare in the
// instructions of a server that registers it.
const (
	declareSentence = "Use pacto_declare to plan declaring a running workload as a contract-only version (dry_run defaults to true and returns the plan " +
		"with its intent_digest); with dry_run false and that intent_digest it writes a draft a person accepts, and it never publishes a declared version. " +
		"With pr, a stdio server opens a pull request with the Git token in its environment; over HTTP pr is refused with the CLI command."
	remoteDeclareSentence = "This server also offers pacto_declare, a registry write tool that plans declaring a running workload with your own cluster " +
		"credentials; when the server has a registry to write to, dry_run false writes an unsigned draft on your behalf for a person to accept, " +
		"otherwise its refusal names the pacto declare command a person runs."
)

// errOnBehalfOfRequired refuses a draft that names nobody it is written for.
var errOnBehalfOfRequired = errors.New("on_behalf_of is required to write a draft: name the person you act for " +
	"(asserted, never verified, and never a service account)")

// registerDeclare adds pacto_declare, the first tool of the agent write
// contract. Every MCP caller is an agent, so a write is always a draft;
// remote says the server answers over HTTP, where it writes a draft only
// when the server has a registry of its own, on behalf of the caller it
// reviewed.
func registerDeclare(server *mcpsdk.Server, d Declarer, remote bool) {
	dir := "A folder pacto init --cluster wrote, declared instead of a new draft (stdio only)"
	draft := "Write a draft-<n> a person accepts later, without a person's login: signed only by an ambient OIDC token when one is set"
	onBehalfOf := "Required with dry_run false: the person you act for (asserted, never verified)"
	pr := "After the write, open a pull request that commits the bundle's pacto.yaml and pacto.inferred.yaml with the Git token in this server's environment; " +
		"an unchanged service exports its baseline's files. A retry writes a new draft: a failed pull request's cli is what a person runs instead"
	repoMap := "A YAML file mapping service names to {repository, folder}, read only with pr (stdio only)"
	if remote {
		pr = "Refused over HTTP with PullRequestNeedsLocalToken: a pull request runs only where your Git token is"
		repoMap = "Refused over HTTP: the file would be this server's"
		dir = "Refused over HTTP: the folder would be this server's"
		draft = "Over HTTP every write is a draft; refused when the server has no registry"
		onBehalfOf = "Over HTTP the caller this server reviewed; naming anyone else is refused"
	}
	addTool(server, &mcpsdk.Tool{
		Name:        "pacto_declare",
		Annotations: writes(false, false, true),
		Description: "Plan declaring a running workload as a contract-only version. dry_run defaults to true and returns the plan with intent_digest. " +
			"With dry_run false and that intent_digest it writes a write-once draft-<n> for a person to accept with pacto declare <service> --from-draft <tag>; " +
			"over HTTP it writes that draft only when the server has a registry, and otherwise refuses with ExecuteNeedsStdio. " +
			"With pr, a stdio server opens a pull request with the Git token in its environment; over HTTP pr is refused with the CLI command. " +
			"It never publishes a declared version. A refusal carries a stable code and the pacto declare command a person runs.",
		InputSchema: inputSchema(map[string]property{
			"service":       {Type: "string", Description: "The workload: name, namespace/name, namespace/Kind/name or k8s:namespace/Kind/name"},
			"namespace":     {Type: "string", Description: "The namespace of the workload (default: every namespace the name is found in)"},
			"dir":           {Type: "string", Description: dir},
			"registry":      {Type: "string", Description: "The registry the contract is published to, as host/path"},
			"accept":        {Type: "array", Items: "string", Description: "Dependencies whose proposed range the draft requests as declared"},
			"on_behalf_of":  {Type: "string", Description: onBehalfOf},
			"dry_run":       {Type: "boolean", Description: "Plan only (default true)"},
			"intent_digest": {Type: "string", Description: "With dry_run false: the intent_digest of the plan you reviewed; a different plan is refused with PlanStale"},
			"max_items":     {Type: "integer", Description: "The most items read per kind of object (default 10000)"},
			"call_window":   {Type: "string", Description: "How far back a call counts as a dependency, as a Go duration (default 24h)"},
			"draft":         {Type: "boolean", Description: draft},
			"pr":            {Type: "boolean", Description: pr},
			"repository":    {Type: "string", Description: "The service's source repository URL, used over every source Pacto reads; with pr a signed provenance or GitOps source must name it too"},
			"folder":        {Type: "string", Description: "The folder of the repository the contract lives in (default: the folder a source names, else the root)"},
			"repo_map":      {Type: "string", Description: repoMap},
		}, []string{"service"}),
		OutputSchema: writeResultSchema(),
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, a DeclareArgs) (*mcpsdk.CallToolResult, any, error) {
		return declare(ctx, d, a, remote)
	})
}

// declare plans, or with dry_run false checks the intent digest against a
// fresh plan and writes the draft. Over HTTP (remote) the server names who
// the draft is for: the caller it reviewed.
func declare(ctx context.Context, d Declarer, a DeclareArgs, remote bool) (*mcpsdk.CallToolResult, any, error) {
	if !dryRun(a.DryRun) && a.OnBehalfOf == "" && !remote {
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
	if refused == nil && plan.Unchanged != "" {
		if !unchangedPR(a, remote) {
			return writeAnswer(out, nil, "") // nothing changed: nothing to write, whatever dry_run says
		}
	} else if refused == nil && !dryRun(a.DryRun) {
		refused = requireIntent(plan.IntentDigest, a.IntentDigest)
	}
	if refused != nil || dryRun(a.DryRun) {
		return writeAnswer(out, refused, refusalCLI(a, refused))
	}
	if raw, refused, err = d.Execute(ctx, a); err != nil {
		return errorResult(err), nil, nil
	}
	return executed(out, raw, refused, a)
}

// unchangedPR is whether a run of an unchanged plan still runs: over stdio
// with pr and dry_run false, its pull request exports the baseline's files.
func unchangedPR(a DeclareArgs, remote bool) bool { return a.PR && !remote && !dryRun(a.DryRun) }

// executed is pacto_declare's answer to a run: what it wrote, its pull
// request, or why that failed after the write.
func executed(out WriteResult, raw json.RawMessage, refused *Refused, a DeclareArgs) (*mcpsdk.CallToolResult, any, error) {
	var run declaredRun
	_ = json.Unmarshal(raw, &run)
	if len(run.Plan) > 0 {
		out.Plan = run.Plan
	}
	var p declaredRun
	_ = json.Unmarshal(run.Plan, &p)
	out.Executed, out.AcceptCLI = run.Executed, cmp.Or(p.AcceptCLI, run.AcceptCLI)
	if run.Draft {
		out.Draft = &DraftRef{Tag: run.Tag, Published: run.Published, Signed: run.Signed}
	}
	out.PullRequest = run.PullRequest
	if e := run.PullRequestError; e != nil && refused == nil {
		// The write stays: only the pull request failed.
		if e.CLI == "" {
			e.CLI = pullRequestCLI(a, p, out, e.Code)
		}
		out.PullRequestError = e
		return &mcpsdk.CallToolResult{IsError: true}, out, nil
	}
	return writeAnswer(out, refused, refusalCLI(a, refused))
}

// pullRequestCLI is the command a person runs after a's pull request
// failed with no command of its own: it names the repository the plan found
// (a public one needs a person to name it), and for a draft it accepts the
// draft rather than declaring anew.
func pullRequestCLI(a DeclareArgs, p declaredRun, out WriteResult, code WriteRefusal) string {
	if p.Repository != nil && a.Repository == "" {
		a.Repository = p.Repository.URL
	}
	if code == RefusalPullRequestFailed && out.Draft != nil && out.AcceptCLI != "" {
		cli := out.AcceptCLI + " --pr"
		if a.Repository != "" {
			cli += " --repository " + a.Repository
		}
		return cli + " --yes"
	}
	return a.RefusalCLI(code)
}

// refusalCLI is the command a person runs about refused, a refusal of a.
func refusalCLI(a DeclareArgs, refused *Refused) string {
	if refused == nil {
		return ""
	}
	return a.RefusalCLI(refused.Code)
}

// writeAnswer is out as the tool's result: a refusal is an error result
// that still carries the plan, its code and the command a person runs (the
// plan command, cli, for a refusal that names none).
func writeAnswer(out WriteResult, refused *Refused, cli string) (*mcpsdk.CallToolResult, any, error) {
	if refused == nil {
		return nil, out, nil
	}
	if refused.CLI == "" {
		refused.CLI = cli
	}
	out.Refusal = refused
	return &mcpsdk.CallToolResult{IsError: true}, out, nil
}
