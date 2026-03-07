package graph

import (
	"fmt"
	"strings"
)

// RenderTree renders the dependency graph as a tree-style string
// similar to the Unix tree command.
func RenderTree(r *Result) string {
	if r == nil || r.Root == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s@%s\n", r.Root.Name, r.Root.Version)
	renderChildren(&b, r.Root.Dependencies, "")

	if len(r.Cycles) > 0 {
		fmt.Fprintf(&b, "\nCycles (%d):\n", len(r.Cycles))
		for _, cycle := range r.Cycles {
			fmt.Fprintf(&b, "  %s\n", strings.Join(cycle, " -> "))
		}
	}

	if len(r.Conflicts) > 0 {
		fmt.Fprintf(&b, "\nConflicts (%d):\n", len(r.Conflicts))
		for _, c := range r.Conflicts {
			fmt.Fprintf(&b, "  %s: %v\n", c.Name, c.Versions)
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

func renderChildren(b *strings.Builder, edges []Edge, prefix string) {
	for i, edge := range edges {
		isLast := i == len(edges)-1
		connector := "├─ "
		childPrefix := "│  "
		if isLast {
			connector = "└─ "
			childPrefix = "   "
		}

		if edge.Error != "" {
			fmt.Fprintf(b, "%s%s%s (error: %s)\n", prefix, connector, ShortRef(edge.Ref), edge.Error)
			continue
		}

		if edge.Node != nil {
			label := edge.Node.Name + "@" + edge.Node.Version
			if edge.Node.Local {
				label += " [local]"
			}
			if edge.Shared {
				fmt.Fprintf(b, "%s%s%s (shared)\n", prefix, connector, label)
			} else {
				fmt.Fprintf(b, "%s%s%s\n", prefix, connector, label)
				renderChildren(b, edge.Node.Dependencies, prefix+childPrefix)
			}
		} else {
			fmt.Fprintf(b, "%s%s%s\n", prefix, connector, ShortRef(edge.Ref))
		}
	}
}
