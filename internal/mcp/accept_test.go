package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// stubAcceptor answers plans of reports whose digest is planDigest and
// drafts with an unsigned pacto-accept-draft-1; refuse refuses every plan,
// err fails both.
type stubAcceptor struct {
	plans, execs *[]AcceptArgs
	refuse       *Refused
	err, execErr error
	signed       bool
}

func (s stubAcceptor) Plan(_ context.Context, a AcceptArgs) (json.RawMessage, *Refused, error) {
	if s.plans != nil {
		*s.plans = append(*s.plans, a)
	}
	if s.err != nil {
		return nil, nil, s.err
	}
	if s.refuse != nil {
		return nil, s.refuse, nil
	}
	return json.RawMessage(`{"service":"ghcr.io/acme/reports","version":"1.6.0","whoMayAccept":"ana@acme.com","intentDigest":"` + planDigest + `"}`), nil, nil
}

func (s stubAcceptor) Execute(_ context.Context, a AcceptArgs) (json.RawMessage, *Refused, error) {
	if s.execs != nil {
		*s.execs = append(*s.execs, a)
	}
	if s.execErr != nil {
		return nil, nil, s.execErr
	}
	source := "none"
	if s.signed {
		source = "ambient"
	}
	return json.RawMessage(`{"plan":{"service":"ghcr.io/acme/reports","intentDigest":"` + planDigest + `"},"executed":true,"signer":{"class":"ci","source":"` +
		source + `"},"draft":"ghcr.io/acme/reports:pacto-accept-draft-1","acceptCli":"pacto accept oci://ghcr.io/acme/reports --draft ghcr.io/acme/reports:pacto-accept-draft-1"}`), nil, nil
}

func callAccept(t *testing.T, a Authoring, args map[string]any) (WriteResult, *mcpsdk.CallToolResult) {
	t.Helper()
	res := callToolOn(t, mustNew("test", a, Modes{}), "pacto_accept", args)
	var out WriteResult
	_ = json.Unmarshal([]byte(resultText(t, res)), &out)
	return out, res
}

const reportsRef = "oci://ghcr.io/acme/reports"

