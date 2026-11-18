package mcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

type stubRefs struct {
	refs []string
	err  error
}

func (s *stubRefs) RecordAt(_ context.Context, ref string) (*delivery.RecordAt, error) {
	s.refs = append(s.refs, ref)
	return &delivery.RecordAt{Ref: ref, Predicate: delivery.PredicateApproval, Verified: true, Counted: delivery.Counted}, s.err
}

// recordTool is the listed record tool name on server.
func recordTool(t *testing.T, server *mcpsdk.Server, name string) *mcpsdk.Tool {
	t.Helper()
	ctx := context.Background()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	s, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	res, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(res.Tools, func(x *mcpsdk.Tool) bool { return x.Name == name })
	if i < 0 {
		t.Fatalf("%s is not listed", name)
	}
	return res.Tools[i]
}

func schemaOf(tool *mcpsdk.Tool) (map[string]any, []any) {
	s := tool.InputSchema.(map[string]any)
	req, _ := s["required"].([]any)
	return s["properties"].(map[string]any), req
}

func TestRecordToolsAcceptRefLocally(t *testing.T) {
	refs := &stubRefs{}
	server := mustNew("v", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Refs: refs}})
	for _, name := range []string{"pacto_delivery_record_get", "pacto_delivery_record_verify"} {
		tool := recordTool(t, server, name)
		props, req := schemaOf(tool)
		if _, ok := props["ref"]; !ok || len(req) != 0 || !strings.Contains(tool.Description, "local server only") {
			t.Fatalf("%s: %v %v", name, props, req)
		}
		var at delivery.RecordAt
		decode(t, callToolOn(t, server, name, map[string]any{"ref": "reg.example/streams@sha256:a"}), &at)
		if at.Ref != "reg.example/streams@sha256:a" || at.Counted != delivery.Counted {
			t.Fatalf("%s: %+v", name, at)
		}
	}
	if len(refs.refs) != 2 {
		t.Fatal(refs.refs)
	}
	refs.err = errors.New("unreachable")
	if res := callToolOn(t, server, "pacto_delivery_record_get", map[string]any{"ref": "reg.example/streams:t"}); !res.IsError || !strings.Contains(resultText(t, res), "unreachable") {
		t.Fatal("the error is not surfaced")
	}
}

func TestRecordToolsRefExclusive(t *testing.T) {
	refs := &stubRefs{}
	server := mustNew("v", Authoring{}, Modes{Delivery: &DeliveryMode{API: &stubDelivery{}, Refs: refs}})
	for name, args := range map[string]map[string]any{
		"pacto_delivery_record_get":    {"ref": "reg/x:t", "record": "sha256:r"},
		"pacto_delivery_record_verify": {"ref": "reg/x:t", "stage": "s"},
	} {
		if res := callToolOn(t, server, name, args); !res.IsError || !strings.Contains(resultText(t, res), "not both") {
			t.Fatalf("%s took both forms", name)
		}
	}
	if res := callToolOn(t, server, "pacto_delivery_record_get", map[string]any{"namespace": "prod"}); !res.IsError ||
		!strings.Contains(resultText(t, res), "pass namespace, stage and record, or ref") {
		t.Fatal("record get without a record")
	}
	var r delivery.RecordView
	decode(t, callToolOn(t, server, "pacto_delivery_record_get", map[string]any{"namespace": "prod", "stage": "s", "record": "sha256:r"}), &r)
	if r.Record != "sha256:r" || len(refs.refs) != 0 {
		t.Fatalf("%+v %v", r, refs.refs)
	}
}
