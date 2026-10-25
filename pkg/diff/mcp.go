package diff

import (
	"bytes"
	"fmt"
	"io/fs"
	"slices"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// diffMCP compares two mcp interface files tool by tool. A removed tool breaks
// every caller of it; an added one breaks nobody. A tool on both sides is
// deep-diffed: its input schema is what the caller sends and its output schema
// is what it gets back. Descriptions are skipped like OpenAPI's info: they
// churn without changing what a caller can send.
func diffMCP(oldPath, newPath string, oldFS, newFS fs.FS) []Change {
	if oldFS == nil || newFS == nil {
		return nil
	}
	oldData, oldErr := fs.ReadFile(oldFS, oldPath)
	newData, newErr := fs.ReadFile(newFS, newPath)
	if oldErr != nil || newErr != nil {
		return nil
	}
	oldTools, oldErr := contract.ParseMCPTools(oldData)
	newTools, newErr := contract.ParseMCPTools(newData)
	if oldErr != nil || newErr != nil {
		return nil
	}
	var changes []Change
	for _, o := range oldTools {
		path := fmt.Sprintf("mcp.tools[%s]", o.Name)
		i := slices.IndexFunc(newTools, func(t contract.MCPTool) bool { return t.Name == o.Name })
		if i < 0 {
			changes = append(changes, Change{Path: path, Type: Removed, OldValue: o.Name,
				Classification: classify("mcp.tools", Removed), Reason: "tool " + o.Name + " removed"})
			continue
		}
		n := newTools[i]
		changes = append(changes, diffJSON(dirRequest, path+".inputSchema", o.InputSchema, n.InputSchema)...)
		changes = append(changes, diffJSON(dirResponse, path+".outputSchema", o.OutputSchema, n.OutputSchema)...)
	}
	for _, n := range newTools {
		if !slices.ContainsFunc(oldTools, func(t contract.MCPTool) bool { return t.Name == n.Name }) {
			changes = append(changes, Change{Path: fmt.Sprintf("mcp.tools[%s]", n.Name), Type: Added, NewValue: n.Name,
				Classification: classify("mcp.tools", Added), Reason: "tool " + n.Name + " added"})
		}
	}
	return changes
}

// diffAgent compares the agent sections. A different model changes what the
// agent does even when every tool keeps its shape, so a model change is
// potentially breaking: it needs at least a minor and goes through the evals.
// The eval suite is what earns the agent its autonomy, and a release that
// changes it can loosen the very check it ships behind (drop the cases it
// fails), so a suite removed, moved or edited is potentially breaking too: a
// person reviews it. Its bytes are compared when both bundles are given; the
// diff cannot tell a stricter suite from a weaker one.
func diffAgent(old, new *contract.Agent, oldFS, newFS fs.FS) []Change {
	var o, n contract.Agent
	if old != nil {
		o = *old
	}
	if new != nil {
		n = *new
	}
	var changes []Change
	for _, m := range o.Models {
		if !slices.Contains(n.Models, m) {
			changes = append(changes, newChange("agent.models", Removed, m, nil))
		}
	}
	for _, m := range n.Models {
		if !slices.Contains(o.Models, m) {
			changes = append(changes, newChange("agent.models", Added, nil, m))
		}
	}
	switch {
	case o.Evals != n.Evals:
		changes = append(changes, newChange("agent.evals", strChangeType(o.Evals, n.Evals), o.Evals, n.Evals))
	case o.Evals != "" && oldFS != nil && newFS != nil:
		ob, oerr := fs.ReadFile(oldFS, o.Evals)
		nb, nerr := fs.ReadFile(newFS, n.Evals)
		if (oerr == nil) != (nerr == nil) || !bytes.Equal(ob, nb) {
			c := newChange("agent.evals", Modified, o.Evals, n.Evals)
			c.Reason = "the eval suite " + n.Evals + " changed"
			changes = append(changes, c)
		}
	}
	return changes
}
