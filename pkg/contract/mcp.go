package contract

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// MCPTool is one tool in an mcp interface's tools/list result. Only what a
// caller depends on is read: the name and the input and output schemas.
type MCPTool struct {
	Name         string `yaml:"name"`
	InputSchema  any    `yaml:"inputSchema"`
	OutputSchema any    `yaml:"outputSchema"`
}

// ParseMCPTools reads an mcp interface file ({"tools": [...]}, JSON or YAML).
// Every tool needs a name, used once.
func ParseMCPTools(data []byte) ([]MCPTool, error) {
	var doc struct {
		Tools []MCPTool `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not a tools/list result: %w", err)
	}
	if len(doc.Tools) == 0 {
		return nil, errors.New(`lists no tools: it must hold {"tools": [...]}`)
	}
	seen := map[string]bool{}
	for i, t := range doc.Tools {
		switch {
		case t.Name == "":
			return nil, fmt.Errorf("tool %d has no name", i)
		case seen[t.Name]:
			return nil, fmt.Errorf("tool %q is listed twice", t.Name)
		}
		seen[t.Name] = true
	}
	return doc.Tools, nil
}
