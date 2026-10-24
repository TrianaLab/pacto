package diff

import (
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func toolsFS(data string) fstest.MapFS {
	return fstest.MapFS{"tools.json": {Data: []byte(data)}}
}

func TestDiffMCP(t *testing.T) {
	old := toolsFS(`{"tools":[
		{"name":"refund","inputSchema":{"type":"object","required":["order"]},"outputSchema":{"type":"object","properties":{"id":{"type":"string"}}}},
		{"name":"lookup","description":"old words"}]}`)
	new := toolsFS(`{"tools":[
		{"name":"refund","inputSchema":{"type":"object","required":["order","reason"]},"outputSchema":{"type":"object","properties":{}}},
		{"name":"lookup","description":"new words"},
		{"name":"escalate"}]}`)
	got := map[string]Classification{}
	for _, c := range diffMCP("tools.json", "tools.json", old, new) {
		got[c.Path+" "+c.Type.String()] = c.Classification
	}
	want := map[string]Classification{
		"mcp.tools[refund].inputSchema.required[reason] added": Breaking,
		"mcp.tools[refund].outputSchema.properties.id removed": Breaking,
		"mcp.tools[escalate] added":                            NonBreaking,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v, want %v (all: %v)", k, got[k], v, got)
		}
	}
	gone := diffMCP("tools.json", "tools.json", old, toolsFS(`{"tools":[{"name":"lookup"}]}`))
	if len(gone) != 1 || gone[0].Path != "mcp.tools[refund]" || gone[0].Classification != Breaking {
		t.Fatalf("removed tool: %+v", gone)
	}
	if got := diffMCP("tools.json", "tools.json", nil, new); got != nil {
		t.Errorf("nil fs: %v", got)
	}
	for name, c := range map[string][2]fstest.MapFS{
		"old missing": {fstest.MapFS{}, new},
		"new missing": {old, fstest.MapFS{}},
		"old invalid": {toolsFS(`{}`), new},
		"new invalid": {old, toolsFS(`{}`)},
	} {
		if got := diffMCP("tools.json", "tools.json", c[0], c[1]); got != nil {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestDiffAgent(t *testing.T) {
	if got := diffAgent(nil, nil); got != nil {
		t.Fatalf("no agent: %v", got)
	}
	got := diffAgent(
		&contract.Agent{Models: []string{"a", "b"}, Evals: "evals/v1.yaml"},
		&contract.Agent{Models: []string{"b", "c"}, Evals: "evals/v2.yaml"})
	want := []string{"agent.models removed POTENTIAL_BREAKING", "agent.models added POTENTIAL_BREAKING", "agent.evals modified NON_BREAKING"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, c := range got {
		if s := c.Path + " " + c.Type.String() + " " + c.Classification.String(); s != want[i] {
			t.Errorf("%d: got %s, want %s", i, s, want[i])
		}
	}
	if got := diffAgent(nil, &contract.Agent{Evals: "e.yaml"}); len(got) != 1 || got[0].Classification != NonBreaking {
		t.Fatalf("evals added: %+v", got)
	}
}

func TestCompare_MCPInterface(t *testing.T) {
	c := func(fsys fstest.MapFS) (*contract.Contract, fstest.MapFS) {
		return &contract.Contract{PactoVersion: "2.1", Service: contract.Service{Name: "a", Version: "1.0.0"},
			Interfaces: []contract.Interface{{Name: "tools", Type: "mcp", Ref: "tools.json"}}}, fsys
	}
	o, ofs := c(toolsFS(`{"tools":[{"name":"x"},{"name":"y"}]}`))
	n, nfs := c(toolsFS(`{"tools":[{"name":"x"}]}`))
	r := Compare(t.Context(), o, n, ofs, nfs)
	if r.Classification != Breaking {
		t.Fatalf("got %v: %+v", r.Classification, r.Changes)
	}
}
