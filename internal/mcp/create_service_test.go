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

// stubCreator is a CreateServicer that records its calls.
type stubCreator struct {
	plans, execs *[]CreateServiceArgs
	plan         string
	refuse       *Refused
	execRefuse   *Refused
	err, execErr error
}

const createDigest = "sha256:create"

func (s stubCreator) Plan(_ context.Context, a CreateServiceArgs) (json.RawMessage, *Refused, error) {
	if s.plans != nil {
		*s.plans = append(*s.plans, a)
	}
	if s.err != nil || s.refuse != nil {
		return json.RawMessage(s.plan), s.refuse, s.err
	}
	return json.RawMessage(`{"service":"invoices","intentDigest":"` + createDigest + `"}`), nil, nil
}

func (s stubCreator) Execute(_ context.Context, a CreateServiceArgs) (json.RawMessage, *Refused, error) {
	if s.execs != nil {
		*s.execs = append(*s.execs, a)
	}
	if s.execErr != nil {
		return nil, nil, s.execErr
	}
	if s.execRefuse != nil {
		return json.RawMessage(`{"refusals":[{"code":"ParameterOutOfRange","message":"m","field":"f"}]}`), s.execRefuse, nil
	}
	return json.RawMessage(`{"executed":true,"tag":"draft-1","published":"r@sha256:1","draft":true,"acceptURL":"https://d/#/fleet/services/k?draft=draft-1"}`), nil, nil
}

var createServiceCall = map[string]any{"path": "ghcr.io/acme/go-http", "name": "invoices", "namespace": "team-billing"}

