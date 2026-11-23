package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

const (
	planDigest  = "sha256:aaaa"
	otherDigest = "sha256:bbbb"
)

// stubDeclarer answers plans with a first declaration of invoices whose
// digest is planDigest, and records the calls; refuse refuses every plan,
// execRefuse every run and err fails both.
type stubDeclarer struct {
	plans, execs *[]DeclareArgs
	refuse       *Refused
	execRefuse   *Refused
	err          error
	execErr      error
	// unchanged plans a service unchanged since 1.4.0: no intent digest.
	unchanged bool
	// pr is a run's pullRequest and pullRequestError members, JSON.
	pr string
	// repository is the run plan's repository URL; declared runs write a
	// declared record, not a draft.
	repository string
	declared   bool
}

func (s stubDeclarer) Plan(_ context.Context, a DeclareArgs) (json.RawMessage, *Refused, error) {
	if s.plans != nil {
		*s.plans = append(*s.plans, a)
	}
	if s.err != nil {
		return nil, nil, s.err
	}
	if s.refuse != nil {
		return nil, s.refuse, nil
	}
	if s.unchanged {
		return json.RawMessage(`{"service":"invoices","unchanged":"1.4.0","intentDigest":""}`), nil, nil
	}
	return json.RawMessage(`{"service":"invoices","version":"0.1.0","state":"draft","intentDigest":"` + planDigest +
		`","acceptCLI":"pacto declare invoices --registry ghcr.io/acme --from-draft <tag>"}`), nil, nil
}

func (s stubDeclarer) Execute(_ context.Context, a DeclareArgs) (json.RawMessage, *Refused, error) {
	if s.execs != nil {
		*s.execs = append(*s.execs, a)
	}
	if s.execErr != nil {
		return nil, nil, s.execErr
	}
	repo, accept, tag := "", `,"acceptCLI":"pacto declare invoices --registry ghcr.io/acme --from-draft draft-1"`, `"draft-1","draft":true`
	if s.repository != "" {
		repo = `,"repository":{"url":"` + s.repository + `"}`
	}
	if s.declared {
		accept, tag = "", `"1.0.0"`
	}
	if s.unchanged {
		return json.RawMessage(`{"plan":{"service":"invoices","unchanged":"1.4.0"` + repo + `},"executed":false,"tag":"1.4.0"` + s.pr + `}`), s.execRefuse, nil
	}
	return json.RawMessage(`{"plan":{"service":"invoices","intentDigest":"` + planDigest + `"` + accept + repo + `},"executed":true,"tag":` + tag +
		`,"published":"ghcr.io/acme/invoices@sha256:dd"` + s.pr + `}`), s.execRefuse, nil
}

