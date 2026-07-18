package dashboard

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

const capOpenAPI = `{"paths":{
  "/ping":{"get":{"operationId":"ping","summary":"Ping"}},
  "/refunds":{"post":{"operationId":"createRefund","summary":"Refund"}}
}}`

func TestCapabilitiesFromContract(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "http", Type: contract.InterfaceTypeHTTP, Contract: "openapi.json"},
	}}
	fsys := fstest.MapFS{"openapi.json": {Data: []byte(capOpenAPI)}}
	tools := capabilitiesFromContract(c, fsys)
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

func TestCapabilitiesFromContract_MultiInterfacePrefix(t *testing.T) {
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "alpha", Type: contract.InterfaceTypeHTTP, Contract: "a.json"},
		{Name: "beta", Type: contract.InterfaceTypeHTTP, Contract: "b.json"},
	}}
	fsys := fstest.MapFS{
		"a.json": {Data: []byte(`{"paths":{"/ping":{"get":{"operationId":"ping"}}}}`)},
		"b.json": {Data: []byte(`{"paths":{"/ping":{"get":{"operationId":"ping"}}}}`)},
	}
	tools := capabilitiesFromContract(c, fsys)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	if !names["alpha_ping"] || !names["beta_ping"] {
		t.Fatalf("expected prefixed names, got %v", names)
	}
}

func TestCapabilitiesFromContract_SkipsAndErrors(t *testing.T) {
	if got := capabilitiesFromContract(&contract.Contract{}, nil); got != nil {
		t.Errorf("nil FS must yield nil, got %v", got)
	}
	c := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "ev", Type: contract.InterfaceTypeEvent, Contract: "e.json"},          // non-http skipped
		{Name: "nocontract", Type: contract.InterfaceTypeHTTP},                       // no contract skipped
		{Name: "broken", Type: contract.InterfaceTypeHTTP, Contract: "missing.json"}, // ReadDoc error skipped
	}}
	if got := capabilitiesFromContract(c, fstest.MapFS{}); got != nil {
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

func TestServiceDetailsFromBundle_Capabilities(t *testing.T) {
	c := &contract.Contract{
		Service:    contract.ServiceIdentity{Name: "demo", Version: "1.0.0"},
		Interfaces: []contract.Interface{{Name: "http", Type: contract.InterfaceTypeHTTP, Contract: "openapi.json"}},
	}
	fsys := fstest.MapFS{
		"openapi.json":    {Data: []byte(capOpenAPI)},
		"skills/usage.md": {Data: []byte("# Usage")},
	}
	svc := ServiceDetailsFromBundle(&contract.Bundle{Contract: c, FS: fsys}, "local")
	if len(svc.Capabilities) != 2 {
		t.Fatalf("capabilities = %+v", svc.Capabilities)
	}
	if len(svc.Skills) != 1 || svc.Skills[0].Name != "usage.md" || svc.Skills[0].Content != "# Usage" {
		t.Fatalf("skills = %+v", svc.Skills)
	}
}
