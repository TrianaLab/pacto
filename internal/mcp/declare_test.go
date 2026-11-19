package mcp

import (
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
	want := append(app, string(RefusalIntentRequired), string(RefusalExecuteNeedsStdio))
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
