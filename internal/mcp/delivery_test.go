package mcp

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

type stubDelivery struct {
	req     delivery.Request
	plan    delivery.PlanAnswer
	op      *delivery.OperationObject
	err     error
	cancels []string
	of      delivery.ServiceRef
	status  delivery.StatusQuery
}

// stubEntries are a change that finished at ten and one with no time.
func stubEntries() []delivery.HistoryEntry {
	return []delivery.HistoryEntry{{Operation: "at-ten", Applied: "2026-11-05T09:50:00Z", Finished: "2026-11-05T10:00:00Z",
		CorrelationID: "INC-1", Cause: &delivery.Cause{Reason: "Latency alert"}}, {Operation: "timeless"}}
}

func (s *stubDelivery) Plan(_ context.Context, req delivery.Request) (delivery.PlanAnswer, error) {
	s.req = req
	return s.plan, s.err
}

func (s *stubDelivery) Propose(_ context.Context, req delivery.Request) (*delivery.OperationObject, delivery.PlanAnswer, error) {
	s.req = req
	return s.op, s.plan, s.err
}

func (s *stubDelivery) Get(_ context.Context, _, name string) (*delivery.OperationObject, error) {
	if s.err != nil {
		return nil, s.err
	}
	op := &delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: name}}
	op.Status.Phase, op.Status.Times.ApprovalDeadline = delivery.PhaseHeld, "2026-10-30T09:00:00Z"
	return op, nil
}

func (s *stubDelivery) Candidates(_ context.Context, ns, stage string) (*delivery.CandidateVersions, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.CandidateVersions{Namespace: ns, Stage: stage, Versions: []delivery.CandidateVersion{{Digest: "sha256:d", Version: "3.0.0"}}}, nil
}

func (s *stubDelivery) Cancel(_ context.Context, ns, name string) error {
	s.cancels = append(s.cancels, ns+"/"+name)
	return s.err
}

func (s *stubDelivery) Explain(_ context.Context, ns, name string) (*delivery.Explanation, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.Explanation{Operation: ns + "/" + name, Summary: "concluded Failed"}, nil
}

func (s *stubDelivery) History(_ context.Context, _, stage string) (*delivery.History, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.History{Stage: delivery.StageNow{Name: stage}, Completeness: "partial", Entries: stubEntries()}, nil
}

func (s *stubDelivery) Blame(_ context.Context, _, stage, digest string) (*delivery.Blame, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.Blame{Stage: delivery.StageNow{Name: stage}, Digest: digest, Completeness: "complete"}, nil
}

func (s *stubDelivery) Undo(_ context.Context, ns, name, stage string) (*delivery.Undo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.Undo{Operation: ns + "/" + name, Kind: "rollback", Note: stage}, nil
}

func (s *stubDelivery) HistoryAll(_ context.Context, ns string, of delivery.ServiceRef) (*delivery.Histories, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.of = of
	return &delivery.Histories{Completeness: "partial", Note: "ns=" + ns,
		Histories: []delivery.StageHistory{{History: delivery.History{Entries: stubEntries()}}}}, nil
}

func (s *stubDelivery) VerifyAll(_ context.Context, ns string) (*delivery.StreamVerifications, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.StreamVerifications{OK: true, Trust: "ns=" + ns}, nil
}

func (s *stubDelivery) VerifyRecords(_ context.Context, ns, stage string) (*delivery.StreamVerification, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.StreamVerification{Stage: ns + "/" + stage, OK: true}, nil
}

func (s *stubDelivery) Record(_ context.Context, ns, stage, id string) (*delivery.RecordView, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.RecordView{Namespace: ns, Stage: stage, Record: id, Verified: true}, nil
}

func (s *stubDelivery) Status(_ context.Context, q delivery.StatusQuery) (*delivery.ServiceStatus, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.status = q
	return &delivery.ServiceStatus{Service: q.Service, Note: "in promotion order"}, nil
}

func deliveryToolNames(t *testing.T, server *mcpsdk.Server) []string {
	t.Helper()
	ctx := context.Background()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	s, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	res, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		if strings.HasPrefix(tool.Name, "pacto_delivery_") {
			names = append(names, tool.Name)
			if !strings.Contains(tool.Description, "There is no approve tool") {
				t.Errorf("%s does not say there is no approve tool", tool.Name)
			}
		}
	}
	return names
}

