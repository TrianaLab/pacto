package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/publish"
)

func TestPublishTools(t *testing.T) {
	var gotPlan []bool
	var res *publish.Result
	var err error
	run := func(_ context.Context, path, revision string, plan bool) (*publish.Result, error) {
		gotPlan = append(gotPlan, plan)
		if path != "svc" || revision != "r" {
			t.Fatalf("args: %s %s", path, revision)
		}
		return res, err
	}
	server := NewPublishServer("v", run, nil)
	args := map[string]any{"path": "svc", "revision": "r"}
	res = &publish.Result{Plan: publish.Plan{Service: "payments", Ready: true, Missing: 2}, Actions: []string{"pushed"}}
	var p publish.Plan
	decode(t, callToolOn(t, server, "pacto_publish_plan", args), &p)
	if !p.Ready || p.Missing != 2 {
		t.Fatalf("plan: %+v", p)
	}
	var r publish.Result
	decode(t, callToolOn(t, server, "pacto_publish", args), &r)
	if len(r.Actions) != 1 || len(gotPlan) != 2 || !gotPlan[0] || gotPlan[1] {
		t.Fatalf("publish: %+v %v", r, gotPlan)
	}
	err = errors.New("publish refused: version: needs a major bump")
	if out := callToolOn(t, server, "pacto_publish", args); !out.IsError {
		t.Fatal("a refused publish is an error that carries the plan")
	}
	res = nil
	for _, tool := range []string{"pacto_publish_plan", "pacto_publish"} {
		if out := callToolOn(t, server, tool, args); !out.IsError {
			t.Fatalf("%s: a failure is an error", tool)
		}
	}
}