func withArgs(extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range createServiceCall {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func callCreate(t *testing.T, c CreateServicer, args map[string]any) (WriteResult, *mcpsdk.CallToolResult) {
	t.Helper()
	res := callToolOn(t, mustNew("test", Authoring{Remote: true, CreateService: c}, Modes{}), "pacto_create_service", args)
	var out WriteResult
	_ = json.Unmarshal([]byte(resultText(t, res)), &out)
	return out, res
}

func TestCreateServiceRegistersOnlyRemote(t *testing.T) {
	stdio := listTools(t, mustNew("test", Authoring{CreateService: stubCreator{}}, Modes{}))
	if _, ok := stdio["pacto_create_service"]; ok {
		t.Error("a stdio server lists pacto_create_service")
	}
	if _, ok := stdio["pacto_create"]; !ok {
		t.Error("a stdio server lost pacto_create")
	}
	remote := listTools(t, mustNew("test", Authoring{Remote: true, CreateService: stubCreator{}}, Modes{}))
	if _, ok := remote["pacto_create_service"]; !ok {
		t.Error("an HTTP server with a creator does not list pacto_create_service")
	}
	if _, ok := remote["pacto_create"]; ok {
		t.Error("an HTTP server lists pacto_create")
	}
	if _, ok := listTools(t, mustNew("test", Authoring{Remote: true}, Modes{}))["pacto_create_service"]; ok {
		t.Error("an HTTP server without a creator lists pacto_create_service")
	}
	names := authoringNames(Authoring{Remote: true, CreateService: stubCreator{}})
	if !slices.Equal(names, []string{"pacto_schema", "pacto_create_service"}) {
		t.Errorf("authoring = %v", names)
	}
}

func TestCreateServiceDryRunDefault(t *testing.T) {
	var plans, execs []CreateServiceArgs
	out, res := callCreate(t, stubCreator{plans: &plans, execs: &execs}, withArgs(map[string]any{"owner": "billing",
		"offerings": []any{map[string]any{"offering": "postgresql", "values": map[string]any{"storageGB": 40}}}}))
	if res.IsError || len(execs) != 0 || len(plans) != 1 || out.Executed || out.IntentDigest != createDigest || out.Plan == nil {
		t.Fatalf("%+v %s", out, resultText(t, res))
	}
	if a := plans[0]; a.Path != "ghcr.io/acme/go-http" || a.Owner != "billing" || len(a.Offerings) != 1 || a.Offerings[0].Values["storageGB"] != float64(40) {
		t.Fatalf("%+v", a)
	}
}

func TestCreateServiceNeedsIntentDigest(t *testing.T) {
	var execs []CreateServiceArgs
	out, res := callCreate(t, stubCreator{execs: &execs}, withArgs(map[string]any{"dry_run": false}))
	if !res.IsError || len(execs) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalIntentRequired {
		t.Fatalf("%+v", out)
	}
}

func TestCreateServicePlanStale(t *testing.T) {
	var execs []CreateServiceArgs
	out, res := callCreate(t, stubCreator{execs: &execs}, withArgs(map[string]any{"dry_run": false, "intent_digest": "sha256:old"}))
	if !res.IsError || len(execs) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalPlanStale || out.IntentDigest != createDigest {
		t.Fatalf("%+v", out)
	}
	// Execute's own staleness check reaches the caller the same way.
	out, res = callCreate(t, stubCreator{execRefuse: &Refused{Code: RefusalPlanStale, Message: "moved"}},
		withArgs(map[string]any{"dry_run": false, "intent_digest": createDigest}))
	if !res.IsError || out.Executed || out.Refusal == nil || out.Refusal.Code != RefusalPlanStale || len(out.Refusals) == 0 {
		t.Fatalf("%+v", out)
	}
}

func TestCreateServiceWritesDraft(t *testing.T) {
	var execs []CreateServiceArgs
	out, res := callCreate(t, stubCreator{execs: &execs}, withArgs(map[string]any{"dry_run": false, "intent_digest": createDigest, "on_behalf_of": "alice"}))
	if res.IsError || len(execs) != 1 || execs[0].OnBehalfOf != "alice" || !out.Executed || out.Draft == nil || out.Draft.Tag != "draft-1" ||
		out.Draft.Signed || !strings.HasSuffix(out.AcceptCLI, "?draft=draft-1") {
		t.Fatalf("%+v", out)
	}
}

func TestCreateServicePlanRefusalsAtTheirField(t *testing.T) {
	plan := `{"refusals":[{"code":"ParameterOutOfRange","message":"too big","field":"configurations[database].values.storageGB","closest":500}]}`
	out, res := callCreate(t, stubCreator{plan: plan, refuse: &Refused{Code: RefusalParameterOutOfRange, Message: "too big", CLI: "pacto init invoices"}},
		withArgs(map[string]any{"dry_run": false, "intent_digest": createDigest}))
	var refusals []map[string]any
	_ = json.Unmarshal(out.Refusals, &refusals)
	if !res.IsError || out.Refusal == nil || out.Refusal.CLI != "pacto init invoices" || len(refusals) != 1 || refusals[0]["closest"] != float64(500) {
		t.Fatalf("%+v", out)
	}
}

func TestCreateServiceErrors(t *testing.T) {
	_, res := callCreate(t, stubCreator{err: errors.New("boom")}, createServiceCall)
	if !res.IsError || !strings.Contains(resultText(t, res), "boom") {
		t.Fatalf("plan error %s", resultText(t, res))
	}
	_, res = callCreate(t, stubCreator{execErr: errors.New("bang")}, withArgs(map[string]any{"dry_run": false, "intent_digest": createDigest}))
	if !res.IsError || !strings.Contains(resultText(t, res), "bang") {
		t.Fatalf("execute error %s", resultText(t, res))
	}
}

// TestCreateServiceOutputSchemaListsCodes: the tool's output schema lists
// every create code in the refusal and the field refusals enums.
func TestCreateServiceOutputSchemaListsCodes(t *testing.T) {
	tool := listTools(t, mustNew("test", Authoring{Remote: true, CreateService: stubCreator{}}, Modes{}))["pacto_create_service"]
	raw, _ := json.Marshal(tool.OutputSchema)
	var schema struct {
		Properties struct {
			Refusal struct {
				Properties struct {
					Code struct {
						Enum []string `json:"enum"`
					} `json:"code"`
				} `json:"properties"`
			} `json:"refusal"`
			Refusals struct {
				Items struct {
					Properties struct {
						Code struct {
							Enum []string `json:"enum"`
						} `json:"code"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"refusals"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(raw, &schema)
	for _, c := range []WriteRefusal{RefusalServiceExists, RefusalGoldenPathNotDeclared, RefusalRepositoryOutsideRegistry, RefusalOfferingNotConsumable,
		RefusalOfferingUnknown, RefusalParameterNotDeclared, RefusalParameterOutOfRange, RefusalParameterSensitive, RefusalClassNotOffered,
		RefusalTierNotTunable, RefusalEnvironmentNotDeclared, RefusalNotAllowed, RefusalReviewFailed, RefusalExecuteNeedsStdio} {
		if !slices.Contains(schema.Properties.Refusal.Properties.Code.Enum, string(c)) || !slices.Contains(schema.Properties.Refusals.Items.Properties.Code.Enum, string(c)) {
			t.Errorf("the output schema does not list %s", c)
		}
	}
}

func TestCreateServiceInstructions(t *testing.T) {
	got := baseFor(Authoring{Remote: true, CreateService: stubCreator{}})
	if !strings.Contains(got, "infrastructure only through offerings, every change planned and checked before it applies") {
		t.Errorf("instructions = %q", got)
	}
	if strings.Contains(baseFor(Authoring{Remote: true}), "pacto_create_service") {
		t.Error("instructions name pacto_create_service without a creator")
	}
}