// TestDeliveryToolsWritesOnlyForAgents: a human identity gets the read-only
// tools; an agent identity gets propose and cancel too; nobody gets approve.
func TestDeliveryToolsWritesOnlyForAgents(t *testing.T) {
	read := strings.Join(deliveryToolNames(t, NewDeliveryServer("v", &stubDelivery{}, false, Authoring{})), ",")
	write := strings.Join(deliveryToolNames(t, NewDeliveryServer("v", &stubDelivery{}, true, Authoring{})), ",")
	if strings.Contains(read, "propose") || strings.Contains(read, "cancel") || !strings.Contains(read, "pacto_delivery_operation_plan") {
		t.Fatalf("human tools: %s", read)
	}
	if !strings.Contains(write, "pacto_delivery_operation_propose") || !strings.Contains(write, "pacto_delivery_cancel") {
		t.Fatalf("agent tools: %s", write)
	}
	if strings.Contains(read+write, "approve") {
		t.Fatal("there must be no approve tool")
	}
	for _, tool := range []string{"pacto_delivery_candidates", "pacto_delivery_explain", "pacto_delivery_history", "pacto_delivery_blame", "pacto_delivery_undo_plan", "pacto_delivery_record_verify", "pacto_delivery_record_get", "pacto_delivery_status"} {
		if !strings.Contains(read, tool) {
			t.Fatalf("%s is a read tool: %s", tool, read)
		}
	}
	// One tool lists and describes the static types; undo says it only plans.
	for _, gone := range []string{"pacto_delivery_operation_describe", "pacto_delivery_undo,"} {
		if strings.Contains(write+",", gone) {
			t.Fatalf("%s must not be registered: %s", gone, write)
		}
	}
	if n := len(strings.Split(write, ",")); n != 13 {
		t.Fatalf("want 11 read and 2 write delivery tools, got %d: %s", n, write)
	}
}

func TestDeliveryToolCalls(t *testing.T) {
	server := NewDeliveryServer("v", &stubDelivery{}, true, Authoring{})
	var types []delivery.OperationType
	decode(t, callToolOn(t, server, "pacto_delivery_operation_types", map[string]any{}), &types)
	if len(types) != len(delivery.Types()) {
		t.Fatalf("types: %d", len(types))
	}
	var desc struct {
		ID     string `json:"id"`
		Digest string `json:"digest"`
	}
	decode(t, callToolOn(t, server, "pacto_delivery_operation_types", map[string]any{"type": delivery.TypePromote}), &desc)
	if desc.ID != delivery.TypePromote || desc.Digest == "" {
		t.Fatalf("describe: %+v", desc)
	}
	if res := callToolOn(t, server, "pacto_delivery_operation_types", map[string]any{"type": "x"}); !res.IsError {
		t.Fatal("unknown type must be an error")
	}
}

func TestDeliveryPlanToolCalls(t *testing.T) {
	api := &stubDelivery{plan: delivery.PlanAnswer{Plan: delivery.Plan{IntentDigest: "sha256:i", Predicted: delivery.PredictHeld}, Next: []string{"held"}}}
	server := NewDeliveryServer("v", api, true, Authoring{})
	var plan delivery.PlanAnswer
	decode(t, callToolOn(t, server, "pacto_delivery_operation_plan", map[string]any{
		"namespace": "prod", "stage": "s", "to": "3.0.0", "on_behalf_of": "alice"}), &plan)
	if plan.Predicted != delivery.PredictHeld || plan.IntentDigest != "sha256:i" || len(plan.Next) != 1 ||
		api.req.OnBehalfOf != "alice" || api.req.Stage != "s" || api.req.To != "3.0.0" {
		t.Fatalf("plan: %+v %+v", plan, api.req)
	}
	decode(t, callToolOn(t, server, "pacto_delivery_operation_plan", map[string]any{
		"namespace": "prod", "stage": "s", "cause_verdict": `{"gate":"health","phase":"verify","result":"Fail"}`}), &plan)
	if api.req.Cause == nil || api.req.Cause.Verdict.Gate != "health" || api.req.Cause.Verdict.Result != delivery.Fail {
		t.Fatalf("cause: %+v", api.req.Cause)
	}
	var e delivery.Explanation
	decode(t, callToolOn(t, server, "pacto_delivery_explain", map[string]any{"namespace": "prod", "name": "p1"}), &e)
	if e.Operation != "prod/p1" || e.Summary != "concluded Failed" {
		t.Fatalf("explain: %+v", e)
	}
	var h delivery.History
	decode(t, callToolOn(t, server, "pacto_delivery_history", map[string]any{"namespace": "prod", "stage": "s"}), &h)
	if h.Stage.Name != "s" || h.Completeness != "partial" {
		t.Fatalf("history: %+v", h)
	}
}

