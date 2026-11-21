package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestGraphRefLessIsNotExternal: a dependency by name is a dependency edge
// that says it resolves by name in the fleet, never an external one.
func TestGraphRefLessIsNotExternal(t *testing.T) {
	c := &contract.Contract{
		Service:      contract.Service{Name: "reports", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "ledger", Compatibility: contract.CompatibilityUnknown, Required: true}},
	}
	res := Resolve(context.Background(), c, &mockFetcher{})
	e := res.Root.Dependencies[0]
	if e.Type != EdgeDependency || e.External || e.Name != "ledger" || e.Error != "no ref: resolved by name in the fleet" || !e.Required {
		t.Fatalf("edge = %+v", e)
	}
	if tree := RenderTreeColored(res, TreeColors{}); !strings.Contains(tree, "ledger (error: no ref: resolved by name in the fleet)") || strings.Contains(tree, "external") {
		t.Fatalf("tree:\n%s", tree)
	}
}
