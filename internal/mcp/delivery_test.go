package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

type stubDelivery struct {
	req     delivery.Request
	plan    delivery.Plan
	op      *delivery.OperationObject
	err     error
	cancels []string
}

func (s *stubDelivery) Plan(_ context.Context, req delivery.Request) (delivery.Plan, error) {
	s.req = req
	return s.plan, s.err
}

func (s *stubDelivery) Propose(_ context.Context, req delivery.Request) (*delivery.OperationObject, delivery.Plan, error) {
	s.req = req
	return s.op, s.plan, s.err
}

func (s *stubDelivery) Get(_ context.Context, _, name string) (*delivery.OperationObject, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: name}}, nil
}

func (s *stubDelivery) Cancel(_ context.Context, ns, name string) error {
	s.cancels = append(s.cancels, ns+"/"+name)
	return s.err
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
	read := strings.Join(deliveryToolNames(t, NewDeliveryServer("v", &stubDelivery{}, false, nil)), ",")
	write := strings.Join(deliveryToolNames(t, NewDeliveryServer("v", &stubDelivery{}, true, nil)), ",")
	if strings.Contains(read, "propose") || strings.Contains(read, "cancel") || !strings.Contains(read, "pacto_delivery_operation_plan") {
		t.Fatalf("human tools: %s", read)
	}
	if !strings.Contains(write, "pacto_delivery_operation_propose") || !strings.Contains(write, "pacto_delivery_cancel") {
		t.Fatalf("agent tools: %s", write)
	}
	if strings.Contains(read+write, "approve") {
		t.Fatal("there must be no approve tool")
	}
}

func TestDeliveryToolCalls(t *testing.T) {
	api := &stubDelivery{plan: delivery.Plan{IntentDigest: "sha256:i", Predicted: delivery.PredictHeld}}
	server := NewDeliveryServer("v", api, true, nil)
	var types []delivery.OperationType
	decode(t, callToolOn(t, server, "pacto_delivery_operation_types", map[string]any{}), &types)
	if len(types) != 4 {
		t.Fatalf("types: %d", len(types))
	}
	var desc struct {
		ID     string `json:"id"`
		Digest string `json:"digest"`
	}
	decode(t, callToolOn(t, server, "pacto_delivery_operation_describe", map[string]any{"type": delivery.TypePromote}), &desc)
	if desc.ID != delivery.TypePromote || desc.Digest == "" {
		t.Fatalf("describe: %+v", desc)
	}
	if res := callToolOn(t, server, "pacto_delivery_operation_describe", map[string]any{"type": "x"}); !res.IsError {
		t.Fatal("unknown type must be an error")
	}
	var plan delivery.Plan
	decode(t, callToolOn(t, server, "pacto_delivery_operation_plan", map[string]any{
		"namespace": "prod", "stage": "s", "to": "sha256:x", "on_behalf_of": "alice"}), &plan)
	if plan.Predicted != delivery.PredictHeld || api.req.OnBehalfOf != "alice" || api.req.Stage != "s" {
		t.Fatalf("plan: %+v %+v", plan, api.req)
	}
}

func TestDeliveryWriteToolCalls(t *testing.T) {
	api := &stubDelivery{plan: delivery.Plan{IntentDigest: "sha256:i", Predicted: delivery.PredictHeld}}
	server := NewDeliveryServer("v", api, true, nil)
	api.op = &delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: "p1"}}
	var proposed struct {
		Operation delivery.OperationObject `json:"operation"`
	}
	decode(t, callToolOn(t, server, "pacto_delivery_operation_propose", map[string]any{
		"namespace": "prod", "stage": "s", "to": "sha256:x", "intent_digest": "sha256:i", "name": "p1"}), &proposed)
	if proposed.Operation.Metadata.Name != "p1" || api.req.IntentDigest != "sha256:i" || api.req.Name != "p1" {
		t.Fatalf("propose: %+v %+v", proposed, api.req)
	}
	var op delivery.OperationObject
	decode(t, callToolOn(t, server, "pacto_delivery_get", map[string]any{"namespace": "prod", "name": "p1"}), &op)
	if op.Metadata.Name != "p1" {
		t.Fatal("get")
	}
	callToolOn(t, server, "pacto_delivery_cancel", map[string]any{"namespace": "prod", "name": "p1"})
	if api.cancels[0] != "prod/p1" {
		t.Fatal("cancel")
	}
}

func TestDeliveryToolErrors(t *testing.T) {
	api := &stubDelivery{plan: delivery.Plan{IntentDigest: "sha256:i", Predicted: delivery.PredictHeld}}
	server := NewDeliveryServer("v", api, true, nil)
	api.op, api.err = nil, errors.New("PlanStale: moved")
	res := callToolOn(t, server, "pacto_delivery_operation_propose", map[string]any{"namespace": "prod", "stage": "s"})
	if !res.IsError || !strings.Contains(resultText(t, res), "PlanStale") || !strings.Contains(resultText(t, res), "sha256:i") {
		t.Fatalf("a stale propose returns the fresh plan as an error: %s", resultText(t, res))
	}
	api.plan = delivery.Plan{}
	for _, tool := range []string{"pacto_delivery_operation_plan", "pacto_delivery_operation_propose"} {
		if res := callToolOn(t, server, tool, map[string]any{"namespace": "prod", "stage": "s"}); !res.IsError {
			t.Fatalf("%s must surface the error", tool)
		}
	}
	for _, tool := range []string{"pacto_delivery_get", "pacto_delivery_cancel"} {
		if res := callToolOn(t, server, tool, map[string]any{"namespace": "prod", "name": "p1"}); !res.IsError {
			t.Fatalf("%s must surface the error", tool)
		}
	}
}