// B7: one Stage's history carries its timeline.
func TestDeliveryHistoryToolTimeline(t *testing.T) {
	server := NewDeliveryServer("v", &stubDelivery{}, true, Authoring{})
	var h delivery.History
	decode(t, callToolOn(t, server, "pacto_delivery_history", map[string]any{"namespace": "prod", "stage": "s"}), &h)
	if h.Timeline == nil || len(h.Timeline.Buckets) != delivery.TimelineBuckets {
		t.Fatalf("history, with its timeline: %+v", h.Timeline)
	}
}

func TestDeliveryCandidatesToolCall(t *testing.T) {
	server := NewDeliveryServer("v", &stubDelivery{}, true, Authoring{})
	var c delivery.CandidateVersions
	decode(t, callToolOn(t, server, "pacto_delivery_candidates", map[string]any{"namespace": "prod", "stage": "s"}), &c)
	if c.Stage != "s" || len(c.Versions) != 1 || c.Versions[0].Version != "3.0.0" {
		t.Fatalf("candidates: %+v", c)
	}
}

func TestDeliveryRecordToolCalls(t *testing.T) {
	server := NewDeliveryServer("v", &stubDelivery{}, false, Authoring{})
	var b delivery.Blame
	decode(t, callToolOn(t, server, "pacto_delivery_blame", map[string]any{"namespace": "prod", "stage": "s", "digest": "sha256:d"}), &b)
	if b.Stage.Name != "s" || b.Digest != "sha256:d" {
		t.Fatalf("blame: %+v", b)
	}
	var u delivery.Undo
	decode(t, callToolOn(t, server, "pacto_delivery_undo_plan", map[string]any{"namespace": "prod", "name": "p1", "stage": "s"}), &u)
	if u.Operation != "prod/p1" || u.Note != "s" {
		t.Fatalf("undo: %+v", u)
	}
	var v delivery.StreamVerification
	decode(t, callToolOn(t, server, "pacto_delivery_record_verify", map[string]any{"namespace": "prod", "stage": "s"}), &v)
	if !v.OK || v.Stage != "prod/s" {
		t.Fatalf("verify: %+v", v)
	}
	// With no stage the tools walk every Stage: a fleet audit.
	var hs delivery.Histories
	decode(t, callToolOn(t, server, "pacto_delivery_history", map[string]any{"namespace": "prod"}), &hs)
	if hs.Note != "ns=prod" || hs.Timeline == nil {
		t.Fatalf("history of every Stage, with one timeline over them (B7): %+v", hs)
	}
	var vs delivery.StreamVerifications
	decode(t, callToolOn(t, server, "pacto_delivery_record_verify", map[string]any{}), &vs)
	if !vs.OK || vs.Trust != "ns=" {
		t.Fatalf("verify of every Stage: %+v", vs)
	}
}

func TestDeliveryWriteToolCalls(t *testing.T) {
	api := &stubDelivery{plan: delivery.PlanAnswer{Plan: delivery.Plan{IntentDigest: "sha256:i", Predicted: delivery.PredictHeld}}}
	server := NewDeliveryServer("v", api, true, Authoring{})
	api.op = &delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: "p1"}}
	var proposed struct {
		Operation delivery.OperationObject `json:"operation"`
	}
	decode(t, callToolOn(t, server, "pacto_delivery_operation_propose", map[string]any{
		"namespace": "prod", "stage": "s", "to": "sha256:x", "intent_digest": "sha256:i", "name": "p1"}), &proposed)
	if proposed.Operation.Metadata.Name != "p1" || api.req.IntentDigest != "sha256:i" || api.req.Name != "p1" {
		t.Fatalf("propose: %+v %+v", proposed, api.req)
	}
	var op struct {
		delivery.OperationObject
		Next string `json:"next"`
	}
	decode(t, callToolOn(t, server, "pacto_delivery_get", map[string]any{"namespace": "prod", "name": "p1"}), &op)
	if op.Metadata.Name != "p1" || !strings.Contains(op.Next, "concludes Expired") {
		t.Fatalf("get says what happens at the deadline: %+v", op)
	}
	callToolOn(t, server, "pacto_delivery_cancel", map[string]any{"namespace": "prod", "name": "p1"})
	if api.cancels[0] != "prod/p1" {
		t.Fatal("cancel")
	}
}