// A stdio run with pr answers the pull request it opened, or, keeping the
// draft it wrote, why the pull request failed and the command to retry.
func TestDeclarePullRequestAnswer(t *testing.T) {
	args := map[string]any{"service": "invoices", "registry": "ghcr.io/acme", "pr": true, "dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest}
	out, res := callDeclare(t, stubDeclarer{pr: `,"pullRequest":{"url":"https://github.com/acme/invoices/pull/7","login":"ana","branch":"b","tokenSource":"your GITHUB_TOKEN"}`}, args)
	if res.IsError || out.PullRequest == nil || out.PullRequest.URL != "https://github.com/acme/invoices/pull/7" || out.Draft == nil {
		t.Fatalf("%+v", out)
	}
	out, res = callDeclare(t, stubDeclarer{pr: `,"pullRequestError":{"code":"PullRequestFailed","message":"the repository is public"}`}, args)
	if !res.IsError || out.Refusal != nil || out.Draft == nil || out.PullRequestError == nil || out.PullRequestError.Code != RefusalPullRequestFailed ||
		out.PullRequestError.CLI != "pacto declare invoices --registry ghcr.io/acme --from-draft draft-1 --pr --yes" {
		t.Fatalf("%+v", out)
	}

	out, _ = callDeclare(t, stubDeclarer{pr: `,"pullRequestError":{"code":"PullRequestFailed","message":"m","cli":"own"}`}, args)
	if out.PullRequestError == nil || out.PullRequestError.CLI != "own" {
		t.Fatalf("%+v", out)
	}
}

// callDeclare calls pacto_declare on a stdio server with d and decodes its
// answer, error result or not.
func callDeclare(t *testing.T, d Declarer, args map[string]any) (WriteResult, *mcpsdk.CallToolResult) {
	t.Helper()
	res := callToolOn(t, mustNew("test", Authoring{Declare: d}, Modes{}), "pacto_declare", args)
	var out WriteResult
	_ = json.Unmarshal([]byte(resultText(t, res)), &out)
	return out, res
}

func TestDeclareDryRunByDefault(t *testing.T) {
	var plans, execs []DeclareArgs
	out, res := callDeclare(t, stubDeclarer{plans: &plans, execs: &execs}, map[string]any{"service": "invoices", "registry": "ghcr.io/acme",
		"accept": []string{"ledger"}, "namespace": "billing", "max_items": 5, "call_window": "2h"})
	if res.IsError || len(execs) != 0 || len(plans) != 1 || out.Executed || out.IntentDigest != planDigest ||
		out.AcceptCLI != "pacto declare invoices --registry ghcr.io/acme --from-draft <tag>" || out.Plan == nil {
		t.Fatalf("%+v %s", out, resultText(t, res))
	}
	if a := plans[0]; a.Namespace != "billing" || a.Registry != "ghcr.io/acme" || !slices.Equal(a.Accept, []string{"ledger"}) || a.MaxItems != 5 || a.CallWindow != "2h" {
		t.Fatalf("%+v", a)
	}
	if res.StructuredContent == nil {
		t.Fatal("no structured content")
	}
}

func TestDeclareExplicitDryRunPlans(t *testing.T) {
	var execs []DeclareArgs
	if out, _ := callDeclare(t, stubDeclarer{execs: &execs}, map[string]any{"service": "invoices", "dry_run": true}); len(execs) != 0 || out.Executed {
		t.Fatalf("%+v", out)
	}
}

func TestDeclareExecuteNeedsIntent(t *testing.T) {
	var execs []DeclareArgs
	out, res := callDeclare(t, stubDeclarer{execs: &execs}, map[string]any{"service": "invoices", "dry_run": false, "on_behalf_of": "ana"})
	if !res.IsError || len(execs) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalIntentRequired || out.IntentDigest != planDigest ||
		out.Refusal.CLI != "pacto declare invoices --registry <registry>" {
		t.Fatalf("%+v", out)
	}
}

func TestDeclarePlanStale(t *testing.T) {
	var execs []DeclareArgs
	out, _ := callDeclare(t, stubDeclarer{execs: &execs}, map[string]any{"service": "invoices", "dry_run": false, "on_behalf_of": "ana", "intent_digest": otherDigest})
	if len(execs) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalPlanStale || !strings.Contains(out.Refusal.Message, planDigest) {
		t.Fatalf("%+v", out)
	}
}

func TestDeclareExecutesDraft(t *testing.T) {
	var execs []DeclareArgs
	out, res := callDeclare(t, stubDeclarer{execs: &execs}, map[string]any{"service": "invoices", "dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest})
	if res.IsError || len(execs) != 1 || execs[0].OnBehalfOf != "ana" || !out.Executed || out.Draft == nil || out.Draft.Tag != "draft-1" ||
		out.Draft.Published != "ghcr.io/acme/invoices@sha256:dd" || out.Draft.Signed || out.AcceptCLI != "pacto declare invoices --registry ghcr.io/acme --from-draft draft-1" {
		t.Fatalf("%+v %s", out, resultText(t, res))
	}
	// A refused run keeps its code and its own command.
	out, res = callDeclare(t, stubDeclarer{execRefuse: &Refused{Code: RefusalVersionConflict, Message: "taken", CLI: "pacto declare x"}},
		map[string]any{"service": "invoices", "dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest})
	if !res.IsError || out.Refusal.Code != RefusalVersionConflict || out.Refusal.CLI != "pacto declare x" {
		t.Fatalf("%+v", out)
	}
}

func TestMCPDeclareNeedsOnBehalfOf(t *testing.T) {
	var plans []DeclareArgs
	_, res := callDeclare(t, stubDeclarer{plans: &plans}, map[string]any{"service": "invoices", "dry_run": false, "intent_digest": planDigest})
	if !res.IsError || len(plans) != 0 || !strings.Contains(resultText(t, res), "on_behalf_of is required") {
		t.Fatalf("%s", resultText(t, res))
	}
}

func TestDeclareErrors(t *testing.T) {
	_, res := callDeclare(t, stubDeclarer{err: errors.New("boom")}, map[string]any{"service": "invoices"})
	if !res.IsError || resultText(t, res) != "boom" {
		t.Fatalf("%s", resultText(t, res))
	}
	_, res = callDeclare(t, stubDeclarer{execErr: errors.New("push failed")},
		map[string]any{"service": "invoices", "dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest})
	if !res.IsError || resultText(t, res) != "push failed" {
		t.Fatalf("%s", resultText(t, res))
	}
}

func TestDeclareRefusalCarriesCodeAndCLI(t *testing.T) {
	args := map[string]any{"service": "billing/invoices", "namespace": "billing", "dir": "drafts/invoices", "registry": "ghcr.io/acme", "accept": []string{"ledger", "audit"}}
	const plan = "pacto declare billing/invoices --namespace billing --dir drafts/invoices --registry ghcr.io/acme --accept ledger --accept audit"
	prCLI := map[WriteRefusal]string{RefusalGitHostUnknown: plan + " --pr --git-host github|gitlab|bitbucket|gitea", RefusalGitTokenMissing: plan + " --pr",
		RefusalPullRequestFailed: plan + " --pr --yes", RefusalPullRequestNeedsLocalToken: plan + " --pr"}
	for _, code := range writeRefusals {
		out, res := callDeclare(t, stubDeclarer{refuse: &Refused{Code: code, Message: "refused"}}, args)
		if !res.IsError || out.Refusal == nil || out.Refusal.Code != code || out.Refusal.CLI != cmp.Or(prCLI[code], plan) {
			t.Errorf("%s: %+v", code, out)
		}
	}
	tools := listTools(t, mustNew("test", Authoring{Declare: stubDeclarer{}}, Modes{}))
	schema, _ := json.Marshal(tools["pacto_declare"].OutputSchema)
	for _, code := range writeRefusals {
		if !strings.Contains(string(schema), `"`+string(code)+`"`) {
			t.Errorf("the output schema does not list %s", code)
		}
	}
	if got := (DeclareArgs{Service: "invoices", Registry: "r"}).Command(planDigest); got != "pacto declare invoices --registry r --intent "+planDigest {
		t.Fatal(got)
	}
	// The pull request target is bound by the digest, so the command replays it.
	if got := (DeclareArgs{Service: "invoices", Registry: "r", PR: true, Repository: "https://github.com/acme/invoices", Folder: "deploy", RepoMap: "m.yaml"}).Command(""); got !=
		"pacto declare invoices --registry r --pr --repository https://github.com/acme/invoices --folder deploy --repo-map m.yaml" {
		t.Fatal(got)
	}
	// The read bounds change the plan, so the command replays them.
	if got := (DeclareArgs{Service: "invoices", Registry: "r", MaxItems: 50, CallWindow: "1h"}).Command(""); got != "pacto declare invoices --registry r --max-items 50 --call-window 1h" {
		t.Fatal(got)
	}
}

// An unchanged plan writes nothing and refuses nothing, whatever dry_run says.
// TestPullRequestFailedCLINamesRepository: the command names the
// repository the plan found, since a public one needs a person to name it;
// a draft's accepts the draft and a declared run's replays the declare.
func TestPullRequestFailedCLINamesRepository(t *testing.T) {
	args := map[string]any{"service": "invoices", "registry": "ghcr.io/acme", "pr": true, "dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest}
	repo := `,"pullRequestError":{"code":"PullRequestFailed","message":"the repository is public: confirm with --repository https://codeberg.org/acme/invoices"}`
	for declared, want := range map[bool]string{
		false: "pacto declare invoices --registry ghcr.io/acme --from-draft draft-1 --pr --repository https://codeberg.org/acme/invoices --yes",
		true:  "pacto declare invoices --registry ghcr.io/acme --pr --repository https://codeberg.org/acme/invoices --yes",
	} {
		out, _ := callDeclare(t, stubDeclarer{pr: repo, repository: "https://codeberg.org/acme/invoices", declared: declared}, args)
		if out.PullRequestError == nil || out.PullRequestError.CLI != want {
			t.Fatalf("declared %v: %+v", declared, out.PullRequestError)
		}
	}
}

// TestDeclareUnchangedOpensPullRequest: over stdio, pr on an unchanged
// service still runs, so the pull request exports the baseline's files;
// a dry run or an HTTP server writes nothing.
func TestDeclareUnchangedOpensPullRequest(t *testing.T) {
	var execs []DeclareArgs
	opened := `,"pullRequest":{"url":"https://github.com/acme/invoices/pull/8","login":"ana","branch":"pacto/declare-invoices-1.4.0","tokenSource":"your GITHUB_TOKEN"}`
	args := map[string]any{"service": "invoices", "registry": "ghcr.io/acme", "pr": true, "dry_run": false, "on_behalf_of": "ana"}
	out, res := callDeclare(t, stubDeclarer{execs: &execs, unchanged: true, pr: opened}, args)
	if res.IsError || out.Refusal != nil || len(execs) != 1 || out.PullRequest == nil || out.PullRequest.Branch != "pacto/declare-invoices-1.4.0" {
		t.Fatalf("%+v", out)
	}
	args["dry_run"] = true
	if out, _ := callDeclare(t, stubDeclarer{execs: &execs, unchanged: true, pr: opened}, args); len(execs) != 1 || out.PullRequest != nil {
		t.Fatalf("a dry run opened it: %+v", out)
	}
}

func TestDeclareUnchangedIsNoRefusal(t *testing.T) {
	for _, dry := range []bool{true, false} {
		var execs []DeclareArgs
		out, res := callDeclare(t, stubDeclarer{execs: &execs, unchanged: true}, map[string]any{"service": "invoices", "dry_run": dry, "on_behalf_of": "ana", "intent_digest": otherDigest})
		if res.IsError || out.Refusal != nil || out.Executed || len(execs) != 0 || out.Plan == nil {
			t.Errorf("dry_run %v: %+v", dry, out)
		}
	}
}

// TestRefusalEnumMatchesApp: every declare refusal code internal/app
// defines is in the enum, and the enum holds nothing else but this
// contract's own codes.
func TestRefusalEnumMatchesApp(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "../app/declare.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// internal/gitexport's codes come back from a stdio run's pull request.
	g, err := parser.ParseFile(token.NewFileSet(), "../gitexport/gitexport.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var app []string
	for _, file := range []struct {
		f      *ast.File
		prefix string
	}{{f, "code"}, {g, "Code"}} {
		ast.Inspect(file.f, func(n ast.Node) bool {
			if v, ok := n.(*ast.ValueSpec); ok && strings.HasPrefix(v.Names[0].Name, file.prefix) && len(v.Values) == 1 {
				if lit, ok := v.Values[0].(*ast.BasicLit); ok {
					s, _ := strconv.Unquote(lit.Value)
					app = append(app, s)
				}
			}
			return true
		})
	}
	want := append(append(app, string(RefusalIntentRequired), string(RefusalExecuteNeedsStdio), string(RefusalNotAllowed), string(RefusalReviewFailed),
		string(RefusalPullRequestNeedsLocalToken)), delivery.OwnerRightCodes...)
	got := make([]string, len(writeRefusals))
	for i, c := range writeRefusals {
		got[i] = string(c)
	}
	slices.Sort(want)
	slices.Sort(got)
	if len(app) < 10 || !slices.Equal(got, want) {
		t.Fatalf("enum %v\napp + own %v", got, want)
	}
}

// TestDeclareToolRefusalCodesEnum: pacto_declare's output schema lists the
// owner rule's four codes, spelled as pkg/delivery defines them.
func TestDeclareToolRefusalCodesEnum(t *testing.T) {
	tools := listTools(t, mustNew("test", Authoring{Declare: stubDeclarer{}}, Modes{}))
	raw, _ := json.Marshal(tools["pacto_declare"].OutputSchema)
	var schema struct {
		Properties struct {
			Refusal struct {
				Properties struct {
					Code struct {
						Enum []string `json:"enum"`
					} `json:"code"`
				} `json:"properties"`
			} `json:"refusal"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(raw, &schema)
	enum := schema.Properties.Refusal.Properties.Code.Enum
	if len(enum) < 4 {
		t.Fatalf("%s", raw)
	}
	if got := enum[len(enum)-4:]; !slices.Equal(got, delivery.OwnerRightCodes) {
		t.Fatalf("%v", enum)
	}
}

func TestDeclareNamedOnlyWhenRegistered(t *testing.T) {
	for _, a := range []Authoring{{}, {Remote: true}} {
		if strings.Contains(baseFor(a), "pacto_declare") || slices.Contains(authoringNames(a), "pacto_declare") {
			t.Errorf("%+v names pacto_declare", a)
		}
		if listTools(t, mustNew("test", a, Modes{}))["pacto_declare"] != nil {
			t.Errorf("%+v registers pacto_declare", a)
		}
		a.Declare = stubDeclarer{}
		if !strings.Contains(baseFor(a), "pacto_declare") || !slices.Contains(authoringNames(a), "pacto_declare") {
			t.Errorf("%+v does not name pacto_declare", a)
		}
	}
	remote := listTools(t, mustNew("test", Authoring{Remote: true, Declare: stubDeclarer{}}, Modes{}))
	if !strings.Contains(remote["pacto_declare"].InputSchema.(map[string]any)["properties"].(map[string]any)["dir"].(map[string]any)["description"].(string), "Refused over HTTP") {
		t.Errorf("%+v", remote["pacto_declare"].InputSchema)
	}
}

func TestMCPDeclareDraftArgument(t *testing.T) {
	var plans, execs []DeclareArgs
	out, res := callDeclare(t, stubDeclarer{plans: &plans, execs: &execs}, map[string]any{"service": "invoices", "draft": true,
		"dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest})
	if res.IsError || len(plans) != 1 || !plans[0].Draft || len(execs) != 1 || !execs[0].Draft || out.Draft == nil {
		t.Fatalf("%+v %+v %s", plans, execs, resultText(t, res))
	}
	stdio := listTools(t, mustNew("test", Authoring{Declare: stubDeclarer{}}, Modes{}))
	if !strings.Contains(stdio["pacto_declare"].InputSchema.(map[string]any)["properties"].(map[string]any)["draft"].(map[string]any)["description"].(string), "without a person's login") {
		t.Errorf("%+v", stdio["pacto_declare"].InputSchema)
	}
}

// TestMCPDeclareHTTPDraftArgumentNeedsRegistry: over HTTP draft is refused
// unless the server has a registry, and on_behalf_of is the reviewed caller.
func TestMCPDeclareHTTPDraftArgumentNeedsRegistry(t *testing.T) {
	remote := listTools(t, mustNew("test", Authoring{Remote: true, Declare: stubDeclarer{}}, Modes{}))
	props := remote["pacto_declare"].InputSchema.(map[string]any)["properties"].(map[string]any)
	if !strings.Contains(props["draft"].(map[string]any)["description"].(string), "refused when the server has no registry") ||
		!strings.Contains(props["on_behalf_of"].(map[string]any)["description"].(string), "the caller this server reviewed") {
		t.Errorf("%+v", props)
	}
	if desc := remote["pacto_declare"].Description; !strings.Contains(desc, "over HTTP it writes that draft only when the server has a registry") {
		t.Error(desc)
	}
}

// TestMCPDeclareHTTPWritesWithoutOnBehalfOf: over HTTP the server names who
// a draft is for, so a write needs no on_behalf_of; a run that answers no
// plan carries its own accept command.
func TestMCPDeclareHTTPWritesWithoutOnBehalfOf(t *testing.T) {
	var execs []DeclareArgs
	d := planOnlyRun{stubDeclarer{execs: &execs}}
	res := callToolOn(t, mustNew("test", Authoring{Remote: true, Declare: d}, Modes{}), "pacto_declare",
		map[string]any{"service": "invoices", "dry_run": false, "intent_digest": planDigest})
	var out WriteResult
	_ = json.Unmarshal([]byte(resultText(t, res)), &out)
	if res.IsError || len(execs) != 1 || out.Draft == nil || out.Plan == nil || out.AcceptCLI != "pacto declare invoices --registry r --from-draft draft-2" {
		t.Fatalf("%+v %s", out, resultText(t, res))
	}
}

// planOnlyRun is a stubDeclarer whose run answers no plan.
type planOnlyRun struct{ stubDeclarer }

func (p planOnlyRun) Execute(ctx context.Context, a DeclareArgs) (json.RawMessage, *Refused, error) {
	_, _, _ = p.stubDeclarer.Execute(ctx, a)
	return json.RawMessage(`{"executed":true,"tag":"draft-2","draft":true,"acceptCLI":"pacto declare invoices --registry r --from-draft draft-2"}`), nil, nil
}
