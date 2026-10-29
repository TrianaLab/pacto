package diff

import (
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
		changes = append(changes, diffJSON(dirRequest, nil, path+".inputSchema", o.InputSchema, n.InputSchema)...)
		changes = append(changes, diffJSON(dirResponse, nil, path+".outputSchema", o.OutputSchema, n.OutputSchema)...)
	}
	for _, n := range newTools {
		if !slices.ContainsFunc(oldTools, func(t contract.MCPTool) bool { return t.Name == n.Name }) {
			changes = append(changes, Change{Path: fmt.Sprintf("mcp.tools[%s]", n.Name), Type: Added, NewValue: n.Name,
				Classification: classify("mcp.tools", Added), Reason: "tool " + n.Name + " added"})
		}
	}
	return changes
}