func TestDeliveryToolErrors(t *testing.T) {
	api := &stubDelivery{plan: delivery.PlanAnswer{Plan: delivery.Plan{IntentDigest: "sha256:i", Predicted: delivery.PredictHeld}}}
	server := NewDeliveryServer("v", api, true, Authoring{})
	api.op, api.err = nil, errors.New("PlanStale: moved")
	res := callToolOn(t, server, "pacto_delivery_operation_propose", map[string]any{"namespace": "prod", "stage": "s"})
	if !res.IsError || !strings.Contains(resultText(t, res), "PlanStale") || !strings.Contains(resultText(t, res), "sha256:i") {
		t.Fatalf("a stale propose returns the fresh plan as an error: %s", resultText(t, res))
	}
	api.plan = delivery.PlanAnswer{}
	for _, tool := range []string{"pacto_delivery_operation_plan", "pacto_delivery_operation_propose"} {
		if res := callToolOn(t, server, tool, map[string]any{"namespace": "prod", "stage": "s"}); !res.IsError {
			t.Fatalf("%s must surface the error", tool)
		}
	}
	for _, tool := range []string{"pacto_delivery_operation_plan", "pacto_delivery_operation_propose"} {
		if res := callToolOn(t, server, tool, map[string]any{"namespace": "prod", "stage": "s", "cause_verdict": "{"}); !res.IsError ||
			!strings.Contains(resultText(t, res), "cause_verdict is not a verdict") {
			t.Fatalf("%s must refuse a malformed cause", tool)
		}
	}
	for _, tool := range []string{"pacto_delivery_history", "pacto_delivery_blame", "pacto_delivery_record_verify", "pacto_delivery_candidates"} {
		if res := callToolOn(t, server, tool, map[string]any{"namespace": "prod", "stage": "s"}); !res.IsError {
			t.Fatalf("%s must surface the error", tool)
		}
	}
	if res := callToolOn(t, server, "pacto_delivery_undo_plan", map[string]any{"namespace": "prod", "name": "p1"}); !res.IsError {
		t.Fatal("undo must surface the error")
	}
	for _, tool := range []string{"pacto_delivery_get", "pacto_delivery_cancel", "pacto_delivery_explain"} {
		if res := callToolOn(t, server, tool, map[string]any{"namespace": "prod", "name": "p1"}); !res.IsError {
			t.Fatalf("%s must surface the error", tool)
		}
	}
}

func TestDeliveryFleetToolErrors(t *testing.T) {
	server := NewDeliveryServer("v", &stubDelivery{err: errors.New("forbidden")}, false, Authoring{})
	for _, tool := range []string{"pacto_delivery_history", "pacto_delivery_record_verify"} {
		if res := callToolOn(t, server, tool, map[string]any{"namespace": "prod"}); !res.IsError {
			t.Fatalf("%s of every Stage must surface the error", tool)
		}
	}
}

