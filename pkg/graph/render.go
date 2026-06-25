package graph

import (
	"fmt"
	"strings"
)

// TreeColors holds optional colorizer functions applied to tree tokens.
// A nil field is treated as identity, so the zero value renders plain text.
type TreeColors struct {
	Name    func(string) string // node / ref display name
	Version func(string) string // @version
	Marker  func(string) string // [local], [ref], (shared)
	Error   func(string) string // (error: ...)
	Warn    func(string) string // cycle / conflict lines
}

func apply(fn func(string) string, s string) string {
	if fn == nil {
		return s
	}
	return fn(s)
}

// RenderTree renders the dependency graph as a tree-style string
// similar to the Unix tree command.
func RenderTree(r *Result) string {
	return RenderTreeColored(r, TreeColors{})
}

// RenderTreeColored renders the tree applying the given colorizers.
func RenderTreeColored(r *Result, col TreeColors) string {
	if r == nil || r.Root == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s@%s\n", apply(col.Name, r.Root.Name), apply(col.Version, r.Root.Version))
	renderChildren(&b, r.Root.Dependencies, "", col)

	if len(r.Cycles) > 0 {
		fmt.Fprintf(&b, "\nCycles (%d):\n", len(r.Cycles))
		for _, cycle := range r.Cycles {
			fmt.Fprintf(&b, "  %s\n", apply(col.Warn, strings.Join(cycle, " -> ")))
		}
	}

	if len(r.Conflicts) > 0 {
		fmt.Fprintf(&b, "\nConflicts (%d):\n", len(r.Conflicts))
		for _, c := range r.Conflicts {
			fmt.Fprintf(&b, "  %s\n", apply(col.Warn, fmt.Sprintf("%s: %v", c.Name, c.Versions)))
		}
	}

	return b.String()
}

// ShortRef extracts a short display name from an OCI reference.
// It strips the registry/repository prefix and truncates digests.
func ShortRef(ref string) string {
	name := ref
	if i := strings.LastIndex(name, "/"); i != -1 {
		name = name[i+1:]
	}
	if at := strings.Index(name, "@sha256:"); at != -1 {
		digest := name[at+8:]
		if len(digest) > 7 {
			digest = digest[:7]
		}
		name = name[:at] + "@sha256:" + digest
	}
	return name
}

// treeConnectors returns the connector and child-prefix strings for tree rendering.
func treeConnectors(isLast bool) (connector, childPrefix string) {
	if isLast {
		return "└─ ", "   "
	}
	return "├─ ", "│  "
}

func renderChildren(b *strings.Builder, edges []Edge, prefix string, col TreeColors) {
	for i, edge := range edges {
		connector, childPrefix := treeConnectors(i == len(edges)-1)

		// Reference edges use a distinct notation
		if edge.Type == EdgeReference {
			fmt.Fprintf(b, "%s%s%s %s\n", prefix, connector, apply(col.Name, ShortRef(edge.Ref)), apply(col.Marker, "[ref]"))
			continue
		}

		if edge.Error != "" {
			fmt.Fprintf(b, "%s%s%s %s\n", prefix, connector, apply(col.Name, ShortRef(edge.Ref)), apply(col.Error, fmt.Sprintf("(error: %s)", edge.Error)))
			continue
		}

		if edge.Node != nil {
			label := apply(col.Name, edge.Node.Name) + "@" + apply(col.Version, edge.Node.Version)
			if edge.Node.Local {
				label += " " + apply(col.Marker, "[local]")
			}
			if edge.Shared {
				fmt.Fprintf(b, "%s%s%s %s\n", prefix, connector, label, apply(col.Marker, "(shared)"))
			} else {
				fmt.Fprintf(b, "%s%s%s\n", prefix, connector, label)
				renderChildren(b, edge.Node.Dependencies, prefix+childPrefix, col)
			}
		} else {
			fmt.Fprintf(b, "%s%s%s\n", prefix, connector, apply(col.Name, ShortRef(edge.Ref)))
		}
	}
}
