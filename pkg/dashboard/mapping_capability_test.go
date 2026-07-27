package dashboard

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

const capOpenAPI = `{"paths":{
  "/ping":{"get":{"operationId":"ping","summary":"Ping"}},
  "/refunds":{"post":{"operationId":"createRefund","summary":"Refund"}}
}}`

func TestCapabilitiesFromContract(t *testing.T) {
	c := &contract.Contract{
		Capabilities: []contract.Capability{
			{Type: contract.CapabilityHealth},
			{Type: contract.CapabilityMetrics, Ref: "/metrics"},
			{Type: "extension", Ref: "/custom"},
		},
	}
	caps := capabilitiesFromContract(c)
	if len(caps) != 3 {
		t.Fatalf("expected 3 capabilities, got %d: %+v", len(caps), caps)
	}
	if caps[0].Type != "health" || caps[0].Ref != "" {
		t.Errorf("caps[0] = %+v", caps[0])
	}
	if caps[1].Type != "metrics" || caps[1].Ref != "/metrics" {
		t.Errorf("caps[1] = %+v", caps[1])
	}
	if caps[2].Type != "extension" || caps[2].Ref != "/custom" {
		t.Errorf("caps[2] = %+v", caps[2])
	}
}

func TestToolsFromContract(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "openapi.json"},
	}}
	fsys := fstest.MapFS{"openapi.json": {Data: []byte(capOpenAPI)}}
	tools := toolsFromContract(c, fsys)
	if len(tools) != 2 {
		t.Fatalf("tools = %+v", tools)
	}
	byName := map[string]CapabilityTool{}
	for _, tl := range tools {
		byName[tl.Name] = tl
	}
	if byName["ping"].Method != "GET" || byName["ping"].Mutating {
		t.Errorf("ping = %+v", byName["ping"])
	}
	if !byName["createRefund"].Mutating || byName["createRefund"].Path != "/refunds" {
		t.Errorf("createRefund = %+v", byName["createRefund"])
	}
}

func TestToolsFromContract_DescriptionFallback(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "o.json"},
	}}
	spec := `{"paths":{"/x":{"get":{"operationId":"getX","description":"Only a description"}}}}`
	tools := toolsFromContract(c, fstest.MapFS{"o.json": {Data: []byte(spec)}})
	if len(tools) != 1 || tools[0].Summary != "Only a description" {
		t.Fatalf("expected description used as summary, got %+v", tools)
	}
}

func TestToolsFromContract_MultiInterfacePrefix(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "alpha", Type: contract.InterfaceTypeOpenAPI, Ref: "a.json"},
		{Name: "beta", Type: contract.InterfaceTypeOpenAPI, Ref: "b.json"},
	}}
	fsys := fstest.MapFS{
		"a.json": {Data: []byte(`{"paths":{"/ping":{"get":{"operationId":"ping"}}}}`)},
		"b.json": {Data: []byte(`{"paths":{"/ping":{"get":{"operationId":"ping"}}}}`)},
	}
	tools := toolsFromContract(c, fsys)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	if !names["alpha_ping"] || !names["beta_ping"] {
		t.Fatalf("expected prefixed names, got %v", names)
	}
}

func TestToolsFromContract_SkipsAndErrors(t *testing.T) {
	if got := toolsFromContract(&contract.Contract{}, nil); got != nil {
		t.Errorf("nil FS must yield nil, got %v", got)
	}
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "ev", Type: contract.InterfaceTypeAsyncAPI, Ref: "e.json"},          // non-openapi skipped
		{Name: "nocontract", Type: contract.InterfaceTypeOpenAPI},                  // no ref skipped
		{Name: "broken", Type: contract.InterfaceTypeOpenAPI, Ref: "missing.json"}, // ReadDoc error skipped
	}}
	if got := toolsFromContract(c, fstest.MapFS{}); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestSkillsFromContract(t *testing.T) {
	if got := skillsFromContract(nil); got != nil {
		t.Errorf("nil FS must yield nil, got %v", got)
	}
	// no skills dir → nil
	if got := skillsFromContract(fstest.MapFS{"pacto.yaml": {Data: []byte("x")}}); got != nil {
		t.Errorf("no skills dir must yield nil, got %v", got)
	}
	fsys := fstest.MapFS{
		"skills/refund.md":  {Data: []byte("# Refund")},
		"skills/onboard.md": {Data: []byte("# Onboard")},
	}
	skills := skillsFromContract(fsys)
	if len(skills) != 2 || skills[0].Name != "onboard.md" || skills[0].Content != "# Onboard" {
		t.Fatalf("skills = %+v", skills)
	}
}

func TestSkillsFromContract_CountCap(t *testing.T) {
	fsys := fstest.MapFS{}
	for i := 0; i < maxDocCount+5; i++ {
		fsys[fmt.Sprintf("skills/s%03d.md", i)] = &fstest.MapFile{Data: []byte("x")}
	}
	if got := skillsFromContract(fsys); len(got) != maxDocCount {
		t.Fatalf("expected count capped at %d, got %d", maxDocCount, len(got))
	}
}

func TestServiceDetailsFromBundle_Tools(t *testing.T) {
	c := &contract.Contract{
		Service:    contract.Service{Name: "demo", Version: "1.0.0"},
		Interfaces: []contract.Interface{{Name: "http", Type: contract.InterfaceTypeOpenAPI, Ref: "openapi.json"}},
	}
	fsys := fstest.MapFS{
		"openapi.json":    {Data: []byte(capOpenAPI)},
		"skills/usage.md": {Data: []byte("# Usage")},
	}
	svc := ServiceDetailsFromBundle(&contract.Bundle{Contract: c, FS: fsys}, "local")
	if len(svc.Tools) != 2 {
		t.Fatalf("tools = %+v", svc.Tools)
	}
	if len(svc.Skills) != 1 || svc.Skills[0].Name != "usage.md" || svc.Skills[0].Content != "# Usage" {
		t.Fatalf("skills = %+v", svc.Skills)
	}
}