// TestDeliveryHistoryWindowAndService: since and until keep the changes
// around a time and every change whose time is unknown; service narrows the
// walk and makes no sense with one Stage.
func TestDeliveryHistoryWindowAndService(t *testing.T) {
	api := &stubDelivery{}
	server := NewDeliveryServer("v", api, false, Authoring{})
	names := func(es []delivery.HistoryEntry) string {
		var out []string
		for _, e := range es {
			out = append(out, e.Operation)
		}
		return strings.Join(out, ",")
	}
	var h delivery.History
	decode(t, callToolOn(t, server, "pacto_delivery_history", map[string]any{"namespace": "prod", "stage": "s",
		"since": "2026-11-05T11:00:00Z"}), &h)
	if names(h.Entries) != "timeless" || !strings.Contains(h.Note, "at or after 2026-11-05T11:00:00Z") {
		t.Fatalf("history after eleven: %+v", h)
	}
	decode(t, callToolOn(t, server, "pacto_delivery_history", map[string]any{"namespace": "prod", "stage": "s",
		"since": "2026-11-05T09:00:00Z", "until": "2026-11-05T10:30:00Z"}), &h)
	if names(h.Entries) != "at-ten,timeless" {
		t.Fatalf("history around ten: %+v", h)
	}
	var hs delivery.Histories
	decode(t, callToolOn(t, server, "pacto_delivery_history", map[string]any{"service": "payments", "domain": "d",
		"until": "2026-11-05T09:00:00Z"}), &hs)
	if api.of != (delivery.ServiceRef{Name: "payments", Domain: "d"}) || names(hs.Histories[0].Entries) != "timeless" {
		t.Fatalf("walk for payments before nine: %+v of %+v", hs, api.of)
	}
	for args, want := range map[string]map[string]any{
		"service filters the walk; leave stage empty": {"namespace": "prod", "stage": "s", "service": "payments"},
		"since takes an RFC 3339 time or a duration":  {"namespace": "prod", "stage": "s", "since": "yesterday"},
		"until takes an RFC 3339 time or a duration":  {"until": "soon"},
		"domain needs service":                        {"domain": "d"},
		"since is after until":                        {"since": "2026-11-05T11:00:00Z", "until": "2026-11-05T09:00:00Z"},
	} {
		if res := callToolOn(t, server, "pacto_delivery_history", want); !res.IsError || !strings.Contains(resultText(t, res), args) {
			t.Fatalf("%v: %s", want, resultText(t, res))
		}
	}
}

// TestHistoryToolDurations: since and until take a duration before now, as
// the CLI flags do, and the schema says so.
func TestHistoryToolDurations(t *testing.T) {
	server := NewDeliveryServer("v", &stubDelivery{}, false, Authoring{})
	var h delivery.History
	decode(t, callToolOn(t, server, "pacto_delivery_history", map[string]any{"namespace": "prod", "stage": "s", "since": "2h"}), &h)
	if len(h.Entries) != 1 || h.Entries[0].Operation != "timeless" || !strings.Contains(h.Note, "at or after") {
		t.Fatalf("history of the last two hours: %+v", h)
	}
	props := historySchema()["properties"].(map[string]any)
	for _, k := range []string{"since", "until"} {
		if d := props[k].(map[string]any)["description"].(string); !strings.Contains(d, "duration before now") {
			t.Fatalf("%s schema: %q", k, d)
		}
	}
}

// TestHistoryToolCorrelation: cause and correlation_id keep only the changes
// tied to an incident, in both modes, and the timeline counts only those.
func TestHistoryToolCorrelation(t *testing.T) {
	server := NewDeliveryServer("v", &stubDelivery{}, false, Authoring{})
	window := map[string]any{"since": "2026-11-05T09:00:00Z", "until": "2026-11-05T11:00:00Z"}
	args := func(extra map[string]any) map[string]any {
		out := map[string]any{"namespace": "prod", "stage": "s"}
		maps.Copy(out, window)
		maps.Copy(out, extra)
		return out
	}
	var h delivery.History
	decode(t, callToolOn(t, server, "pacto_delivery_history", args(nil)), &h)
	if len(h.Entries) != 2 || h.Timeline.Untimed != 1 {
		t.Fatalf("unfiltered: %+v", h)
	}
	decode(t, callToolOn(t, server, "pacto_delivery_history", args(map[string]any{"correlation_id": "INC-1"})), &h)
	if len(h.Entries) != 1 || h.Entries[0].Operation != "at-ten" || h.Timeline.Untimed != 0 || h.Timeline.Totals == (delivery.TimelineCounts{}) ||
		!strings.Contains(h.Note, "only changes with correlation id INC-1") {
		t.Fatalf("correlation INC-1: %+v", h)
	}
	decode(t, callToolOn(t, server, "pacto_delivery_history", args(map[string]any{"cause": "nothing like it"})), &h)
	if len(h.Entries) != 0 || h.Timeline.Untimed != 0 || h.Timeline.Totals != (delivery.TimelineCounts{}) {
		t.Fatalf("no cause matches: %+v", h)
	}
	var hs delivery.Histories
	walk := map[string]any{"cause": "LATENCY"}
	maps.Copy(walk, window)
	decode(t, callToolOn(t, server, "pacto_delivery_history", walk), &hs)
	if len(hs.Histories[0].Entries) != 1 || hs.Timeline.Untimed != 0 || hs.Timeline.Totals == (delivery.TimelineCounts{}) {
		t.Fatalf("walk by cause: %+v", hs)
	}
}