func TestMCPAcceptDefaultsToPlan(t *testing.T) {
	var plans, execs []AcceptArgs
	out, res := callAccept(t, Authoring{Accept: stubAcceptor{plans: &plans, execs: &execs}}, map[string]any{"service": reportsRef, "proposed": true,
		"fields": []string{"dependencies[ledger].ref"}})
	if res.IsError || len(execs) != 0 || len(plans) != 1 || out.Executed || out.IntentDigest != planDigest || !strings.Contains(string(mustJSON(t, out.Plan)), "whoMayAccept") {
		t.Fatalf("%+v %s", out, resultText(t, res))
	}
	if a := plans[0]; !a.Proposed || !slices.Equal(a.Fields, []string{"dependencies[ledger].ref"}) {
		t.Fatalf("%+v", a)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMCPAcceptExecuteNeedsDigest(t *testing.T) {
	var execs []AcceptArgs
	out, res := callAccept(t, Authoring{Accept: stubAcceptor{execs: &execs}}, map[string]any{"service": reportsRef, "proposed": true, "dry_run": false, "on_behalf_of": "ana"})
	if !res.IsError || len(execs) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalIntentRequired || out.Refusal.CLI != "pacto accept "+reportsRef+" --proposed" {
		t.Fatalf("%+v", out)
	}
}

func TestMCPAcceptPlanStale(t *testing.T) {
	var execs []AcceptArgs
	out, _ := callAccept(t, Authoring{Accept: stubAcceptor{execs: &execs}}, map[string]any{"service": reportsRef, "proposed": true, "dry_run": false,
		"on_behalf_of": "ana", "intent_digest": otherDigest})
	if len(execs) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalPlanStale {
		t.Fatalf("%+v", out)
	}
}

func TestMCPAcceptAsAgentWritesDraft(t *testing.T) {
	for _, signed := range []bool{false, true} {
		var execs []AcceptArgs
		out, res := callAccept(t, Authoring{Accept: stubAcceptor{execs: &execs, signed: signed}}, map[string]any{"service": reportsRef, "proposed": true,
			"dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest})
		if res.IsError || len(execs) != 1 || execs[0].OnBehalfOf != "ana" || !out.Executed || out.Draft == nil || out.Draft.Tag != "pacto-accept-draft-1" ||
			out.Draft.Signed != signed || !strings.Contains(out.AcceptCLI, "--draft ghcr.io/acme/reports:pacto-accept-draft-1") {
			t.Fatalf("%+v %s", out, resultText(t, res))
		}
	}
}

func TestMCPAcceptNeedsOnBehalfOf(t *testing.T) {
	var plans []AcceptArgs
	_, res := callAccept(t, Authoring{Accept: stubAcceptor{plans: &plans}}, map[string]any{"service": reportsRef, "dry_run": false, "intent_digest": planDigest})
	if !res.IsError || len(plans) != 0 || !strings.Contains(resultText(t, res), "on_behalf_of is required") {
		t.Fatalf("%s", resultText(t, res))
	}
}

func TestMCPAcceptHTTPExecuteNeedsStdio(t *testing.T) {
	var execs []AcceptArgs
	out, res := callAccept(t, Authoring{Remote: true, Accept: stubAcceptor{execs: &execs}}, map[string]any{"service": reportsRef, "break": "ledger@3.0.0",
		"from": "oci://ghcr.io/acme/ledger:3.0.0", "dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest})
	if !res.IsError || len(execs) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalExecuteNeedsStdio ||
		out.Refusal.CLI != "pacto accept "+reportsRef+" --break ledger@3.0.0 --from oci://ghcr.io/acme/ledger:3.0.0 --intent "+planDigest {
		t.Fatalf("%+v", out)
	}
}

func TestAcceptRefusalsCarryCodeAndCommand(t *testing.T) {
	for _, code := range []WriteRefusal{RefusalAcceptNothingProposed, RefusalFieldNotAcceptable, RefusalBreakNotAnnounced, RefusalNotPublished,
		RefusalPersonSigningUnavailable} {
		out, res := callAccept(t, Authoring{Accept: stubAcceptor{refuse: &Refused{Code: code, Message: "refused"}}}, map[string]any{"service": reportsRef,
			"fields": []string{"dependencies[ledger].compatibility=^2.4.0"}})
		if !res.IsError || out.Refusal == nil || out.Refusal.Code != code || out.Refusal.CLI != "pacto accept "+reportsRef+" --field 'dependencies[ledger].compatibility=^2.4.0'" {
			t.Errorf("%s: %+v", code, out)
		}
	}
	schema, _ := json.Marshal(listTools(t, mustNew("test", Authoring{Accept: stubAcceptor{}}, Modes{}))["pacto_accept"].OutputSchema)
	for _, code := range writeRefusals {
		if !strings.Contains(string(schema), `"`+string(code)+`"`) {
			t.Errorf("the output schema does not list %s", code)
		}
	}
}

func TestMCPAcceptErrors(t *testing.T) {
	_, res := callAccept(t, Authoring{Accept: stubAcceptor{err: errors.New("boom")}}, map[string]any{"service": reportsRef})
	if !res.IsError || resultText(t, res) != "boom" {
		t.Fatalf("%s", resultText(t, res))
	}
	_, res = callAccept(t, Authoring{Accept: stubAcceptor{execErr: errors.New("push failed")}},
		map[string]any{"service": reportsRef, "dry_run": false, "on_behalf_of": "ana", "intent_digest": planDigest})
	if !res.IsError || resultText(t, res) != "push failed" {
		t.Fatalf("%s", resultText(t, res))
	}
}

func TestAcceptNamedOnlyWhenRegistered(t *testing.T) {
	for _, a := range []Authoring{{}, {Remote: true}} {
		if strings.Contains(baseFor(a), "pacto_accept") || slices.Contains(authoringNames(a), "pacto_accept") {
			t.Errorf("%+v names pacto_accept", a)
		}
		a.Accept = stubAcceptor{}
		if !strings.Contains(baseFor(a), "pacto_accept") || !slices.Contains(authoringNames(a), "pacto_accept") ||
			listTools(t, mustNew("test", a, Modes{}))["pacto_accept"] == nil {
			t.Errorf("%+v does not name pacto_accept", a)
		}
	}
}