// TestRecordVerifySchemaHasNoWindow: record verify shares the audit schema,
// which takes no time window or service.
func TestRecordVerifySchemaHasNoWindow(t *testing.T) {
	for _, k := range []string{"since", "until", "service", "domain"} {
		if _, ok := auditSchema()["properties"].(map[string]any)[k]; ok {
			t.Fatalf("the audit schema takes %s", k)
		}
		if _, ok := historySchema()["properties"].(map[string]any)[k]; !ok {
			t.Fatalf("the history schema lacks %s", k)
		}
	}
}

// TestDeliveryDescriptionsExplainCheckedPass: an agent reading a plan, an
// operation or an explanation learns that an evidence gate's Pass only says
// the gate ran, as the CLI and the dashboard label it Checked.
func TestDeliveryDescriptionsExplainCheckedPass(t *testing.T) {
	const checked = "A pacto.diff, pacto.impact or pacto.environment verdict passes once it ran"
	if !strings.Contains(deliveryInstructions, checked) || !strings.Contains(deliveryInstructions, "show it as Checked") {
		t.Fatal("the delivery instructions must explain a checked Pass")
	}
	ctx := context.Background()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := NewDeliveryServer("v", &stubDelivery{}, false, Authoring{}).Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	s, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	res, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"pacto_delivery_operation_plan": true, "pacto_delivery_get": true, "pacto_delivery_explain": true}
	for _, tool := range res.Tools {
		if want[tool.Name] {
			delete(want, tool.Name)
			if !strings.Contains(tool.Description, checked) {
				t.Errorf("%s does not explain a checked Pass", tool.Name)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("tools not found: %v", want)
	}
}

// TestDeliveryStatusTool: the arguments reach Status; a missing service is
// an error; a person's server, which registers no propose tool, says so in
// the note and an agent's does not.
func TestDeliveryStatusTool(t *testing.T) {
	api := &stubDelivery{}
	var got delivery.ServiceStatus
	decode(t, callToolOn(t, NewDeliveryServer("v", api, false, Authoring{}), "pacto_delivery_status",
		map[string]any{"service": "payments", "domain": "d", "namespace": "prod", "why": true}), &got)
	if api.status != (delivery.StatusQuery{Namespace: "prod", Service: "payments", Domain: "d", Why: true}) ||
		got.Note != "in promotion order"+noProposeNote {
		t.Fatalf("status %+v from %+v", got, api.status)
	}
	decode(t, callToolOn(t, NewDeliveryServer("v", api, true, Authoring{}), "pacto_delivery_status", map[string]any{"service": "payments"}), &got)
	if got.Note != "in promotion order" {
		t.Fatalf("an agent's server has propose: %q", got.Note)
	}
	if res := callToolOn(t, NewDeliveryServer("v", api, false, Authoring{}), "pacto_delivery_status", map[string]any{}); !res.IsError {
		t.Fatal("a missing service must be an error")
	}
	api.err = errors.New("boom")
	if res := callToolOn(t, NewDeliveryServer("v", api, false, Authoring{}), "pacto_delivery_status", map[string]any{"service": "p"}); !res.IsError {
		t.Fatal("an error must be an error result")
	}
}

func TestRecordGetTool(t *testing.T) {
	api := &stubDelivery{}
	server := NewDeliveryServer("v", api, false, Authoring{})
	var r delivery.RecordView
	decode(t, callToolOn(t, server, "pacto_delivery_record_get", map[string]any{"namespace": "prod", "stage": "s", "record": "sha256:r"}), &r)
	if r.Namespace != "prod" || r.Stage != "s" || r.Record != "sha256:r" || !r.Verified {
		t.Fatalf("record: %+v", r)
	}
	if res := callToolOn(t, server, "pacto_delivery_record_get", map[string]any{"namespace": "prod", "stage": "s"}); !res.IsError {
		t.Fatal("record get needs a record id")
	}
	api.err = errors.New("record sha256:x is not in the stream")
	if res := callToolOn(t, server, "pacto_delivery_record_get", map[string]any{"namespace": "prod", "stage": "s", "record": "sha256:x"}); !res.IsError ||
		!strings.Contains(resultText(t, res), "not in the stream") {
		t.Fatal("record get must surface the error")
	}
}
